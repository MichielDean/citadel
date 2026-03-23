package cataractae

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// initTestRepo creates a git repo with an initial commit and sets
// refs/remotes/origin/main to HEAD — simulating a fetched remote without
// needing a real one. Returns the temp directory path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	runGit(t, dir, "update-ref", "refs/remotes/origin/main", runGit(t, dir, "rev-parse", "HEAD"))

	return dir
}

// TestGenerateDiff_NonEmptyWithChanges is an end-to-end regression test for
// ci-s5eg9: adversarial-review got an empty diff.patch because generateDiff
// was called on the worker's own sandbox (on main) instead of the per-droplet
// worktree (on feat/<id> with committed changes).
//
// This test verifies that generateDiff produces a non-empty diff when the
// sandbox directory contains committed changes on a feature branch vs
// origin/main. It is the "closed loop" counterpart to
// TestDispatch_DiffOnlyStepGetsSandboxDir, which only verifies that the
// correct path is passed — not that the diff itself is non-empty.
func TestGenerateDiff_NonEmptyWithChanges(t *testing.T) {
	dir := initTestRepo(t)

	// Create feature branch and commit a new file — simulates an implementer pass.
	runGit(t, dir, "checkout", "-b", "feat/ci-s5eg9-test")
	if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "feat: add feature.go")

	// generateDiff must return a non-empty diff containing the new file.
	diff, err := generateDiff(dir)
	if err != nil {
		t.Fatalf("generateDiff: %v", err)
	}
	if len(diff) == 0 {
		t.Fatal("generateDiff returned empty diff — diff_only reviewer would see empty diff.patch (regression: ci-s5eg9)")
	}
	if !strings.Contains(string(diff), "feature.go") {
		t.Errorf("generateDiff output should contain 'feature.go'; got:\n%s", diff)
	}
}

// TestGenerateDiff_EmptyOnMain verifies that generateDiff returns an empty
// diff (not an error) when the sandbox is on the same commit as origin/main.
// This is a boundary test: no-changes produces empty bytes, not an error.
// The actual regression guard for ci-s5eg9 is TestGenerateDiff_NonEmptyWithChanges.
func TestGenerateDiff_EmptyOnMain(t *testing.T) {
	dir := initTestRepo(t)

	diff, err := generateDiff(dir)
	if err != nil {
		t.Fatalf("generateDiff: %v", err)
	}
	if len(diff) != 0 {
		t.Errorf("expected empty diff when HEAD == origin/main; got %d bytes:\n%s", len(diff), diff)
	}
}
