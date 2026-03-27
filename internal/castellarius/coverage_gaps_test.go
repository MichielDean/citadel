package castellarius

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// testDB creates a temporary cistern database and returns its path.
func testDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := cistern.New(dbPath, "test"); err != nil {
		t.Fatalf("cistern.New: %v", err)
	}
	return dbPath
}

// --- AqueductPool gap tests ---

func TestAqueductPool_IsFlowing(t *testing.T) {
	pool := NewAqueductPool("repo", []string{"alpha", "beta"})

	// Neither is flowing initially.
	if pool.IsFlowing("alpha") {
		t.Error("alpha should not be flowing before assignment")
	}
	if pool.IsFlowing("beta") {
		t.Error("beta should not be flowing before assignment")
	}
	if pool.IsFlowing("nonexistent") {
		t.Error("nonexistent aqueduct should not be flowing")
	}

	// Assign alpha — it becomes flowing.
	w := pool.AvailableAqueduct()
	pool.Assign(w, "drop-1", "implement")

	if !pool.IsFlowing("alpha") {
		t.Error("alpha should be flowing after assignment")
	}
	if pool.IsFlowing("beta") {
		t.Error("beta should still be idle")
	}

	// Release alpha — back to idle.
	pool.Release(w)
	if pool.IsFlowing("alpha") {
		t.Error("alpha should be idle after release")
	}
}

func TestAqueductPool_FindAndClaimByName(t *testing.T) {
	pool := NewAqueductPool("repo", []string{"alpha", "beta"})

	// Claim alpha by name — returns it and marks flowing.
	w := pool.FindAndClaimByName("alpha")
	if w == nil {
		t.Fatal("FindAndClaimByName(alpha) returned nil, want non-nil")
	}
	if w.Name != "alpha" {
		t.Errorf("claimed aqueduct name = %q, want %q", w.Name, "alpha")
	}
	if w.Status != AqueductFlowing {
		t.Errorf("claimed aqueduct status = %q, want flowing", w.Status)
	}

	// Trying to claim alpha again while flowing returns nil.
	if pool.FindAndClaimByName("alpha") != nil {
		t.Error("FindAndClaimByName(alpha) on a flowing aqueduct should return nil")
	}

	// Unknown name returns nil.
	if pool.FindAndClaimByName("nonexistent") != nil {
		t.Error("FindAndClaimByName(nonexistent) should return nil")
	}

	// Beta is still available.
	wb := pool.FindAndClaimByName("beta")
	if wb == nil || wb.Name != "beta" {
		t.Errorf("FindAndClaimByName(beta) = %v, want beta", wb)
	}
}

func TestAqueductPool_AvailableAqueductExcluding(t *testing.T) {
	pool := NewAqueductPool("repo", []string{"alpha", "beta", "gamma"})

	// Exclude alpha and beta — should get gamma.
	w := pool.AvailableAqueductExcluding(map[string]bool{"alpha": true, "beta": true})
	if w == nil || w.Name != "gamma" {
		t.Errorf("AvailableAqueductExcluding = %v, want gamma", w)
	}

	// Exclude all three — returns nil.
	w2 := pool.AvailableAqueductExcluding(map[string]bool{"alpha": true, "beta": true, "gamma": true})
	if w2 != nil {
		t.Errorf("AvailableAqueductExcluding all = %v, want nil", w2)
	}

	// Assign alpha; AvailableAqueductExcluding with empty exclude skips it as flowing.
	w0 := pool.AvailableAqueduct()
	if w0 == nil {
		t.Fatal("AvailableAqueduct returned nil before any assignment")
	}
	pool.Assign(w0, "drop-1", "implement")
	// alpha is now flowing — available excluding {} returns beta (first idle).
	w3 := pool.AvailableAqueductExcluding(map[string]bool{})
	if w3 == nil {
		t.Fatal("AvailableAqueductExcluding with empty exclude should return an idle aqueduct")
	}
	if w3.Name == "alpha" {
		t.Error("AvailableAqueductExcluding should not return a flowing aqueduct")
	}
}

// --- isSupervisedProcess tests ---

func TestIsSupervisedProcess_EnvVars(t *testing.T) {
	tests := []struct {
		envVar, value string
	}{
		{"CT_SUPERVISED", "1"},
		{"INVOCATION_ID", "some-systemd-id"},
		{"SUPERVISOR_ENABLED", "1"},
	}
	for _, tc := range tests {
		t.Run(tc.envVar, func(t *testing.T) {
			t.Setenv(tc.envVar, tc.value)
			if !isSupervisedProcess() {
				t.Errorf("%s=%s should be detected as supervised", tc.envVar, tc.value)
			}
		})
	}
}

func TestIsSupervisedProcess_NotSupervised(t *testing.T) {
	// Clear all supervisor environment variables.
	t.Setenv("CT_SUPERVISED", "")
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("SUPERVISOR_ENABLED", "")
	// When env vars are cleared and ppid != 1, the function must return false.
	// Skip the assertion if ppid == 1 (running inside Docker or as a PID1 child).
	if os.Getppid() != 1 && isSupervisedProcess() {
		t.Error("isSupervisedProcess() = true with all env vars cleared and ppid != 1")
	}
}

// --- WithLogger / WithPollInterval option tests ---

func TestWithLogger_Option(t *testing.T) {
	client := newMockClient()
	sched := testScheduler(client, newMockRunner(client))
	customLogger := slog.Default()

	WithLogger(customLogger)(sched)

	if sched.logger != customLogger {
		t.Error("WithLogger did not set the logger")
	}
}

func TestWithPollInterval_Option(t *testing.T) {
	client := newMockClient()
	interval := 42 * time.Second

	sched := testScheduler(client, newMockRunner(client))
	WithPollInterval(interval)(sched)

	if sched.pollInterval != interval {
		t.Errorf("WithPollInterval = %v, want %v", sched.pollInterval, interval)
	}
}

// --- purgeOldItems tests ---

// purgeTrackingClient wraps mockClient and tracks Purge calls.
type purgeTrackingClient struct {
	*mockClient
	purgeCalls int
	purgeN     int
}

func (p *purgeTrackingClient) Purge(olderThan time.Duration, dryRun bool) (int, error) {
	p.purgeCalls++
	return p.purgeN, nil
}

func TestPurgeOldItems_CallsPurgeOnAllRepos(t *testing.T) {
	mc := &purgeTrackingClient{mockClient: newMockClient(), purgeN: 2}
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": mc}
	sched := NewFromParts(config, workflows, clients, newMockRunner(mc.mockClient))

	sched.purgeOldItems()

	if mc.purgeCalls != 1 {
		t.Errorf("Purge called %d times, want 1", mc.purgeCalls)
	}
}

func TestPurgeOldItems_DefaultRetentionDays(t *testing.T) {
	mc := &purgeTrackingClient{mockClient: newMockClient()}
	config := testConfig() // RetentionDays = 0 → default 90
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": mc}
	sched := NewFromParts(config, workflows, clients, newMockRunner(mc.mockClient))

	sched.purgeOldItems()

	if mc.purgeCalls != 1 {
		t.Errorf("purgeOldItems with default retention should call Purge once, got %d", mc.purgeCalls)
	}
}

// --- recoverInProgress tests ---

func TestRecoverInProgress(t *testing.T) {
	tests := []struct {
		name     string
		item     *cistern.Droplet
		wantStep string
	}{
		{
			name: "item with outcome not reset",
			item: &cistern.Droplet{
				ID: "r1", CurrentCataractae: "implement", Status: "in_progress",
				Assignee: "alpha", Outcome: "pass",
			},
			wantStep: "", // not touched — observe phase handles it
		},
		{
			name: "item without outcome reset to current step",
			item: &cistern.Droplet{
				ID: "r2", CurrentCataractae: "review", Status: "in_progress",
				Assignee: "alpha", Outcome: "",
			},
			wantStep: "review",
		},
		{
			name: "empty step falls back to first workflow step",
			item: &cistern.Droplet{
				ID: "r3", CurrentCataractae: "", Status: "in_progress",
				Assignee: "alpha", Outcome: "",
			},
			wantStep: "implement",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newMockClient()
			client.items[tc.item.ID] = tc.item
			sched := testScheduler(client, newMockRunner(client))
			sched.recoverInProgress()
			client.mu.Lock()
			defer client.mu.Unlock()
			if client.steps[tc.item.ID] != tc.wantStep {
				t.Errorf("step = %q, want %q", client.steps[tc.item.ID], tc.wantStep)
			}
		})
	}
}

// --- heartbeatRepo tests ---

func TestHeartbeatRepo(t *testing.T) {
	tests := []struct {
		name      string
		item      *cistern.Droplet
		wantNotes int // number of stall notes appended
	}{
		{
			name: "writes stall note for droplet with no recent signals",
			item: &cistern.Droplet{
				ID: "hb-1", CurrentCataractae: "implement", Status: "in_progress",
				Assignee: "", Outcome: "",
				// No notes, no worktree, no session log — zero signals → stalled
				// (default 45-min threshold; zero time is far older than 45 min).
			},
			wantNotes: 1,
		},
		{
			name: "skips item with outcome",
			item: &cistern.Droplet{
				ID: "hb-2", CurrentCataractae: "review", Status: "in_progress",
				Assignee: "", Outcome: "pass",
			},
			wantNotes: 0, // observe phase handles items with outcomes
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := newMockClient()
			client.items[tc.item.ID] = tc.item
			sched := testScheduler(client, newMockRunner(client))
			sched.heartbeatRepo(context.Background(), sched.config.Repos[0])
			client.mu.Lock()
			defer client.mu.Unlock()
			if len(client.attached) != tc.wantNotes {
				t.Errorf("stall notes = %d, want %d", len(client.attached), tc.wantNotes)
			}
		})
	}
}

func TestHeartbeatRepo_StallDetected_ForAssignedDroplet(t *testing.T) {
	// A droplet assigned to a worker with no recent signals should receive a
	// stall note. Mock tmux as alive so the liveness check passes through to
	// the stall detector.
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = orig })

	client := newMockClient()
	item := &cistern.Droplet{
		ID:                "hb-assigned-stall",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		Outcome:           "",
	}
	client.items[item.ID] = item

	sched := testScheduler(client, newMockRunner(client))
	sched.heartbeatRepo(context.Background(), sched.config.Repos[0])

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.attached) != 1 {
		t.Errorf("expected 1 stall note for assigned droplet with no signals, got %d", len(client.attached))
	}
}

func TestHeartbeatRepo_ActiveDroplet_NotStalled(t *testing.T) {
	// A droplet whose newest note signal is within the stall threshold should
	// not receive a stall note.
	client := newMockClient()
	item := &cistern.Droplet{
		ID:                "hb-active",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		Outcome:           "",
	}
	client.items[item.ID] = item
	// Recent note signal: 1 minute ago, well within the 45-minute default threshold.
	client.notes[item.ID] = []cistern.CataractaeNote{
		{CreatedAt: time.Now().Add(-1 * time.Minute)},
	}

	sched := testScheduler(client, newMockRunner(client))
	sched.heartbeatRepo(context.Background(), sched.config.Repos[0])

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.attached) != 0 {
		t.Errorf("expected 0 stall notes for active droplet, got %d", len(client.attached))
	}
}

func TestHeartbeatRepo_UnknownAssignee_WritesStallNote(t *testing.T) {
	// A droplet with an unknown assignee and no recent signals should receive
	// a stall note. Mock tmux as alive so liveness check passes through.
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = orig })

	client := newMockClient()
	item := &cistern.Droplet{
		ID:                "hb-unknown",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "removed-aqueduct", // not in pool
		Outcome:           "",
	}
	client.items[item.ID] = item

	sched := testScheduler(client, newMockRunner(client))
	sched.heartbeatRepo(context.Background(), sched.config.Repos[0])

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.attached) != 1 {
		t.Errorf("expected 1 stall note for unknown-assignee droplet with no signals, got %d", len(client.attached))
	}
}

func TestHeartbeatInProgress_CallsHeartbeatForAllRepos(t *testing.T) {
	// Basic smoke test: heartbeatInProgress should iterate all repos without
	// panic and write a stall note for a droplet with no recent signals.
	client := newMockClient()
	item := &cistern.Droplet{
		ID:                "hb-all-1",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "",
		Outcome:           "",
	}
	client.items["hb-all-1"] = item

	sched := testScheduler(client, newMockRunner(client))
	sched.heartbeatInProgress(context.Background())

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.attached) != 1 {
		t.Errorf("heartbeatInProgress: expected 1 stall note for stalled item, got %d", len(client.attached))
	}
}

// --- worktreeRegistered test ---

func TestWorktreeRegistered_NonGitDir_ReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	// A non-git directory: git worktree list will fail → returns false.
	if worktreeRegistered(dir, "/some/worktree/path") {
		t.Error("worktreeRegistered should return false for a non-git directory")
	}
}

// --- removeDropletWorktree tests ---

func TestRemoveDropletWorktree_NonGitDir_NoOp(t *testing.T) {
	// Calling removeDropletWorktree on a non-git directory ignores the error.
	// The key behavior is that it does not panic or crash.
	primaryDir := t.TempDir()
	sandboxRoot := t.TempDir()
	removeDropletWorktree(primaryDir, sandboxRoot, "myrepo", "drop-noop")
}

// TestRemoveDropletWorktree_DeletesBranchFromPrimary verifies that removing a
// worktree also deletes the feat/<id> branch from the primary clone so branches
// do not accumulate permanently.
func TestRemoveDropletWorktree_DeletesBranchFromPrimary(t *testing.T) {
	primaryDir := makeBareAndClone(t)
	sandboxRoot := t.TempDir()

	// Create a worktree on feat/drop-rm.
	worktreePath, err := prepareDropletWorktree(primaryDir, sandboxRoot, "myrepo", "drop-rm")
	if err != nil {
		t.Fatalf("prepareDropletWorktree: %v", err)
	}
	if !branchExists(t, primaryDir, "feat/drop-rm") {
		t.Fatal("feat/drop-rm should exist in primary after prepareDropletWorktree")
	}
	if _, statErr := os.Stat(worktreePath); statErr != nil {
		t.Fatalf("worktree path should exist: %v", statErr)
	}

	removeDropletWorktree(primaryDir, sandboxRoot, "myrepo", "drop-rm")

	if branchExists(t, primaryDir, "feat/drop-rm") {
		t.Error("feat/drop-rm should have been deleted from primary by removeDropletWorktree")
	}
}

// --- hookTmpCleanup test ---

func TestHookTmpCleanup_Succeeds(t *testing.T) {
	// hookTmpCleanup removes ct-diff-*, ct-review-*, and ct-qa-* dirs older than 2h.
	// Dirs younger than 2h are left in place; this test verifies no error is returned
	// regardless of what is currently present in /tmp.
	if err := hookTmpCleanup(discardLogger()); err != nil {
		t.Errorf("hookTmpCleanup: unexpected error: %v", err)
	}
}

// --- hookDBVacuum tests ---

func TestHookDBVacuum_EmptyPath_ReturnsError(t *testing.T) {
	if err := hookDBVacuum("", discardLogger()); err == nil {
		t.Error("hookDBVacuum with empty path should return an error")
	}
}

func TestHookDBVacuum_ValidDB_Succeeds(t *testing.T) {
	if err := hookDBVacuum(testDB(t), discardLogger()); err != nil {
		t.Errorf("hookDBVacuum on valid DB: %v", err)
	}
}

// --- hookEventsPrune tests ---

func TestHookEventsPrune(t *testing.T) {
	tests := []struct {
		name     string
		useDB    bool
		keepDays int
		wantErr  bool
	}{
		{"empty path returns error", false, 30, true},
		{"valid DB with keep_days 30", true, 30, false},
		{"default keep_days (zero)", true, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := ""
			if tc.useDB {
				dbPath = testDB(t)
			}
			hook := aqueduct.DroughtHook{Name: "test", Action: "events_prune", KeepDays: tc.keepDays}
			err := hookEventsPrune(dbPath, hook, discardLogger())
			if (err != nil) != tc.wantErr {
				t.Errorf("hookEventsPrune() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

// --- RunDroughtHooks tests ---

func TestRunDroughtHooks_Actions(t *testing.T) {
	tests := []struct {
		name  string
		hooks []aqueduct.DroughtHook
		useDB bool
	}{
		{"db_vacuum", []aqueduct.DroughtHook{{Name: "v", Action: "db_vacuum"}}, true},
		{"events_prune", []aqueduct.DroughtHook{{Name: "p", Action: "events_prune", KeepDays: 30}}, true},
		{"tmp_cleanup", []aqueduct.DroughtHook{{Name: "t", Action: "tmp_cleanup"}}, false},
		{"unknown_action", []aqueduct.DroughtHook{{Name: "u", Action: "completely_unknown_action"}}, false},
		{"restart_self_unsupervised", []aqueduct.DroughtHook{{Name: "r", Action: "restart_self"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := ""
			if tc.useDB {
				dbPath = testDB(t)
			}
			// Must not panic.
			RunDroughtHooks(DroughtHookParams{
				Hooks:       tc.hooks,
				Config:      &aqueduct.AqueductConfig{},
				DBPath:      dbPath,
				SandboxRoot: t.TempDir(),
				Logger:      discardLogger(),
			})
		})
	}
}

// RestartSelf with an onReload callback and unsupervised mode: onReload must NOT be
// called because restart_self does not set workflowChanged.
func TestRunDroughtHooks_RestartSelf_OnReloadNotCalled(t *testing.T) {
	reloadCalled := false
	hooks := []aqueduct.DroughtHook{{Name: "restart", Action: "restart_self"}}
	RunDroughtHooks(DroughtHookParams{
		Hooks:       hooks,
		Config:      &aqueduct.AqueductConfig{},
		SandboxRoot: t.TempDir(),
		Logger:      discardLogger(),
		OnReload:    func() { reloadCalled = true },
	})
	if reloadCalled {
		t.Error("onReload should not be called for restart_self without workflowChanged")
	}
}

// --- checkStuckDeliveries tests ---

func TestCheckStuckDeliveries_NoDeliveryItems_NoOp(t *testing.T) {
	client := newMockClient()
	// No items in the queue — should be a no-op.
	sched := testScheduler(client, newMockRunner(client))
	sched.checkStuckDeliveries(context.Background())
}

func TestCheckStuckDeliveries_ItemNotPastThreshold_Skipped(t *testing.T) {
	client := newMockClient()
	// An in_progress delivery item that is recent (well within threshold).
	item := &cistern.Droplet{
		ID:                "sd-skip",
		CurrentCataractae: "delivery",
		Status:            "in_progress",
		Assignee:          "alpha",
		Outcome:           "",
		UpdatedAt:         time.Now(), // just updated — not past threshold
	}
	client.items["sd-skip"] = item

	sched := testScheduler(client, newMockRunner(client))
	sched.checkStuckDeliveries(context.Background())

	client.mu.Lock()
	defer client.mu.Unlock()
	// Item should not have been touched.
	if client.steps["sd-skip"] != "" {
		t.Errorf("recent delivery item should not be reset, got step %q", client.steps["sd-skip"])
	}
}

func TestCheckStuckDeliveries_ItemPastThreshold_DeadSession_Skipped(t *testing.T) {
	client := newMockClient()
	// Item is well past the default stuck threshold (67.5m) but the tmux session
	// is dead (no tmux in test env) → isTmuxAlive returns false → recovery skipped.
	item := &cistern.Droplet{
		ID:                "sd-past",
		CurrentCataractae: "delivery",
		Status:            "in_progress",
		Assignee:          "alpha",
		Outcome:           "",
		UpdatedAt:         time.Now().Add(-2 * time.Hour),
	}
	client.items["sd-past"] = item

	sched := testScheduler(client, newMockRunner(client))
	sched.checkStuckDeliveries(context.Background())

	client.mu.Lock()
	defer client.mu.Unlock()
	// Session is dead → item not modified.
	if client.steps["sd-past"] != "" {
		t.Errorf("past-threshold item with dead session should not be modified, got step %q", client.steps["sd-past"])
	}
}

func TestCheckStuckDeliveries_CancelledContext_Returns(t *testing.T) {
	client := newMockClient()
	sched := testScheduler(client, newMockRunner(client))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	// Should return immediately without panicking.
	sched.checkStuckDeliveries(ctx)
}

// --- doReloadWorkflows tests ---

func TestDoReloadWorkflows_ValidFile_UpdatesWorkflow(t *testing.T) {
	// Write a valid workflow YAML to a temp file.
	wfContent := `name: feature
cataractae:
  - name: implement
    type: agent
    identity: implementer
    on_pass: done
    on_fail: blocked
`
	wfPath := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(wfContent), 0o644); err != nil {
		t.Fatal(err)
	}

	config := aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "test-repo", WorkflowPath: wfPath, Cataractae: 1, Names: []string{"alpha"}, Prefix: "test"},
		},
		MaxCataractae: 1,
	}
	client := newMockClient()
	sched := NewFromParts(config,
		map[string]*aqueduct.Workflow{"test-repo": testWorkflow()},
		map[string]CisternClient{"test-repo": client},
		newMockRunner(client))

	sched.doReloadWorkflows()

	// Workflow should have been updated to the one from the file (1 step: implement).
	wf := sched.workflows["test-repo"]
	if wf == nil {
		t.Fatal("workflow should not be nil after reload")
	}
	if len(wf.Cataractae) != 1 {
		t.Errorf("reloaded workflow should have 1 cataractae, got %d", len(wf.Cataractae))
	}
	if wf.Cataractae[0].Name != "implement" {
		t.Errorf("reloaded workflow step = %q, want implement", wf.Cataractae[0].Name)
	}
}

func TestDoReloadWorkflows_InvalidFile_KeepsOldWorkflow(t *testing.T) {
	wfPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(wfPath, []byte("not: valid: yaml: {{{\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	config := aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "test-repo", WorkflowPath: wfPath, Cataractae: 1, Names: []string{"alpha"}, Prefix: "test"},
		},
		MaxCataractae: 1,
	}
	original := testWorkflow()
	client := newMockClient()
	sched := NewFromParts(config,
		map[string]*aqueduct.Workflow{"test-repo": original},
		map[string]CisternClient{"test-repo": client},
		newMockRunner(client))

	sched.doReloadWorkflows()

	// Old workflow should be preserved on parse error.
	if sched.workflows["test-repo"] != original {
		t.Error("workflow should not be replaced on parse failure")
	}
}

// --- dirtyNonContextFiles tests ---

// makeSimpleGitRepo creates a git repo at a temp dir with one initial commit.
// Uses branchGitCmd/branchMustRun helpers from branch_lifecycle_test.go.
func makeSimpleGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	branchMustRun(t, branchGitCmd(dir, "init"))
	branchMustRun(t, branchGitCmd(dir, "config", "user.email", "test@test.com"))
	branchMustRun(t, branchGitCmd(dir, "config", "user.name", "Test"))
	branchMustRun(t, branchGitCmd(dir, "config", "commit.gpgsign", "false"))
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(dir, "add", "."))
	branchMustRun(t, branchGitCmd(dir, "commit", "-m", "initial"))
	return dir
}

func TestDirtyNonContextFiles_CleanRepo_Empty(t *testing.T) {
	dir := makeSimpleGitRepo(t)
	dirty, err := dirtyNonContextFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 0 {
		t.Errorf("clean repo should have no dirty files, got %v", dirty)
	}
}

func TestDirtyNonContextFiles_UntrackedFile_Ignored(t *testing.T) {
	dir := makeSimpleGitRepo(t)
	// Untracked file ("??" prefix in git status --porcelain) should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "untracked.go"), []byte("// new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := dirtyNonContextFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 0 {
		t.Errorf("untracked files should be ignored, got %v", dirty)
	}
}

func TestDirtyNonContextFiles_ModifiedNonContext_Reported(t *testing.T) {
	dir := makeSimpleGitRepo(t)
	// Modify a tracked file (not CONTEXT.md) — should appear as dirty.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := dirtyNonContextFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) == 0 {
		t.Error("modified tracked file should appear in dirty list, got empty")
	}
}

func TestDirtyNonContextFiles_OnlyContextMd_Empty(t *testing.T) {
	dir := makeSimpleGitRepo(t)
	// Add CONTEXT.md to the repo first so it's tracked.
	if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte("context\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(dir, "add", "CONTEXT.md"))
	branchMustRun(t, branchGitCmd(dir, "commit", "-m", "add context"))
	// Now modify CONTEXT.md — it should be excluded from the dirty list.
	if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte("updated context\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := dirtyNonContextFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range dirty {
		if strings.Contains(f, "CONTEXT.md") {
			t.Errorf("CONTEXT.md should be excluded from dirty list, got %v", dirty)
		}
	}
}

func TestDirtyNonContextFiles_NonGitDir_ReturnsError(t *testing.T) {
	dir := t.TempDir() // not a git repo
	dirty, err := dirtyNonContextFiles(dir)
	if err == nil {
		t.Error("expected error for non-git directory, got nil")
	}
	if dirty != nil {
		t.Errorf("expected nil files on error, got %v", dirty)
	}
}
