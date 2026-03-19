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
	if s.Identity != "" {
		args = append(args, "-e", "CT_CATARACTA_NAME="+s.Identity)
	}
	args = append(args, claudeCmd)
	cmd := exec.Command("tmux", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session %s: %w: %s", s.ID, err, out)
	}

	log.Printf("session %s: spawned in %s", s.ID, s.WorkDir)
	return nil
}

// baseCataractaPrompt is the constitutional layer — hardcoded in the binary,
// cannot be corrupted by YAML edits or file changes. It establishes the
// non-negotiable contract for every cataracta session.
const baseCataractaPrompt = `You are a Cataracta operating within the Cistern agentic pipeline.

Cistern is an automated software delivery system. The Castellarius (a pure state
machine) watches the cistern and routes droplets (units of work) into named
aqueducts. You are one cataracta — one gate — in that aqueduct. You receive a
droplet, complete your assigned role, and signal your outcome so the droplet
continues flowing.

THE CASTELLARIUS WATCHES THE CISTERN, ROUTES DROPLETS INTO AVAILABLE AQUEDUCTS.
EACH AQUEDUCT FLOWS THE DROPLET THROUGH ITS CATARACTAE.

## Your contract — non-negotiable

1. Read CONTEXT.md before doing anything else. It contains your droplet ID,
   requirements, and all revision notes from prior cycles.
2. Adopt the persona described in your role instructions below.
3. Complete your work according to that persona.
4. Signal your outcome before exiting. You MUST call one of:
     ct droplet pass <id> --notes "..."
     ct droplet recirculate <id> --notes "..."
     ct droplet block <id> --notes "..."
   A cataracta that exits without signaling leaves the droplet stranded.

Your role persona and skill instructions follow.
`

// buildPrompt constructs the full agent prompt: constitutional base + persona + skills.
func (s *Session) buildPrompt() string {
	// Layer 1: Constitutional base (immutable — hardcoded in binary)
	prompt := baseCataractaPrompt

	// Layer 2: Persona (from CLAUDE.md / cataracta_definitions YAML)
	if s.Identity != "" {
		identityPath := s.resolveIdentityPath()
		if content, err := os.ReadFile(identityPath); err == nil {
			prompt += "\n## Your Role\n\n" + string(content)
		} else {
			// File missing/unreadable — fall back to pointer so agent can try to find it
			prompt += "\nRead " + identityPath + " for your role instructions. "
		}
	}

	// Layer 3: Skills are injected via CONTEXT.md available_skills block (see context.go)

	return prompt
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
