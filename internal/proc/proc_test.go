package proc_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/proc"
)

// writeFakeProcEntry creates a minimal /proc/<pid> directory under procRoot
// with a status file containing the given ppid and a cmdline file with
// null-separated args.
func writeFakeProcEntry(t *testing.T, procRoot, pid, ppid string, argv ...string) {
	t.Helper()
	dir := filepath.Join(procRoot, pid)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	status := fmt.Sprintf("Name:\tsh\nPPid:\t%s\nUid:\t1000\n", ppid)
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0644); err != nil {
		t.Fatal(err)
	}
	cmdline := strings.Join(argv, "\x00") + "\x00"
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestIsProcPIDEntry_ValidAndInvalid exercises the PID name filter.
func TestIsProcPIDEntry_ValidAndInvalid(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"123", true},
		{"1", true},
		{"0", true},
		{"", false},
		{"abc", false},
		{"12a", false},
		{"net", false},
		{"self", false},
	}
	for _, tc := range cases {
		if got := proc.IsProcPIDEntry(tc.in); got != tc.want {
			t.Errorf("IsProcPIDEntry(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestParsePPid_ExtractsPPidLine verifies ParsePPid returns the PPid value.
func TestParsePPid_ExtractsPPidLine(t *testing.T) {
	status := "Name:\tbash\nPid:\t42\nPPid:\t7\nUid:\t1000\n"
	if got := proc.ParsePPid(status); got != "7" {
		t.Errorf("ParsePPid = %q, want %q", got, "7")
	}
}

// TestParsePPid_MissingLine returns empty string when PPid line is absent.
func TestParsePPid_MissingLine(t *testing.T) {
	if got := proc.ParsePPid("Name:\tbash\nPid:\t42\n"); got != "" {
		t.Errorf("ParsePPid = %q, want empty", got)
	}
}

// TestIsClaudeCmdline covers positive and negative cases.
func TestIsClaudeCmdline(t *testing.T) {
	cases := []struct {
		cmdline string
		want    bool
	}{
		{"/usr/bin/claude\x00--dangerously-skip-permissions\x00", true},
		{"claude\x00", true},
		{"/home/user/.nvm/bin/claude-code\x00arg\x00", true},
		{"/bin/bash\x00-c\x00sleep 100\x00", false},
		{"sh\x00", false},
		{"", false},
		{"\x00", false},
		{"/usr/bin/notclaude\x00", false},
		{"/usr/bin/claudeX\x00", false}, // not exact "claude" or "claude-*"
	}
	for _, tc := range cases {
		if got := proc.IsClaudeCmdline(tc.cmdline); got != tc.want {
			t.Errorf("IsClaudeCmdline(%q) = %v, want %v", tc.cmdline, got, tc.want)
		}
	}
}

// TestClaudeAliveUnderPIDIn_EmptyPID_ReturnsFalse ensures the empty PID guard fires.
func TestClaudeAliveUnderPIDIn_EmptyPID_ReturnsFalse(t *testing.T) {
	if proc.ClaudeAliveUnderPIDIn("", t.TempDir()) {
		t.Error("expected false for empty panePIDStr")
	}
}

// TestClaudeAliveUnderPIDIn_NoClaudeProcess_ReturnsFalse verifies that a
// process tree with no claude process returns false.
func TestClaudeAliveUnderPIDIn_NoClaudeProcess_ReturnsFalse(t *testing.T) {
	procRoot := t.TempDir()
	// Pane PID 1 (shell), child 2 (another shell). Neither is claude.
	writeFakeProcEntry(t, procRoot, "1", "0", "/bin/bash")
	writeFakeProcEntry(t, procRoot, "2", "1", "/usr/bin/python3", "script.py")
	if proc.ClaudeAliveUnderPIDIn("1", procRoot) {
		t.Error("expected false when no claude descendant exists")
	}
}

// TestClaudeAliveUnderPIDIn_DirectClaudeChild_ReturnsTrue verifies that a
// direct claude child of the pane PID is detected.
func TestClaudeAliveUnderPIDIn_DirectClaudeChild_ReturnsTrue(t *testing.T) {
	procRoot := t.TempDir()
	writeFakeProcEntry(t, procRoot, "1", "0", "/bin/bash")
	writeFakeProcEntry(t, procRoot, "2", "1", "/usr/local/bin/claude", "--dangerously-skip-permissions")
	if !proc.ClaudeAliveUnderPIDIn("1", procRoot) {
		t.Error("expected true when direct claude child exists")
	}
}

// TestClaudeAliveUnderPIDIn_DeepDescendant_ReturnsTrue verifies that claude is
// found even when it is several levels deep in the process tree (bash → sh → node → claude).
func TestClaudeAliveUnderPIDIn_DeepDescendant_ReturnsTrue(t *testing.T) {
	procRoot := t.TempDir()
	// Tree: 1 → 2 (sh) → 3 (node) → 4 (claude)
	writeFakeProcEntry(t, procRoot, "1", "0", "/bin/bash")
	writeFakeProcEntry(t, procRoot, "2", "1", "/bin/sh", "-c", "node launcher.js")
	writeFakeProcEntry(t, procRoot, "3", "2", "/usr/bin/node", "launcher.js")
	writeFakeProcEntry(t, procRoot, "4", "3", "/home/user/.local/bin/claude", "arg")
	if !proc.ClaudeAliveUnderPIDIn("1", procRoot) {
		t.Error("expected true when claude is a deep descendant")
	}
}

// TestClaudeAliveUnderPIDIn_UnrelatedClaudeProcess_ReturnsFalse verifies that
// a claude process that is NOT a descendant of the pane PID is not reported.
func TestClaudeAliveUnderPIDIn_UnrelatedClaudeProcess_ReturnsFalse(t *testing.T) {
	procRoot := t.TempDir()
	// Pane PID is 10; claude runs under PID 1 (unrelated).
	writeFakeProcEntry(t, procRoot, "1", "0", "/bin/bash")
	writeFakeProcEntry(t, procRoot, "2", "1", "/usr/bin/claude")
	writeFakeProcEntry(t, procRoot, "10", "0", "/bin/bash") // our pane, no claude children
	if proc.ClaudeAliveUnderPIDIn("10", procRoot) {
		t.Error("expected false when claude is not a descendant of pane PID")
	}
}

// TestClaudeAliveUnderPIDIn_NonexistentPanePID_ReturnsFalse handles a pane PID
// that no longer appears in /proc (process already gone).
func TestClaudeAliveUnderPIDIn_NonexistentPanePID_ReturnsFalse(t *testing.T) {
	procRoot := t.TempDir()
	writeFakeProcEntry(t, procRoot, "1", "0", "/bin/bash")
	if proc.ClaudeAliveUnderPIDIn("9999", procRoot) {
		t.Error("expected false when pane PID does not appear in /proc")
	}
}
