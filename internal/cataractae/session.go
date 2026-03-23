package cataractae

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichielDean/cistern/internal/provider"
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

	// Identity is the agent cataractae identity (e.g., "implementer", "reviewer").
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

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("spawn: cannot determine home directory: %w", err)
	}
	skillsDir := filepath.Join(home, ".cistern", "skills")
	claudeCmd := s.buildClaudeCmd(skillsDir)

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
	if db := os.Getenv("CT_DB"); db != "" {
		args = append(args, "-e", "CT_DB="+db)
	}
	args = append(args, claudeCmd)
	cmd := exec.Command("tmux", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session %s: %w: %s", s.ID, err, out)
	}

	log.Printf("session %s: spawned in %s", s.ID, s.WorkDir)
	return nil
}

// buildClaudeCmd constructs the shell command string passed to tmux new-session.
// skillsDir is shell-quoted so paths containing spaces are handled correctly.
func (s *Session) buildClaudeCmd(skillsDir string) string {
	// The prompt must be single-quoted so that tmux/sh doesn't word-split it —
	// unquoted spaces would cause only the first word to be passed to -p.
	// Single-quote the prompt and escape any literal single quotes inside it
	// using the 'x'\''y' idiom.
	prompt := strings.ReplaceAll(s.buildPrompt(), "'", `'\''`)
	var flagsStr string
	if s.Model != "" {
		flagsStr = "--model " + s.Model + " "
	}
	return fmt.Sprintf("%s --dangerously-skip-permissions --add-dir %s %s-p '%s'",
		claudePathFn(), shellQuote(skillsDir), flagsStr, prompt)
}

// buildPresetCmd constructs the shell command string for a ProviderPreset.
// The output is byte-for-byte identical to buildClaudeCmd when called with the
// built-in "claude" preset and CLAUDE_PATH set to "claude".
func (s *Session) buildPresetCmd(preset provider.ProviderPreset, skillsDir string) string {
	prompt := strings.ReplaceAll(s.buildPrompt(), "'", `'\''`)

	parts := append([]string{preset.Command}, preset.Args...)

	if preset.AddDirFlag != "" {
		parts = append(parts, preset.AddDirFlag, shellQuote(skillsDir))
	}

	if s.Model != "" && preset.ModelFlag != "" {
		parts = append(parts, preset.ModelFlag, s.Model)
	}

	parts = append(parts, "-p", "'"+prompt+"'")

	return strings.Join(parts, " ")
}

// shellQuote wraps s in single quotes, escaping any single quotes within s,
// so the result is safe to embed in a POSIX shell command string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// baseCataractaePrompt is the constitutional layer — hardcoded in the binary,
// cannot be corrupted by YAML edits or file changes. It establishes the
// non-negotiable contract for every cataractae session.
const baseCataractaePrompt = `You are a Cataracta operating within the Cistern agentic pipeline.

Cistern is an automated software delivery system. The Castellarius (a pure state
machine) watches the cistern and routes droplets (units of work) into named
aqueducts. You are one cataractae — one gate — in that aqueduct. You receive a
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
   A cataractae that exits without signaling leaves the droplet stranded.

Your role persona and skill instructions follow.
`

// buildPrompt constructs the full agent prompt: constitutional base + persona + skills.
func (s *Session) buildPrompt() string {
	// Layer 1: Constitutional base (immutable — hardcoded in binary)
	prompt := baseCataractaePrompt

	// Layer 2: Persona (from CLAUDE.md / cataractae_definitions YAML)
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

// resolveIdentityPath returns the path to the cataractae identity's CLAUDE.md file.
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

// claudePathFn resolves the path to the claude executable. It is a variable so
// tests can substitute it to inject a known absolute path without modifying the
// process environment or requiring the binary to exist on the test machine.
var claudePathFn = claudePath

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
