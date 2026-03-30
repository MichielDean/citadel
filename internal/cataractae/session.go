package cataractae

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/MichielDean/cistern/internal/aqueduct"
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

	// TemplateCtx holds the step and droplet data used to render CLAUDE.md as a
	// Go template at spawn time. When the zero value is passed, templates are
	// rendered with empty data — static content (no markers) passes through unchanged.
	TemplateCtx aqueduct.TemplateContext
}

// Spawn creates a new tmux session running the agent and returns immediately.
// The Castellarius observe loop detects completion via the outcome field in the DB —
// agents signal their outcome by calling `ct droplet pass/recirculate/pool <id>`.
// When the session exits for any reason the heartbeat detects the dead tmux session
// and resets the droplet for re-dispatch. If a session log file exists at
// ~/.cistern/session-logs/<id>.log the heartbeat reads and logs the tail for diagnostics.
func (s *Session) Spawn() error {
	return s.spawn()
}

// spawn creates a new tmux session running the agent.
// If the preset defines a ContinueFlag and a prior session exists in WorkDir,
// the agent is resumed rather than started fresh — preserving conversation
// context accumulated across prior cataractae cycles.
func (s *Session) spawn() error {
	// Guard: if a session with this ID already exists, inspect it before acting.
	//
	// - Session alive + agent alive → agent is actively working. Do not
	//   interfere. Return nil so the caller treats this as a successful spawn.
	//   The observe loop will pick up the outcome when the agent finishes.
	//
	// - Session alive + agent dead → zombie (tmux shell open but agent exited
	//   without writing an outcome). Kill it and spawn fresh.
	//
	// - Session dead → fall through and spawn normally.
	//
	// This replaces the previous unconditional kill-before-spawn, which was
	// silently terminating healthy sessions whenever the heartbeat reset a
	// droplet and the dispatcher picked it up again.
	if isSessionAlive(s.ID) {
		if s.isAgentAlive() {
			slog.Default().Info("session: already running — skipping respawn",
				"session", s.ID)
			return nil
		}
		// Zombie: session exists but agent has exited without writing an outcome.
		slog.Default().Warn("session: killing zombie session before respawn",
			"session", s.ID,
			"note", "tmux session was alive but agent process had already exited")
		s.kill()
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("spawn: cannot determine home directory: %w", err)
	}

	skillsDir := filepath.Join(home, ".cistern", "skills")

	args := []string{"new-session", "-d", "-s", s.ID, "-c", s.WorkDir}

	// Decide whether to continue a prior session or start fresh.
	sessionCount := priorSessionCount(home, s.WorkDir)
	resume := s.Preset.ContinueFlag != "" && sessionCount > 0

	if resume {
		escaped := strings.ReplaceAll(s.WorkDir, "/", "-")
		projectDir := filepath.Join(home, ".claude", "projects", escaped)
		slog.Default().Info("session: resuming prior context",
			"session", s.ID,
			"project_dir", projectDir,
			"prior_session_count", sessionCount,
		)
	}

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

	// Resolve the command path for the spawn log.
	cmdPath := claudePathFn()
	if s.Preset.Name != "" {
		cmdPath = resolveCommandFn(s.Preset.Command)
	}

	contextType := "fresh"
	if resume {
		contextType = "resume"
	}
	slog.Default().Info("session: spawning",
		"session", s.ID,
		"work_dir", s.WorkDir,
		"command", cmdPath,
		"model", s.Model,
		"preset", s.Preset.Name,
		"context_type", contextType,
	)

	// Session output log: tee the agent command's stdout+stderr to a file so
	// we can read the last few lines when diagnosing quick-exit failures.
	// The log is written to ~/.cistern/session-logs/<sessionID>.log and is
	// cleaned up after a successful run (or after being read on quick-exit).
	sessionLogPath := filepath.Join(home, ".cistern", "session-logs", s.ID+".log")
	if err := os.MkdirAll(filepath.Dir(sessionLogPath), 0o750); err == nil {
		// Wrap the agent command to tee stdout+stderr to the session log file.
		// IMPORTANT: use 'bash' explicitly — tmux spawns commands via /bin/sh
		// which may be dash on Ubuntu/Debian, and dash does not support PIPESTATUS.
		// We need PIPESTATUS[0] to propagate the agent's exit code through the pipe
		// so the tmux session exits with the correct status.
		agentCmd = "bash -c " + shellQuote("( "+agentCmd+" ) 2>&1 | tee "+shellQuote(sessionLogPath)+"; exit ${PIPESTATUS[0]}")
	}

	args = append(args, s.collectEnvArgs()...)
	args = append(args, agentCmd)
	out, spawnErr := execTmuxNewSession(args)
	if spawnErr != nil {
		if !isTmuxServerDeadError(out) {
			return fmt.Errorf("tmux new-session %s [args: %v]: %w: %s", s.ID, redactArgs(args), spawnErr, out)
		}
		// The tmux server is dead — attempt recovery with double-checked locking.
		// Serialize recovery so concurrent spawns do not interleave: one goroutine
		// must complete the full kill→retry cycle before another begins.
		// Double-check after acquiring the lock: if another goroutine already
		// recovered the server while we were waiting, the retry will succeed and
		// we skip the kill entirely, preventing destruction of the recovered session.
		tmuxRecoveryMu.Lock()
		defer tmuxRecoveryMu.Unlock()
		if out, spawnErr = execTmuxNewSession(args); spawnErr != nil {
			if !isTmuxServerDeadError(out) {
				return fmt.Errorf("tmux new-session %s [args: %v]: %w: %s", s.ID, redactArgs(args), spawnErr, out)
			}
			// Server is still dead — kill stale state and retry.
			slog.Default().Info("session: dead tmux server detected — attempting restart",
				"session", s.ID)
			execTmuxKillServer()
			if out, spawnErr = execTmuxNewSession(args); spawnErr != nil {
				slog.Default().Error("session: tmux server recovery failed — spawn aborted",
					"session", s.ID, "error", spawnErr)
				return fmt.Errorf("tmux new-session %s [args: %v]: server dead, recovery failed: %w: %s", s.ID, redactArgs(args), spawnErr, out)
			}
			slog.Default().Info("session: recovered from dead tmux server — retried spawn successfully",
				"session", s.ID)
		}
	}

	return nil
}

// priorSessionCount returns the number of prior session files Claude has stored
// for workDir. Claude stores sessions under ~/.claude/projects/<escaped-path>/.
// Returns 0 when the directory does not exist or cannot be read.
func priorSessionCount(home, workDir string) int {
	// Claude encodes the absolute path by replacing '/' with '-'.
	escaped := strings.ReplaceAll(workDir, "/", "-")
	projectDir := filepath.Join(home, ".claude", "projects", escaped)
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return 0
	}
	return len(entries)
}

// isSessionAlive returns true if a tmux session with the given ID is running.
// Extracted as a package-level function so the quick-exit goroutine can call
// it without holding a *Session reference.
func isSessionAlive(sessionID string) bool {
	return exec.Command("tmux", "has-session", "-t", sessionID).Run() == nil
}

// collectEnvArgs builds the tmux -e env argument pairs for the session.
// The preset path forwards EnvPassthrough vars and ExtraEnv static values.
// Platform-level vars (PATH, GH_TOKEN, CT_CATARACTA_NAME, CT_DB) are always
// forwarded regardless of provider.
func (s *Session) collectEnvArgs() []string {
	var args []string

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

	// Explicitly unset ANTHROPIC_API_KEY in every spawned session. Claude CLI
	// manages its own OAuth credentials via ~/.claude/.credentials.json — it
	// does not need this var, and a stale value in the tmux global environment
	// (from a previous Castellarius process that sourced ~/.cistern/env) would
	// override Claude's own valid credentials and cause auth failures.
	// Passing an empty value via -e overrides the tmux global env inheritance.
	args = append(args, "-e", "ANTHROPIC_API_KEY=")

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

// execTmuxNewSession runs "tmux" with the given args (which begin with "new-session")
// and returns the combined output and any error. It is a variable so tests can
// substitute a fake implementation without requiring a real tmux server.
var execTmuxNewSession = func(args []string) ([]byte, error) {
	return exec.Command("tmux", args...).CombinedOutput()
}

// execTmuxKillServer runs "tmux kill-server" to clear any stale tmux server state.
// Errors are silently ignored because the server may already be gone.
// It is a variable so tests can substitute a no-op without requiring tmux.
var execTmuxKillServer = func() {
	exec.Command("tmux", "kill-server").Run() //nolint:errcheck
}

// redactArgs returns a copy of args with the value portion of any -e KEY=VALUE
// pair replaced by [REDACTED], preventing secrets from appearing in error messages.
// Structural args (session ID, workdir, flags) are preserved for operator diagnostics.
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 1; i < len(out); i++ {
		if args[i-1] == "-e" {
			if idx := strings.IndexByte(out[i], '='); idx >= 0 {
				out[i] = out[i][:idx+1] + "[REDACTED]"
			}
		}
	}
	return out
}

// isTmuxServerDeadError reports whether the combined output of a failed tmux
// command indicates that the server is not running or unreachable — as opposed
// to an auth failure or missing binary, which manifest as quick-exit sessions
// rather than failed new-session invocations.
func isTmuxServerDeadError(output []byte) bool {
	msg := strings.ToLower(string(output))
	return strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "failed to connect to server") ||
		strings.Contains(msg, "error connecting to")
}

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
     ct droplet pool <id> --notes "..."
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
				prompt += "\n## Your Role\n\n" + aqueduct.RenderTemplate(preamble, s.TemplateCtx)
			} else {
				// Fallback: point agent to the file location.
				prompt += "\nRead " + s.resolveIdentityPath() + " for your role instructions. "
			}
		} else {
			// Provider has AddDirFlag (e.g. claude): instructions file is available via
			// filesystem injection (--add-dir). Also embed it in the prompt for reliability.
			identityPath := s.resolveIdentityPath()
			if content, err := os.ReadFile(identityPath); err == nil {
				prompt += "\n## Your Role\n\n" + aqueduct.RenderTemplate(string(content), s.TemplateCtx)
			} else {
				// File missing/unreadable — fall back to pointer so agent can try to find it.
				prompt += "\nRead " + identityPath + " for your role instructions. "
			}
		}
	}

	// Layer 3: Skills — injected as text for providers without AddDirFlag support.
	// Providers with SupportsAddDir receive skill files automatically via --add-dir.
	if !s.Preset.SupportsAddDir && len(s.Skills) > 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			slog.Default().Warn("buildPrompt: cannot determine home directory — skills not injected", "error", err)
		} else {
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

// kill terminates the tmux session. Errors are silently ignored since the
// session may already be dead. Only called for confirmed zombie sessions;
// healthy sessions are left running (see spawn guard above).
func (s *Session) kill() {
	exec.Command("tmux", "kill-session", "-t", s.ID).Run() //nolint:errcheck
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

// tmuxRecoveryMu serializes dead-tmux-server recovery across concurrent spawn
// goroutines (scheduler.go dispatches spawns in parallel). Without serialization,
// two goroutines that both detect a dead server can interleave: A recovers and
// starts a session, then B calls execTmuxKillServer and destroys A's server.
// Holding this lock during the detect→kill→retry block ensures only one goroutine
// restarts the tmux server at a time.
var tmuxRecoveryMu sync.Mutex

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
