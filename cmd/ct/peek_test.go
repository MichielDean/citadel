package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/cistern"
)

func TestStripANSI(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"\x1b[32mhello\x1b[0m", "hello"},
		{"\x1b[1;31mred bold\x1b[0m text", "red bold text"},
		{"no codes here", "no codes here"},
		{"", ""},
	}
	for _, tc := range cases {
		got := stripANSI(tc.input)
		if got != tc.want {
			t.Errorf("stripANSI(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDropletPeekNotFlowing(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("myrepo", "Test item", "", 1, 3)
	c.Close()
	if err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	peekLines = 50
	peekRaw = false
	peekFollow = false
	err = dropletPeekCmd.RunE(dropletPeekCmd, []string{item.ID})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "not currently flowing") {
		t.Errorf("expected 'not currently flowing' message, got: %q", out)
	}
	if !strings.Contains(out, "queued") {
		t.Errorf("expected status 'queued' in output, got: %q", out)
	}
}

func TestDropletPeekNoSession(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Add("myrepo", "Test item", "", 1, 3); err != nil {
		t.Fatal(err)
	}
	// Advance to in_progress, then assign a non-existent worker so tmux lookup fails.
	item, err := c.GetReady("myrepo")
	if err != nil || item == nil {
		t.Fatalf("GetReady failed: %v", err)
	}
	if err := c.Assign(item.ID, "ghost-worker-xyz987", "implement"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddNote(item.ID, "implement", "line one\nline two\nline three"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	peekLines = 50
	peekRaw = false
	peekFollow = false
	err = dropletPeekCmd.RunE(dropletPeekCmd, []string{item.ID})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "No active tmux session found") {
		t.Errorf("expected no-session message, got: %q", out)
	}
	// Should fall back to showing the most recent note content.
	if !strings.Contains(out, "line three") {
		t.Errorf("expected fallback note content, got: %q", out)
	}
	// Elapsed time header should appear before pane output.
	if !strings.Contains(out, "flowing") {
		t.Errorf("expected elapsed time header with 'flowing', got: %q", out)
	}
}
