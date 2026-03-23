package cataractae

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	dir := t.TempDir()

	// Initialize repo with base commit.
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

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	// Record initial commit and set it as the origin/main remote-tracking ref.
	// This simulates what git fetch would set, without needing a real remote.
	initialHash, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	setRef := exec.Command("git", "update-ref", "refs/remotes/origin/main", strings.TrimSpace(string(initialHash)))
	setRef.Dir = dir
	if out, err := setRef.CombinedOutput(); err != nil {
		t.Fatalf("update-ref origin/main: %v\n%s", err, out)
	}

	// Create feature branch and commit a new file — simulates an implementer pass.
	checkout := exec.Command("git", "checkout", "-b", "feat/ci-s5eg9-test")
	checkout.Dir = dir
	if out, err := checkout.CombinedOutput(); err != nil {
		t.Fatalf("checkout feature branch: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "feat: add feature.go"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

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
// diff when the sandbox is on the same commit as origin/main — i.e. no feature
// changes have been committed. This is the failure mode the ci-s5eg9 fix
// prevents: if the worker's own sandbox (on main) is passed instead of the
// per-droplet worktree, the diff is empty.
func TestGenerateDiff_EmptyOnMain(t *testing.T) {
	dir := t.TempDir()

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

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	// Set origin/main to current HEAD — no feature changes.
	currentHash, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	setRef := exec.Command("git", "update-ref", "refs/remotes/origin/main", strings.TrimSpace(string(currentHash)))
	setRef.Dir = dir
	if out, err := setRef.CombinedOutput(); err != nil {
		t.Fatalf("update-ref origin/main: %v\n%s", err, out)
	}

	diff, err := generateDiff(dir)
	if err != nil {
		t.Fatalf("generateDiff: %v", err)
	}
	if len(diff) != 0 {
		t.Errorf("expected empty diff when HEAD == origin/main; got %d bytes:\n%s", len(diff), diff)
	}
}
