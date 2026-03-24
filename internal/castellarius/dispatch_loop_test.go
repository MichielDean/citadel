package castellarius

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

func TestDispatchLoopTracker_RecentFailureCount(t *testing.T) {
	tracker := newDispatchLoopTracker()

	// No failures yet.
	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}

	// Record failures below threshold.
	for range 4 {
		tracker.recordFailure("drop1")
	}
	if n := tracker.recentFailureCount("drop1"); n != 4 {
		t.Fatalf("expected 4, got %d", n)
	}

	// Record one more — now at threshold.
	tracker.recordFailure("drop1")
	if n := tracker.recentFailureCount("drop1"); n != 5 {
		t.Fatalf("expected 5, got %d", n)
	}
}

func TestDispatchLoopTracker_Reset(t *testing.T) {
	tracker := newDispatchLoopTracker()

	tracker.recordFailure("drop1")
	tracker.recordFailure("drop1")
	tracker.incrementFix("drop1")

	tracker.reset("drop1")

	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("expected 0 failures after reset, got %d", n)
	}
	// Fix count should also be gone — incrementFix should return 1 again.
	if n := tracker.incrementFix("drop1"); n != 1 {
		t.Fatalf("expected fix count 1 after reset, got %d", n)
	}
}

func TestDispatchLoopTracker_ResetFailuresKeepsFixCount(t *testing.T) {
	tracker := newDispatchLoopTracker()

	tracker.recordFailure("drop1")
	tracker.incrementFix("drop1") // fix count = 1

	tracker.resetFailures("drop1")

	// Failures cleared.
	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("expected 0 failures after resetFailures, got %d", n)
	}
	// Fix count preserved — next increment should return 2.
	if n := tracker.incrementFix("drop1"); n != 2 {
		t.Fatalf("expected fix count 2 after resetFailures + increment, got %d", n)
	}
}

func TestDispatchLoopTracker_StaleFailuresIgnored(t *testing.T) {
	tracker := newDispatchLoopTracker()

	// Inject a stale failure by directly appending to the map.
	tracker.mu.Lock()
	tracker.failures["drop1"] = []time.Time{
		time.Now().Add(-3 * time.Minute),
	}
	tracker.mu.Unlock()

	// The stale failure is outside the window — should not count.
	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("expected 0 (stale failure outside window), got %d", n)
	}
}

func TestDispatchLoopTracker_IncrementFix(t *testing.T) {
	tracker := newDispatchLoopTracker()

	if n := tracker.incrementFix("drop1"); n != 1 {
		t.Fatalf("expected 1, got %d", n)
	}
	if n := tracker.incrementFix("drop1"); n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
	if n := tracker.incrementFix("drop1"); n != 3 {
		t.Fatalf("expected 3, got %d", n)
	}
}

func TestDispatchLoopTracker_IndependentDroplets(t *testing.T) {
	tracker := newDispatchLoopTracker()

	for range 5 {
		tracker.recordFailure("drop1")
	}
	tracker.recordFailure("drop2")

	if n := tracker.recentFailureCount("drop1"); n != 5 {
		t.Fatalf("drop1: expected 5, got %d", n)
	}
	if n := tracker.recentFailureCount("drop2"); n != 1 {
		t.Fatalf("drop2: expected 1, got %d", n)
	}

	tracker.reset("drop1")
	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("drop1: expected 0 after reset, got %d", n)
	}
	if n := tracker.recentFailureCount("drop2"); n != 1 {
		t.Fatalf("drop2: should be unaffected by drop1 reset, got %d", n)
	}
}

// --- worktreeInOutput tests ---

func TestWorktreeInOutput_ExactPathMatches(t *testing.T) {
	out := []byte("worktree /sandbox/repo/_primary\nHEAD abc123\nbranch refs/heads/main\n\nworktree /sandbox/repo/ci-5o65q\nHEAD def456\nbranch refs/heads/feat/ci-5o65q\n")
	if !worktreeInOutput(out, "/sandbox/repo/ci-5o65q") {
		t.Error("expected exact path to match")
	}
}

func TestWorktreeInOutput_PrefixSharingPathDoesNotMatch(t *testing.T) {
	// /sandbox/repo/ci-5 is a substring of /sandbox/repo/ci-5o65q — must not match.
	out := []byte("worktree /sandbox/repo/_primary\nHEAD abc123\nbranch refs/heads/main\n\nworktree /sandbox/repo/ci-5o65q\nHEAD def456\nbranch refs/heads/feat/ci-5o65q\n")
	if worktreeInOutput(out, "/sandbox/repo/ci-5") {
		t.Error("prefix-sharing path must not match")
	}
}

func TestWorktreeInOutput_EmptyOutputReturnsFalse(t *testing.T) {
	if worktreeInOutput([]byte{}, "/sandbox/repo/ci-5o65q") {
		t.Error("empty output must return false")
	}
}

func TestWorktreeInOutput_NoTrailingNewlineStillMatches(t *testing.T) {
	// Last line with no trailing newline.
	out := []byte("worktree /sandbox/repo/ci-5o65q")
	if !worktreeInOutput(out, "/sandbox/repo/ci-5o65q") {
		t.Error("expected match even without trailing newline")
	}
}

// --- recoverDispatchLoop reset/clean failure tests ---

// makeGitSandboxNoCommit initialises a git repo in dir with identity config but
// no initial commit, so HEAD is unborn. git reset --hard HEAD will fail in this state.
func makeGitSandboxNoCommit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
}

// TestRecoverDispatchLoop_ResetFails_DoesNotWriteSuccessNote verifies that when
// git reset --hard HEAD fails (e.g. repo has no initial commit), the recovery
// function does NOT write the "dirty worktree reset" success note and falls
// through to the worktree-recreation path instead of claiming success.
func TestRecoverDispatchLoop_ResetFails_DoesNotWriteSuccessNote(t *testing.T) {
	sandboxRoot := t.TempDir()

	const itemID = "dl-reset-fail-1"
	worktreeDir := filepath.Join(sandboxRoot, "test-repo", itemID)
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Init a repo with no initial commit so git reset --hard HEAD will fail.
	makeGitSandboxNoCommit(t, worktreeDir)

	// Stage a file so dirtyNonContextFiles reports it as dirty (staged, not untracked).
	if err := os.WriteFile(filepath.Join(worktreeDir, "dirty.go"), []byte("package foo"), 0o644); err != nil {
		t.Fatal(err)
	}
	addCmd := exec.Command("git", "add", "dirty.go")
	addCmd.Dir = worktreeDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	// Precondition: dirtyNonContextFiles sees dirty files.
	files, err := dirtyNonContextFiles(worktreeDir)
	if err != nil || len(files) == 0 {
		t.Fatalf("precondition: expected dirty files, got files=%v err=%v", files, err)
	}

	// Precondition: git reset --hard HEAD fails (unborn HEAD).
	resetCheck := exec.Command("git", "reset", "--hard", "HEAD")
	resetCheck.Dir = worktreeDir
	if err := resetCheck.Run(); err == nil {
		t.Fatal("precondition: expected git reset --hard HEAD to fail in repo with no commits")
	}

	client := newMockClient()
	item := &cistern.Droplet{ID: itemID, CurrentCataractae: "implement", Status: "in_progress"}
	client.items[itemID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner, WithSandboxRoot(sandboxRoot))

	sched.recoverDispatchLoop(client, item, config.Repos[0])

	// The success note must NOT have been written.
	client.mu.Lock()
	defer client.mu.Unlock()
	for _, n := range client.attached {
		if n.id == itemID && strings.Contains(n.notes, "dirty worktree reset") {
			t.Errorf("must not write dirty-worktree-reset success note when reset/clean failed; got note: %q", n.notes)
		}
	}
}

// TestRecoverDispatchLoop_WorktreeRecreateFails_DoesNotWriteSuccessNote verifies
// that when prepareDropletWorktree fails during Recovery 2, the success note
// "worktree recreated" is NOT emitted, and a failure note IS written instead.
func TestRecoverDispatchLoop_WorktreeRecreateFails_DoesNotWriteSuccessNote(t *testing.T) {
	sandboxRoot := t.TempDir()

	const itemID = "dl-recreate-fail-1"
	// primaryDir exists but is not a git repo — git commands will fail.
	primaryDir := filepath.Join(sandboxRoot, "test-repo", "_primary")
	if err := os.MkdirAll(primaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// worktreePath does not exist → worktreeRegistered returns false → triggers Recovery 2.
	// (os.Stat on worktreePath also fails → Recovery 1 is skipped.)

	client := newMockClient()
	item := &cistern.Droplet{ID: itemID, CurrentCataractae: "implement", Status: "in_progress"}
	client.items[itemID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner, WithSandboxRoot(sandboxRoot))

	sched.recoverDispatchLoop(client, item, config.Repos[0])

	client.mu.Lock()
	defer client.mu.Unlock()

	// Success note must NOT have been written.
	for _, n := range client.attached {
		if n.id == itemID && strings.Contains(n.notes, "worktree recreated") {
			t.Errorf("must not write 'worktree recreated' success note when recreation failed; got: %q", n.notes)
		}
	}

	// A failure note must have been written.
	var hasFailureNote bool
	for _, n := range client.attached {
		if n.id == itemID && strings.Contains(n.notes, "worktree recreate failed") {
			hasFailureNote = true
			break
		}
	}
	if !hasFailureNote {
		t.Error("expected a failure note for worktree recreation failure, got none")
	}
}

// TestRecoverDispatchLoop_AddNoteError_EscalationPath_LogsWarn verifies that
// when AddNote returns an error during the escalation path (fixAttempt >
// dispatchMaxSelfFix), the error is logged at Warn level rather than silently
// discarded.
func TestRecoverDispatchLoop_AddNoteError_EscalationPath_LogsWarn(t *testing.T) {
	const itemID = "dl-note-err-1"

	client := newMockClient()
	client.addNoteErr = errors.New("db write error")
	item := &cistern.Droplet{ID: itemID, CurrentCataractae: "implement", Status: "in_progress"}
	client.items[itemID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)

	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	sched := NewFromParts(config, workflows, clients, runner,
		WithLogger(logger),
		WithSandboxRoot(t.TempDir()),
	)

	// Push fix attempt count above dispatchMaxSelfFix to trigger the escalation path.
	for range dispatchMaxSelfFix + 1 {
		sched.dispatchLoop.incrementFix(itemID)
	}

	sched.recoverDispatchLoop(client, item, config.Repos[0])

	if !strings.Contains(buf.String(), "WARN") {
		t.Errorf("expected WARN log when AddNote fails during escalation; log: %q", buf.String())
	}
}
