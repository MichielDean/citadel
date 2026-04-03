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

// makeWorktreeDirWithoutFeatureBranch creates a git repo at worktreeDir with
// an initial commit but no feat/<dropletID> branch, so that
// git checkout feat/<dropletID> will fail with "pathspec did not match".
func makeWorktreeDirWithoutFeatureBranch(t *testing.T, worktreeDir string) {
	t.Helper()
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(worktreeDir, "init"))
	branchMustRun(t, branchGitCmd(worktreeDir, "config", "user.email", "test@test.com"))
	branchMustRun(t, branchGitCmd(worktreeDir, "config", "user.name", "Test"))
	if err := os.WriteFile(filepath.Join(worktreeDir, "file.txt"), []byte("init"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(worktreeDir, "add", "."))
	branchMustRun(t, branchGitCmd(worktreeDir, "commit", "-m", "init"))
}

// makePrimaryClone creates a bare remote and clones it to primaryDir with an
// initial commit on main. It mirrors makeBareAndClone in branch_lifecycle_test.go
// but accepts a caller-specified destination so the clone lands at the path that
// recoverDispatchLoop expects (sandboxRoot/<repoName>/_primary).
func makePrimaryClone(t *testing.T, primaryDir string) {
	t.Helper()
	parent := filepath.Dir(primaryDir)
	remoteDir := filepath.Join(parent, "remote")
	initDir := filepath.Join(parent, "init")
	branchMustRun(t, branchGitCmd(".", "init", "--bare", remoteDir))
	branchMustRun(t, branchGitCmd(".", "init", initDir))
	branchMustRun(t, branchGitCmd(initDir, "config", "user.email", "test@test.com"))
	branchMustRun(t, branchGitCmd(initDir, "config", "user.name", "Test"))
	if err := os.WriteFile(filepath.Join(initDir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(initDir, "add", "."))
	branchMustRun(t, branchGitCmd(initDir, "commit", "-m", "initial"))
	branchMustRun(t, branchGitCmd(initDir, "branch", "-M", "main"))
	branchMustRun(t, branchGitCmd(initDir, "remote", "add", "origin", remoteDir))
	branchMustRun(t, branchGitCmd(initDir, "push", "-u", "origin", "main"))
	if err := os.MkdirAll(filepath.Dir(primaryDir), 0o755); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(".", "clone", remoteDir, primaryDir))
	branchMustRun(t, branchGitCmd(primaryDir, "config", "user.email", "test@test.com"))
	branchMustRun(t, branchGitCmd(primaryDir, "config", "user.name", "Test"))
}

// TestRecoverDispatchLoop_PathspecError_LogsWarnAndPools verifies that when
// prepareDropletWorktree fails with "pathspec did not match any file(s) known to git"
// (i.e. the feature branch was deleted) and the fresh-branch fallback also fails,
// the recovery logs at WARN level, pools the droplet with a note
// containing the branch name and failure reason, and resets the dispatch tracker.
func TestRecoverDispatchLoop_PathspecError_LogsWarnAndPools(t *testing.T) {
	sandboxRoot := t.TempDir()
	const itemID = "dl-pathspec-1"

	// Create a git repo at the worktree path with no feat/<itemID> branch.
	// This triggers the resume path in prepareDropletWorktree, which runs
	// git checkout feat/<itemID> and fails with "pathspec did not match".
	worktreeDir := filepath.Join(sandboxRoot, "test-repo", itemID)
	makeWorktreeDirWithoutFeatureBranch(t, worktreeDir)

	// Precondition: git checkout feat/<itemID> fails with "pathspec".
	checkoutCmd := exec.Command("git", "checkout", "feat/"+itemID)
	checkoutCmd.Dir = worktreeDir
	if out, err := checkoutCmd.CombinedOutput(); err == nil {
		t.Fatal("precondition: expected git checkout to fail")
	} else if !strings.Contains(string(out), "pathspec") {
		t.Fatalf("precondition: expected pathspec error, got: %s", out)
	}

	// primaryDir does not exist as a git repo → second prepareDropletWorktree also fails.
	// (The path sandboxRoot/test-repo/_primary is not created.)

	var buf bytes.Buffer
	client := newMockClient()
	item := &cistern.Droplet{ID: itemID, CurrentCataractae: "implement", Status: "in_progress"}
	client.items[itemID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner,
		WithSandboxRoot(sandboxRoot),
		WithLogger(newTestLogger(&buf)),
	)

	sched.recoverDispatchLoop(client, item, config.Repos[0])

	logOut := buf.String()

	// WARN must be logged.
	if !strings.Contains(logOut, "WARN") {
		t.Errorf("expected WARN log when pathspec error detected; got: %s", logOut)
	}
	// Log must contain the droplet ID.
	if !strings.Contains(logOut, itemID) {
		t.Errorf("expected droplet ID in WARN log; got: %s", logOut)
	}
	// Log must contain the branch name.
	if !strings.Contains(logOut, "feat/"+itemID) {
		t.Errorf("expected branch name in WARN log; got: %s", logOut)
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	// Pool must have been called.
	reason, pooled := client.pooled[itemID]
	if !pooled {
		t.Error("expected client.Pool to be called when fresh-branch fallback also fails")
	}
	// Pool reason must contain branch name.
	if !strings.Contains(reason, "feat/"+itemID) {
		t.Errorf("pool reason must contain branch name; got: %q", reason)
	}

	// An addNote call must contain the branch name and a failure reason.
	var hasNote bool
	for _, n := range client.attached {
		if n.id == itemID && strings.Contains(n.notes, "feat/"+itemID) {
			hasNote = true
			break
		}
	}
	if !hasNote {
		t.Errorf("expected a note containing branch name %q; notes: %v", "feat/"+itemID, client.attached)
	}

	// Tracker must be reset — next incrementFix should return 1.
	if n := sched.dispatchLoop.incrementFix(itemID); n != 1 {
		t.Errorf("expected fix count 1 after tracker reset; got %d", n)
	}
}

// TestRecoverDispatchLoop_PathspecError_FreshBranchSucceeds_NoPooling verifies
// that when prepareDropletWorktree fails with "pathspec did not match" but the
// fresh-branch fallback succeeds, the droplet is NOT pooled and a recovery
// note is written.
func TestRecoverDispatchLoop_PathspecError_FreshBranchSucceeds_NoPooling(t *testing.T) {
	sandboxRoot := t.TempDir()
	const repoName = "test-repo"
	const itemID = "dl-pathspec-fresh"

	primaryDir := filepath.Join(sandboxRoot, repoName, "_primary")
	worktreeDir := filepath.Join(sandboxRoot, repoName, itemID)
	makePrimaryClone(t, primaryDir)
	makeWorktreeDirWithoutFeatureBranch(t, worktreeDir)

	client := newMockClient()
	item := &cistern.Droplet{ID: itemID, CurrentCataractae: "implement", Status: "in_progress"}
	client.items[itemID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{repoName: testWorkflow()}
	clients := map[string]CisternClient{repoName: client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner,
		WithSandboxRoot(sandboxRoot),
	)

	sched.recoverDispatchLoop(client, item, config.Repos[0])

	client.mu.Lock()
	defer client.mu.Unlock()

	// Must NOT pool.
	if _, pooled := client.pooled[itemID]; pooled {
		t.Error("expected no pooling when fresh-branch fallback succeeds")
	}

	// A note mentioning the fresh-branch recovery must be written.
	var hasNote bool
	for _, n := range client.attached {
		if n.id == itemID && (strings.Contains(n.notes, "fresh branch") || strings.Contains(n.notes, "origin/main")) {
			hasNote = true
			break
		}
	}
	if !hasNote {
		t.Errorf("expected a note about fresh-branch recovery; notes: %v", client.attached)
	}
}

// TestRecoverDispatchLoop_PathspecError_RemoveAllFails_PoolsImmediately verifies
// that when os.RemoveAll fails on the stale worktree directory (after a pathspec
// error), the recovery logs at WARN level, pools immediately with a reason
// containing "stale worktree directory", and does NOT attempt the fresh-branch retry.
func TestRecoverDispatchLoop_PathspecError_RemoveAllFails_PoolsImmediately(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("cannot simulate permission errors when running as root")
	}

	sandboxRoot := t.TempDir()
	const repoName = "test-repo"
	const itemID = "dl-pathspec-rmfail"

	// Create a git repo at the worktree path with no feat/<itemID> branch.
	// This causes the resume path in prepareDropletWorktree to fail with "pathspec did not match".
	worktreeDir := filepath.Join(sandboxRoot, repoName, itemID)
	makeWorktreeDirWithoutFeatureBranch(t, worktreeDir)

	// Make the parent directory not writable so os.RemoveAll(worktreeDir) fails.
	repoDir := filepath.Join(sandboxRoot, repoName)
	if err := os.Chmod(repoDir, 0o555); err != nil {
		t.Fatal(err)
	}
	// Restore permissions in cleanup so t.TempDir can remove the directory.
	t.Cleanup(func() { _ = os.Chmod(repoDir, 0o755) })

	var buf bytes.Buffer
	client := newMockClient()
	item := &cistern.Droplet{ID: itemID, CurrentCataractae: "implement", Status: "in_progress"}
	client.items[itemID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{repoName: testWorkflow()}
	clients := map[string]CisternClient{repoName: client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner,
		WithSandboxRoot(sandboxRoot),
		WithLogger(newTestLogger(&buf)),
	)

	sched.recoverDispatchLoop(client, item, config.Repos[0])

	logOut := buf.String()

	// WARN must be logged for the RemoveAll failure.
	if !strings.Contains(logOut, "WARN") {
		t.Errorf("expected WARN log when os.RemoveAll fails; got: %s", logOut)
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	// Must pool immediately with a reason mentioning the stale worktree directory.
	reason, pooled := client.pooled[itemID]
	if !pooled {
		t.Error("expected client.Pool to be called when os.RemoveAll fails")
	}
	if !strings.Contains(reason, "stale worktree") {
		t.Errorf("expected pool reason to mention 'stale worktree'; got: %q", reason)
	}

	// Must NOT have written a fresh-branch note (retry was skipped).
	for _, n := range client.attached {
		if n.id == itemID && (strings.Contains(n.notes, "fresh branch") || strings.Contains(n.notes, "origin/main")) {
			t.Errorf("must not write fresh-branch note when RemoveAll fails; got note: %q", n.notes)
		}
	}
}

// TestRecoverDispatchLoop_AddNoteError_PoolingPath_LogsWarn verifies that
// when AddNote returns an error during the pooling path (fixAttempt >
// dispatchMaxSelfFix), the error is logged at Warn level rather than silently
// discarded.
func TestRecoverDispatchLoop_AddNoteError_PoolingPath_LogsWarn(t *testing.T) {
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

	// Push fix attempt count above dispatchMaxSelfFix to trigger the pooling path.
	for range dispatchMaxSelfFix + 1 {
		sched.dispatchLoop.incrementFix(itemID)
	}

	sched.recoverDispatchLoop(client, item, config.Repos[0])

	if !strings.Contains(buf.String(), "WARN") {
		t.Errorf("expected WARN log when AddNote fails during pooling; log: %q", buf.String())
	}
}

// --- spawn-cycle tracker tests ---

func TestSpawnCycleTracker_RecordSuccess_RecordsTimestamp(t *testing.T) {
	tracker := newDispatchLoopTracker()

	if n := tracker.recentSpawnCount("drop1"); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
	tracker.recordSuccess("drop1")
	if n := tracker.recentSpawnCount("drop1"); n != 1 {
		t.Fatalf("expected 1 after recordSuccess, got %d", n)
	}
}

func TestSpawnCycleTracker_BelowThreshold_CountsCorrectly(t *testing.T) {
	tracker := newDispatchLoopTracker()

	for range spawnCycleThreshold - 1 {
		tracker.recordSuccess("drop1")
	}
	if n := tracker.recentSpawnCount("drop1"); n != spawnCycleThreshold-1 {
		t.Fatalf("expected %d, got %d", spawnCycleThreshold-1, n)
	}
}

func TestSpawnCycleTracker_AtThreshold_CountsCorrectly(t *testing.T) {
	tracker := newDispatchLoopTracker()

	for range spawnCycleThreshold {
		tracker.recordSuccess("drop1")
	}
	if n := tracker.recentSpawnCount("drop1"); n != spawnCycleThreshold {
		t.Fatalf("expected %d, got %d", spawnCycleThreshold, n)
	}
}

func TestSpawnCycleTracker_StaleSpawnsIgnored(t *testing.T) {
	tracker := newDispatchLoopTracker()

	// Inject a stale spawn by directly writing to the map.
	tracker.mu.Lock()
	tracker.spawnCycles["drop1"] = []time.Time{
		time.Now().Add(-(spawnCycleWindow + time.Minute)),
	}
	tracker.mu.Unlock()

	if n := tracker.recentSpawnCount("drop1"); n != 0 {
		t.Fatalf("expected 0 (stale spawn outside window), got %d", n)
	}
}

func TestSpawnCycleTracker_ResetSpawnCycles_ClearsCount(t *testing.T) {
	tracker := newDispatchLoopTracker()

	for range spawnCycleThreshold {
		tracker.recordSuccess("drop1")
	}
	if n := tracker.recentSpawnCount("drop1"); n != spawnCycleThreshold {
		t.Fatalf("precondition: expected %d, got %d", spawnCycleThreshold, n)
	}

	tracker.resetSpawnCycles("drop1")

	if n := tracker.recentSpawnCount("drop1"); n != 0 {
		t.Fatalf("expected 0 after resetSpawnCycles, got %d", n)
	}
}

func TestSpawnCycleTracker_RecordSuccess_ResetsFailures(t *testing.T) {
	tracker := newDispatchLoopTracker()

	for range 3 {
		tracker.recordFailure("drop1")
	}
	tracker.incrementFix("drop1")

	tracker.recordSuccess("drop1")

	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("expected 0 failures after recordSuccess, got %d", n)
	}
	// Fix count reset too — incrementFix should return 1.
	if n := tracker.incrementFix("drop1"); n != 1 {
		t.Fatalf("expected fix count 1 after recordSuccess, got %d", n)
	}
}

func TestSpawnCycleTracker_Reset_ClearsSpawnCycles(t *testing.T) {
	tracker := newDispatchLoopTracker()

	for range 3 {
		tracker.recordSuccess("drop1")
	}
	tracker.reset("drop1")

	if n := tracker.recentSpawnCount("drop1"); n != 0 {
		t.Fatalf("expected 0 spawn cycles after full reset, got %d", n)
	}
}

func TestSpawnCycleTracker_IndependentDroplets(t *testing.T) {
	tracker := newDispatchLoopTracker()

	for range spawnCycleThreshold {
		tracker.recordSuccess("drop1")
	}
	tracker.recordSuccess("drop2")

	if n := tracker.recentSpawnCount("drop1"); n != spawnCycleThreshold {
		t.Fatalf("drop1: expected %d, got %d", spawnCycleThreshold, n)
	}
	if n := tracker.recentSpawnCount("drop2"); n != 1 {
		t.Fatalf("drop2: expected 1, got %d", n)
	}

	tracker.resetSpawnCycles("drop1")
	if n := tracker.recentSpawnCount("drop1"); n != 0 {
		t.Fatalf("drop1: expected 0 after reset, got %d", n)
	}
	if n := tracker.recentSpawnCount("drop2"); n != 1 {
		t.Fatalf("drop2: should be unaffected by drop1 resetSpawnCycles, got %d", n)
	}
}
