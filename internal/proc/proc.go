// Package proc provides helpers for inspecting the Linux /proc filesystem to
// detect live agent processes. It is shared between the castellarius scheduler
// and the cataractae session manager so both use identical logic.
package proc

import (
	"os"
	"path/filepath"
	"strings"
)

// ClaudeAliveUnderPIDIn returns true when any descendant of panePIDStr (read
// from procRoot) has a cmdline whose argv[0] base name is "claude" or starts
// with "claude-". procRoot is the proc filesystem root; tests may pass a fake
// directory.
func ClaudeAliveUnderPIDIn(panePIDStr, procRoot string) bool {
	if panePIDStr == "" {
		return false
	}

	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return false
	}

	type procInfo struct {
		ppid    string
		cmdline string
	}
	infos := make(map[string]procInfo, len(entries))

	for _, entry := range entries {
		name := entry.Name()
		if !IsProcPIDEntry(name) {
			continue
		}
		statusData, err := os.ReadFile(filepath.Join(procRoot, name, "status"))
		if err != nil {
			continue
		}
		ppid := ParsePPid(string(statusData))
		cmdlineData, _ := os.ReadFile(filepath.Join(procRoot, name, "cmdline"))
		infos[name] = procInfo{ppid: ppid, cmdline: string(cmdlineData)}
	}

	// Build parent → children map.
	children := make(map[string][]string, len(infos))
	for pid, info := range infos {
		if info.ppid != "" {
			children[info.ppid] = append(children[info.ppid], pid)
		}
	}

	// BFS from panePIDStr; return true on the first claude descendant found.
	queue := []string{panePIDStr}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if info, ok := infos[pid]; ok && IsClaudeCmdline(info.cmdline) {
			return true
		}
		queue = append(queue, children[pid]...)
	}
	return false
}

// IsProcPIDEntry reports whether s is a valid Linux /proc PID directory name
// (a non-empty string of decimal digits).
func IsProcPIDEntry(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ParsePPid extracts the PPid value from a /proc/<pid>/status file.
func ParsePPid(status string) string {
	for _, line := range strings.Split(status, "\n") {
		if after, ok := strings.CutPrefix(line, "PPid:"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// IsClaudeCmdline returns true when the null-separated cmdline identifies a
// claude process — the base name of argv[0] is "claude" or starts with "claude-".
func IsClaudeCmdline(cmdline string) bool {
	if cmdline == "" {
		return false
	}
	argv0, _, _ := strings.Cut(cmdline, "\x00")
	if argv0 == "" {
		return false
	}
	base := filepath.Base(argv0)
	return base == "claude" || strings.HasPrefix(base, "claude-")
}
