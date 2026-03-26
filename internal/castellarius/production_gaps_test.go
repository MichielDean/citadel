package castellarius

// production_gaps_test.go — tests for failure modes that caused real incidents.
//
// These tests cover the interaction paths that were MISSING before 2026-03-25
// and whose absence allowed the self-kill bug, silent backoff, and heartbeat
// race to go undetected.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// --- Heartbeat minimum-age guard ---

// TestHeartbeat_DoesNotResetFreshlyDispatchedSession is the direct regression
// test for the self-kill bug. The heartbeat must not reset a session that was
// just dispatched — the minimum-age guard (2× pollInterval = 20s) prevents it.
//
// Without the guard:
//   1. Dispatch runs, sets UpdatedAt = now
//   2. Heartbeat fires ~20s later, sees tmux dead (session still starting),
//      resets droplet to open
//   3. Dispatch picks it up again, spawn() kills the running session
//   4. Killed session recorded at exactly 20s — the precise crash timing seen in prod.
func TestHeartbeat_DoesNotResetFreshlyDispatchedSession(t *testing.T) {
	buf := &bytes.Buffer{}
	client := newMockClient()
	sched := newTestScheduler(buf, client)

	// Mark "alpha" as flowing so the heartbeat reaches the tmux+age check.
	pool := sched.pools["repo"]
	w := pool.FindByName("alpha")
	if w == nil {
		t.Fatal("pool has no 'alpha' worker — test setup incorrect")
	}
	pool.Assign(w, "fresh-dispatch", "implement")

	// Droplet dispatched 5s ago — well within the 20s minimum-age guard.
	item := &cistern.Droplet{
		ID:                "fresh-dispatch",
		Repo:              "repo",
		Status:            "in_progress",
		Assignee:          "alpha",
		CurrentCataractae: "implement",
		UpdatedAt:         time.Now().Add(-5 * time.Second),
	}
	client.items["fresh-dispatch"] = item

	sched.heartbeatRepo(context.Background(), aqueduct.RepoConfig{Name: "repo"})

	log := buf.String()
	if strings.Contains(log, "resetting stalled droplet") {
		t.Errorf("heartbeat reset a freshly dispatched session (5s old < 20s guard); log:\n%s", log)
	}
}

// TestHeartbeat_ResetsStaleSession verifies the heartbeat DOES act on genuinely
// stale sessions that are old enough (beyond the minimum-age guard).
func TestHeartbeat_ResetsStaleSession(t *testing.T) {
	buf := &bytes.Buffer{}
	client := newMockClient()
	sched := newTestScheduler(buf, client)

	// Mark "alpha" as flowing — the heartbeat only checks tmux for flowing workers.
	pool := sched.pools["repo"]
	w := pool.FindByName("alpha")
	if w == nil {
		t.Fatal("pool has no 'alpha' worker — test setup incorrect")
	}
	pool.Assign(w, "stale-session", "implement")

	item := &cistern.Droplet{
		ID:                "stale-session",
		Repo:              "repo",
		Status:            "in_progress",
		Assignee:          "alpha",
		CurrentCataractae: "implement",
		UpdatedAt:         time.Now().Add(-45 * time.Second), // clearly stale
	}
	client.items["stale-session"] = item

	sched.heartbeatRepo(context.Background(), aqueduct.RepoConfig{Name: "repo"})

	log := buf.String()
	if !strings.Contains(log, "resetting stalled droplet") {
		t.Errorf("heartbeat should reset a 45s-old stale session (flowing + tmux dead); log:\n%s", log)
	}
}

// TestHeartbeat_SkipsItemsWithOutcome verifies that the heartbeat never resets
// a droplet that has an outcome — even if tmux is dead and the session is old.
// This prevents the observe loop from losing work before it has a chance to run.
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
		UpdatedAt:         time.Now().Add(-60 * time.Second),
	}
	client.items["has-outcome"] = item

	sched.heartbeatRepo(context.Background(), aqueduct.RepoConfig{Name: "repo"})

	log := buf.String()
	if strings.Contains(log, "resetting stalled droplet") {
		t.Errorf("heartbeat reset a droplet whose outcome was already written; log:\n%s", log)
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
