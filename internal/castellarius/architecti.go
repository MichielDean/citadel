package castellarius

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

const (
	architectiSessionName               = "architecti"
	architectiRateLimitWindow           = 24 * time.Hour
	architectiRestartCastellariusFactor = 5 // lastTickAt must exceed this multiple of pollInterval
	architectiSessionTimeout            = 10 * time.Minute
	maxActionsPerRun                    = 1000 // cap total actions per invocation for defense-in-depth

	// architectiQueueCap is the capacity of the serial in-memory queue.
	// Sized generously to accommodate transient bursts of pooled transitions.
	architectiQueueCap = 64

	// architectiDefaultMaxFilesPerRun is used when no ArchitectiConfig is present.
	architectiDefaultMaxFilesPerRun = 10

	// architectiStuckRoutingThreshold is the number of consecutive Assign
	// failures before a stuck-routing droplet is enqueued for Architecti.
	architectiStuckRoutingThreshold = 3

	// architectiInvocationNotePrefix is the content prefix written to a
	// droplet's notes when it is enqueued for Architecti. Used as a dedup
	// guard: if a note with this prefix already exists for the droplet,
	// tryEnqueueArchitecti skips the droplet without re-enqueueing.
	architectiInvocationNotePrefix = "[architecti] enqueued:"
)

// architectiConfigOrDefault returns the ArchitectiConfig from scheduler
// configuration, falling back to built-in defaults when nil.
func (s *Castellarius) architectiConfigOrDefault() aqueduct.ArchitectiConfig {
	if s.config.Architecti != nil {
		return *s.config.Architecti
	}
	return aqueduct.ArchitectiConfig{MaxFilesPerRun: architectiDefaultMaxFilesPerRun}
}

// tryEnqueueArchitecti attempts to enqueue droplet for Architecti processing.
// It is a no-op when an invocation note already exists for the droplet,
// providing the one-to-one guarantee: each bad-state transition triggers at
// most one Architecti invocation.
//
// The channel send is attempted first. The invocation note is written only
// after a successful send, so that a full queue does not permanently silence
// a droplet by recording a dedup note that was never backed by an actual enqueue.
func (s *Castellarius) tryEnqueueArchitecti(client CisternClient, droplet *cistern.Droplet) {
	if s.architectiQueue == nil {
		return
	}
	notes, err := client.GetNotes(droplet.ID)
	if err != nil {
		s.logger.Error("architecti: enqueue check: get notes failed",
			"droplet", droplet.ID, "error", err)
		return
	}
	for _, n := range notes {
		if n.CataractaeName == "architecti" && strings.HasPrefix(n.Content, architectiInvocationNotePrefix) {
			s.logger.Debug("architecti: already enqueued — skipping", "droplet", droplet.ID)
			return
		}
	}
	// Attempt non-blocking send first. Only write the invocation note on
	// success so a full queue does not permanently silence the droplet.
	select {
	case s.architectiQueue <- droplet:
		// Send succeeded. Write the invocation note so subsequent poll cycles
		// and restarts see it and skip re-enqueue (crash-safe dedup guard).
		noteContent := architectiInvocationNotePrefix + " " + droplet.Status
		if err := client.AddNote(droplet.ID, "architecti", noteContent); err != nil {
			s.logger.Error("architecti: write invocation note failed",
				"droplet", droplet.ID, "error", err)
			// Droplet is already queued and will be processed. Missing note
			// means the next poll cycle may attempt a duplicate enqueue;
			// the drainer's seen-map handles within-burst deduplication.
		}
		s.logger.Info("architecti: enqueued", "droplet", droplet.ID, "status", droplet.Status)
	default:
		s.logger.Warn("architecti: queue full — droplet not enqueued", "droplet", droplet.ID)
	}
}

// startArchitectiQueue starts the single background goroutine that drains the
// serial Architecti queue. It processes one droplet at a time: reads from the
// buffered channel, deduplicates within the current queue contents (race
// between channel send and note write), calls runArchitectiFn, then reads the
// next. The goroutine exits when ctx is cancelled.
func (s *Castellarius) startArchitectiQueue(ctx context.Context) {
	s.architectiWg.Add(1)
	go func() {
		defer s.architectiWg.Done()
		// seen deduplicates duplicate IDs within the in-flight queue.
		// Note-based dedup in tryEnqueueArchitecti prevents most duplicates;
		// seen handles the narrow race between channel send and note write.
		// It is cleared when the channel drains to bound its size.
		seen := make(map[string]struct{})
		for {
			select {
			case <-ctx.Done():
				return
			case droplet, ok := <-s.architectiQueue:
				if !ok {
					return
				}
				if _, dup := seen[droplet.ID]; dup {
					s.logger.Debug("architecti: drainer: duplicate — skipping", "droplet", droplet.ID)
				} else {
					seen[droplet.ID] = struct{}{}
					func() {
						defer func() {
							if r := recover(); r != nil {
								s.logger.Error("architecti: drainer: panic recovered",
									"droplet", droplet.ID, "panic", r)
							}
						}()
						cfg := s.architectiConfigOrDefault()
						if err := s.runArchitectiFn(ctx, droplet, cfg); err != nil {
							s.logger.Error("architecti: run failed",
								"droplet", droplet.ID, "error", err)
						}
					}()
				}
				// Clear seen when the channel is drained — bounds map size to
				// the queue capacity; safe because all in-burst duplicates have
				// been consumed before the channel appears empty.
				if len(s.architectiQueue) == 0 {
					seen = make(map[string]struct{})
				}
			}
		}
	}()
}

// ArchitectiAction is a single action output by the Architecti agent.
type ArchitectiAction struct {
	Action     string `json:"action"`
	DropletID  string `json:"droplet_id,omitempty"`
	Cataractae string `json:"cataractae,omitempty"`
	Reason     string `json:"reason"`
	// "file" action fields
	Repo        string `json:"repo,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Complexity  string `json:"complexity,omitempty"` // "standard", "full", "critical"
	// "note" action field
	Body string `json:"body,omitempty"`
}

// runArchitecti is the real implementation of the Architecti autonomous recovery
// agent. It is assigned to runArchitectiFn in New/NewFromParts at construction
// time. It builds a system snapshot, invokes the agent, parses the JSON action
// array, and dispatches each approved action via the Go API.
func (s *Castellarius) runArchitecti(ctx context.Context, droplet *cistern.Droplet, config aqueduct.ArchitectiConfig) error {
	// Global singleton guard: at most one Architecti session runs at a time
	// across all droplets. If another goroutine already holds the slot, skip.
	if !s.architectiRunning.CompareAndSwap(false, true) {
		s.logger.Info("architecti: already running globally — skipping",
			"droplet", droplet.ID)
		return nil
	}
	defer s.architectiRunning.Store(false)

	// Build system state snapshot for the agent.
	snapshot, repoByDroplet := s.buildArchitectiSnapshot(ctx, droplet, config)

	// Invoke the agent (real impl runs claude in a tmux session; injected in tests).
	output, err := s.architectiExecFn(ctx, snapshot)
	if err != nil {
		return fmt.Errorf("architecti: exec: %w", err)
	}

	// Parse the JSON action array.
	actions, err := parseArchitectiOutput(output, config.MaxFilesPerRun)
	if err != nil {
		// Truncate raw output to bounded length for security
		rawOutput := string(output)
		if len(rawOutput) > 500 {
			rawOutput = rawOutput[:500] + "...(truncated)"
		}
		s.logger.Error("architecti: parse output failed",
			"droplet", droplet.ID,
			"error", err,
			"raw_output", rawOutput)
		return fmt.Errorf("architecti: parse: %w", err)
	}

	if len(actions) == 0 {
		s.logger.Info("architecti: no action taken", "droplet", droplet.ID)
		return nil
	}

	s.logger.Info("architecti: dispatching actions",
		"droplet", droplet.ID,
		"count", len(actions))
	return s.dispatchArchitectiActions(ctx, actions, repoByDroplet)
}

// buildArchitectiSnapshot assembles a comprehensive system state document for
// the Architecti agent. Returns the snapshot text and a map of dropletID→repo
// used when looking up clients during action dispatch.
func (s *Castellarius) buildArchitectiSnapshot(ctx context.Context, trigger *cistern.Droplet, config aqueduct.ArchitectiConfig) (string, map[string]string) {
	var sb strings.Builder
	repoByDroplet := make(map[string]string)

	sb.WriteString("# Architecti System Snapshot\n")
	fmt.Fprintf(&sb, "Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&sb, "Triggering droplet: %s (status=%s, idle=%s)\n\n",
		trigger.ID, trigger.Status, time.Since(trigger.UpdatedAt).Round(time.Second))

	// --- Droplet State Inventory ---
	sb.WriteString("## Droplet State Inventory\n\n")

	var allPooled, allInProgress []*cistern.Droplet

	for _, repo := range s.config.Repos {
		if ctx.Err() != nil {
			break
		}
		client, ok := s.clients[repo.Name]
		if !ok {
			continue
		}
		for _, status := range []string{"pooled", "in_progress"} {
			items, err := client.List(repo.Name, status)
			if err != nil {
				s.logger.Warn("architecti: snapshot: list failed",
					"repo", repo.Name, "status", status, "error", err)
				continue
			}
			for _, item := range items {
				repoByDroplet[item.ID] = repo.Name
				switch status {
				case "pooled":
					allPooled = append(allPooled, item)
				case "in_progress":
					allInProgress = append(allInProgress, item)
				}
			}
		}
	}

	writeDropletTable(&sb, "### Pooled Droplets", allPooled, false)

	// In-progress: separate stuck-routing from active.
	var stuckRouting, active []*cistern.Droplet
	for _, d := range allInProgress {
		if d.Outcome != "" {
			stuckRouting = append(stuckRouting, d)
		} else {
			active = append(active, d)
		}
	}
	writeDropletTable(&sb, "### In-Progress Droplets", active, true)
	writeDropletTable(&sb, "### Stuck Routing (outcome set, not yet routed)", stuckRouting, true)

	// --- Infrastructure Health ---
	sb.WriteString("## Infrastructure Health\n\n")

	sb.WriteString("### Castellarius Health File\n")
	if s.dbPath != "" {
		hf, err := ReadHealthFile(filepath.Dir(s.dbPath))
		if err != nil {
			fmt.Fprintf(&sb, "Error reading health file: %v\n\n", err)
		} else {
			lastTickAge := time.Since(hf.LastTickAt).Round(time.Second)
			hungThreshold := time.Duration(architectiRestartCastellariusFactor) * s.pollInterval
			hung := ""
			if lastTickAge > hungThreshold {
				hung = " [POSSIBLY HUNG]"
			}
			fmt.Fprintf(&sb, "- Last tick: %s ago%s\n", lastTickAge, hung)
			fmt.Fprintf(&sb, "- Poll interval: %ds\n", hf.PollIntervalSec)
			fmt.Fprintf(&sb, "- Drought running: %v\n\n", hf.DroughtRunning)
		}
	} else {
		sb.WriteString("DB path not configured — health file unavailable.\n\n")
	}

	// --- Session Health ---
	sb.WriteString("### Active Tmux Sessions\n")
	if out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name} #{session_created_string}").Output(); err == nil {
		sessions := strings.TrimSpace(string(out))
		if sessions == "" {
			sb.WriteString("None.\n\n")
		} else {
			sb.WriteString("```\n")
			sb.WriteString(sessions)
			sb.WriteString("\n```\n\n")
		}
	} else {
		sb.WriteString("(tmux not available or no sessions running)\n\n")
	}

	// --- Recent Log Tail ---
	sb.WriteString("## Recent Castellarius Log (last 50 lines)\n")
	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, ".cistern", "castellarius.log")
	if out, err := exec.Command("tail", "-n", "50", logPath).Output(); err == nil {
		sb.WriteString("```\n")
		sb.WriteString(strings.TrimRight(string(out), "\n"))
		sb.WriteString("\n```\n\n")
	} else {
		fmt.Fprintf(&sb, "(log file not found at %s)\n\n", logPath)
	}

	// --- Configuration ---
	sb.WriteString("## Configuration\n")
	fmt.Fprintf(&sb, "- MaxFilesPerRun: %d\n", config.MaxFilesPerRun)
	fmt.Fprintf(&sb, "- Poll interval: %s\n\n", s.pollInterval)

	return sb.String(), repoByDroplet
}

// writeDropletTable writes a markdown table of droplets to sb.
func writeDropletTable(sb *strings.Builder, heading string, items []*cistern.Droplet, showOutcome bool) {
	sb.WriteString(heading + "\n")
	if len(items) == 0 {
		sb.WriteString("None.\n\n")
		return
	}
	if showOutcome {
		sb.WriteString("| ID | Repo | Age | Cataractae | Assignee | Outcome |\n")
		sb.WriteString("|---|---|---|---|---|---|\n")
	} else {
		sb.WriteString("| ID | Repo | Age | Cataractae | Assignee |\n")
		sb.WriteString("|---|---|---|---|---|\n")
	}
	for _, d := range items {
		age := time.Since(d.UpdatedAt).Round(time.Minute)
		if showOutcome {
			outcome := d.Outcome
			if outcome == "" {
				outcome = "(none)"
			}
			fmt.Fprintf(sb, "| %s | %s | %s | %s | %s | %s |\n",
				d.ID, d.Repo, age, d.CurrentCataractae, d.Assignee, outcome)
		} else {
			fmt.Fprintf(sb, "| %s | %s | %s | %s | %s |\n",
				d.ID, d.Repo, age, d.CurrentCataractae, d.Assignee)
		}
	}
	sb.WriteString("\n")
}

// parseArchitectiOutput extracts and validates the JSON action array from raw
// agent output. Enforces MaxFilesPerRun by dropping excess "file" actions.
// Returns nil slice (not an error) when the array is empty.
func parseArchitectiOutput(output []byte, maxFiles int) ([]ArchitectiAction, error) {
	raw := extractJSONArray(output)
	if raw == nil {
		return nil, fmt.Errorf("no JSON array found in output")
	}

	var actions []ArchitectiAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	// Enforce MaxFilesPerRun and cap total actions per invocation.
	fileCount := 0
	var filtered []ArchitectiAction
	for _, a := range actions {
		if a.Action == "file" {
			fileCount++
			if fileCount > maxFiles {
				continue
			}
		}
		if len(filtered) >= maxActionsPerRun {
			break
		}
		filtered = append(filtered, a)
	}

	return filtered, nil
}

// extractJSONArray finds the first complete JSON array in output.
// Returns nil if no array is found.
func extractJSONArray(output []byte) []byte {
	start := bytes.IndexByte(output, '[')
	if start < 0 {
		return nil
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(output); i++ {
		b := output[i]
		if escape {
			escape = false
			continue
		}
		if b == '\\' && inString {
			escape = true
			continue
		}
		if b == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch b {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return output[start : i+1]
			}
		}
	}
	return nil
}

// dispatchArchitectiActions executes each action in sequence. Individual action
// errors are logged but do not abort remaining actions.
func (s *Castellarius) dispatchArchitectiActions(ctx context.Context, actions []ArchitectiAction, repoByDroplet map[string]string) error {
	for _, action := range actions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.dispatchArchitectiAction(ctx, action, repoByDroplet); err != nil {
			s.logger.Error("architecti: action failed",
				"action", action.Action,
				"droplet", action.DropletID,
				"error", err)
		}
	}
	return nil
}

// dispatchArchitectiAction routes a single action to its handler.
func (s *Castellarius) dispatchArchitectiAction(_ context.Context, action ArchitectiAction, repoByDroplet map[string]string) error {
	switch action.Action {
	case "restart":
		return s.architectiRestart(action, repoByDroplet)
	case "cancel":
		return s.architectiCancel(action, repoByDroplet)
	case "file":
		return s.architectiFile(action)
	case "note":
		return s.architectiNote(action, repoByDroplet)
	case "restart_castellarius":
		return s.architectiRestartCastellarius(action)
	default:
		s.logger.Warn("architecti: unknown action type — ignoring", "action", action.Action)
		return nil
	}
}

func (s *Castellarius) architectiRestart(action ArchitectiAction, repoByDroplet map[string]string) error {
	if action.DropletID == "" {
		return fmt.Errorf("restart action missing droplet_id")
	}
	if action.Cataractae == "" {
		return fmt.Errorf("restart action missing cataractae")
	}
	// Validate cataractae against configured steps.
	if !s.isValidCataractae(action.Cataractae) {
		return fmt.Errorf("restart action has invalid cataractae: %q not found in any configured workflow", action.Cataractae)
	}
	client, err := s.architectiClient(action.DropletID, repoByDroplet)
	if err != nil {
		return fmt.Errorf("restart: %w", err)
	}

	// Rate limit: at most 1 restart per droplet per 24h rolling window.
	s.architectiRestartsMu.Lock()
	if last, ok := s.architectiRestarts[action.DropletID]; ok && time.Since(last) < architectiRateLimitWindow {
		s.architectiRestartsMu.Unlock()
		s.logger.Info("architecti: restart rate-limited",
			"droplet", action.DropletID,
			"last_restart_ago", time.Since(last).Round(time.Second))
		return nil
	}
	s.architectiRestartsMu.Unlock()

	s.logger.Info("architecti: restarting droplet",
		"droplet", action.DropletID,
		"cataractae", action.Cataractae,
		"reason", action.Reason)

	// Note the restart so history is preserved.
	if noteErr := client.AddNote(action.DropletID, "architecti",
		fmt.Sprintf("Architecti restart → %s: %s", action.Cataractae, action.Reason)); noteErr != nil {
		s.logger.Warn("architecti: restart note failed",
			"droplet", action.DropletID, "error", noteErr)
	}
	if err := client.Assign(action.DropletID, "", action.Cataractae); err != nil {
		return err
	}

	// Record rate limit timestamp only after a successful Assign.
	s.architectiRestartsMu.Lock()
	s.architectiRestarts[action.DropletID] = time.Now()
	s.architectiRestartsMu.Unlock()
	return nil
}

func (s *Castellarius) architectiCancel(action ArchitectiAction, repoByDroplet map[string]string) error {
	if action.DropletID == "" {
		return fmt.Errorf("cancel action missing droplet_id")
	}
	client, err := s.architectiClient(action.DropletID, repoByDroplet)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}

	s.logger.Info("architecti: cancelling droplet",
		"droplet", action.DropletID,
		"reason", action.Reason)
	return client.Cancel(action.DropletID, fmt.Sprintf("Architecti: %s", action.Reason))
}

func (s *Castellarius) architectiFile(action ArchitectiAction) error {
	if action.Repo == "" {
		return fmt.Errorf("file action missing repo")
	}
	if action.Title == "" {
		return fmt.Errorf("file action missing title")
	}
	client, ok := s.clients[action.Repo]
	if !ok {
		return fmt.Errorf("file: no client for repo %q", action.Repo)
	}

	description := action.Description
	if action.Reason != "" {
		if description != "" {
			description += "\n\n"
		}
		description += "Reason: " + action.Reason
	}

	complexity := complexityLevel(action.Complexity)
	s.logger.Info("architecti: filing new droplet",
		"repo", action.Repo,
		"title", action.Title,
		"complexity", complexity,
		"reason", action.Reason)
	_, err := client.FileDroplet(action.Repo, action.Title, description, 2, complexity)
	return err
}

func (s *Castellarius) architectiNote(action ArchitectiAction, repoByDroplet map[string]string) error {
	if action.DropletID == "" {
		return fmt.Errorf("note action missing droplet_id")
	}
	client, err := s.architectiClient(action.DropletID, repoByDroplet)
	if err != nil {
		return fmt.Errorf("note: %w", err)
	}

	body := action.Body
	if body == "" {
		body = action.Reason
	}
	if body == "" {
		return fmt.Errorf("note action missing body and reason")
	}
	s.logger.Info("architecti: adding note", "droplet", action.DropletID)
	return client.AddNote(action.DropletID, "architecti", body)
}

func (s *Castellarius) architectiRestartCastellarius(action ArchitectiAction) error {
	// Guard: only restart if the scheduler is positively confirmed to be hung.
	// Fail-closed: refuse to restart if the health file is unavailable or
	// unreadable — we cannot verify the hung state and the restart may be unsafe.
	if s.dbPath == "" {
		s.logger.Warn("architecti: restart_castellarius refused — dbPath not configured (cannot verify hung state)")
		return nil
	}
	hf, err := ReadHealthFile(filepath.Dir(s.dbPath))
	if err != nil {
		s.logger.Warn("architecti: restart_castellarius refused — health file unreadable", "error", err)
		return nil
	}
	maxAge := time.Duration(architectiRestartCastellariusFactor) * s.pollInterval
	lastTickAge := time.Since(hf.LastTickAt)
	if lastTickAge < maxAge {
		s.logger.Info("architecti: restart_castellarius skipped — scheduler is healthy",
			"last_tick_age", lastTickAge.Round(time.Second),
			"threshold", maxAge)
		return nil
	}

	s.logger.Warn("architecti: restarting castellarius", "reason", action.Reason)
	return s.architectiRestartCastellariusFn()
}

// architectiClient returns the client for the repo that owns the given droplet.
func (s *Castellarius) architectiClient(dropletID string, repoByDroplet map[string]string) (CisternClient, error) {
	repo, ok := repoByDroplet[dropletID]
	if !ok {
		return nil, fmt.Errorf("unknown droplet %q (not in snapshot)", dropletID)
	}
	client, ok := s.clients[repo]
	if !ok {
		return nil, fmt.Errorf("no client for repo %q", repo)
	}
	return client, nil
}

// complexityLevel converts an Architecti complexity string to the integer
// used by the cistern DB (1=standard, 2=full, 3=critical).
func complexityLevel(s string) int {
	switch strings.ToLower(s) {
	case "full":
		return 2
	case "critical":
		return 3
	default:
		return 1 // standard
	}
}

// defaultArchitectiExec runs the Architecti agent in a singleton tmux session
// named "architecti" and captures the output via a session log file. It
// resolves the system prompt file, writes the context doc to a temp file, and
// runs `claude --system-prompt-file <path> -p "$(cat <context>)"`.
func (s *Castellarius) defaultArchitectiExec(ctx context.Context, contextDoc string) ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("architecti exec: home dir: %w", err)
	}

	// Resolve system prompt file.
	systemPromptPath, err := s.resolveArchitectiSystemPrompt()
	if err != nil {
		return nil, fmt.Errorf("architecti exec: %w", err)
	}

	// Write context doc to a temp file (may be large).
	tmpDir, err := os.MkdirTemp("", "architecti-*")
	if err != nil {
		return nil, fmt.Errorf("architecti exec: tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	contextFile := filepath.Join(tmpDir, "context.md")
	if err := os.WriteFile(contextFile, []byte(contextDoc), 0o600); err != nil {
		return nil, fmt.Errorf("architecti exec: write context: %w", err)
	}

	// Session log path.
	sessionLogDir := filepath.Join(home, ".cistern", "session-logs")
	if err := os.MkdirAll(sessionLogDir, 0o750); err != nil {
		return nil, fmt.Errorf("architecti exec: mkdir session-logs: %w", err)
	}
	sessionLogPath := filepath.Join(sessionLogDir, architectiSessionName+".log")

	// Kill any stale architecti session before spawning.
	exec.Command("tmux", "kill-session", "-t", architectiSessionName).Run() //nolint:errcheck

	// Build the wrapper script.
	claudePath := architectiClaudePath()
	script := fmt.Sprintf("#!/bin/bash\n%s --system-prompt-file %s -p \"$(cat %s)\" 2>&1 | tee %s; exit ${PIPESTATUS[0]}\n",
		architectiShellQuote(claudePath),
		architectiShellQuote(systemPromptPath),
		architectiShellQuote(contextFile),
		architectiShellQuote(sessionLogPath))
	scriptPath := filepath.Join(tmpDir, "run.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return nil, fmt.Errorf("architecti exec: write script: %w", err)
	}

	// Spawn tmux session.
	args := []string{"new-session", "-d", "-s", architectiSessionName}
	if path := os.Getenv("PATH"); path != "" {
		args = append(args, "-e", "PATH="+path)
	}
	// Clear ANTHROPIC_API_KEY — Claude Code manages its own credentials.
	args = append(args, "-e", "ANTHROPIC_API_KEY=")
	if db := os.Getenv("CT_DB"); db != "" {
		args = append(args, "-e", "CT_DB="+db)
	}
	args = append(args, "bash", scriptPath)

	if out, spawnErr := exec.Command("tmux", args...).CombinedOutput(); spawnErr != nil {
		return nil, fmt.Errorf("architecti exec: tmux spawn: %w: %s", spawnErr, out)
	}

	// Wait for the session to exit.
	if err := architectiWaitSession(ctx, architectiSessionName, architectiSessionTimeout); err != nil {
		exec.Command("tmux", "kill-session", "-t", architectiSessionName).Run() //nolint:errcheck
		return nil, fmt.Errorf("architecti exec: wait: %w", err)
	}

	// Read and return the captured output.
	data, err := os.ReadFile(sessionLogPath)
	if err != nil {
		return nil, fmt.Errorf("architecti exec: read output: %w", err)
	}
	return data, nil
}

// resolveArchitectiSystemPrompt locates the Architecti SYSTEM_PROMPT.md.
// Checks ~/.cistern/cataractae/architecti/ first, then the primary clone of the
// first configured repo.
func (s *Castellarius) resolveArchitectiSystemPrompt() (string, error) {
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".cistern", "cataractae", "architecti", "SYSTEM_PROMPT.md")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if len(s.config.Repos) > 0 {
		p := filepath.Join(s.sandboxRoot, s.config.Repos[0].Name, "_primary",
			"cataractae", "architecti", "SYSTEM_PROMPT.md")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("SYSTEM_PROMPT.md not found: check ~/.cistern/cataractae/architecti/ or the primary clone")
}

// architectiWaitSession polls until the tmux session exits, ctx is cancelled,
// or timeout is reached.
func architectiWaitSession(ctx context.Context, sessionName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %s", timeout)
		}
		if exec.Command("tmux", "has-session", "-t", sessionName).Run() != nil {
			return nil // session gone → exited
		}
		time.Sleep(2 * time.Second)
	}
}

// architectiClaudePath returns the path to the claude binary.
func architectiClaudePath() string {
	if p := os.Getenv("CLAUDE_PATH"); p != "" {
		return p
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin", "claude")
}

// architectiShellQuote single-quotes a string for safe embedding in a shell script.
func architectiShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RunArchitectiAdHoc builds a system snapshot, invokes the Architecti agent,
// and optionally dispatches the resulting actions. Returns the snapshot text,
// raw agent output bytes, the parsed/filtered actions (nil when dryRun or no
// actions), and any error. When dryRun is true, raw output is returned without
// parsing or dispatching.
func (s *Castellarius) RunArchitectiAdHoc(ctx context.Context, trigger *cistern.Droplet, config aqueduct.ArchitectiConfig, dryRun bool) (snapshot string, rawOutput []byte, actions []ArchitectiAction, err error) {
	snapshot, repoByDroplet := s.buildArchitectiSnapshot(ctx, trigger, config)

	rawOutput, err = s.architectiExecFn(ctx, snapshot)
	if err != nil {
		return snapshot, nil, nil, fmt.Errorf("architecti: exec: %w", err)
	}

	if dryRun {
		return snapshot, rawOutput, nil, nil
	}

	actions, err = parseArchitectiOutput(rawOutput, config.MaxFilesPerRun)
	if err != nil {
		return snapshot, rawOutput, nil, fmt.Errorf("architecti: parse: %w", err)
	}

	if len(actions) == 0 {
		return snapshot, rawOutput, nil, nil
	}

	return snapshot, rawOutput, actions, s.dispatchArchitectiActions(ctx, actions, repoByDroplet)
}

// defaultRestartCastellarius restarts the Castellarius systemd user service.
func defaultRestartCastellarius() error {
	return exec.Command("systemctl", "--user", "restart", "castellarius").Run()
}

// isValidCataractae checks if a cataractae name exists in any configured workflow.
func (s *Castellarius) isValidCataractae(name string) bool {
	for _, workflow := range s.workflows {
		for _, step := range workflow.Cataractae {
			if step.Name == name {
				return true
			}
		}
	}
	return false
}
