package castellarius

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- git helpers for branch lifecycle tests ---

func branchGitCmd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd
}

func branchMustRun(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v failed: %v\n%s", cmd.Args, err, out)
	}
}

// makeBareAndClone creates:
//
//	base/remote/ — bare git repo with one commit on main
//	base/primary/ — full clone of remote (has origin remote set)
//
// Returns the primary directory. Callers have origin/main available for fetch.
func makeBareAndClone(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	remoteDir := filepath.Join(base, "remote")
	primaryDir := filepath.Join(base, "primary")

	// Create an intermediate repo to build the initial commit, then push to bare.
	initDir := filepath.Join(base, "init")

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

	// Clone the bare remote to create the primary (inherits origin remote).
	branchMustRun(t, branchGitCmd(".", "clone", remoteDir, primaryDir))
	branchMustRun(t, branchGitCmd(primaryDir, "config", "user.email", "test@test.com"))
	branchMustRun(t, branchGitCmd(primaryDir, "config", "user.name", "Test"))

	return primaryDir
}

// currentBranch returns the symbolic branch name of HEAD, or "HEAD" if detached.
func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// branchExists reports whether branchName appears in 'git branch --list'.
func branchExists(t *testing.T, dir, branchName string) bool {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "branch", "--list", branchName).Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	return strings.TrimSpace(string(out)) != ""
}

// --- prepareBranchInSandbox tests ---

// TestPrepareBranchInSandbox_NewBranch verifies that calling prepareBranchInSandbox
// on a repo that does not yet have the feature branch creates it from origin/main.
func TestPrepareBranchInSandbox_NewBranch(t *testing.T) {
	dir := makeBareAndClone(t)

	if err := prepareBranchInSandbox(dir, "drop-new"); err != nil {
		t.Fatalf("prepareBranchInSandbox: %v", err)
	}

	if got := currentBranch(t, dir); got != "feat/drop-new" {
		t.Errorf("HEAD branch = %q, want feat/drop-new", got)
	}
}

// TestPrepareBranchInSandbox_NewBranch_ConfiguresGitIdentity verifies that
// git user.name and user.email are set in the repo after the call.
func TestPrepareBranchInSandbox_NewBranch_ConfiguresGitIdentity(t *testing.T) {
	dir := makeBareAndClone(t)

	if err := prepareBranchInSandbox(dir, "drop-ident"); err != nil {
		t.Fatalf("prepareBranchInSandbox: %v", err)
	}

	nameOut, err := exec.Command("git", "-C", dir, "config", "user.name").Output()
	if err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if got := strings.TrimSpace(string(nameOut)); got != "Cistern Agent" {
		t.Errorf("user.name = %q, want %q", got, "Cistern Agent")
	}

	emailOut, err := exec.Command("git", "-C", dir, "config", "user.email").Output()
	if err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	if got := strings.TrimSpace(string(emailOut)); got != "agent@cistern.local" {
		t.Errorf("user.email = %q, want %q", got, "agent@cistern.local")
	}
}

// TestPrepareBranchInSandbox_ResumeBranch verifies that when the feature branch
// already exists, prepareBranchInSandbox checks it out without resetting it —
// preserving any commits already on the branch.
func TestPrepareBranchInSandbox_ResumeBranch(t *testing.T) {
	dir := makeBareAndClone(t)

	// First call creates the branch.
	if err := prepareBranchInSandbox(dir, "drop-resume"); err != nil {
		t.Fatalf("prepareBranchInSandbox (create): %v", err)
	}

	// Make a commit on the feature branch to represent prior agent work.
	if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(dir, "add", "."))
	branchMustRun(t, branchGitCmd(dir, "commit", "-m", "agent work"))

	before, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse before: %v", err)
	}

	// Second call on an existing branch must resume, not reset.
	if err := prepareBranchInSandbox(dir, "drop-resume"); err != nil {
		t.Fatalf("prepareBranchInSandbox (resume): %v", err)
	}

	after, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse after: %v", err)
	}
	if strings.TrimSpace(string(before)) != strings.TrimSpace(string(after)) {
		t.Errorf("branch was reset instead of resumed: HEAD changed %s → %s",
			strings.TrimSpace(string(before)), strings.TrimSpace(string(after)))
	}

	if got := currentBranch(t, dir); got != "feat/drop-resume" {
		t.Errorf("HEAD branch after resume = %q, want feat/drop-resume", got)
	}
}

// --- removeDropletWorktree tests ---

// TestRemoveDropletWorktree_DeletesBranch verifies that removeDropletWorktree
// deletes the feat/<id> branch ref in the primary clone, not just the worktree
// directory. Without this, dead branch refs accumulate indefinitely.
func TestRemoveDropletWorktree_DeletesBranch(t *testing.T) {
	primaryDir := makeBareAndClone(t)
	sandboxRoot := t.TempDir()

	// Create a worktree so there's a branch to remove.
	_, err := prepareDropletWorktree(primaryDir, sandboxRoot, "myrepo", "drop-rm")
	if err != nil {
		t.Fatalf("prepareDropletWorktree: %v", err)
	}
	if !branchExists(t, primaryDir, "feat/drop-rm") {
		t.Fatal("feat/drop-rm should exist after prepareDropletWorktree")
	}

	removeDropletWorktree(primaryDir, sandboxRoot, "myrepo", "drop-rm", false)

	if branchExists(t, primaryDir, "feat/drop-rm") {
		t.Error("feat/drop-rm should have been deleted by removeDropletWorktree")
	}
}

// --- prepareDropletWorktree tests ---

// TestPrepareDropletWorktree_NewWorktree_CreatesOnFeatureBranch verifies that a
// new worktree is created at the correct path on the feature branch.
func TestPrepareDropletWorktree_NewWorktree_CreatesOnFeatureBranch(t *testing.T) {
	primaryDir := makeBareAndClone(t)
	sandboxRoot := t.TempDir()

	worktreePath, err := prepareDropletWorktree(primaryDir, sandboxRoot, "myrepo", "drop-new")
	if err != nil {
		t.Fatalf("prepareDropletWorktree: %v", err)
	}

	if _, statErr := os.Stat(worktreePath); statErr != nil {
		t.Fatalf("worktree path does not exist: %v", statErr)
	}

	if got := currentBranch(t, worktreePath); got != "feat/drop-new" {
		t.Errorf("HEAD branch = %q, want feat/drop-new", got)
	}
}

// TestPrepareDropletWorktree_FreshBranch_StartsAtOriginMain verifies that when
// the feature branch does not yet exist, the new worktree is created from
// origin/main and the worktree is clean — no dirty state from the primary clone.
func TestPrepareDropletWorktree_FreshBranch_StartsAtOriginMain(t *testing.T) {
	primaryDir := makeBareAndClone(t)
	sandboxRoot := t.TempDir()

	originMainSHA := func() string {
		out, err := exec.Command("git", "-C", primaryDir, "rev-parse", "origin/main").Output()
		if err != nil {
			t.Fatalf("rev-parse origin/main: %v", err)
		}
		return strings.TrimSpace(string(out))
	}()

	worktreePath, err := prepareDropletWorktree(primaryDir, sandboxRoot, "myrepo", "drop-fresh")
	if err != nil {
		t.Fatalf("prepareDropletWorktree: %v", err)
	}

	worktreeHEAD := func() string {
		out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatalf("rev-parse HEAD in worktree: %v", err)
		}
		return strings.TrimSpace(string(out))
	}()

	if worktreeHEAD != originMainSHA {
		t.Errorf("worktree HEAD = %s, want origin/main = %s", worktreeHEAD, originMainSHA)
	}

	// Worktree must be clean after creation.
	statusOut, statusErr := exec.Command("git", "-C", worktreePath, "status", "--porcelain").Output()
	if statusErr != nil {
		t.Fatalf("git status: %v", statusErr)
	}
	if strings.TrimSpace(string(statusOut)) != "" {
		t.Errorf("worktree is not clean after prepareDropletWorktree:\n%s", statusOut)
	}
}

// --- keepBranch / stagnant-resume tests ---

// newBranchLifecycleLogger creates a slog.Logger that writes to w.
// Pass io.Discard for tests that don't assert log output.
func newBranchLifecycleLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// TestRemoveDropletWorktree_KeepBranch_WhenStagnant_PreservesFeatureBranch verifies
// that when keepBranch=true the worktree directory is removed but the feature
// branch ref survives in the primary clone (stagnant path).
func TestRemoveDropletWorktree_KeepBranch_WhenStagnant_PreservesFeatureBranch(t *testing.T) {
	// Given: a worktree created for a droplet with a commit on the feature branch.
	primaryDir := makeBareAndClone(t)
	sandboxRoot := t.TempDir()
	l := newBranchLifecycleLogger(io.Discard)

	worktreePath, err := prepareDropletWorktreeWithLogger(l, primaryDir, sandboxRoot, "myrepo", "drop-stagnant")
	if err != nil {
		t.Fatalf("prepareDropletWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "work.go"), []byte("// work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(worktreePath, "add", "."))
	branchMustRun(t, branchGitCmd(worktreePath, "commit", "-m", "agent work"))

	// When: stagnant cleanup (keepBranch=true).
	removeDropletWorktreeWithLogger(l, primaryDir, sandboxRoot, "myrepo", "drop-stagnant", true)

	// Then: worktree directory is gone.
	if _, statErr := os.Stat(worktreePath); statErr == nil {
		t.Error("worktree directory should have been removed on stagnant cleanup")
	}

	// Then: feature branch still exists in primary clone.
	if !branchExists(t, primaryDir, "feat/drop-stagnant") {
		t.Error("feat/drop-stagnant should be preserved in primary clone after stagnant cleanup")
	}
}

// TestRemoveDropletWorktree_DeletesBranchAndDir_WhenDone verifies that when
// keepBranch=false both the worktree directory and the feature branch are
// removed (done/cancelled path).
func TestRemoveDropletWorktree_DeletesBranchAndDir_WhenDone(t *testing.T) {
	// Given: a worktree created for a droplet.
	primaryDir := makeBareAndClone(t)
	sandboxRoot := t.TempDir()
	l := newBranchLifecycleLogger(io.Discard)

	worktreePath, err := prepareDropletWorktreeWithLogger(l, primaryDir, sandboxRoot, "myrepo", "drop-done")
	if err != nil {
		t.Fatalf("prepareDropletWorktree: %v", err)
	}

	// When: done/cancelled cleanup (keepBranch=false).
	removeDropletWorktreeWithLogger(l, primaryDir, sandboxRoot, "myrepo", "drop-done", false)

	// Then: worktree directory is gone.
	if _, statErr := os.Stat(worktreePath); statErr == nil {
		t.Error("worktree directory should have been removed on done cleanup")
	}

	// Then: feature branch is deleted.
	if branchExists(t, primaryDir, "feat/drop-done") {
		t.Error("feat/drop-done should have been deleted on done cleanup")
	}
}

// TestPrepareDropletWorktree_ResumesFromExistingBranch_AfterStagnantCleanup verifies
// that after a stagnant cleanup (worktree dir removed, branch preserved) a
// subsequent prepareDropletWorktree call attaches to the existing branch via
// the no-b path, retaining all prior commits.
func TestPrepareDropletWorktree_ResumesFromExistingBranch_AfterStagnantCleanup(t *testing.T) {
	// Given: a worktree created, agent commits some work, stagnant cleanup runs.
	primaryDir := makeBareAndClone(t)
	sandboxRoot := t.TempDir()
	l := newBranchLifecycleLogger(io.Discard)

	worktreePath, err := prepareDropletWorktreeWithLogger(l, primaryDir, sandboxRoot, "myrepo", "drop-resume-stagnant")
	if err != nil {
		t.Fatalf("prepareDropletWorktree (first): %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "impl.go"), []byte("// implementation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(worktreePath, "add", "."))
	branchMustRun(t, branchGitCmd(worktreePath, "commit", "-m", "implement work"))

	beforeSHA, err := exec.Command("git", "-C", worktreePath, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse before: %v", err)
	}

	// Stagnant cleanup: remove worktree dir but keep branch (keepBranch=true).
	removeDropletWorktreeWithLogger(l, primaryDir, sandboxRoot, "myrepo", "drop-resume-stagnant", true)

	if _, statErr := os.Stat(worktreePath); statErr == nil {
		t.Fatal("worktree directory should be gone after stagnant cleanup")
	}

	// When: Architecti restarts the droplet — prepareDropletWorktree is called again.
	newWorktreePath, err := prepareDropletWorktreeWithLogger(l, primaryDir, sandboxRoot, "myrepo", "drop-resume-stagnant")
	if err != nil {
		t.Fatalf("prepareDropletWorktree (resume): %v", err)
	}

	// Then: the same branch is checked out (no fresh branch from origin/main).
	if got := currentBranch(t, newWorktreePath); got != "feat/drop-resume-stagnant" {
		t.Errorf("HEAD branch after resume = %q, want feat/drop-resume-stagnant", got)
	}

	// Then: prior commits are intact — HEAD matches the commit from before cleanup.
	afterSHA, err := exec.Command("git", "-C", newWorktreePath, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse after: %v", err)
	}
	before, after := strings.TrimSpace(string(beforeSHA)), strings.TrimSpace(string(afterSHA))
	if before != after {
		t.Errorf("prior commits lost: HEAD before=%s after=%s", before, after)
	}
}

// --- repoMu serialization tests ---

// TestPrepareDropletWorktree_ConcurrentSameRepo verifies that two goroutines
// calling prepareDropletWorktree for different droplets against the same
// primary clone succeed without error when serialized by a per-repo mutex —
// the pattern used by the Castellarius dispatch loop via repoMu.
//
// Run with -race to confirm no Go-level data races are introduced by the
// mutex acquisition pattern.
func TestPrepareDropletWorktree_ConcurrentSameRepo(t *testing.T) {
	primaryDir := makeBareAndClone(t)
	sandboxRoot := t.TempDir()

	const repoName = "myrepo"
	var mu sync.Mutex // simulates s.repoMu[repoName]

	type result struct {
		path string
		err  error
	}
	results := make([]result, 2)

	var wg sync.WaitGroup
	for i, id := range []string{"drop-concurrent-1", "drop-concurrent-2"} {
		wg.Add(1)
		i, id := i, id
		go func() {
			defer wg.Done()
			mu.Lock()
			path, err := prepareDropletWorktree(primaryDir, sandboxRoot, repoName, id)
			mu.Unlock()
			results[i] = result{path, err}
		}()
	}
	wg.Wait()

	for i, r := range results {
		if r.err != nil {
			t.Errorf("goroutine %d: prepareDropletWorktree failed: %v", i, r.err)
		}
		if r.path == "" {
			t.Errorf("goroutine %d: empty worktree path returned", i)
		}
		if _, statErr := os.Stat(r.path); statErr != nil {
			t.Errorf("goroutine %d: worktree path does not exist: %v", i, statErr)
		}
	}
}
