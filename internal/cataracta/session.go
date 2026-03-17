package cataracta

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Session manages a Claude Code execution inside a tmux session.
type Session struct {
	// ID is the tmux session name (e.g., "myrepo-alice").
	ID string

	// WorkDir is the directory claude runs in.
	WorkDir string

	// Model is the LLM model to use (e.g., "sonnet", "haiku").
	// Empty means default.
	Model string

	// Identity is the agent cataracta identity (e.g., "implementer", "reviewer").
	// Used to locate cataractae/<identity>/CLAUDE.md in the working directory.
	Identity string

	// TimeoutMinutes is the maximum runtime hint passed to the agent via CONTEXT.md.
	// 0 means default (60 minutes).
	TimeoutMinutes int
}

// Spawn creates a new tmux session running claude and returns immediately.
// The Castellarius observe loop detects completion via the outcome field in the DB —
// agents signal their outcome by calling `ct droplet pass/recirculate/block <id>`.
func (s *Session) Spawn() error {
	return s.spawn()
}

// spawn creates a new tmux session running claude.
func (s *Session) spawn() error {
	// Kill any stale session with the same name.
	s.kill()

	// Build the claude command string. The prompt must be single-quoted so that
	// tmux/sh doesn't word-split it — unquoted spaces would cause only the first
	// word to be passed to -p. Single-quote the prompt and escape any literal
	// single quotes inside it using the 'x'\''y' idiom.
	prompt := strings.ReplaceAll(s.buildPrompt(), "'", `'\''`)

	var flagsStr string
	if s.Model != "" {
		flagsStr = "--model " + s.Model + " "
	}
	claudeCmd := fmt.Sprintf("%s --dangerously-skip-permissions %s-p '%s'", claudePath(), flagsStr, prompt)

	args := []string{"new-session", "-d", "-s", s.ID, "-c", s.WorkDir}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		args = append(args, "-e", "ANTHROPIC_API_KEY="+key)
	}
	if path := os.Getenv("PATH"); path != "" {
		args = append(args, "-e", "PATH="+path)
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		args = append(args, "-e", "GH_TOKEN="+tok)
	}
	args = append(args, claudeCmd)
	cmd := exec.Command("tmux", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session %s: %w: %s", s.ID, err, out)
	}

	log.Printf("session %s: spawned in %s", s.ID, s.WorkDir)
	return nil
}

// buildPrompt constructs the directive prompt for Claude.
func (s *Session) buildPrompt() string {
	identityInstr := ""
	if s.Identity != "" {
		identityPath := s.resolveIdentityPath()
		identityInstr = "Read " + identityPath + " for your detailed instructions and protocol. "
	}
	return "You are a Cistern agent. " +
		"Read CONTEXT.md in this directory — it contains your assignment and the exact ct commands to signal completion. " +
		identityInstr +
		"Complete the work described fully. " +
		"Signal your outcome with the ct commands shown in CONTEXT.md when done."
}

// resolveIdentityPath returns the path to the cataracta identity's CLAUDE.md file.
// Checks ~/.cistern/cataractae/<identity>/CLAUDE.md first, then cataractae/<identity>/CLAUDE.md in the sandbox.
func (s *Session) resolveIdentityPath() string {
	home, err := os.UserHomeDir()
	if err == nil {
		cisternPath := filepath.Join(home, ".cistern", "cataractae", s.Identity, "CLAUDE.md")
		if _, err := os.Stat(cisternPath); err == nil {
			return cisternPath
		}
	}
	return "cataractae/" + s.Identity + "/CLAUDE.md"
}

// kill terminates the tmux session if it exists.
func (s *Session) kill() {
	exec.Command("tmux", "kill-session", "-t", s.ID).Run()
}

// isAlive checks whether the tmux session still exists.
func (s *Session) isAlive() bool {
	err := exec.Command("tmux", "has-session", "-t", s.ID).Run()
	return err == nil
}

// claudePath returns the absolute path to the claude binary.
func claudePath() string {
	if p := os.Getenv("CLAUDE_PATH"); p != "" {
		return p
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return os.ExpandEnv("$HOME/.local/bin/claude")
}
