package castellarius

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// captureHandler is a minimal slog.Handler that records log entries for assertions.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *captureHandler) hasWarn() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == slog.LevelWarn {
			return true
		}
	}
	return false
}

// --- test helpers ---

// stuckDeliveryWorkflow returns a workflow with a delivery step configured with
// a 60-minute timeout (yields a 90-minute stuck threshold).
func stuckDeliveryWorkflow() *aqueduct.Workflow {
	return &aqueduct.Workflow{
		Name: "feature",
		Cataractae: []aqueduct.WorkflowCataractae{
			{Name: "implement", Type: aqueduct.CataractaeTypeAgent, OnPass: "delivery", OnFail: "pooled"},
			{
				Name:           "delivery",
				Type:           aqueduct.CataractaeTypeAgent,
				TimeoutMinutes: 60,
				OnPass:         "done",
				OnRecirculate:  "implement",
			},
		},
	}
}

// stuckItem returns an in-progress delivery droplet that is past the stuck threshold.
func stuckItem(id string, age time.Duration) *cistern.Droplet {
	return &cistern.Droplet{
		ID:                id,
		Repo:              "test-repo",
		Status:            "in_progress",
		CurrentCataractae: "delivery",
		Assignee:          "virgo",
		UpdatedAt:         time.Now().Add(-age),
	}
}

// newStuckClient creates a mockClient pre-populated with the given item.
func newStuckClient(item *cistern.Droplet) *mockClient {
	c := newMockClient()
	cp := *item
	c.items[item.ID] = &cp
	return c
}

type findPRResult struct {
	prURL            string
	state            string
	mergeStateStatus string
	err              error
}

// stuckScheduler builds a Castellarius with injectable stuck-delivery functions.
func stuckScheduler(
	client CisternClient,
	pr findPRResult,
	killErr error,
	rebaseErr error,
	ghMergeFirstErr error,
	ghMergeSecondErr error,
) *Castellarius {
	config := aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "test-repo", Prefix: "tr"},
		},
		MaxCataractae: 1,
	}
	wf := stuckDeliveryWorkflow()
	workflows := map[string]*aqueduct.Workflow{"test-repo": wf}
	clients := map[string]CisternClient{"test-repo": client}
	s := NewFromParts(config, workflows, clients, nil)

	s.findPRFn = func(_ context.Context, _, _, _ string) (string, string, string, error) {
		return pr.prURL, pr.state, pr.mergeStateStatus, pr.err
	}
	s.killSessionFn = func(_ string) error {
		return killErr
	}
	s.rebaseAndPushFn = func(_ context.Context, _ string) error {
		return rebaseErr
	}
	ghMergeCall := 0
	s.ghMergeFn = func(_ context.Context, _, _ string, _ bool) error {
		ghMergeCall++
		if ghMergeCall == 1 {
			return ghMergeFirstErr
		}
		return ghMergeSecondErr
	}
	return s
}

// outcome reads the outcome written to the mock client.
func outcomeFor(c *mockClient, id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if item, ok := c.items[id]; ok {
		return item.Outcome
	}
	return ""
}

// --- threshold tests ---

func TestStuckDeliveryThreshold_DefaultTimeout(t *testing.T) {
	wf := &aqueduct.Workflow{} // no delivery step configured
	got := stuckDeliveryThreshold(wf)
	want := time.Duration(float64(defaultDeliveryTimeoutMinutes) * stuckDeliveryThresholdFactor * float64(time.Minute))
	if got != want {
		t.Errorf("threshold = %v, want %v", got, want)
	}
}

func TestStuckDeliveryThreshold_CustomTimeout(t *testing.T) {
	wf := &aqueduct.Workflow{
		Cataractae: []aqueduct.WorkflowCataractae{
			{Name: "delivery", TimeoutMinutes: 60},
		},
	}
	got := stuckDeliveryThreshold(wf)
	want := time.Duration(float64(60)*stuckDeliveryThresholdFactor) * time.Minute // 90m
	if got != want {
		t.Errorf("threshold = %v, want %v", got, want)
	}
}

// --- filter tests (checkStuckDeliveriesForRepo) ---

func TestCheckStuckDeliveries_SkipsNonDeliveryStep(t *testing.T) {
	item := &cistern.Droplet{
		ID: "sd-skip-1", Status: "in_progress",
		CurrentCataractae: "implement", // not delivery
		Assignee:          "virgo",
		UpdatedAt:         time.Now().Add(-2 * time.Hour),
	}
	c := newStuckClient(item)
	s := stuckScheduler(c, findPRResult{}, nil, nil, nil, nil)

	s.checkStuckDeliveriesForRepo(context.Background(), s.config.Repos[0])

	if got := outcomeFor(c, item.ID); got != "" {
		t.Errorf("expected no outcome for non-delivery item, got %q", got)
	}
}

func TestCheckStuckDeliveries_SkipsItemWithOutcome(t *testing.T) {
	item := &cistern.Droplet{
		ID: "sd-skip-2", Status: "in_progress",
		CurrentCataractae: "delivery",
		Assignee:          "virgo",
		Outcome:           "pass", // already has outcome
		UpdatedAt:         time.Now().Add(-2 * time.Hour),
	}
	c := newStuckClient(item)
	s := stuckScheduler(c, findPRResult{}, nil, nil, nil, nil)

	s.checkStuckDeliveriesForRepo(context.Background(), s.config.Repos[0])

	// Outcome should remain "pass" — not overwritten.
	if got := outcomeFor(c, item.ID); got != "pass" {
		t.Errorf("outcome = %q, want %q (should not be overwritten)", got, "pass")
	}
}

func TestCheckStuckDeliveries_SkipsNoAssignee(t *testing.T) {
	item := &cistern.Droplet{
		ID: "sd-skip-3", Status: "in_progress",
		CurrentCataractae: "delivery",
		Assignee:          "", // no assignee
		UpdatedAt:         time.Now().Add(-2 * time.Hour),
	}
	c := newStuckClient(item)
	s := stuckScheduler(c, findPRResult{}, nil, nil, nil, nil)

	s.checkStuckDeliveriesForRepo(context.Background(), s.config.Repos[0])

	if got := outcomeFor(c, item.ID); got != "" {
		t.Errorf("expected no outcome for unassigned item, got %q", got)
	}
}

func TestCheckStuckDeliveries_SkipsNotYetStuck(t *testing.T) {
	// stuckDeliveryWorkflow sets timeout_minutes=60 → threshold=90m.
	// Item running for only 30m should be skipped.
	item := stuckItem("sd-skip-4", 30*time.Minute)
	c := newStuckClient(item)
	s := stuckScheduler(c, findPRResult{}, nil, nil, nil, nil)

	s.checkStuckDeliveriesForRepo(context.Background(), s.config.Repos[0])

	if got := outcomeFor(c, item.ID); got != "" {
		t.Errorf("expected no outcome (not yet stuck), got %q", got)
	}
}

func TestCheckStuckDeliveries_SkipsDeadSession(t *testing.T) {
	// Session is dead — isTmuxAlive returns false → item is skipped.
	// (In test environments tmux is not running, so isTmuxAlive always returns false.)
	item := stuckItem("sd-skip-5", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c, findPRResult{prURL: "https://github.com/o/r/pull/1", state: "OPEN", mergeStateStatus: "CLEAN"}, nil, nil, nil, nil)

	s.checkStuckDeliveriesForRepo(context.Background(), s.config.Repos[0])

	// No outcome — session was dead so recovery was skipped.
	if got := outcomeFor(c, item.ID); got != "" {
		t.Errorf("expected no outcome (dead session skipped), got %q", got)
	}
}

// --- recoverStuckDelivery routing tests ---

func TestRecoverStuckDelivery_Merged_Pass(t *testing.T) {
	item := stuckItem("sd-merged", 2*time.Hour)
	c := newStuckClient(item)
	killed := false
	s := stuckScheduler(c,
		findPRResult{prURL: "https://github.com/o/r/pull/1", state: "MERGED"},
		nil, nil, nil, nil,
	)
	s.killSessionFn = func(_ string) error { killed = true; return nil }

	s.recoverStuckDelivery(context.Background(), s.config.Repos[0], c, item)

	if got := outcomeFor(c, item.ID); got != "pass" {
		t.Errorf("outcome = %q, want %q", got, "pass")
	}
	if !killed {
		t.Error("expected session to be killed")
	}
}

func TestRecoverStuckDelivery_Closed_Recirculates(t *testing.T) {
	item := stuckItem("sd-closed", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c,
		findPRResult{prURL: "https://github.com/o/r/pull/2", state: "CLOSED"},
		nil, nil, nil, nil,
	)

	s.recoverStuckDelivery(context.Background(), s.config.Repos[0], c, item)

	if got := outcomeFor(c, item.ID); got != "recirculate" {
		t.Errorf("outcome = %q, want %q", got, "recirculate")
	}
}

func TestRecoverStuckDelivery_PRLookupFail_Recirculates(t *testing.T) {
	item := stuckItem("sd-lookupfail", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c,
		findPRResult{err: errors.New("gh: network error")},
		nil, nil, nil, nil,
	)

	s.recoverStuckDelivery(context.Background(), s.config.Repos[0], c, item)

	if got := outcomeFor(c, item.ID); got != "recirculate" {
		t.Errorf("outcome = %q, want %q", got, "recirculate")
	}
}

func TestRecoverStuckDelivery_NoPR_Recirculates(t *testing.T) {
	item := stuckItem("sd-nopr", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c,
		findPRResult{prURL: "", state: "", mergeStateStatus: ""}, // no PR found
		nil, nil, nil, nil,
	)

	s.recoverStuckDelivery(context.Background(), s.config.Repos[0], c, item)

	if got := outcomeFor(c, item.ID); got != "recirculate" {
		t.Errorf("outcome = %q, want %q", got, "recirculate")
	}
}

func TestRecoverStuckDelivery_UnexpectedState_Recirculates(t *testing.T) {
	item := stuckItem("sd-unknown-state", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c,
		findPRResult{prURL: "https://github.com/o/r/pull/5", state: "DRAFT"},
		nil, nil, nil, nil,
	)

	s.recoverStuckDelivery(context.Background(), s.config.Repos[0], c, item)

	if got := outcomeFor(c, item.ID); got != "recirculate" {
		t.Errorf("outcome = %q, want %q", got, "recirculate")
	}
}

func TestRecoverStuckDelivery_SessionKilledOnMerged(t *testing.T) {
	item := stuckItem("sd-kill-check", 2*time.Hour)
	c := newStuckClient(item)
	var killedSession string
	s := stuckScheduler(c,
		findPRResult{prURL: "https://github.com/o/r/pull/6", state: "MERGED"},
		nil, nil, nil, nil,
	)
	s.killSessionFn = func(id string) error {
		killedSession = id
		return nil
	}

	s.recoverStuckDelivery(context.Background(), s.config.Repos[0], c, item)

	want := "test-repo-virgo"
	if killedSession != want {
		t.Errorf("killed session = %q, want %q", killedSession, want)
	}
}

// --- recoverOpenPR routing tests ---

func TestRecoverOpenPR_Behind_RebaseOK_Pass(t *testing.T) {
	item := stuckItem("sd-behind-ok", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c,
		findPRResult{prURL: "https://github.com/o/r/pull/10", state: "OPEN", mergeStateStatus: "BEHIND"},
		nil, // killErr
		nil, // rebaseErr (success)
		nil, // ghMergeFirstErr (auto-merge succeeds)
		nil,
	)

	s.recoverOpenPR(context.Background(), c, item, "/sandbox",
		"https://github.com/o/r/pull/10", "BEHIND")

	if got := outcomeFor(c, item.ID); got != "pass" {
		t.Errorf("outcome = %q, want %q", got, "pass")
	}
}

func TestRecoverOpenPR_Behind_RebaseFail_Recirculates(t *testing.T) {
	item := stuckItem("sd-behind-rebase-fail", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c,
		findPRResult{},
		nil,
		errors.New("rebase: conflict"), // rebaseErr
		nil,
		nil,
	)

	s.recoverOpenPR(context.Background(), c, item, "/sandbox",
		"https://github.com/o/r/pull/11", "BEHIND")

	if got := outcomeFor(c, item.ID); got != "recirculate" {
		t.Errorf("outcome = %q, want %q", got, "recirculate")
	}
}

func TestRecoverOpenPR_Behind_AutoMergeFail_StillPass(t *testing.T) {
	// Rebase succeeds but enabling auto-merge fails — should still signal pass.
	item := stuckItem("sd-behind-auto-fail", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c,
		findPRResult{},
		nil,
		nil,                             // rebaseErr (success)
		errors.New("auto-merge failed"), // ghMergeFirstErr (auto-merge attempt fails)
		nil,
	)

	s.recoverOpenPR(context.Background(), c, item, "/sandbox",
		"https://github.com/o/r/pull/12", "BEHIND")

	if got := outcomeFor(c, item.ID); got != "pass" {
		t.Errorf("outcome = %q, want %q (auto-merge failure should not block pass)", got, "pass")
	}
}

func TestRecoverOpenPR_Blocked_Recirculates(t *testing.T) {
	item := stuckItem("sd-blocked", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c, findPRResult{}, nil, nil, nil, nil)

	s.recoverOpenPR(context.Background(), c, item, "/sandbox",
		"https://github.com/o/r/pull/13", "BLOCKED")

	if got := outcomeFor(c, item.ID); got != "recirculate" {
		t.Errorf("outcome = %q, want %q", got, "recirculate")
	}
}

func TestRecoverOpenPR_Unstable_Recirculates(t *testing.T) {
	item := stuckItem("sd-unstable", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c, findPRResult{}, nil, nil, nil, nil)

	s.recoverOpenPR(context.Background(), c, item, "/sandbox",
		"https://github.com/o/r/pull/14", "UNSTABLE")

	if got := outcomeFor(c, item.ID); got != "recirculate" {
		t.Errorf("outcome = %q, want %q", got, "recirculate")
	}
}

func TestRecoverOpenPR_Clean_DirectOK_Pass(t *testing.T) {
	item := stuckItem("sd-clean-direct", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c,
		findPRResult{},
		nil,
		nil,
		nil, // first ghMerge call (direct) succeeds
		nil,
	)

	s.recoverOpenPR(context.Background(), c, item, "/sandbox",
		"https://github.com/o/r/pull/15", "CLEAN")

	if got := outcomeFor(c, item.ID); got != "pass" {
		t.Errorf("outcome = %q, want %q", got, "pass")
	}
}

func TestRecoverOpenPR_Clean_DirectFail_AutoOK_Pass(t *testing.T) {
	item := stuckItem("sd-clean-auto", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c,
		findPRResult{},
		nil,
		nil,
		errors.New("direct merge: protected branch"), // first call fails
		nil, // second call (auto) succeeds
	)

	s.recoverOpenPR(context.Background(), c, item, "/sandbox",
		"https://github.com/o/r/pull/16", "CLEAN")

	if got := outcomeFor(c, item.ID); got != "pass" {
		t.Errorf("outcome = %q, want %q", got, "pass")
	}
}

func TestRecoverOpenPR_CleanBothFail_RecirculatesWithAutoErr(t *testing.T) {
	item := stuckItem("sd-clean-both-fail", 2*time.Hour)
	c := newStuckClient(item)
	directErr := errors.New("direct merge failed")
	autoErr := errors.New("auto-merge also failed")
	s := stuckScheduler(c,
		findPRResult{},
		nil,
		nil,
		directErr, // first call fails
		autoErr,   // second call also fails
	)

	s.recoverOpenPR(context.Background(), c, item, "/sandbox",
		"https://github.com/o/r/pull/17", "CLEAN")

	if got := outcomeFor(c, item.ID); got != "recirculate" {
		t.Errorf("outcome = %q, want %q", got, "recirculate")
	}
	// Verify the note mentions the auto-merge error (not the direct-merge error).
	c.mu.Lock()
	defer c.mu.Unlock()
	var found bool
	for _, n := range c.attached {
		if n.id == item.ID && strings.Contains(n.notes, autoErr.Error()) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected note to contain auto-merge error %q", autoErr.Error())
	}
}

func TestRecoverOpenPR_Dirty_Recirculates(t *testing.T) {
	item := stuckItem("sd-dirty", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c, findPRResult{}, nil, nil, nil, nil)

	s.recoverOpenPR(context.Background(), c, item, "/sandbox",
		"https://github.com/o/r/pull/18", "DIRTY")

	if got := outcomeFor(c, item.ID); got != "recirculate" {
		t.Errorf("outcome = %q, want %q", got, "recirculate")
	}
}

func TestRecoverOpenPR_Unknown_Recirculates(t *testing.T) {
	item := stuckItem("sd-unknown", 2*time.Hour)
	c := newStuckClient(item)
	s := stuckScheduler(c, findPRResult{}, nil, nil, nil, nil)

	s.recoverOpenPR(context.Background(), c, item, "/sandbox",
		"https://github.com/o/r/pull/19", "UNKNOWN")

	if got := outcomeFor(c, item.ID); got != "recirculate" {
		t.Errorf("outcome = %q, want %q", got, "recirculate")
	}
}

// --- defaultFindPR integration tests (requires a fake gh) ---

// TestDefaultFindPR_IgnoresGhStderrWarnings verifies that when gh writes
// warnings to stderr but valid JSON to stdout, defaultFindPR successfully
// parses the PR data. CombinedOutput() would corrupt the JSON with the warning
// prefix; Output() must be used instead.
func TestDefaultFindPR_IgnoresGhStderrWarnings(t *testing.T) {
	tmpDir := t.TempDir()
	sandboxDir := t.TempDir()

	ghScript := "#!/bin/sh\n" +
		"echo 'warning: oauth scope missing' >&2\n" +
		"echo '[{\"url\":\"https://github.com/o/r/pull/42\",\"state\":\"OPEN\",\"mergeStateStatus\":\"CLEAN\"}]'\n"
	ghPath := filepath.Join(tmpDir, "gh")
	if err := os.WriteFile(ghPath, []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	prURL, state, mergeState, err := defaultFindPR(context.Background(), "", "ci-test", sandboxDir)
	if err != nil {
		t.Fatalf("defaultFindPR returned unexpected error: %v", err)
	}
	if prURL != "https://github.com/o/r/pull/42" {
		t.Errorf("prURL = %q, want %q", prURL, "https://github.com/o/r/pull/42")
	}
	if state != "OPEN" {
		t.Errorf("state = %q, want %q", state, "OPEN")
	}
	if mergeState != "CLEAN" {
		t.Errorf("mergeStateStatus = %q, want %q", mergeState, "CLEAN")
	}
}

// TestDefaultFindPR_ErrorIncludesStderr verifies that when gh exits non-zero,
// the returned error includes the stderr output so operators can diagnose the failure.
func TestDefaultFindPR_ErrorIncludesStderr(t *testing.T) {
	tmpDir := t.TempDir()
	sandboxDir := t.TempDir()

	ghScript := "#!/bin/sh\n" +
		"echo 'error: not authenticated' >&2\n" +
		"exit 1\n"
	ghPath := filepath.Join(tmpDir, "gh")
	if err := os.WriteFile(ghPath, []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, _, _, err := defaultFindPR(context.Background(), "", "ci-test", sandboxDir)
	if err == nil {
		t.Fatal("expected error from defaultFindPR, got nil")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("error %q does not contain stderr output 'not authenticated'", err.Error())
	}
}

// --- defaultRebaseAndPush integration tests (requires git) ---

func TestDefaultRebaseAndPush_SucceedsWhenBehind(t *testing.T) {
	// Set up a bare remote, a clone, and push a commit to make the clone behind.
	primaryDir := makeBareAndClone(t)
	base := filepath.Dir(primaryDir)
	remoteDir := filepath.Join(base, "remote")

	// Configure git identity in primary.
	branchMustRun(t, branchGitCmd(primaryDir, "config", "user.email", "test@test.com"))
	branchMustRun(t, branchGitCmd(primaryDir, "config", "user.name", "Test"))

	// Create a feature branch with a commit.
	branchMustRun(t, branchGitCmd(primaryDir, "checkout", "-b", "feat/sd-test"))
	if err := os.WriteFile(filepath.Join(primaryDir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(primaryDir, "add", "feature.txt"))
	branchMustRun(t, branchGitCmd(primaryDir, "commit", "-m", "add feature"))

	// Push the feature branch so force-with-lease works.
	branchMustRun(t, branchGitCmd(primaryDir, "push", "-u", "origin", "feat/sd-test"))

	// Add a commit to main on the remote (making the clone behind).
	initDir2 := filepath.Join(filepath.Dir(remoteDir), "updater")
	branchMustRun(t, branchGitCmd(".", "clone", remoteDir, initDir2))
	branchMustRun(t, branchGitCmd(initDir2, "config", "user.email", "test@test.com"))
	branchMustRun(t, branchGitCmd(initDir2, "config", "user.name", "Test"))
	if err := os.WriteFile(filepath.Join(initDir2, "main.txt"), []byte("main update\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(initDir2, "add", "main.txt"))
	branchMustRun(t, branchGitCmd(initDir2, "commit", "-m", "main update"))
	branchMustRun(t, branchGitCmd(initDir2, "push", "origin", "main"))

	// defaultRebaseAndPush should fetch, rebase, and push without error.
	if err := defaultRebaseAndPush(context.Background(), primaryDir); err != nil {
		t.Fatalf("defaultRebaseAndPush: %v", err)
	}
}

func TestDefaultRebaseAndPush_AbortsOnConflict(t *testing.T) {
	primaryDir := makeBareAndClone(t)
	base := filepath.Dir(primaryDir)
	remoteDir := filepath.Join(base, "remote")

	branchMustRun(t, branchGitCmd(primaryDir, "config", "user.email", "test@test.com"))
	branchMustRun(t, branchGitCmd(primaryDir, "config", "user.name", "Test"))

	// Feature branch modifies a file.
	branchMustRun(t, branchGitCmd(primaryDir, "checkout", "-b", "feat/sd-conflict"))
	if err := os.WriteFile(filepath.Join(primaryDir, "README.md"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(primaryDir, "add", "README.md"))
	branchMustRun(t, branchGitCmd(primaryDir, "commit", "-m", "feature changes README"))
	branchMustRun(t, branchGitCmd(primaryDir, "push", "-u", "origin", "feat/sd-conflict"))

	// Main also modifies the same file → conflict.
	updaterDir := filepath.Join(filepath.Dir(remoteDir), "updater2")
	branchMustRun(t, branchGitCmd(".", "clone", remoteDir, updaterDir))
	branchMustRun(t, branchGitCmd(updaterDir, "config", "user.email", "test@test.com"))
	branchMustRun(t, branchGitCmd(updaterDir, "config", "user.name", "Test"))
	if err := os.WriteFile(filepath.Join(updaterDir, "README.md"), []byte("conflicting main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(updaterDir, "add", "README.md"))
	branchMustRun(t, branchGitCmd(updaterDir, "commit", "-m", "main changes README"))
	branchMustRun(t, branchGitCmd(updaterDir, "push", "origin", "main"))

	if err := defaultRebaseAndPush(context.Background(), primaryDir); err == nil {
		t.Fatal("expected error from defaultRebaseAndPush on conflict, got nil")
	}

	// Rebase should have been aborted — HEAD should still be on the feature branch.
	cmd := branchGitCmd(primaryDir, "status", "--porcelain")
	out, _ := cmd.Output()
	if len(out) > 0 {
		t.Logf("git status after abort: %s", out)
	}
	// Verify no rebase in progress.
	rebaseDir := filepath.Join(primaryDir, ".git", "rebase-merge")
	if _, err := os.Stat(rebaseDir); err == nil {
		t.Error("expected rebase-merge dir to be absent after abort")
	}
}

// TestRecoverStuckDelivery_SandboxDirUsesDropletID verifies that recoverStuckDelivery
// passes a path keyed on item.ID (not item.Assignee) to findPRFn. The per-droplet
// worktree refactor placed worktrees at sandboxRoot/<repo>/<dropletID>, so recovery
// must target the same location — not the old per-worker path.
func TestRecoverStuckDelivery_SandboxDirUsesDropletID(t *testing.T) {
	item := stuckItem("sd-dir-check", 2*time.Hour)
	c := newStuckClient(item)
	var capturedDir string
	s := stuckScheduler(c,
		findPRResult{prURL: "https://github.com/o/r/pull/1", state: "MERGED"},
		nil, nil, nil, nil,
	)
	s.sandboxRoot = "/sandbox"
	s.findPRFn = func(_ context.Context, _, _, dir string) (string, string, string, error) {
		capturedDir = dir
		return "https://github.com/o/r/pull/1", "MERGED", "", nil
	}

	s.recoverStuckDelivery(context.Background(), s.config.Repos[0], c, item)

	// sandboxDir must use item.ID (droplet ID), not item.Assignee (worker name).
	wantDir := filepath.Join("/sandbox", "test-repo", item.ID)
	if capturedDir != wantDir {
		t.Errorf("sandboxDir = %q, want %q (must use droplet ID, not assignee %q)",
			capturedDir, wantDir, item.Assignee)
	}
}

// --- AddNote error logging tests ---

// TestRecoverStuckDelivery_AddNoteError_LogsWarn verifies that when AddNote
// returns an error, a WARN-level log is emitted and the outcome is still set
// (non-blocking behavior).
func TestRecoverStuckDelivery_AddNoteError_LogsWarn(t *testing.T) {
	item := stuckItem("sd-note-err", 2*time.Hour)
	c := newStuckClient(item)
	c.addNoteErr = errors.New("db write failed")

	h := &captureHandler{}
	logger := slog.New(h)

	s := stuckScheduler(c,
		findPRResult{prURL: "https://github.com/o/r/pull/1", state: "MERGED"},
		nil, nil, nil, nil,
	)
	s.logger = logger

	s.recoverStuckDelivery(context.Background(), s.config.Repos[0], c, item)

	// Outcome must still be set — AddNote failure is non-blocking.
	if got := outcomeFor(c, item.ID); got != "pass" {
		t.Errorf("outcome = %q, want %q", got, "pass")
	}

	// A WARN must have been logged for the AddNote failure.
	if !h.hasWarn() {
		t.Error("expected WARN log for AddNote failure, got none")
	}
}

// TestRecoverStuckDelivery_UsesDropletIDForSandboxDir verifies that
// recoverStuckDelivery constructs the sandboxDir from item.ID, not item.Assignee.
// Per-droplet worktrees live at <sandboxRoot>/<repo>/<dropletID>; using
// item.Assignee (the aqueduct slot name) would target a non-existent directory.
func TestRecoverStuckDelivery_UsesDropletIDForSandboxDir(t *testing.T) {
	item := &cistern.Droplet{
		ID:                "sd-id-dir-check",
		Repo:              "test-repo",
		Status:            "in_progress",
		CurrentCataractae: "delivery",
		Assignee:          "virgo", // Assignee differs from ID
		UpdatedAt:         time.Now().Add(-2 * time.Hour),
	}
	c := newStuckClient(item)

	var capturedSandboxDir string
	s := stuckScheduler(c,
		findPRResult{prURL: "https://github.com/o/r/pull/1", state: "MERGED"},
		nil, nil, nil, nil,
	)
	s.sandboxRoot = "/sandbox"
	s.findPRFn = func(_ context.Context, _, _, dir string) (string, string, string, error) {
		capturedSandboxDir = dir
		return "https://github.com/o/r/pull/1", "MERGED", "", nil
	}

	s.recoverStuckDelivery(context.Background(), s.config.Repos[0], c, item)

	// sandboxDir must contain the droplet ID, not the assignee slot name.
	if !strings.Contains(capturedSandboxDir, item.ID) {
		t.Errorf("sandboxDir %q does not contain droplet ID %q", capturedSandboxDir, item.ID)
	}
	if strings.Contains(capturedSandboxDir, item.Assignee) {
		t.Errorf("sandboxDir %q must not contain assignee %q — worktrees are keyed by droplet ID", capturedSandboxDir, item.Assignee)
	}
}
