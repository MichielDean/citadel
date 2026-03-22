package skills

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCachePath_UsesHomeDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	got := LocalPath("example")
	want := filepath.Join(tmp, ".cistern", "skills", "example", "SKILL.md")
	if got != want {
		t.Errorf("CachePath = %q, want %q", got, want)
	}
}

func TestInstall_DownloadsAndCaches(t *testing.T) {
	const content = "# My Skill\n\nThis skill does awesome things.\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content)) //nolint:errcheck
	}))
	defer srv.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := Install("my-skill", srv.URL+"/SKILL.md"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	data, err := os.ReadFile(LocalPath("my-skill"))
	if err != nil {
		t.Fatalf("cached file not found: %v", err)
	}
	if string(data) != content {
		t.Errorf("cached content = %q, want %q", string(data), content)
	}
}

func TestInstall_Idempotent(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Write([]byte("# Skill\n")) //nolint:errcheck
	}))
	defer srv.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := Install("idempotent-skill", srv.URL+"/SKILL.md"); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	if err := Install("idempotent-skill", srv.URL+"/SKILL.md"); err != nil {
		t.Fatalf("second Install: %v", err)
	}

	if callCount != 1 {
		t.Errorf("HTTP server called %d times, want 1 (idempotent)", callCount)
	}
}

func TestInstall_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	err := Install("missing-skill", srv.URL+"/SKILL.md")
	if err == nil {
		t.Fatal("expected error for HTTP 404, got nil")
	}
}

func TestInstall_PathTraversal(t *testing.T) {
	err := Install("../../evil", "http://example.com/SKILL.md")
	if err == nil {
		t.Fatal("expected error for path-traversal skill name, got nil")
	}
}

func TestForceUpdate_PathTraversal(t *testing.T) {
	err := Update("../escape", "http://example.com/SKILL.md")
	if err == nil {
		t.Fatal("expected error for path-traversal skill name, got nil")
	}
}

func TestInstall_DownloadExceedsMaxSize(t *testing.T) {
	// Serve a body larger than the 1 MiB cap; Install must return an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxSkillSize+1)) //nolint:errcheck
	}))
	defer srv.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	err := Install("oversized-skill", srv.URL+"/SKILL.md")
	if err == nil {
		t.Fatal("expected error for response exceeding max size, got nil")
	}
}

func TestInstall_Update(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Write([]byte("# Updated Skill\n")) //nolint:errcheck
	}))
	defer srv.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Normal install caches once.
	if err := Install("force-skill", srv.URL+"/SKILL.md"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// ForceUpdate re-fetches even though cached.
	if err := Update("force-skill", srv.URL+"/SKILL.md"); err != nil {
		t.Fatalf("ForceUpdate: %v", err)
	}

	if callCount != 2 {
		t.Errorf("HTTP server called %d times, want 2 (force re-fetch)", callCount)
	}
}

// --- Deploy tests ---

func TestDeploy_WritesContentToLocalPath(t *testing.T) {
	// Given: no skill installed.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	content := []byte("# Deployed Skill\nContent from git.\n")

	// When: Deploy is called with skill content.
	changed, err := Deploy("git-skill", content)

	// Then: file written, changed=true, no error.
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !changed {
		t.Error("expected changed=true for new skill")
	}
	data, err := os.ReadFile(LocalPath("git-skill"))
	if err != nil {
		t.Fatalf("skill file not created: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content = %q, want %q", string(data), string(content))
	}
}

func TestDeploy_UpdatesManifestWithLocalSource(t *testing.T) {
	// Given: no skill installed.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// When: Deploy is called.
	if _, err := Deploy("manifest-skill", []byte("# Skill\n")); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	// Then: manifest records the skill with source_url "local".
	entries, err := ListInstalled()
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Name == "manifest-skill" {
			found = true
			if e.SourceURL != "local" {
				t.Errorf("source_url = %q, want %q", e.SourceURL, "local")
			}
		}
	}
	if !found {
		t.Error("skill not found in manifest after Deploy")
	}
}

func TestDeploy_NoOpWhenContentUnchanged(t *testing.T) {
	// Given: skill already deployed with same content.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	content := []byte("# Skill\nSame content.\n")
	if _, err := Deploy("noop-skill", content); err != nil {
		t.Fatalf("first Deploy: %v", err)
	}

	// When: Deploy called again with identical content.
	changed, err := Deploy("noop-skill", content)

	// Then: no-op (changed=false, no error).
	if err != nil {
		t.Fatalf("second Deploy: %v", err)
	}
	if changed {
		t.Error("expected changed=false when content is identical")
	}
}

func TestDeploy_WritesNewContentWhenChanged(t *testing.T) {
	// Given: skill deployed with v1 content.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	v1 := []byte("# Skill v1\n")
	v2 := []byte("# Skill v2\n")
	if _, err := Deploy("update-skill", v1); err != nil {
		t.Fatalf("Deploy v1: %v", err)
	}

	// When: Deploy called with new content.
	changed, err := Deploy("update-skill", v2)

	// Then: file updated, changed=true.
	if err != nil {
		t.Fatalf("Deploy v2: %v", err)
	}
	if !changed {
		t.Error("expected changed=true when content differs")
	}
	data, _ := os.ReadFile(LocalPath("update-skill"))
	if string(data) != string(v2) {
		t.Errorf("content = %q, want %q", string(data), string(v2))
	}
}

func TestDeploy_RejectsInvalidName(t *testing.T) {
	// Given: an invalid skill name with path traversal.
	// When: Deploy is called.
	_, err := Deploy("../../evil", []byte("bad"))

	// Then: error returned.
	if err == nil {
		t.Fatal("expected error for invalid skill name, got nil")
	}
}
