package cataractae

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichielDean/cistern/internal/oauth"
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
// If the preset defines a ContinueFlag and a prior session exists in WorkDir,
// the agent is resumed rather than started fresh — preserving conversation
// context accumulated across prior cataractae cycles.
func (s *Session) spawn() error {
	// Kill any stale tmux session with the same name before creating a new one.
	s.kill()

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("spawn: cannot determine home directory: %w", err)
	}

	// Pre-spawn: silently refresh Claude OAuth token if expired or near expiry.
	if err := ensureClaudeOAuthFresh(home); err != nil {
		return fmt.Errorf("spawn: %w", err)
	}

	skillsDir := filepath.Join(home, ".cistern", "skills")

	args := []string{"new-session", "-d", "-s", s.ID, "-c", s.WorkDir}

	// Decide whether to continue a prior session or start fresh.
	resume := s.Preset.ContinueFlag != "" && hasPriorSession(home, s.WorkDir)

	var agentCmd string
	if s.Preset.Name != "" {
		if resume {
			agentCmd, err = s.buildContinueCmd(s.Preset, skillsDir)
		} else {
			agentCmd, err = s.buildPresetCmd(s.Preset, skillsDir)
		}
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

	if resume {
		log.Printf("session %s: resumed in %s", s.ID, s.WorkDir)
	} else {
		log.Printf("session %s: spawned in %s", s.ID, s.WorkDir)
	}
	return nil
}

// hasPriorSession reports whether the agent has prior session state for workDir.
// Claude stores sessions under ~/.claude/projects/<escaped-path>/.
// We consider a prior session to exist if that directory is non-empty.
func hasPriorSession(home, workDir string) bool {
	// Claude encodes the absolute path by replacing '/' with '-'.
	escaped := strings.ReplaceAll(workDir, "/", "-")
	projectDir := filepath.Join(home, ".claude", "projects", escaped)
	entries, err := os.ReadDir(projectDir)
	return err == nil && len(entries) > 0
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
		flagsStr = "--model " + shellQuote(s.Model) + " "
	}
	return fmt.Sprintf("%s --dangerously-skip-permissions --add-dir %s %s-p '%s'",
		claudePathFn(), shellQuote(skillsDir), flagsStr, prompt)
}

// resolveCommandFn resolves a preset command name to an absolute path. It is a
// variable so tests can substitute a deterministic resolver without requiring the
// real agent binaries to be installed on the test machine.
var resolveCommandFn = resolveCommand

// resolveCommand resolves preset.Command to an absolute path using exec.LookPath,
// falling back to the raw value if lookup fails. This ensures the agent binary is
// found even when the tmux session's PATH differs from the Castellarius process PATH.
func resolveCommand(command string) string {
	if p, err := exec.LookPath(command); err == nil {
		return p
	}
	return command
}

// presetBaseParts builds the shared command parts for preset commands: the
// resolved+quoted command, shell-quoted args, optional --add-dir, and optional
// model flag. Both buildPresetCmd and buildContinueCmd append their own tail.
func (s *Session) presetBaseParts(preset provider.ProviderPreset, skillsDir string) []string {
	parts := []string{shellQuote(resolveCommandFn(preset.Command))}
	for _, a := range preset.Args {
		parts = append(parts, shellQuote(a))
	}
	if preset.AddDirFlag != "" {
		parts = append(parts, preset.AddDirFlag, shellQuote(skillsDir))
	}
	if s.Model != "" && preset.ModelFlag != "" {
		parts = append(parts, preset.ModelFlag, shellQuote(s.Model))
	}
	return parts
}

// buildPresetCmd constructs the shell command string for a ProviderPreset.
// Returns an error if preset.Command is empty.
// The command is resolved to an absolute path via exec.LookPath so the tmux
// session can find the binary regardless of its inherited PATH.
func (s *Session) buildPresetCmd(preset provider.ProviderPreset, skillsDir string) (string, error) {
	if preset.Command == "" {
		return "", fmt.Errorf("preset %q has no command configured", preset.Name)
	}

	parts := s.presetBaseParts(preset, skillsDir)

	if preset.PromptFlag != "" {
		prompt := strings.ReplaceAll(s.buildPrompt(), "'", `'\''`)
		parts = append(parts, preset.PromptFlag, "'"+prompt+"'")
	}

	return strings.Join(parts, " "), nil
}

// buildContinueCmd constructs the shell command string to resume the most recent
// prior session in the working directory. It uses preset.ContinueFlag (e.g.
// "--continue") in place of the prompt flag so the agent picks up where it left off.
// --add-dir is still injected so skill context is available in the resumed session.
func (s *Session) buildContinueCmd(preset provider.ProviderPreset, skillsDir string) (string, error) {
	if preset.Command == "" {
		return "", fmt.Errorf("preset %q has no command configured", preset.Name)
	}
	if preset.ContinueFlag == "" {
		return "", fmt.Errorf("preset %q has no ContinueFlag configured", preset.Name)
	}

	parts := s.presetBaseParts(preset, skillsDir)
	parts = append(parts, preset.ContinueFlag)

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

// oauthTokenURL is the OAuth token endpoint used for pre-spawn token refresh.
// Replaced in tests with a test server URL.
var oauthTokenURL = oauth.DefaultTokenURL

// oauthHTTPDo is the HTTP transport used for pre-spawn token refresh.
// Replaced in tests with a test server client.
var oauthHTTPDo func(*http.Request) (*http.Response, error) = http.DefaultClient.Do

// ensureClaudeOAuthFreshMu guards ensureClaudeOAuthFresh against concurrent calls
// from parallel spawn goroutines. This prevents concurrent read-modify-write races
// on ~/.claude/.credentials.json, env.conf, and os.Setenv.
var ensureClaudeOAuthFreshMu sync.Mutex

// ensureClaudeOAuthFresh checks whether the Claude OAuth access token is expired
// or within the 5-minute refresh window and, if so, attempts a silent refresh.
// On success the new token is written to credentials and injected into the
// current process environment so collectEnvArgs picks it up.
// Returns nil when no credentials file is present or no refresh token is available
// (those cases are skipped silently — other auth methods may be in use).
// Returns an error if the token needs refreshing but the refresh fails.
func ensureClaudeOAuthFresh(home string) error {
	ensureClaudeOAuthFreshMu.Lock()
	defer ensureClaudeOAuthFreshMu.Unlock()

	creds := oauth.Read(home)
	if creds == nil || creds.RefreshToken == "" {
		return nil // no credentials or no refresh token — skip silently
	}
	if !oauth.IsExpiredOrNear(creds, 5*time.Minute) {
		return nil // token is fresh
	}

	result, err := oauth.Refresh(creds.RefreshToken, oauthTokenURL, oauthHTTPDo)
	if err != nil {
		return fmt.Errorf("Claude OAuth token expired and refresh failed — run claude interactively to re-authenticate: %w", err)
	}

	if err := oauth.WriteAccessToken(home, result.AccessToken, result.ExpiresAt); err != nil {
		log.Printf("session: warning: could not write refreshed OAuth token: %v", err)
	}

	// Update env.conf for persistence across service restarts (best-effort).
	envConfPath := filepath.Join(home, ".config", "systemd", "user",
		"cistern-castellarius.service.d", "env.conf")
	if _, statErr := os.Stat(envConfPath); statErr == nil {
		if err := oauth.UpdateEnvConf(envConfPath, result.AccessToken); err != nil {
			log.Printf("session: warning: could not update env.conf with refreshed token: %v", err)
		}
	}

	// Inject into current process so collectEnvArgs picks up the new token.
	os.Setenv("ANTHROPIC_API_KEY", result.AccessToken) //nolint:errcheck

	log.Printf("session: Claude OAuth token refreshed successfully")
	return nil
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
