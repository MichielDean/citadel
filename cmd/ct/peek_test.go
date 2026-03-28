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

// TestDropletPeekLiveSession_AttachesReadOnly verifies that when a tmux session
// exists and --snapshot is not set, peek calls attach-session with the -r flag.
//
// Given: a droplet in progress with a tmux session present (injected)
// When:  droplet peek is run without --snapshot
// Then:  tmuxAttachFunc is called with the correct session name (<repo>-<assignee>)
func TestDropletPeekLiveSession_AttachesReadOnly(t *testing.T) {
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
	item, err := c.GetReady("myrepo")
	if err != nil || item == nil {
		t.Fatalf("GetReady failed: %v", err)
	}
	if err := c.Assign(item.ID, "test-worker", "implement"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	// Inject has-session to simulate a live tmux session.
	origHasSession := tmuxHasSession
	tmuxHasSession = func(_ string) bool { return true }
	defer func() { tmuxHasSession = origHasSession }()

	// Capture the attach call to verify -r semantics (attach-session -t <session> -r).
	var attachedSession string
	origAttach := tmuxAttachFunc
	tmuxAttachFunc = func(session string) error {
		attachedSession = session
		return nil
	}
	defer func() { tmuxAttachFunc = origAttach }()

	peekLines = 50
	peekRaw = false
	peekFollow = false
	peekSnapshot = false

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = dropletPeekCmd.RunE(dropletPeekCmd, []string{item.ID})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantSession := "myrepo-test-worker"
	if attachedSession != wantSession {
		t.Errorf("tmuxAttachFunc called with session %q, want %q (attach-session -t <session> -r)", attachedSession, wantSession)
	}

	// Drain the pipe to avoid a broken pipe.
	var buf bytes.Buffer
	buf.ReadFrom(r)
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

// TestDropletPeekRaw_ReadsSessionLog verifies that --raw reads and prints the
// session log file for the droplet's session.
//
// Given: a droplet in_progress and a session log file exists
// When:  peek is run with --raw
// Then:  the log file contents are written to stdout
func TestDropletPeekRaw_ReadsSessionLog(t *testing.T) {
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
	item, err := c.GetReady("myrepo")
	if err != nil || item == nil {
		t.Fatalf("GetReady failed: %v", err)
	}
	if err := c.Assign(item.ID, "test-worker", "implement"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	// Create a session log file in a temp dir.
	logDir := t.TempDir()
	sessionLogDir = logDir
	defer func() { sessionLogDir = "" }()

	logContent := "agent output line 1\nagent output line 2\n"
	logFile := filepath.Join(logDir, "myrepo-test-worker.log")
	if err := os.WriteFile(logFile, []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	peekLines = 0
	peekRaw = true
	peekFollow = false
	peekSnapshot = false

	err = dropletPeekCmd.RunE(dropletPeekCmd, []string{item.ID})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "agent output line 1") {
		t.Errorf("expected session log content in output, got: %q", out)
	}
	if !strings.Contains(out, "agent output line 2") {
		t.Errorf("expected session log content in output, got: %q", out)
	}
}

// TestDropletPeekRaw_NoLogFile_PrintsHelpfulMessage verifies that --raw prints
// a helpful message when no session log file exists for the droplet.
//
// Given: a droplet in_progress but no session log file exists
// When:  peek is run with --raw
// Then:  a helpful "No session log found" message is printed to stdout
func TestDropletPeekRaw_NoLogFile_PrintsHelpfulMessage(t *testing.T) {
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
	item, err := c.GetReady("myrepo")
	if err != nil || item == nil {
		t.Fatalf("GetReady failed: %v", err)
	}
	if err := c.Assign(item.ID, "test-worker", "implement"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	// Point sessionLogDir to an empty temp dir (no log file present).
	sessionLogDir = t.TempDir()
	defer func() { sessionLogDir = "" }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	peekLines = 0
	peekRaw = true
	peekFollow = false
	peekSnapshot = false

	err = dropletPeekCmd.RunE(dropletPeekCmd, []string{item.ID})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "No session log") {
		t.Errorf("expected 'No session log' message, got: %q", out)
	}
}

// TestDropletPeekRaw_WithSnapshot_ReturnsError verifies that --raw and --snapshot
// are mutually exclusive.
//
// Given: a droplet in_progress
// When:  peek is run with both --raw and --snapshot
// Then:  an error mentioning --snapshot is returned
func TestDropletPeekRaw_WithSnapshot_ReturnsError(t *testing.T) {
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
	item, err := c.GetReady("myrepo")
	if err != nil || item == nil {
		t.Fatalf("GetReady failed: %v", err)
	}
	if err := c.Assign(item.ID, "test-worker", "implement"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	peekLines = 0
	peekRaw = true
	peekFollow = false
	peekSnapshot = true

	err = dropletPeekCmd.RunE(dropletPeekCmd, []string{item.ID})

	if err == nil {
		t.Fatal("expected error when --raw used with --snapshot, got nil")
	}
	if !strings.Contains(err.Error(), "--snapshot") {
		t.Errorf("error should mention --snapshot, got: %q", err.Error())
	}
}

// TestDropletPeek_FollowWithoutSnapshot_ReturnsError verifies that running
// 'ct droplet peek --follow' without --snapshot returns an error explaining
// that --follow requires --snapshot.
//
// Given: a droplet in progress with a live tmux session
// When:  peek is run with --follow but without --snapshot
// Then:  an error is returned containing guidance to use --snapshot
func TestDropletPeek_FollowWithoutSnapshot_ReturnsError(t *testing.T) {
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
	item, err := c.GetReady("myrepo")
	if err != nil || item == nil {
		t.Fatalf("GetReady failed: %v", err)
	}
	if err := c.Assign(item.ID, "test-worker", "implement"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	peekLines = 50
	peekRaw = false
	peekFollow = true
	peekSnapshot = false

	err = dropletPeekCmd.RunE(dropletPeekCmd, []string{item.ID})

	if err == nil {
		t.Fatal("expected error when --follow used without --snapshot, got nil")
	}
	if !strings.Contains(err.Error(), "--snapshot") {
		t.Errorf("error should mention --snapshot, got: %q", err.Error())
	}
}
