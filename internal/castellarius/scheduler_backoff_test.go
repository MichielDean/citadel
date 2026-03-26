package castellarius

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// --- heartbeat quick-exit detection tests ---

// TestHeartbeatRepo_QuickExit_LogsBackoff verifies that heartbeatRepo emits a
// "droplet backing off after quick exit" log when a session with tmux_dead
// stallReason had a short duration (within the quick-exit threshold).
func TestHeartbeatRepo_QuickExit_LogsBackoff(t *testing.T) {
	var buf bytes.Buffer
	client := newMockClient()

	// Droplet died within the quick-exit window (5s ago — well under 30s default).
	item := &cistern.Droplet{
		ID:                "qe-backoff",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		UpdatedAt:         time.Now().Add(-25 * time.Second),
	}
	client.items[item.ID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner,
		WithLogger(newTestLogger(&buf)))

	// Mark alpha as flowing so the pool check falls through to the tmux check.
	pool := sched.pools["test-repo"]
	if w := pool.FindByName("alpha"); w != nil {
		pool.Assign(w, item.ID, "implement")
	}

	// The tmux session "test-repo-alpha" does not exist → stallReason=tmux_dead.
	sched.heartbeatRepo(context.Background(), config.Repos[0])

	out := buf.String()
	if !strings.Contains(out, "droplet backing off after quick exit") {
		t.Errorf("expected backoff log; got: %s", out)
	}
	if !strings.Contains(out, "qe-backoff") {
		t.Errorf("backoff log should include droplet ID; got: %s", out)
	}
	if !strings.Contains(out, "backoff=") {
		t.Errorf("backoff log should include backoff duration; got: %s", out)
	}
	if !strings.Contains(out, "consecutive_exits=1") {
		t.Errorf("backoff log should show consecutive_exits=1; got: %s", out)
	}
}

// TestHeartbeatRepo_QuickExit_RecordsInTracker verifies that the quick-exit
// event is reflected in the tracker's state after heartbeatRepo runs.
func TestHeartbeatRepo_QuickExit_RecordsInTracker(t *testing.T) {
	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "qe-tracker",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		UpdatedAt:         time.Now().Add(-25 * time.Second),
	}
	client.items[item.ID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	pool := sched.pools["test-repo"]
	if w := pool.FindByName("alpha"); w != nil {
		pool.Assign(w, item.ID, "implement")
	}

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Tracker should have 1 consecutive exit and a non-zero backoff.
	if n := sched.quickExitBackoff.consecutiveExits(item.ID); n != 1 {
		t.Errorf("expected 1 consecutive exit, got %d", n)
	}
	if remaining := sched.quickExitBackoff.currentBackoff(item.ID); remaining <= 0 {
		t.Errorf("expected positive backoff remaining, got %v", remaining)
	}
}

// TestHeartbeatRepo_LongSession_NoBackoff confirms that a session that ran
// longer than the quick-exit threshold does NOT trigger backoff.
func TestHeartbeatRepo_LongSession_NoBackoff(t *testing.T) {
	var buf bytes.Buffer
	client := newMockClient()

	// Session ran for 2 minutes — well above the 30s threshold.
	item := &cistern.Droplet{
		ID:                "long-session",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		UpdatedAt:         time.Now().Add(-2 * time.Minute),
	}
	client.items[item.ID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner,
		WithLogger(newTestLogger(&buf)))

	pool := sched.pools["test-repo"]
	if w := pool.FindByName("alpha"); w != nil {
		pool.Assign(w, item.ID, "implement")
	}

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Should still reset the stall, but NOT log backoff.
	out := buf.String()
	if strings.Contains(out, "droplet backing off") {
		t.Errorf("long session should not trigger backoff; got: %s", out)
	}
	if n := sched.quickExitBackoff.consecutiveExits(item.ID); n != 0 {
		t.Errorf("long session should not record quick exit, got %d exits", n)
	}
}

// TestHeartbeatRepo_NoAssignee_NoBackoff confirms that no_assignee stalls do
// not trigger the quick-exit backoff path (only tmux_dead does).
func TestHeartbeatRepo_NoAssignee_NoBackoff(t *testing.T) {
	var buf bytes.Buffer
	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "no-assignee-drop",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "",
		UpdatedAt:         time.Now().Add(-25 * time.Second),
	}
	client.items[item.ID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner,
		WithLogger(newTestLogger(&buf)))

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	out := buf.String()
	if strings.Contains(out, "droplet backing off") {
		t.Errorf("no_assignee stall should not trigger backoff; got: %s", out)
	}
	if n := sched.quickExitBackoff.consecutiveExits(item.ID); n != 0 {
		t.Errorf("no_assignee stall should not record quick exit, got %d", n)
	}
}

// --- provider degradation detection tests ---

// TestHeartbeatRepo_ProviderDegradation_LogsWhenDetected verifies that when
// quick exits from multiple aqueducts cross the threshold within the window,
// a "provider appears degraded" log is emitted.
func TestHeartbeatRepo_ProviderDegradation_LogsWhenDetected(t *testing.T) {
	var buf bytes.Buffer
	client := newMockClient()

	// Two prior quick-exit events on the same provider from different aqueducts
	// injected directly — alpha has 2 events, beta has 1 (total 3 events, 2 aqueducts).
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner,
		WithLogger(newTestLogger(&buf)))

	// Pre-seed two events (alpha × 2) so the next event from beta triggers degradation.
	providerName := sched.resolveProviderName("test-repo")
	now := time.Now()
	sched.quickExitBackoff.mu.Lock()
	sched.quickExitBackoff.providerEvents[providerName] = []providerEvent{
		{at: now, aqueductName: "alpha"},
		{at: now, aqueductName: "alpha"},
	}
	sched.quickExitBackoff.mu.Unlock()

	// Heartbeat detects a fresh tmux_dead quick exit from beta — triggers degradation.
	item := &cistern.Droplet{
		ID:                "degrade-trigger",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "beta",
		UpdatedAt:         time.Now().Add(-25 * time.Second),
	}
	client.items[item.ID] = item

	pool := sched.pools["test-repo"]
	if w := pool.FindByName("beta"); w != nil {
		pool.Assign(w, item.ID, "implement")
	}

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	out := buf.String()
	if !strings.Contains(out, "provider appears degraded") {
		t.Errorf("expected provider degradation log; got: %s", out)
	}
	if !sched.quickExitBackoff.isProviderDegraded(providerName) {
		t.Error("provider should be degraded after threshold")
	}
}

// --- dispatch-time backoff enforcement tests ---

// TestDispatch_DropletsInBackoff_NotSpawned verifies that a droplet in its
// backoff window is not spawned on Tick — it is returned to open status.
func TestDispatch_DropletsInBackoff_NotSpawned(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "backed-off-drop", Title: "test item"},
	}
	client.items["backed-off-drop"] = &cistern.Droplet{
		ID:                "backed-off-drop",
		Title:             "test item",
		CurrentCataractae: "implement",
		Status:            "open",
	}

	runner := newMockRunner(client)
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)

	// Inject an active backoff for the droplet.
	sched.quickExitBackoff.mu.Lock()
	sched.quickExitBackoff.dropletExits["backed-off-drop"] = 1
	sched.quickExitBackoff.dropletBackoffUntil["backed-off-drop"] = time.Now().Add(30 * time.Minute)
	sched.quickExitBackoff.mu.Unlock()

	sched.Tick(context.Background())

	// The dispatch goroutine should have returned early without calling Spawn.
	// Allow time for the goroutine to run.
	if runner.waitCalls(1, 200*time.Millisecond) {
		runner.mu.Lock()
		n := len(runner.calls)
		runner.mu.Unlock()
		t.Errorf("backed-off droplet should not be spawned; got %d Spawn call(s)", n)
	}

	// Droplet should be back to open status after being returned.
	client.mu.Lock()
	item := client.items["backed-off-drop"]
	status := ""
	if item != nil {
		status = item.Status
	}
	client.mu.Unlock()
	if status != "open" {
		t.Errorf("backed-off droplet should be returned to open status, got %q", status)
	}
}

// TestDispatch_DropletsWithDegradedProvider_FastForwardedToMax verifies that
// when the provider is degraded, a dispatched droplet is fast-forwarded to max
// backoff and not spawned.
func TestDispatch_DropletsWithDegradedProvider_FastForwardedToMax(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "degrade-drop", Title: "provider degraded"},
	}
	client.items["degrade-drop"] = &cistern.Droplet{
		ID:                "degrade-drop",
		Title:             "provider degraded",
		CurrentCataractae: "implement",
		Status:            "open",
	}

	runner := newMockRunner(client)
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)

	// Mark the provider as degraded.
	providerName := sched.resolveProviderName("test-repo")
	sched.quickExitBackoff.mu.Lock()
	sched.quickExitBackoff.providerDegraded[providerName] = true
	sched.quickExitBackoff.mu.Unlock()

	sched.Tick(context.Background())

	if runner.waitCalls(1, 200*time.Millisecond) {
		runner.mu.Lock()
		n := len(runner.calls)
		runner.mu.Unlock()
		t.Errorf("droplet with degraded provider should not be spawned; got %d call(s)", n)
	}

	// The droplet should now be at max backoff.
	remaining := sched.quickExitBackoff.currentBackoff("degrade-drop")
	if remaining < 29*time.Minute {
		t.Errorf("degraded provider: expected ~30m backoff, got %v", remaining)
	}
}

// --- observe-phase recovery tests ---

// TestObserveRepo_ClearsBackoffOnOutcome verifies that when a droplet writes an
// outcome, its per-droplet backoff state is cleared.
func TestObserveRepo_ClearsBackoffOnOutcome(t *testing.T) {
	client := newMockClient()
	item := &cistern.Droplet{
		ID:                "observe-drop",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		Outcome:           "pass",
	}
	client.items[item.ID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	// Pre-inject backoff state for the droplet.
	sched.quickExitBackoff.mu.Lock()
	sched.quickExitBackoff.dropletExits[item.ID] = 3
	sched.quickExitBackoff.dropletBackoffUntil[item.ID] = time.Now().Add(10 * time.Minute)
	sched.quickExitBackoff.mu.Unlock()

	sched.observeRepo(context.Background(), config.Repos[0])

	if n := sched.quickExitBackoff.consecutiveExits(item.ID); n != 0 {
		t.Errorf("after observe with outcome: expected 0 consecutive exits, got %d", n)
	}
	if remaining := sched.quickExitBackoff.currentBackoff(item.ID); remaining != 0 {
		t.Errorf("after observe with outcome: expected 0 backoff, got %v", remaining)
	}
}

// TestObserveRepo_LogsProviderRecovery verifies that the "provider recovered"
// log is emitted when the first successful outcome clears a degraded provider.
func TestObserveRepo_LogsProviderRecovery(t *testing.T) {
	var buf bytes.Buffer
	client := newMockClient()
	item := &cistern.Droplet{
		ID:                "recover-drop",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		Outcome:           "pass",
	}
	client.items[item.ID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner,
		WithLogger(newTestLogger(&buf)))

	// Mark the provider as degraded.
	providerName := sched.resolveProviderName("test-repo")
	sched.quickExitBackoff.mu.Lock()
	sched.quickExitBackoff.providerDegraded[providerName] = true
	sched.quickExitBackoff.mu.Unlock()

	sched.observeRepo(context.Background(), config.Repos[0])

	out := buf.String()
	if !strings.Contains(out, "provider recovered") {
		t.Errorf("expected provider recovery log; got: %s", out)
	}
	if sched.quickExitBackoff.isProviderDegraded(providerName) {
		t.Error("provider should be marked recovered after first successful session")
	}
}

// --- configuration tests ---

// TestNewFromParts_ReadsBackoffConfig verifies that QuickExitThresholdSeconds
// and MaxBackoffMinutes are read from AqueductConfig when constructing via New.
func TestNewFromParts_ReadsBackoffConfig(t *testing.T) {
	config := testConfig()
	config.QuickExitThresholdSeconds = 60
	config.MaxBackoffMinutes = 5

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": newMockClient()}
	sched := NewFromParts(config, workflows, clients, newMockRunner(newMockClient()))

	if sched.quickExitBackoff.quickExitThreshold != 60*time.Second {
		t.Errorf("quickExitThreshold: want 60s, got %v", sched.quickExitBackoff.quickExitThreshold)
	}
	if sched.quickExitBackoff.maxBackoff != 5*time.Minute {
		t.Errorf("maxBackoff: want 5m, got %v", sched.quickExitBackoff.maxBackoff)
	}
}

// TestNewFromParts_DefaultsWhenConfigZero verifies that zero config values fall
// back to package defaults (30s threshold, 30m max backoff).
func TestNewFromParts_DefaultsWhenConfigZero(t *testing.T) {
	config := testConfig()
	// Leave QuickExitThresholdSeconds and MaxBackoffMinutes at zero.

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": newMockClient()}
	sched := NewFromParts(config, workflows, clients, newMockRunner(newMockClient()))

	if sched.quickExitBackoff.quickExitThreshold != defaultQuickExitThreshold {
		t.Errorf("want default threshold %v, got %v",
			defaultQuickExitThreshold, sched.quickExitBackoff.quickExitThreshold)
	}
	if sched.quickExitBackoff.maxBackoff != defaultMaxBackoff {
		t.Errorf("want default max backoff %v, got %v",
			defaultMaxBackoff, sched.quickExitBackoff.maxBackoff)
	}
}

// TestResolveProviderName_FallsBackToClaude verifies the fallback for repos
// with no explicit provider configuration.
func TestResolveProviderName_FallsBackToClaude(t *testing.T) {
	config := testConfig() // no provider configured
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": newMockClient()}
	sched := NewFromParts(config, workflows, clients, newMockRunner(newMockClient()))

	got := sched.resolveProviderName("test-repo")
	if got != "claude" {
		t.Errorf("resolveProviderName(no config): want %q, got %q", "claude", got)
	}
}

// TestResolveProviderName_UnknownRepo_FallsBackToClaude verifies that an
// unknown repo name also returns the default.
func TestResolveProviderName_UnknownRepo_FallsBackToClaude(t *testing.T) {
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": newMockClient()}
	sched := NewFromParts(config, workflows, clients, newMockRunner(newMockClient()))

	got := sched.resolveProviderName("nonexistent-repo")
	if got != "claude" {
		t.Errorf("resolveProviderName(unknown repo): want %q, got %q", "claude", got)
	}
}
