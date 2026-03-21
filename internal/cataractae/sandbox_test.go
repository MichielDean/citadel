package cataractae

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeGitRepoWithCommit initialises a git repo with one commit on main.
func makeGitRepoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	steps := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range steps {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
		{"git", "branch", "-M", "main"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// isWorktreeRegistered reports whether absPath appears in 'git worktree list' for primaryDir.
func isWorktreeRegistered(t *testing.T, primaryDir, absPath string) bool {
	t.Helper()
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = primaryDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "worktree "+absPath {
			return true
		}
	}
	return false
}

func TestEnsureWorktree_CreatesWorktree(t *testing.T) {
	primary := makeGitRepoWithCommit(t)
	worktreeDir := filepath.Join(t.TempDir(), "wt")

	if err := EnsureWorktree(primary, worktreeDir); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	if _, err := os.Stat(worktreeDir); err != nil {
		t.Fatalf("worktree dir not created: %v", err)
	}

	abs, _ := filepath.Abs(worktreeDir)
	if !isWorktreeRegistered(t, primary, abs) {
		t.Error("worktree not registered in primary clone")
	}
}

func TestEnsureWorktree_Idempotent(t *testing.T) {
	primary := makeGitRepoWithCommit(t)
	worktreeDir := filepath.Join(t.TempDir(), "wt")

	if err := EnsureWorktree(primary, worktreeDir); err != nil {
		t.Fatalf("first EnsureWorktree: %v", err)
	}
	if err := EnsureWorktree(primary, worktreeDir); err != nil {
		t.Fatalf("second EnsureWorktree (idempotent): %v", err)
	}

	// Still registered exactly once (not duplicated).
	abs, _ := filepath.Abs(worktreeDir)
	if !isWorktreeRegistered(t, primary, abs) {
		t.Error("worktree not registered after idempotent call")
	}
}

func TestEnsureWorktree_ReplacesLegacyClone(t *testing.T) {
	primary := makeGitRepoWithCommit(t)
	worktreeDir := filepath.Join(t.TempDir(), "legacy")

	// Simulate a legacy dedicated clone: a plain directory with files.
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(worktreeDir, "legacy_file.txt")
	if err := os.WriteFile(sentinel, []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureWorktree(primary, worktreeDir); err != nil {
		t.Fatalf("EnsureWorktree on legacy dir: %v", err)
	}

	// Legacy contents must be gone.
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("legacy sentinel file should have been removed")
	}

	abs, _ := filepath.Abs(worktreeDir)
	if !isWorktreeRegistered(t, primary, abs) {
		t.Error("worktree not registered after legacy clone replacement")
	}
}

func TestEnsureWorktree_PrunesStaleRegistrations(t *testing.T) {
	primary := makeGitRepoWithCommit(t)
	worktreeDir := filepath.Join(t.TempDir(), "wt")

	// Register the worktree.
	if err := EnsureWorktree(primary, worktreeDir); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	// Simulate staleness: delete the worktree files without deregistering.
	if err := os.RemoveAll(worktreeDir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	// Re-add the same path. prune must clear the stale entry so add succeeds.
	if err := EnsureWorktree(primary, worktreeDir); err != nil {
		t.Fatalf("EnsureWorktree after stale deletion: %v", err)
	}

	abs, _ := filepath.Abs(worktreeDir)
	if !isWorktreeRegistered(t, primary, abs) {
		t.Error("worktree not registered after prune+re-add")
	}
}
