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

	got := CachePath("example")
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

	data, err := os.ReadFile(CachePath("my-skill"))
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
	err := ForceUpdate("../escape", "http://example.com/SKILL.md")
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

func TestInstall_ForceUpdate(t *testing.T) {
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
	if err := ForceUpdate("force-skill", srv.URL+"/SKILL.md"); err != nil {
		t.Fatalf("ForceUpdate: %v", err)
	}

	if callCount != 2 {
		t.Errorf("HTTP server called %d times, want 2 (force re-fetch)", callCount)
	}
}
