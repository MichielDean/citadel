package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	_ "github.com/mattn/go-sqlite3"
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

// makeInProgressItem creates a DB with a single in_progress droplet assigned to
// "test-worker" and returns its ID. CT_DB is set for the test.
func makeInProgressItem(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "test.db")
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
	return item.ID
}

// TestDropletPeekRaw_ReadsSessionLog verifies that --raw reads and prints the
// session log file for the droplet's session.
//
// Given: a droplet in_progress and a session log file exists
// When:  peek is run with --raw
// Then:  the log file contents are written to stdout
func TestDropletPeekRaw_ReadsSessionLog(t *testing.T) {
	itemID := makeInProgressItem(t)

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

	err := dropletPeekCmd.RunE(dropletPeekCmd, []string{itemID})

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
	itemID := makeInProgressItem(t)

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

	err := dropletPeekCmd.RunE(dropletPeekCmd, []string{itemID})

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
	itemID := makeInProgressItem(t)

	peekLines = 0
	peekRaw = true
	peekFollow = false
	peekSnapshot = true

	err := dropletPeekCmd.RunE(dropletPeekCmd, []string{itemID})

	if err == nil {
		t.Fatal("expected error when --raw used with --snapshot, got nil")
	}
	if !strings.Contains(err.Error(), "--snapshot") {
		t.Errorf("error should mention --snapshot, got: %q", err.Error())
	}
}

// TestDropletPeekRaw_WithFollow_ReturnsError verifies that --raw and --follow
// are mutually exclusive.
//
// Given: a droplet in_progress
// When:  peek is run with both --raw and --follow
// Then:  an error mentioning --follow is returned
func TestDropletPeekRaw_WithFollow_ReturnsError(t *testing.T) {
	itemID := makeInProgressItem(t)

	peekLines = 0
	peekRaw = true
	peekFollow = true
	peekSnapshot = false

	err := dropletPeekCmd.RunE(dropletPeekCmd, []string{itemID})

	if err == nil {
		t.Fatal("expected error when --raw used with --follow, got nil")
	}
	if !strings.Contains(err.Error(), "--follow") {
		t.Errorf("error should mention --follow, got: %q", err.Error())
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

// TestDropletPeek_StageElapsedNonZero_ShowsStageAge verifies that the peek CLI
// header includes '(stage X)' when StageDispatchedAt is set far enough in the
// past to produce a non-zero formatted duration.
//
// Given: a droplet in_progress with StageDispatchedAt 5 minutes ago
// When:  droplet peek is run
// Then:  the header line contains '(stage 5m'
func TestDropletPeek_StageElapsedNonZero_ShowsStageAge(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	dr, err := c.Add("myrepo", "Stage peek test", "", 1, 3)
	if err != nil {
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

	// Backdate StageDispatchedAt so formatStageElapsed produces a visible duration.
	dbConn, err := sql.Open("sqlite3", db)
	if err != nil {
		t.Fatal(err)
	}
	defer dbConn.Close()
	dispatchedAt := time.Now().Add(-5 * time.Minute)
	_, err = dbConn.Exec(
		`UPDATE droplets SET stage_dispatched_at = ? WHERE id = ?`,
		dispatchedAt.UTC().Format("2006-01-02T15:04:05.999Z"), dr.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	origHasSession := tmuxHasSession
	tmuxHasSession = func(_ string) bool { return false }
	defer func() { tmuxHasSession = origHasSession }()

	old := os.Stdout
	r2, w, _ := os.Pipe()
	os.Stdout = w

	peekLines = 50
	peekRaw = false
	peekFollow = false
	peekSnapshot = false

	err = dropletPeekCmd.RunE(dropletPeekCmd, []string{dr.ID})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r2)
	out := buf.String()

	if !strings.Contains(out, "(stage ") {
		t.Errorf("peek header should contain '(stage X)' when StageDispatchedAt is set, got:\n%q", out)
	}
	if !strings.Contains(out, "5m") && !strings.Contains(out, "4m") {
		t.Errorf("peek header should show ~5m stage elapsed, got:\n%q", out)
	}
}

// TestDropletPeek_StageElapsedZero_OmitsStageAge verifies that the peek CLI
// header does not include '(stage' when StageDispatchedAt is not set.
//
// Given: a droplet in_progress with StageDispatchedAt = zero
// When:  droplet peek is run
// Then:  the header line contains 'flowing' but NOT '(stage'
func TestDropletPeek_StageElapsedZero_OmitsStageAge(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	dr, err := c.Add("myrepo", "Zero dispatch test", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	// Directly set in_progress without StageDispatchedAt.
	dbConn, err := sql.Open("sqlite3", db)
	if err != nil {
		t.Fatal(err)
	}
	defer dbConn.Close()
	_, err = dbConn.Exec(
		`UPDATE droplets SET assignee = 'test-worker', current_cataractae = 'implement', status = 'in_progress' WHERE id = ?`,
		dr.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	origHasSession := tmuxHasSession
	tmuxHasSession = func(_ string) bool { return false }
	defer func() { tmuxHasSession = origHasSession }()

	old := os.Stdout
	r2, w, _ := os.Pipe()
	os.Stdout = w

	peekLines = 50
	peekRaw = false
	peekFollow = false
	peekSnapshot = false

	err = dropletPeekCmd.RunE(dropletPeekCmd, []string{dr.ID})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r2)
	out := buf.String()

	if strings.Contains(out, "(stage ") {
		t.Errorf("peek header should NOT contain '(stage' when StageDispatchedAt is zero, got:\n%q", out)
	}
}

// TestDropletPeek_StageElapsedSubSecond_OmitsStageAge verifies that the peek CLI
// header does not include '(stage X)' when StageDispatchedAt was set less than 1s
// ago (formatElapsed rounds to "0s" which formatStageElapsed filters out).
//
// Given: a droplet just dispatched (StageDispatchedAt < 1s ago, formats to "0s")
// When:  droplet peek is run
// Then:  the header line does NOT contain '(stage'
func TestDropletPeek_StageElapsedSubSecond_OmitsStageAge(t *testing.T) {
	itemID := makeInProgressItem(t)

	origHasSession := tmuxHasSession
	tmuxHasSession = func(_ string) bool { return false }
	defer func() { tmuxHasSession = origHasSession }()

	old := os.Stdout
	r2, w, _ := os.Pipe()
	os.Stdout = w

	peekLines = 50
	peekRaw = false
	peekFollow = false
	peekSnapshot = false

	err := dropletPeekCmd.RunE(dropletPeekCmd, []string{itemID})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r2)
	out := buf.String()

	if strings.Contains(out, "(stage ") {
		t.Errorf("peek header should NOT contain '(stage' when StageElapsed formats to '0s', got:\n%q", out)
	}
}

func TestFormatPeekFollowSeparator_StageElapsedNonZero_IncludesStageAge(t *testing.T) {
	updatedAt := time.Now().Add(-10 * time.Minute)
	stageDispatchedAt := time.Now().Add(-5 * time.Minute)

	got := formatPeekFollowSeparator(updatedAt, stageDispatchedAt)

	if !strings.Contains(got, "(stage ") {
		t.Errorf("expected '(stage X)' in separator when StageDispatchedAt is set, got: %q", got)
	}
	if !strings.Contains(got, "5m") && !strings.Contains(got, "4m") {
		t.Errorf("expected ~5m stage elapsed in separator, got: %q", got)
	}
	if !strings.Contains(got, "10m") && !strings.Contains(got, "9m") {
		t.Errorf("expected ~10m overall elapsed in separator, got: %q", got)
	}
}

func TestFormatPeekFollowSeparator_StageDispatchedAtZero_OmitsStageAge(t *testing.T) {
	updatedAt := time.Now().Add(-10 * time.Minute)
	stageDispatchedAt := time.Time{}

	got := formatPeekFollowSeparator(updatedAt, stageDispatchedAt)

	if strings.Contains(got, "(stage ") {
		t.Errorf("expected NO '(stage X)' in separator when StageDispatchedAt is zero, got: %q", got)
	}
	if !strings.Contains(got, "10m") && !strings.Contains(got, "9m") {
		t.Errorf("expected overall elapsed in separator, got: %q", got)
	}
}

func TestFormatPeekFollowSeparator_SubSecondStage_OmitsStageAge(t *testing.T) {
	updatedAt := time.Now().Add(-10 * time.Minute)
	stageDispatchedAt := time.Now().Add(-200 * time.Millisecond)

	got := formatPeekFollowSeparator(updatedAt, stageDispatchedAt)

	if strings.Contains(got, "(stage ") {
		t.Errorf("expected NO '(stage X)' in separator when stage elapsed formats to 0s, got: %q", got)
	}
	if !strings.Contains(got, "10m") && !strings.Contains(got, "9m") {
		t.Errorf("expected overall elapsed in separator, got: %q", got)
	}
}
