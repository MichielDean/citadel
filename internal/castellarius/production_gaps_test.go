package castellarius

// production_gaps_test.go — tests for failure modes that caused real incidents.
//
// These tests cover the interaction paths that were MISSING before 2026-03-25
// and whose absence allowed the self-kill bug, silent backoff, and heartbeat
// race to go undetected.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// --- Heartbeat progress-monitoring tests ---

// TestHeartbeat_StallDetected_WhenNoSignals verifies that the heartbeat detects
// a stall and logs "stall detected" when all three progress signals are absent
// (no notes, no worktree files, no session log).
func TestHeartbeat_StallDetected_WhenNoSignals(t *testing.T) {
	// Mock tmux as alive so liveness check passes through to stall detector.
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = orig })
	// Mock agent as alive so the agent-dead zombie path is not triggered.
	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	buf := &bytes.Buffer{}
	client := newMockClient()
	sched := newTestScheduler(buf, client)

	item := &cistern.Droplet{
		ID:                "stale-session",
		Repo:              "repo",
		Status:            "in_progress",
		Assignee:          "alpha",
		CurrentCataractae: "implement",
	}
	client.items["stale-session"] = item

	sched.heartbeatRepo(context.Background(), aqueduct.RepoConfig{Name: "repo"})

	log := buf.String()
	if !strings.Contains(log, "stall detected") {
		t.Errorf("heartbeat should log 'stall detected' when no signals present; log:\n%s", log)
	}
}

// TestHeartbeat_NoStallNote_WhenRecentHeartbeat verifies that the heartbeat
// does not write a stall note when the agent's LastHeartbeatAt is within the
// 45-minute default threshold.
func TestHeartbeat_NoStallNote_WhenRecentHeartbeat(t *testing.T) {
	// Mock tmux/agent as alive so zombie detection is bypassed and stall
	// detection runs on the heartbeat timestamp.
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = orig })
	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	buf := &bytes.Buffer{}
	client := newMockClient()
	sched := newTestScheduler(buf, client)

	item := &cistern.Droplet{
		ID:                "fresh-dispatch",
		Repo:              "repo",
		Status:            "in_progress",
		Assignee:          "alpha",
		CurrentCataractae: "implement",
		// Recent heartbeat: 5 seconds ago — well within the 45-minute default threshold.
		LastHeartbeatAt: time.Now().Add(-5 * time.Second),
	}
	client.items["fresh-dispatch"] = item

	sched.heartbeatRepo(context.Background(), aqueduct.RepoConfig{Name: "repo"})

	log := buf.String()
	if strings.Contains(log, "stall detected") {
		t.Errorf("heartbeat flagged a recently-heartbeating droplet as stalled; log:\n%s", log)
	}
}

// TestHeartbeat_SkipsItemsWithOutcome verifies that the heartbeat never writes
// a stall note for a droplet that already has an outcome — the observe loop
// handles those and must not be interfered with.
func TestHeartbeat_SkipsItemsWithOutcome(t *testing.T) {
	buf := &bytes.Buffer{}
	client := newMockClient()
	sched := newTestScheduler(buf, client)

	item := &cistern.Droplet{
		ID:                "has-outcome",
		Repo:              "repo",
		Status:            "in_progress",
		Assignee:          "alpha",
		CurrentCataractae: "implement",
		Outcome:           "pass",
	}
	client.items["has-outcome"] = item

	sched.heartbeatRepo(context.Background(), aqueduct.RepoConfig{Name: "repo"})

	log := buf.String()
	if strings.Contains(log, "stall detected") {
		t.Errorf("heartbeat flagged a droplet with an existing outcome; log:\n%s", log)
	}
}

// --- Dispatch error paths ---

// TestDispatch_GetReadyError_ReleasesWorker verifies that a DB error in GetReady
// releases the worker so subsequent ticks can still dispatch work.
func TestDispatch_GetReadyError_ReleasesWorker(t *testing.T) {
	buf := &bytes.Buffer{}
	client := newMockClient()
	client.getReadyErr = errors.New("db locked")
	sched := newTestScheduler(buf, client)

	sched.dispatchRepo(context.Background(), aqueduct.RepoConfig{Name: "repo"})

	log := buf.String()
	if !strings.Contains(log, "poll failed") {
		t.Errorf("expected 'poll failed' log; got:\n%s", log)
	}
	if !poolAllIdle(sched.pools["repo"]) {
		t.Error("worker not released after GetReady error — pool would deadlock on next tick")
	}
}

// TestDispatch_SpawnFailure_ResetsDropletAndReleasesWorker verifies that when
// Spawn() returns an error, the droplet is reset to open (not left stuck in_progress)
// and the worker is released.
func TestDispatch_SpawnFailure_ResetsDropletAndReleasesWorker(t *testing.T) {
	buf := &bytes.Buffer{}
	client := newMockClient()

	droplet := &cistern.Droplet{
		ID:                "spawn-fail",
		Repo:              "repo",
		CurrentCataractae: "implement",
	}
	client.readyItems = []*cistern.Droplet{droplet}
	client.items["spawn-fail"] = droplet

	var spawnCalled int64
	runner := &funcRunner{fn: func(_ context.Context, _ CataractaeRequest) error {
		atomic.AddInt64(&spawnCalled, 1)
		return errors.New("tmux server dead")
	}}
	sched := newTestSchedulerWithRunner(buf, client, runner)

	sched.dispatchRepo(context.Background(), aqueduct.RepoConfig{Name: "repo"})

	// Wait for the goroutine to finish.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if poolAllIdle(sched.pools["repo"]) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !poolAllIdle(sched.pools["repo"]) {
		t.Error("worker not released after spawn failure")
	}
	log := buf.String()
	if !strings.Contains(log, "spawn failed") {
		t.Errorf("expected 'spawn failed' log; got:\n%s", log)
	}

	// Droplet must have been reset to open (Assign called with empty worker).
	client.mu.Lock()
	status := ""
	if it, ok := client.items["spawn-fail"]; ok {
		status = it.Status
	}
	client.mu.Unlock()
	if status != "open" {
		t.Errorf("droplet status = %q after spawn failure; want 'open' so it can be retried", status)
	}
}

// TestDispatch_DispatchLoopThreshold_StopsRetrying verifies that when a droplet
// has hit the dispatch-loop failure threshold, the dispatcher triggers recovery
// and does NOT call Spawn again — preventing infinite retry loops.
func TestDispatch_DispatchLoopThreshold_StopsRetrying(t *testing.T) {
	buf := &bytes.Buffer{}
	client := newMockClient()

	droplet := &cistern.Droplet{
		ID:                "loop-droplet",
		Repo:              "repo",
		CurrentCataractae: "implement",
	}
	client.readyItems = []*cistern.Droplet{droplet}
	client.items["loop-droplet"] = droplet

	var spawnCalled int64
	runner := &funcRunner{fn: func(_ context.Context, _ CataractaeRequest) error {
		atomic.AddInt64(&spawnCalled, 1)
		return nil
	}}
	sched := newTestSchedulerWithRunner(buf, client, runner)

	// Push the droplet past the threshold.
	for i := 0; i < dispatchLoopThreshold; i++ {
		sched.dispatchLoop.recordFailure("loop-droplet")
	}

	sched.dispatchRepo(context.Background(), aqueduct.RepoConfig{Name: "repo"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if poolAllIdle(sched.pools["repo"]) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	log := buf.String()
	if !strings.Contains(log, "dispatch-loop threshold reached") {
		t.Errorf("expected dispatch-loop threshold log; got:\n%s", log)
	}
	if n := atomic.LoadInt64(&spawnCalled); n > 0 {
		t.Errorf("Spawn() called %d times despite dispatch-loop threshold — should have taken recovery path", n)
	}
}

// TestDispatch_SuccessfulSpawn_WorkerRemainsFlowing verifies the happy path:
// after a successful spawn the worker stays busy (observe loop releases it),
// not prematurely returned to idle.
func TestDispatch_SuccessfulSpawn_WorkerRemainsFlowing(t *testing.T) {
	buf := &bytes.Buffer{}
	client := newMockClient()

	droplet := &cistern.Droplet{
		ID:                "success",
		Repo:              "repo",
		CurrentCataractae: "implement",
	}
	client.readyItems = []*cistern.Droplet{droplet}
	client.items["success"] = droplet

	runner := &funcRunner{fn: func(_ context.Context, _ CataractaeRequest) error {
		return nil // success
	}}
	sched := newTestSchedulerWithRunner(buf, client, runner)

	sched.dispatchRepo(context.Background(), aqueduct.RepoConfig{Name: "repo"})

	// Give goroutine time to run.
	time.Sleep(50 * time.Millisecond)

	// Worker should still be flowing — the observe loop hasn't released it yet.
	if poolAllIdle(sched.pools["repo"]) {
		t.Error("worker returned to idle immediately after successful spawn — should stay flowing until observe loop releases it")
	}
}

// --- helpers ---

// poolAllIdle returns true when every aqueduct in the pool is idle.
func poolAllIdle(pool *AqueductPool) bool {
	if pool == nil {
		return true
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for _, a := range pool.aqueducts {
		if a.Status != AqueductIdle {
			return false
		}
	}
	return true
}

// newTestScheduler builds a minimal Castellarius for unit testing.
func newTestScheduler(buf *bytes.Buffer, client *mockClient) *Castellarius {
	return newTestSchedulerWithRunner(buf, client, &funcRunner{fn: func(_ context.Context, _ CataractaeRequest) error {
		return nil
	}})
}

func newTestSchedulerWithRunner(buf *bytes.Buffer, client *mockClient, runner CataractaeRunner) *Castellarius {
	wf := &aqueduct.Workflow{
		Name: "feature",
		Cataractae: []aqueduct.WorkflowCataractae{
			{Name: "implement", Type: aqueduct.CataractaeTypeAgent, OnPass: "done"},
		},
	}
	cfg := aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "repo", Prefix: "r", WorkflowPath: "test", Names: []string{"alpha"}},
		},
	}
	return NewFromParts(cfg,
		map[string]*aqueduct.Workflow{"repo": wf},
		map[string]CisternClient{"repo": client},
		runner,
		WithLogger(newTestLogger(buf)),
		WithPollInterval(10*time.Second),
	)
}

// funcRunner is a CataractaeRunner backed by an arbitrary function.
type funcRunner struct {
	fn func(ctx context.Context, req CataractaeRequest) error
}

func (r *funcRunner) Spawn(ctx context.Context, req CataractaeRequest) error {
	return r.fn(ctx, req)
}

// Extend mockClient with getReadyErrOnce for error-path tests.
// (Other mockClient fields/methods are defined in scheduler_test.go.)
func init() {
	// Verify mockClient still satisfies the interface after our additions.
	var _ CisternClient = (*mockClient)(nil)
}

// --- Heartbeat DB integration tests ---
//
// These tests use a real cistern.Client backed by SQLite to catch column scan
// ordering bugs that mock-client tests cannot detect. If List() has a bug that
// leaves LastHeartbeatAt always zero, the stall detector falls back to
// UpdatedAt. Because UpdatedAt is artificially aged in these tests, such a
// scan bug would cause a false stall in TestHeartbeat_DB_NotStalled and
// the test would fail.

// TestHeartbeat_DB_NotStalled_WhenRecentHeartbeat uses a real DB to verify
// that the stall detector skips agents whose last_heartbeat_at is recent, even
// when updated_at is old. Detects scan ordering bugs in List().
func TestHeartbeat_DB_NotStalled_WhenRecentHeartbeat(t *testing.T) {
	origTmux := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = origTmux })
	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	dbPath := filepath.Join(t.TempDir(), "test.db")
	c, err := cistern.New(dbPath, "ts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	item, err := c.Add("test-repo", "DB integration task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateStatus(item.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}

	// Age updated_at so the stall detector fires on the fallback path if
	// last_heartbeat_at is not scanned correctly (zero value scan bug).
	rawDB, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := rawDB.Exec(`UPDATE droplets SET updated_at = ? WHERE id = ?`, past, item.ID); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	rawDB.Close()

	// Emit a heartbeat — last_heartbeat_at is now current.
	if err := c.Heartbeat(item.ID); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	cfg.StallThresholdMinutes = 1
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": c}
	sched := NewFromParts(cfg, workflows, clients, newMockRunner(nil))

	sched.heartbeatRepo(context.Background(), cfg.Repos[0])

	// Recent heartbeat → no stall note should be written.
	notes, err := c.GetNotes(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if strings.HasPrefix(n.Content, stallNotePrefix) {
			t.Errorf("DB integration: stall note written for heartbeating agent: %s", n.Content)
		}
	}
}

// TestHeartbeat_DB_Stalled_WhenNoHeartbeat uses a real DB to verify that an
// agent with no heartbeat and an aged updated_at is detected as stalled and
// an escalation note with heartbeat=none is written.
func TestHeartbeat_DB_Stalled_WhenNoHeartbeat(t *testing.T) {
	origTmux := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = origTmux })
	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	dbPath := filepath.Join(t.TempDir(), "test.db")
	c, err := cistern.New(dbPath, "ts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	item, err := c.Add("test-repo", "DB stall task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateStatus(item.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}

	// Age updated_at — no heartbeat was ever emitted; fallback triggers stall.
	rawDB, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := rawDB.Exec(`UPDATE droplets SET updated_at = ? WHERE id = ?`, past, item.ID); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	rawDB.Close()

	cfg := testConfig()
	cfg.StallThresholdMinutes = 1
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": c}
	sched := NewFromParts(cfg, workflows, clients, newMockRunner(nil))

	sched.heartbeatRepo(context.Background(), cfg.Repos[0])

	// No heartbeat → stall detected → escalation note written with heartbeat=none.
	notes, err := c.GetNotes(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stallNote string
	for _, n := range notes {
		if strings.HasPrefix(n.Content, stallNotePrefix) {
			stallNote = n.Content
			break
		}
	}
	if stallNote == "" {
		t.Error("DB integration: expected stall note for no-heartbeat agent, got none")
		return
	}
	if !strings.Contains(stallNote, "heartbeat=none") {
		t.Errorf("DB integration: stall note missing heartbeat=none; got: %s", stallNote)
	}
}
