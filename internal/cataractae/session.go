package cataractae

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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

	// Model is the LLM model to use (e.g. "ollama/glm-5.1:cloud", "sonnet").
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

// spawn creates a new tmux session running the agent fresh. With exec prefix,
// if the session is alive, the agent IS alive — no zombie state is possible.
func (s *Session) spawn() error {
	// With exec prefix, if the session is alive the agent is alive.
	// No zombie detection needed — return nil and let the existing session continue.
	if isSessionAlive(s.ID) {
		slog.Default().Info("session: already running — skipping respawn",
			"session", s.ID)
		return nil
	}

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

	// Resolve the command path for the spawn log.
	cmdPath := claudePathFn()
	if s.Preset.Name != "" {
		cmdPath = resolveCommandFn(s.Preset.Command)
	}

	slog.Default().Info("session: spawning",
		"session", s.ID,
		"work_dir", s.WorkDir,
		"command", cmdPath,
		"model", s.Model,
		"preset", s.Preset.Name,
		"context_type", "fresh",
	)

	// Session output log: prepare the log directory so pipe-pane can write to it
	// after the session is created. The log path is also read by the heartbeat
	// for quick-exit diagnostics.
	sessionLogPath := filepath.Join(home, ".cistern", "session-logs", s.ID+".log")
	logDirReady := os.MkdirAll(filepath.Dir(sessionLogPath), 0o750) == nil

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

	// Ensure the session exits when the agent process exits, regardless of the
	// user's tmux.conf. The remain-on-exit option defaults to off, but a global
	// setting can override it — set it explicitly so agents never leave ghost
	// "[Process completed]" panes after their work is done.
	if err := exec.Command("tmux", "set-window-option", "-t", s.ID, "remain-on-exit", "off").Run(); err != nil {
		slog.Default().Warn("session: could not set remain-on-exit", "session", s.ID, "error", err)
	}

	// pipe-pane must be called after the session exists.
	if logDirReady {
		execTmuxPipePaneCmd(s.ID, sessionLogPath)
	}

	return nil
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

	// Always-unset: env vars that interfere with non-interactive agent execution.
	// OPENCODE_SERVER_* causes "session not found" errors when opencode run
	// tries to connect to an authenticated server instead of starting fresh.
	// See: https://github.com/anomalyco/opencode/issues/8502
	args = append(args,
		"-e", "OPENCODE_SERVER_USERNAME=",
		"-e", "OPENCODE_SERVER_PASSWORD=",
		"-e", "OPENCODE_PID=",
		"-e", "OPENCODE=",
	)

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
	return fmt.Sprintf("exec %s --dangerously-skip-permissions --add-dir %s %s-p '%s'",
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

// execTmuxPipePaneCmd attaches tmux pipe-pane to the named session, appending all
// PTY output to logPath. Unlike a tee wrapper around stdout/stderr, pipe-pane
// hooks into tmux's own PTY capture and works regardless of how the agent writes
// output — including direct PTY writes that bypass stdout/stderr entirely.
// Errors are silently ignored: the session is already running and a pipe-pane
// failure only means the log will be empty, not that the agent is broken.
// It is a variable so tests can substitute a no-op without requiring tmux.
var execTmuxPipePaneCmd = func(sessionID, logPath string) {
	exec.Command("tmux", "pipe-pane", "-o", "-t", sessionID, "cat >> "+shellQuote(logPath)).Run() //nolint:errcheck
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
// model flag.
func (s *Session) presetBaseParts(preset provider.ProviderPreset, skillsDir string) []string {
	parts := []string{shellQuote(resolveCommandFn(preset.Command))}
	if preset.Subcommand != "" {
		parts = append(parts, preset.Subcommand)
	}
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

	prompt := s.buildPrompt()

	// Determine how the prompt is delivered to the agent:
	//   1. PromptFlag set → use flag + value (e.g. claude uses -p)
	//   2. PromptPositional + PromptFileTemplate → write AGENTS.md, append short prompt as positional arg
	//   3. PromptFileTemplate only → write AGENTS.md, no prompt arg (agent reads it natively)
	//   4. None → build the prompt flag from NonInteractive config as fallback
	promptFlag := preset.PromptFlag
	if promptFlag == "" && preset.NonInteractive.PromptFlag != "" && !preset.PromptPositional {
		promptFlag = preset.NonInteractive.PromptFlag
	}

	if promptFlag != "" {
		if preset.PromptFileTemplate != "" {
			identityPath := s.resolveIdentityPath()
			promptFile := strings.ReplaceAll(preset.PromptFileTemplate, "{identity}", identityPath)
			promptFilePath := filepath.Join(s.WorkDir, promptFile)
			if err := os.WriteFile(promptFilePath, []byte(prompt), 0o644); err != nil {
				slog.Default().Error("spawn: failed to write prompt file",
					"session", s.ID,
					"path", promptFilePath,
					"error", err)
			} else {
				slog.Default().Info("spawn: wrote prompt file",
					"session", s.ID,
					"path", promptFilePath,
					"bytes", len(prompt))
			}
			shortPrompt := "Read CONTEXT.md for your task and begin work. Follow the instructions in " + promptFile + "."
			parts = append(parts, promptFlag, shellQuote(shortPrompt))
		} else if len(prompt) > maxPromptForTmux {
			promptFile := filepath.Join(s.WorkDir, ".prompt")
			if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
				return "", fmt.Errorf("spawn: write prompt file: %w", err)
			}
			parts = append(parts, promptFlag, "\"$(cat "+shellQuote(promptFile)+")\"")
		} else {
			parts = append(parts, promptFlag, "'"+strings.ReplaceAll(prompt, "'", `'\''`)+"'")
		}
	} else if preset.PromptPositional && preset.PromptFileTemplate != "" {
		// Providers like opencode take the prompt as a positional argument
		// rather than a flag. Write the full prompt to the instructions file
		// and append a short positional prompt after all flags.
		identityPath := s.resolveIdentityPath()
		promptFile := strings.ReplaceAll(preset.PromptFileTemplate, "{identity}", identityPath)
		promptFilePath := filepath.Join(s.WorkDir, promptFile)
		if err := os.WriteFile(promptFilePath, []byte(prompt), 0o644); err != nil {
			slog.Default().Error("spawn: failed to write prompt file",
				"session", s.ID,
				"path", promptFilePath,
				"error", err)
		} else {
			slog.Default().Info("spawn: wrote prompt file",
				"session", s.ID,
				"path", promptFilePath,
				"bytes", len(prompt))
		}
		shortPrompt := "Read CONTEXT.md for your task and begin work. Follow the instructions in " + promptFile + "."
		parts = append(parts, shellQuote(shortPrompt))
	} else if preset.PromptFileTemplate != "" {
		// No prompt flag and no positional mode, but the provider reads an
		// instructions file natively. Write the full prompt to it.
		identityPath := s.resolveIdentityPath()
		promptFile := strings.ReplaceAll(preset.PromptFileTemplate, "{identity}", identityPath)
		promptFilePath := filepath.Join(s.WorkDir, promptFile)
		if err := os.WriteFile(promptFilePath, []byte(prompt), 0o644); err != nil {
			slog.Default().Error("spawn: failed to write prompt file",
				"session", s.ID,
				"path", promptFilePath,
				"error", err)
		} else {
			slog.Default().Info("spawn: wrote prompt file",
				"session", s.ID,
				"path", promptFilePath,
				"bytes", len(prompt))
		}
	}

	return "exec " + strings.Join(parts, " "), nil
}

// shellQuote wraps s in single quotes, escaping any single quotes within s,
// so the result is safe to embed in a POSIX shell command string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// maxPromptForTmux is the maximum prompt length that can be safely passed as a
// command-line argument to tmux. Prompts exceeding this limit are written to
// a file instead.
const maxPromptForTmux = 4096

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
4. Every 60 seconds while working, call: ct droplet heartbeat <id>
   This signals the scheduler that you are alive and making progress. Without
   heartbeats, the stall detector may flag your session as stuck.
5. Signal your outcome before exiting. You MUST call one of:
     ct droplet pass <id> --notes "..."
     ct droplet recirculate <id> --notes "..."
     ct droplet pool <id> --notes "..."
   A cataractae that exits without signaling leaves the droplet stranded.

Your role persona and skill instructions follow.

## System safety invariants — never break these

The Castellarius is a state machine. Its correctness depends on these invariants holding. If your work could affect any of them, you must verify they still hold before signaling pass.

**1. Signaling is the only valid way to advance state.**
Never manipulate droplet state by any means other than ct droplet pass/recirculate/pool.
Never exit without signaling — a stranded droplet burns resources indefinitely.

**2. Session spawning must expose the agent process directly to tmux.**
Do not wrap the agent command in a shell (bash -c, sh -c, pipes, tee) unless you have explicitly verified that pane_current_command and /proc/<pid>/cmdline still correctly identify the agent. Wrappers that change what the process monitor sees will cause every healthy session to be classified as zombie and respawned in a loop.

**3. CONTEXT.md is pipeline state — never commit it.**
CONTEXT.md is injected at dispatch time and listed in .gitignore. If you see it in a git add or git commit, stop. Committing it causes merge conflicts across concurrent deliveries and corrupts origin/main.

**4. The circuit breaker pools after 5 dead sessions in 15 minutes.**
If your droplet keeps dying without signaling an outcome, the scheduler will pool it automatically. This stops token burn — a human needs to investigate before the droplet is re-opened.

**5. Do not call git add -f or git add --force on any ignored file.**
The .gitignore exists for a reason. Overriding it for pipeline state files (CONTEXT.md, .current-stage, session logs) corrupts the state machine.
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

// claudePathFn resolves the path to the claude executable.It is a variable so
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
