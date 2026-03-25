package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestCapturePane_FullScrollback_ReturnsHistoryBeyondVisible is an integration
// test that spawns a real tmux session, writes 200 lines of known output
// (far more than the default 24-row visible area), and then asserts that
// capturePane with lines=0 returns the full scrollback — including early lines
// that have long since scrolled past the visible area.
//
// Given: a tmux session with 200 lines of known output
// When:  capturePane is called with lines=0 (full scrollback)
// Then:  the first line "scrollback-line-0001" appears in the captured output
func TestCapturePane_FullScrollback_ReturnsHistoryBeyondVisible(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not in PATH — skipping integration test")
	}

	session := fmt.Sprintf("ct-test-scrollback-%d", os.Getpid())

	// Create a detached session so the test is non-interactive.
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", session).Run() //nolint:errcheck
	})

	// Send a shell loop that writes 200 uniquely-numbered lines and then prints
	// a sentinel so we can poll for completion without a fixed sleep.
	const nLines = 200
	script := `for i in $(seq 1 200); do printf 'scrollback-line-%04d\n' "$i"; done; echo SCROLLBACK_DONE`
	if out, err := exec.Command("tmux", "send-keys", "-t", session+":0.0", script, "Enter").CombinedOutput(); err != nil {
		t.Fatalf("tmux send-keys: %v: %s", err, out)
	}

	// Poll the visible pane until the sentinel appears (up to 10 seconds).
	found := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, _ := exec.Command("tmux", "capture-pane", "-t", session+":0.0", "-p").Output()
		if strings.Contains(string(raw), "SCROLLBACK_DONE") {
			found = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !found {
		t.Fatal("timed out waiting for tmux shell to finish writing scrollback lines")
	}

	// Capture with lines=0: must return the full scrollback buffer.
	content, err := capturePane(session, 0)
	if err != nil {
		t.Fatalf("capturePane(session, 0): %v", err)
	}

	// The very first line must appear — not just the final screenfull.
	if !strings.Contains(content, "scrollback-line-0001") {
		tail := content
		if len(tail) > 500 {
			tail = "…" + tail[len(tail)-500:]
		}
		t.Errorf("full scrollback should contain 'scrollback-line-0001' (early history); content tail:\n%s", tail)
	}

	// The last line must also appear.
	want := fmt.Sprintf("scrollback-line-%04d", nLines)
	if !strings.Contains(content, want) {
		t.Errorf("full scrollback missing last line %q", want)
	}
}
