package castellarius

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// --- cleanupBranchInSandbox tests ---

// TestCleanupBranchInSandbox_DeletesBranch verifies that cleanup detaches HEAD
// and deletes the feature branch.
func TestCleanupBranchInSandbox_DeletesBranch(t *testing.T) {
	dir := makeBareAndClone(t)

	if err := prepareBranchInSandbox(dir, "drop-clean"); err != nil {
		t.Fatalf("prepareBranchInSandbox: %v", err)
	}
	// Make a commit so the branch is not identical to origin/main.
	if err := os.WriteFile(filepath.Join(dir, "work.go"), []byte("// work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchMustRun(t, branchGitCmd(dir, "add", "."))
	branchMustRun(t, branchGitCmd(dir, "commit", "-m", "work"))

	cleanupBranchInSandbox(dir, "feat/drop-clean")

	if branchExists(t, dir, "feat/drop-clean") {
		t.Error("feat/drop-clean should have been deleted by cleanup")
	}

	// HEAD must be detached after cleanup.
	if got := currentBranch(t, dir); got != "HEAD" {
		t.Errorf("HEAD after cleanup = %q, want detached (HEAD)", got)
	}
}

// TestCleanupBranchInSandbox_NoopWhenBranchMissing verifies that cleanup is
// best-effort and does not panic or error when the branch does not exist.
func TestCleanupBranchInSandbox_NoopWhenBranchMissing(t *testing.T) {
	dir := makeBareAndClone(t)
	// cleanupBranchInSandbox ignores errors — must not panic.
	cleanupBranchInSandbox(dir, "feat/nonexistent")
}
