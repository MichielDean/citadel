package cataractae

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichielDean/cistern/internal/provider"
)

// Session manages an agent execution inside a tmux session.
type Session struct {
	// ID is the tmux session name (e.g., "myrepo-alice").
	ID string

	// WorkDir is the directory the agent runs in.
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

	// Preset is the provider preset that controls how the agent is launched.
	// When Name is empty, spawn falls back to the legacy claude hard-coded path.
	Preset provider.ProviderPreset

	// Skills is the list of skill names to inject into the prompt for providers
	// that do not support AddDirFlag. Skills are read from ~/.cistern/skills/<name>/SKILL.md.
	// Providers with SupportsAddDir=true receive skill files automatically via --add-dir.
	Skills []string
}

// Spawn creates a new tmux session running the agent and returns immediately.
// The Castellarius observe loop detects completion via the outcome field in the DB —
// agents signal their outcome by calling `ct droplet pass/recirculate/block <id>`.
func (s *Session) Spawn() error {
	return s.spawn()
}

// spawn creates a new tmux session running the agent.
func (s *Session) spawn() error {
	// Kill any stale session with the same name.
	s.kill()

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("spawn: cannot determine home directory: %w", err)
	}
	skillsDir := filepath.Join(home, ".cistern", "skills")

	args := []string{"new-session", "-d", "-s", s.ID, "-c", s.WorkDir}

	var agentCmd string
	if s.Preset.Name != "" {
		agentCmd, err = s.buildPresetCmd(s.Preset, skillsDir)
		if err != nil {
			return fmt.Errorf("spawn: %w", err)
		}
	} else {
		// Legacy fallback: no preset configured — use the hardcoded claude path.
		agentCmd = s.buildClaudeCmd(skillsDir)
	}

	args = append(args, s.collectEnvArgs()...)
	args = append(args, agentCmd)
	cmd := exec.Command("tmux", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session %s: %w: %s", s.ID, err, out)
	}

	log.Printf("session %s: spawned in %s", s.ID, s.WorkDir)
	return nil
}

// collectEnvArgs builds the tmux -e env argument pairs for the session.
// The preset path forwards EnvPassthrough vars and ExtraEnv static values.
// The legacy path forwards ANTHROPIC_API_KEY.
// Platform-level vars (PATH, GH_TOKEN, CT_CATARACTA_NAME, CT_DB) are always
// forwarded regardless of provider.
func (s *Session) collectEnvArgs() []string {
	var args []string

	if s.Preset.Name != "" {
		// Preset-driven env passthrough: forward each listed var if set.
		for _, envVar := range s.Preset.EnvPassthrough {
			if val := os.Getenv(envVar); val != "" {
				args = append(args, "-e", envVar+"="+val)
			}
		}
		// Extra env: static values injected from preset config overrides.
		for k, v := range s.Preset.ExtraEnv {
			args = append(args, "-e", k+"="+v)
		}
	} else {
		// Legacy fallback: forward the claude API key.
		if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			args = append(args, "-e", "ANTHROPIC_API_KEY="+key)
		}
	}

	// Always-pass: platform-level vars needed regardless of provider.
	if path := os.Getenv("PATH"); path != "" {
		args = append(args, "-e", "PATH="+path)
	}
	if s.Identity != "" {
		args = append(args, "-e", "CT_CATARACTA_NAME="+s.Identity)
	}
	if db := os.Getenv("CT_DB"); db != "" {
		args = append(args, "-e", "CT_DB="+db)
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		args = append(args, "-e", "GH_TOKEN="+tok)
	}

	return args
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
// Returns an error if preset.Command is empty.
// The output is byte-for-byte identical to buildClaudeCmd when called with the
// built-in "claude" preset and CLAUDE_PATH set to "claude".
func (s *Session) buildPresetCmd(preset provider.ProviderPreset, skillsDir string) (string, error) {
	if preset.Command == "" {
		return "", fmt.Errorf("preset %q has no command configured", preset.Name)
	}

	parts := append([]string{preset.Command}, preset.Args...)

	if preset.AddDirFlag != "" {
		parts = append(parts, preset.AddDirFlag, shellQuote(skillsDir))
	}

	if s.Model != "" && preset.ModelFlag != "" {
		parts = append(parts, preset.ModelFlag, s.Model)
	}

	if preset.PromptFlag != "" {
		prompt := strings.ReplaceAll(s.buildPrompt(), "'", `'\''`)
		parts = append(parts, preset.PromptFlag, "'"+prompt+"'")
	}

	return strings.Join(parts, " "), nil
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

	// Layer 2: Persona (from instructions file / cataractae_definitions YAML)
	if s.Identity != "" {
		if !s.Preset.SupportsAddDir {
			// Provider cannot inject context via filesystem (no AddDirFlag).
			// Build context preamble by reading the instructions file directly.
			identityDir := s.resolveIdentityDir()
			preamble := buildContextPreamble(identityDir, s.Preset)
			if preamble != "" {
				prompt += "\n## Your Role\n\n" + preamble
			} else {
				// Fallback: point agent to the file location.
				prompt += "\nRead " + s.resolveIdentityPath() + " for your role instructions. "
			}
		} else {
			// Provider has AddDirFlag (e.g. claude): instructions file is available via
			// filesystem injection (--add-dir). Also embed it in the prompt for reliability.
			identityPath := s.resolveIdentityPath()
			if content, err := os.ReadFile(identityPath); err == nil {
				prompt += "\n## Your Role\n\n" + string(content)
			} else {
				// File missing/unreadable — fall back to pointer so agent can try to find it.
				prompt += "\nRead " + identityPath + " for your role instructions. "
			}
		}
	}

	// Layer 3: Skills — injected as text for providers without AddDirFlag support.
	// Providers with SupportsAddDir receive skill files automatically via --add-dir.
	if !s.Preset.SupportsAddDir && len(s.Skills) > 0 {
		home, _ := os.UserHomeDir()
		skillsDir := filepath.Join(home, ".cistern", "skills")
		var sb strings.Builder
		for _, name := range s.Skills {
			skillPath := filepath.Join(skillsDir, name, "SKILL.md")
			if data, err := os.ReadFile(skillPath); err == nil {
				sb.WriteString("\n## Skill: " + name + "\n\n")
				sb.WriteString(string(data))
			}
		}
		if sb.Len() > 0 {
			prompt += "\n## Skills\n" + sb.String()
		}
	}

	return prompt
}

// buildContextPreamble returns the combined role context text for the given identity
// directory. It is called for providers that lack AddDirFlag support — they cannot
// inject context via the filesystem, so the content is embedded directly in the prompt.
//
// Resolution order:
//  1. Read <identityDir>/<preset.InstructionsFile> (the generated combined file).
//  2. If not found, concatenate PERSONA.md + INSTRUCTIONS.md from identityDir.
func buildContextPreamble(identityDir string, preset provider.ProviderPreset) string {
	if data, err := os.ReadFile(filepath.Join(identityDir, preset.InstrFile())); err == nil {
		return string(data)
	}
	// Fallback: concatenate source files directly.
	var parts []string
	if data, err := os.ReadFile(filepath.Join(identityDir, "PERSONA.md")); err == nil {
		parts = append(parts, string(data))
	}
	if data, err := os.ReadFile(filepath.Join(identityDir, "INSTRUCTIONS.md")); err == nil {
		parts = append(parts, string(data))
	}
	return strings.Join(parts, "\n\n")
}

// resolveIdentityDir returns the directory containing the cataractae identity files.
// Checks ~/.cistern/cataractae/<identity>/ (directory existence) first; falls back
// to the sandbox-relative path when the cistern directory is absent.
//
// resolveIdentityPath delegates here so both methods always agree on which
// location to use — preventing the divergence that occurs when the cistern
// directory exists but a provider-specific instructions file does not.
func (s *Session) resolveIdentityDir() string {
	home, err := os.UserHomeDir()
	if err == nil {
		cisternDir := filepath.Join(home, ".cistern", "cataractae", s.Identity)
		if _, err := os.Stat(cisternDir); err == nil {
			return cisternDir
		}
	}
	return filepath.Join("cataractae", s.Identity)
}

// resolveIdentityPath returns the path to the cataractae identity's instructions file.
// Delegates to resolveIdentityDir for location resolution.
func (s *Session) resolveIdentityPath() string {
	return filepath.Join(s.resolveIdentityDir(), s.Preset.InstrFile())
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

// tmuxDisplayMessage queries tmux for the current command running in the first
// pane of the named session. It is a variable so tests can substitute a fake
// implementation without requiring tmux to be installed on the test machine.
var tmuxDisplayMessage = func(sessionID string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", sessionID, "#{pane_current_command}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// isAgentAlive reports whether the agent process is still running inside the
// tmux session. It queries pane_current_command and compares it against
// preset.ProcessNames. A session can be alive (tmux exists) while the agent
// has exited — isAgentAlive detects this zombie state.
//
// Returns true when ProcessNames is empty: no detection is configured so the
// function conservatively assumes the agent is alive.
func (s *Session) isAgentAlive() bool {
	if len(s.Preset.ProcessNames) == 0 {
		return true // no process names configured — cannot detect zombie
	}
	current, err := tmuxDisplayMessage(s.ID)
	if err != nil {
		return false
	}
	return slices.Contains(s.Preset.ProcessNames, current)
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
