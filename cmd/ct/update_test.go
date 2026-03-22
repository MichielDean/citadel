package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- verifyUpdateRepoPath ---

func TestVerifyUpdateRepoPath_ValidRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	got, err := verifyUpdateRepoPath(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Errorf("got %q, want %q", got, dir)
	}
}

func TestVerifyUpdateRepoPath_MissingGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	_, err := verifyUpdateRepoPath(dir)
	if err == nil {
		t.Fatal("expected error for missing .git")
	}
}

func TestVerifyUpdateRepoPath_MissingGoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	_, err := verifyUpdateRepoPath(dir)
	if err == nil {
		t.Fatal("expected error for missing go.mod")
	}
}

func TestVerifyUpdateRepoPath_NonExistentPath(t *testing.T) {
	_, err := verifyUpdateRepoPath("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

// --- findRepoFromBinaryPath ---

// makeSimulatedCisternRepo creates a directory that passes verifyUpdateRepoPath.
func makeSimulatedCisternRepo(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/cistern"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}

func TestFindRepoFromBinaryPath_OneLevelUp(t *testing.T) {
	// Simulate: ~/bin/ct  →  ~/cistern
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	binPath := filepath.Join(binDir, "ct")

	cisternDir := makeSimulatedCisternRepo(t, root, "cistern")

	got, err := findRepoFromBinaryPath(binPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cisternDir {
		t.Errorf("got %q, want %q", got, cisternDir)
	}
}

func TestFindRepoFromBinaryPath_TwoLevelsUp(t *testing.T) {
	// Simulate: ~/go/bin/ct  →  ~/cistern
	root := t.TempDir()
	binDir := filepath.Join(root, "go", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir go/bin: %v", err)
	}
	binPath := filepath.Join(binDir, "ct")

	cisternDir := makeSimulatedCisternRepo(t, root, "cistern")

	got, err := findRepoFromBinaryPath(binPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cisternDir {
		t.Errorf("got %q, want %q", got, cisternDir)
	}
}

func TestFindRepoFromBinaryPath_ThreeLevelsUp(t *testing.T) {
	// Simulate: ~/a/b/bin/ct  →  ~/cistern
	root := t.TempDir()
	binDir := filepath.Join(root, "a", "b", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir a/b/bin: %v", err)
	}
	binPath := filepath.Join(binDir, "ct")

	cisternDir := makeSimulatedCisternRepo(t, root, "cistern")

	got, err := findRepoFromBinaryPath(binPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cisternDir {
		t.Errorf("got %q, want %q", got, cisternDir)
	}
}

func TestFindRepoFromBinaryPath_NotFound(t *testing.T) {
	// No cistern repo sibling anywhere.
	root := t.TempDir()
	binDir := filepath.Join(root, "go", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	binPath := filepath.Join(binDir, "ct")

	_, err := findRepoFromBinaryPath(binPath)
	if err == nil {
		t.Fatal("expected error when no cistern repo found")
	}
}

// --- resolveUpdateRepoPath ---

func TestResolveUpdateRepoPath_ExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/cistern"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	got, err := resolveUpdateRepoPath(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Errorf("got %q, want %q", got, dir)
	}
}

func TestResolveUpdateRepoPath_EnvVar(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/cistern"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	t.Setenv("CT_REPO_PATH", dir)

	got, err := resolveUpdateRepoPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Errorf("got %q, want %q", got, dir)
	}
}

func TestResolveUpdateRepoPath_HomeRepo(t *testing.T) {
	// Set HOME to a temp dir containing ~/.cistern/repo as a valid repo.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CT_REPO_PATH", "") // clear any env override

	cisternDir := filepath.Join(home, ".cistern", "repo")
	if err := os.MkdirAll(cisternDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(cisternDir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cisternDir, "go.mod"), []byte("module example.com/cistern"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	got, err := resolveUpdateRepoPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cisternDir {
		t.Errorf("got %q, want %q", got, cisternDir)
	}
}

func TestResolveUpdateRepoPath_NoneFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CT_REPO_PATH", "")

	_, err := resolveUpdateRepoPath("")
	if err == nil {
		t.Fatal("expected error when no repo can be found")
	}
	if !strings.Contains(err.Error(), "CT_REPO_PATH") {
		t.Errorf("expected error message to mention CT_REPO_PATH, got: %v", err)
	}
}

// --- isFFOnlyFailure ---

func TestIsFFOnlyFailure_Diverged(t *testing.T) {
	cases := []string{
		"fatal: Not possible to fast-forward, aborting.",
		"hint: Diverging branches can't be fast-forwarded.",
		"fatal: Not possible to fast-forward",
		"Not possible to fast-forward, aborting",
	}
	for _, out := range cases {
		if !isFFOnlyFailure(out) {
			t.Errorf("isFFOnlyFailure(%q) = false, want true", out)
		}
	}
}

func TestIsFFOnlyFailure_OtherErrors(t *testing.T) {
	cases := []string{
		"error: Could not read from remote repository.",
		"fatal: repository 'origin' does not exist",
		"",
		"Already up to date.",
	}
	for _, out := range cases {
		if isFFOnlyFailure(out) {
			t.Errorf("isFFOnlyFailure(%q) = true, want false", out)
		}
	}
}

// --- shortSHA ---

func TestShortSHA_LongSHA(t *testing.T) {
	sha := "abc1234567890abcdef"
	got := shortSHA(sha)
	if got != "abc1234" {
		t.Errorf("shortSHA(%q) = %q, want %q", sha, got, "abc1234")
	}
}

func TestShortSHA_ShortInput(t *testing.T) {
	sha := "abc12"
	got := shortSHA(sha)
	if got != sha {
		t.Errorf("shortSHA(%q) = %q, want %q", sha, got, sha)
	}
}
