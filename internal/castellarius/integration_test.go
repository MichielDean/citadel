// Integration tests for the Castellarius pipeline using a real tmux server,
// real SQLite database, and the fakeagent binary.  These tests catch regressions
// that unit tests with mocks cannot — specifically session lifecycle, environment
// propagation, and liveness recovery.
//
// Tests are skipped gracefully when tmux or ct are unavailable.
package castellarius_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/castellarius"
	"github.com/MichielDean/cistern/internal/cistern"
)

// ─────────────────────────────────────────────────────────────────────────────
// Prerequisites
// ─────────────────────────────────────────────────────────────────────────────

// checkIntegrationPrereqs skips the test if required binaries are unavailable.
func checkIntegrationPrereqs(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available: skipping integration test")
	}
	if _, err := exec.LookPath("ct"); err != nil {
		t.Skip("ct binary not on PATH: skipping integration test")
	}
}

// buildFakeagent compiles the fakeagent binary into a temp dir and returns its path.
// Go tests run with cwd = the package directory (internal/castellarius/), so the
// module root is two levels up.
func buildFakeagent(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "fakeagent")
	cmd := exec.Command("go", "build", "-o", out, "./internal/testutil/fakeagent")
	cmd.Dir = "../.."
	if result, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakeagent: %v\n%s", err, result)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// integrationRunner — CataractaeRunner for integration tests
// ─────────────────────────────────────────────────────────────────────────────

// integrationRunner spawns real tmux sessions running fakeagent.
// It creates a minimal CONTEXT.md in a temp workdir so fakeagent can read the
// droplet ID and signal pass via `ct droplet pass <id>`.
type integrationRunner struct {
	agentBin string            // absolute path to the fakeagent binary
	dbPath   string            // SQLite database path forwarded as CT_DB
	logDir   string            // directory for session output logs
	extraEnv map[string]string // additional env vars injected into the session

	mu       sync.Mutex
	sessions []string // tmux session IDs for cleanup
}

// intShellQuote wraps s in single quotes, escaping any single quotes within.
func intShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Spawn creates a workdir, writes CONTEXT.md, and starts a tmux session running
// fakeagent. Returns immediately (non-blocking).
func (r *integrationRunner) Spawn(_ context.Context, req castellarius.CataractaeRequest) error {
	dir, err := os.MkdirTemp("", "cistern-inttest-*")
	if err != nil {
		return fmt.Errorf("integrationRunner: mkdir: %w", err)
	}

	// Write a minimal CONTEXT.md so fakeagent can extract the droplet ID.
	contextMD := fmt.Sprintf("# Context\n\n## Item: %s\n\nIntegration test droplet.\n", req.Item.ID)
	if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte(contextMD), 0o644); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("integrationRunner: write CONTEXT.md: %w", err)
	}

	sessionID := "inttest-" + req.Item.ID
	r.mu.Lock()
	r.sessions = append(r.sessions, sessionID)
	r.mu.Unlock()

	// Build the fakeagent command.  The agent runs in interactive mode
	// (no --print flag), reads CONTEXT.md, then calls `ct droplet pass <id>`.
	agentCmd := r.agentBin + " --dangerously-skip-permissions"

	// Wrap the command to tee stdout+stderr to a session log file.
	if r.logDir != "" {
		if err := os.MkdirAll(r.logDir, 0o750); err == nil {
			logPath := filepath.Join(r.logDir, sessionID+".log")
			agentCmd = "bash -c " + intShellQuote(
				"( "+agentCmd+" ) 2>&1 | tee "+intShellQuote(logPath)+"; exit ${PIPESTATUS[0]}")
		}
	}

	// Build tmux args: forward only the env vars the session needs.
	args := []string{"new-session", "-d", "-s", sessionID, "-c", dir}
	args = append(args, "-e", "CT_DB="+r.dbPath)
	if path := os.Getenv("PATH"); path != "" {
		args = append(args, "-e", "PATH="+path)
	}
	for k, v := range r.extraEnv {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, agentCmd)

	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-session %s: %w: %s", sessionID, err, out)
	}
	return nil
}

// sessionIDs returns a snapshot of spawned tmux session IDs.
func (r *integrationRunner) sessionIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.sessions))
	copy(out, r.sessions)
	return out
}

// cleanup kills all tmux sessions spawned by this runner.
func (r *integrationRunner) cleanup() {
	r.mu.Lock()
	sessions := r.sessions
	r.mu.Unlock()
	for _, s := range sessions {
		exec.Command("tmux", "kill-session", "-t", s).Run() //nolint:errcheck
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Workflow and scheduler helpers
// ─────────────────────────────────────────────────────────────────────────────

// intWorkflow returns a minimal single-step workflow: implement → done.
func intWorkflow() *aqueduct.Workflow {
	return &aqueduct.Workflow{
		Name: "integration",
		Cataractae: []aqueduct.WorkflowCataractae{
			{
				Name:   "implement",
				Type:   aqueduct.CataractaeTypeAgent,
				OnPass: "done",
				OnFail: "blocked",
			},
		},
	}
}

// intConfig returns a minimal AqueductConfig for integration tests.
func intConfig() aqueduct.AqueductConfig {
	return aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{
				Name:       "myrepo",
				Cataractae: 1,
				Names:      []string{"worker-alpha"},
				Prefix:     "it",
			},
		},
		MaxCataractae: 1,
	}
}

// newIntScheduler creates a Castellarius configured for integration tests with
// short poll and heartbeat intervals to keep test runtime under 30s.
func newIntScheduler(client *cistern.Client, runner castellarius.CataractaeRunner) *castellarius.Castellarius {
	config := intConfig()
	wf := intWorkflow()
	workflows := map[string]*aqueduct.Workflow{"myrepo": wf}
	clients := map[string]castellarius.CisternClient{"myrepo": client}

	return castellarius.NewFromParts(config, workflows, clients, runner,
		castellarius.WithPollInterval(500*time.Millisecond),
		castellarius.WithHeartbeatInterval(time.Second),
		castellarius.WithDrainTimeout(3*time.Second),
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// Polling helper
// ─────────────────────────────────────────────────────────────────────────────

// waitDelivered polls every 200ms until the named droplet reaches 'delivered'
// status or ctx expires.  Returns true on delivery, false on timeout.
func waitDelivered(ctx context.Context, client *cistern.Client, dropletID string) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
		d, err := client.Get(dropletID)
		if err == nil && d != nil && d.Status == "delivered" {
			return true
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Integration tests
// ─────────────────────────────────────────────────────────────────────────────

// TestIntegration_HappyPath_FakeAgentDeliversDroplet verifies the end-to-end
// Castellarius pipeline with real tmux + real SQLite + fakeagent:
//
//	Given: a droplet in the open state
//	When:  Castellarius dispatches it; fakeagent signals pass via ct droplet pass
//	Then:  droplet reaches 'delivered' status within 20s
func TestIntegration_HappyPath_FakeAgentDeliversDroplet(t *testing.T) {
	checkIntegrationPrereqs(t)
	fakeagentPath := buildFakeagent(t)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	client, err := cistern.New(dbPath, "it")
	if err != nil {
		t.Fatalf("cistern.New: %v", err)
	}
	defer client.Close()

	runner := &integrationRunner{
		agentBin: fakeagentPath,
		dbPath:   dbPath,
		logDir:   t.TempDir(),
	}
	t.Cleanup(runner.cleanup)

	sched := newIntScheduler(client, runner)

	droplet, err := client.Add("myrepo", "integration happy path", "desc", 1, 3)
	if err != nil {
		t.Fatalf("client.Add: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	go sched.Run(ctx) //nolint:errcheck

	if !waitDelivered(ctx, client, droplet.ID) {
		d, _ := client.Get(droplet.ID)
		status := "unknown"
		if d != nil {
			status = d.Status
		}
		t.Fatalf("droplet %s did not reach 'delivered' within timeout (status: %s)", droplet.ID, status)
	}
}

// TestIntegration_DeadSession_RecoverInProgress_RedeliversDroplet verifies that a
// droplet left in_progress with no outcome (simulating an agent that died before
// signaling) is recovered and eventually delivered:
//
//	Given: a droplet is in_progress/no-outcome when the Castellarius starts
//	When:  recoverInProgress resets it to open; Castellarius dispatches it
//	Then:  fakeagent signals pass and the droplet reaches 'delivered' within 20s
func TestIntegration_DeadSession_RecoverInProgress_RedeliversDroplet(t *testing.T) {
	checkIntegrationPrereqs(t)
	fakeagentPath := buildFakeagent(t)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	client, err := cistern.New(dbPath, "it")
	if err != nil {
		t.Fatalf("cistern.New: %v", err)
	}
	defer client.Close()

	runner := &integrationRunner{
		agentBin: fakeagentPath,
		dbPath:   dbPath,
		logDir:   t.TempDir(),
	}
	t.Cleanup(runner.cleanup)

	// Add a droplet and put it into in_progress/no-outcome state — simulating a
	// prior Castellarius run where the agent session died before signaling.
	droplet, err := client.Add("myrepo", "recovery test", "desc", 1, 3)
	if err != nil {
		t.Fatalf("client.Add: %v", err)
	}
	// GetReady atomically marks the item in_progress (mimics a prior dispatch).
	if _, err := client.GetReady("myrepo"); err != nil {
		t.Fatalf("client.GetReady: %v", err)
	}
	// Give it a dead assignee so recoverInProgress sees a realistic stale item.
	if err := client.Assign(droplet.ID, "dead-worker", "implement"); err != nil {
		t.Fatalf("client.Assign dead-worker: %v", err)
	}

	// Verify precondition: in_progress with no outcome.
	d, err := client.Get(droplet.ID)
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	if d.Status != "in_progress" || d.Outcome != "" {
		t.Fatalf("precondition failed: want in_progress/no-outcome, got status=%s outcome=%q",
			d.Status, d.Outcome)
	}

	// Start the Castellarius.  recoverInProgress will reset the item to open,
	// then dispatchRepo picks it up and fakeagent delivers it.
	sched := newIntScheduler(client, runner)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	go sched.Run(ctx) //nolint:errcheck

	if !waitDelivered(ctx, client, droplet.ID) {
		d2, _ := client.Get(droplet.ID)
		status := "unknown"
		if d2 != nil {
			status = d2.Status
		}
		t.Fatalf("recovered droplet %s did not reach 'delivered' within timeout (status: %s)",
			droplet.ID, status)
	}
}

// TestIntegration_EnvHygiene_APIKeyNotForwardedToSession verifies that
// ANTHROPIC_API_KEY is NOT present in the environment of a spawned tmux session
// when it is not set in the Castellarius's own environment:
//
//	Given: ANTHROPIC_API_KEY is absent from the runner's env passthrough
//	When:  a session is spawned; fakeagent prints its env (FAKEAGENT_MODE=env_dump)
//	Then:  the session log does not contain ANTHROPIC_API_KEY
func TestIntegration_EnvHygiene_APIKeyNotForwardedToSession(t *testing.T) {
	checkIntegrationPrereqs(t)
	fakeagentPath := buildFakeagent(t)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	client, err := cistern.New(dbPath, "it")
	if err != nil {
		t.Fatalf("cistern.New: %v", err)
	}
	defer client.Close()

	logDir := t.TempDir()

	// The runner explicitly only forwards CT_DB, PATH, and FAKEAGENT_MODE —
	// ANTHROPIC_API_KEY is intentionally excluded even if the caller sets it.
	runner := &integrationRunner{
		agentBin: fakeagentPath,
		dbPath:   dbPath,
		logDir:   logDir,
		extraEnv: map[string]string{
			"FAKEAGENT_MODE": "env_dump", // causes fakeagent to print its env to stdout
		},
	}
	t.Cleanup(runner.cleanup)

	// Set the sentinel value in the test process env.  The runner must not
	// forward it — verifying that callers cannot accidentally leak secrets.
	const sentinelKey = "ANTHROPIC_API_KEY"
	const sentinelVal = "test-secret-must-not-leak"
	t.Setenv(sentinelKey, sentinelVal)

	sched := newIntScheduler(client, runner)

	droplet, err := client.Add("myrepo", "env hygiene test", "desc", 1, 3)
	if err != nil {
		t.Fatalf("client.Add: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	go sched.Run(ctx) //nolint:errcheck

	// Wait for the droplet to be delivered before inspecting the session log.
	if !waitDelivered(ctx, client, droplet.ID) {
		t.Fatalf("droplet %s did not reach 'delivered' within timeout", droplet.ID)
	}

	// Locate the session log written by the runner's tee wrapper.
	sessions := runner.sessionIDs()
	if len(sessions) == 0 {
		t.Fatal("no sessions were spawned")
	}
	logPath := filepath.Join(logDir, sessions[0]+".log")

	// Retry briefly: the tee process may still be flushing after the droplet is delivered.
	var logContent string
	for range 15 {
		data, readErr := os.ReadFile(logPath)
		if readErr == nil && len(data) > 0 {
			logContent = string(data)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if logContent == "" {
		t.Fatalf("session log not found or empty at %s", logPath)
	}

	// Confirm the env dump ran (sanity-check that fakeagent used env_dump mode).
	if !strings.Contains(logContent, "=== FAKEAGENT ENV DUMP ===") {
		t.Errorf("session log missing env dump header; fakeagent may not have run in env_dump mode\nlog:\n%s",
			logContent)
	}

	// Core assertion: the API key must not appear anywhere in the session log.
	if strings.Contains(logContent, sentinelKey) {
		t.Errorf("%s was found in the session log — API key leaked into the tmux session\nlog:\n%s",
			sentinelKey, logContent)
	}
}
