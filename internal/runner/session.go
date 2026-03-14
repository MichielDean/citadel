package runner

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

	// Role is the agent role (e.g., "implementer", "reviewer").
	// Used to locate roles/<role>/CLAUDE.md in the working directory.
	Role string

	// TimeoutMinutes is the maximum runtime. 0 means 60 minutes.
	TimeoutMinutes int

	// HandoffThreshold is the token count at which to trigger session handoff.
	HandoffThreshold int
}

const (
	outcomeFile  = "outcome.json"
	handoffFile  = "handoff.md"
	pollInterval = 5 * time.Second
)

// Run spawns a Claude Code session in tmux, polls for outcome.json, and returns
// the parsed outcome. Handles session handoff when the token limit approaches.
func (s *Session) Run() (*Outcome, error) {
	timeout := time.Duration(s.TimeoutMinutes) * time.Minute
	if timeout == 0 {
		timeout = 60 * time.Minute
	}

	// Remove stale outcome file from previous runs.
	os.Remove(filepath.Join(s.WorkDir, outcomeFile))

	if err := s.spawn(); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			s.kill()
			return nil, fmt.Errorf("session %s: timed out after %v", s.ID, timeout)
		}

		// Check for outcome.json.
		outcome, err := s.checkOutcome()
		if err == nil && outcome != nil {
			s.kill()
			return outcome, nil
		}

		// Check for handoff.md — agent hit token limit and wrote a handoff.
		if s.checkHandoff() {
			log.Printf("session %s: handoff detected, respawning", s.ID)
			s.kill()

			if err := s.prependHandoffToContext(); err != nil {
				return nil, fmt.Errorf("handoff: %w", err)
			}

			// Remove handoff.md and outcome.json for fresh session.
			os.Remove(filepath.Join(s.WorkDir, handoffFile))
			os.Remove(filepath.Join(s.WorkDir, outcomeFile))

			if err := s.spawn(); err != nil {
				return nil, fmt.Errorf("respawn after handoff: %w", err)
			}
			// Reset deadline for the new session.
			deadline = time.Now().Add(timeout)
			continue
		}

		// Check if tmux session is still alive.
		if !s.isAlive() {
			// Session died without writing outcome — treat as failure.
			return &Outcome{
				Result: "fail",
				Notes:  "session exited without writing outcome.json",
			}, nil
		}

		time.Sleep(pollInterval)
	}
}

// spawn creates a new tmux session running claude.
func (s *Session) spawn() error {
	// Kill any stale session with the same name.
	s.kill()

	claudeArgs := []string{"--dangerously-skip-permissions"}
	if s.Model != "" {
		claudeArgs = append(claudeArgs, "--model", s.Model)
	}
	claudeArgs = append(claudeArgs, "-p", s.buildPrompt())

	claudeCmd := claudePath() + " " + strings.Join(claudeArgs, " ")

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
// The prompt explicitly tells the agent to do real work before writing outcome.json.
func (s *Session) buildPrompt() string {
	roleInstr := ""
	if s.Role != "" {
		roleInstr = "Read roles/" + s.Role + "/CLAUDE.md for your detailed instructions and protocol. "
	}
	return "You are a Bullet Farm agent. " +
		"Read CONTEXT.md in this directory — it contains your assignment. " +
		roleInstr +
		"Complete the work described in your assignment fully before writing outcome.json. " +
		"Do NOT write outcome.json until the work is actually finished. " +
		"If you are running low on context window, write handoff.md summarizing " +
		"your progress and remaining work, then stop — do not write outcome.json."
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

// checkOutcome reads and parses outcome.json if it exists.
func (s *Session) checkOutcome() (*Outcome, error) {
	path := filepath.Join(s.WorkDir, outcomeFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var outcome Outcome
	if err := json.Unmarshal(data, &outcome); err != nil {
		return nil, fmt.Errorf("parse outcome.json: %w", err)
	}
	return &outcome, nil
}

// checkHandoff returns true if the agent wrote a handoff.md file.
func (s *Session) checkHandoff() bool {
	_, err := os.Stat(filepath.Join(s.WorkDir, handoffFile))
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

// prependHandoffToContext reads handoff.md and prepends its content
// to CONTEXT.md so the next session gets the handoff context.
func (s *Session) prependHandoffToContext() error {
	handoffPath := filepath.Join(s.WorkDir, handoffFile)
	handoff, err := os.ReadFile(handoffPath)
	if err != nil {
		return fmt.Errorf("read handoff.md: %w", err)
	}

	ctxPath := filepath.Join(s.WorkDir, "CONTEXT.md")
	ctx, err := os.ReadFile(ctxPath)
	if err != nil {
		return fmt.Errorf("read CONTEXT.md: %w", err)
	}

	var b strings.Builder
	b.WriteString("# Handoff from Previous Session\n\n")
	b.Write(handoff)
	b.WriteString("\n\n---\n\n")
	b.Write(ctx)

	if err := os.WriteFile(ctxPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write CONTEXT.md: %w", err)
	}

	return nil
}
