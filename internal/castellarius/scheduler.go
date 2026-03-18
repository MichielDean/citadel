// Package castellarius implements the Castellarius — the overseer of all aqueducts.
//
// It polls the work cistern for each configured repo, assigns droplets to
// named operators, runs workflow cataractae via an injected CataractaRunner, reads
// outcomes, and routes to the next cataracta via deterministic workflow rules.
// No AI in the Castellarius — pure state machine.
package castellarius

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// CisternClient is the interface for interacting with the work cistern.
// *cistern.Client satisfies this interface.
type CisternClient interface {
	GetReady(repo string) (*cistern.Droplet, error)
	Assign(id, worker, step string) error

	AddNote(id, step, content string) error
	GetNotes(id string) ([]cistern.CataractaNote, error)
	Escalate(id, reason string) error
	CloseItem(id string) error
	List(repo, status string) ([]*cistern.Droplet, error)
	Purge(olderThan time.Duration, dryRun bool) (int, error)
	SetCataracta(id, cataracta string) error
}

// CataractaRunner executes a single workflow step.
// Spawn is non-blocking for agent steps (spawns tmux, returns immediately)
// and synchronous for automated steps (runs the gate, writes outcome to DB).
// The observe phase of the scheduler reads outcomes written to the DB on each tick.
type CataractaRunner interface {
	Spawn(ctx context.Context, req CataractaRequest) error
}

// CataractaRequest contains everything needed to execute a workflow step.
type CataractaRequest struct {
	Item       *cistern.Droplet
	Step       aqueduct.WorkflowCataracta
	Workflow   *aqueduct.Workflow
	RepoConfig aqueduct.RepoConfig
	AqueductName string
	Notes      []cistern.CataractaNote // context from previous steps
}

// Castellarius is the core loop that polls for work, assigns it to operators,
// and routes outcomes through workflow cataractae.
type Castellarius struct {
	config            aqueduct.AqueductConfig
	workflows         map[string]*aqueduct.Workflow
	clients           map[string]CisternClient
	pools             map[string]*AqueductPool
	runner            CataractaRunner
	logger            *slog.Logger
	pollInterval      time.Duration
	// heartbeatInterval controls how often orphaned in-progress droplets are
	// checked. Independent of pollInterval so it fires even when the main tick
	// is busy. Defaults to 30s.
	heartbeatInterval time.Duration
	sandboxRoot       string
	cleanupInterval   time.Duration
	dbPath            string
	wasDrought        bool
}

// Option configures a flow.
type Option func(*Castellarius)

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option {
	return func(s *Castellarius) { s.logger = l }
}

// WithPollInterval sets how often the scheduler polls for work.
func WithPollInterval(d time.Duration) Option {
	return func(s *Castellarius) { s.pollInterval = d }
}

// WithSandboxRoot sets the root directory for worker sandboxes.
func WithSandboxRoot(root string) Option {
	return func(s *Castellarius) { s.sandboxRoot = root }
}

// New creates a Castellarius from an AqueductConfig.
// Workflows are loaded from each RepoConfig.WorkflowPath.
// Each repo gets its own cistern.Client scoped by prefix.
func New(config aqueduct.AqueductConfig, dbPath string, runner CataractaRunner, opts ...Option) (*Castellarius, error) {
	s := &Castellarius{
		config:            config,
		workflows:         make(map[string]*aqueduct.Workflow),
		clients:           make(map[string]CisternClient),
		pools:             make(map[string]*AqueductPool),
		runner:            runner,
		logger:            slog.Default(),
		pollInterval:      10 * time.Second,
		heartbeatInterval: 30 * time.Second,
		dbPath:            dbPath,
	}
	for _, o := range opts {
		o(s)
	}

	if s.sandboxRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("castellarius: home dir: %w", err)
		}
		s.sandboxRoot = filepath.Join(home, ".cistern", "sandboxes")
	}

	if config.CleanupInterval != "" {
		d, err := time.ParseDuration(config.CleanupInterval)
		if err != nil {
			return nil, fmt.Errorf("castellarius: invalid cleanup_interval %q: %w", config.CleanupInterval, err)
		}
		s.cleanupInterval = d
	} else {
		s.cleanupInterval = 24 * time.Hour
	}

	if config.HeartbeatInterval != "" {
		d, err := time.ParseDuration(config.HeartbeatInterval)
		if err != nil {
			return nil, fmt.Errorf("castellarius: invalid heartbeat_interval %q: %w", config.HeartbeatInterval, err)
		}
		s.heartbeatInterval = d
	}

	for _, repo := range config.Repos {
		wf, err := aqueduct.ParseWorkflow(repo.WorkflowPath)
		if err != nil {
			return nil, fmt.Errorf("load workflow for %s: %w", repo.Name, err)
		}
		s.workflows[repo.Name] = wf

		client, err := cistern.New(dbPath, repo.Prefix)
		if err != nil {
			return nil, fmt.Errorf("queue for %s: %w", repo.Name, err)
		}
		s.clients[repo.Name] = client

		names := repo.Names
		if len(names) == 0 {
			names = defaultAqueductNames(repo.Cataractae)
		}
		s.pools[repo.Name] = NewAqueductPool(repo.Name, names)
	}

	return s, nil
}

// NewFromParts creates a Castellarius with pre-built components (for testing).
func NewFromParts(
	config aqueduct.AqueductConfig,
	workflows map[string]*aqueduct.Workflow,
	clients map[string]CisternClient,
	runner CataractaRunner,
	opts ...Option,
) *Castellarius {
	s := &Castellarius{
		config:            config,
		workflows:         workflows,
		clients:           clients,
		pools:             make(map[string]*AqueductPool),
		runner:            runner,
		logger:            slog.Default(),
		pollInterval:      10 * time.Second,
		heartbeatInterval: 30 * time.Second,
	}
	for _, o := range opts {
		o(s)
	}

	for _, repo := range config.Repos {
		names := repo.Names
		if len(names) == 0 {
			names = defaultAqueductNames(repo.Cataractae)
		}
		s.pools[repo.Name] = NewAqueductPool(repo.Name, names)
	}

	return s
}

// romanAqueducts is the namepool for auto-assigned operators — real Roman aqueducts,
// historically significant and thematically fitting for a water-metaphor pipeline.
var romanAqueducts = []string{
	"virgo",       // still flows today, feeds the Trevi Fountain
	"marcia",      // considered Rome's finest water quality
	"claudia",     // 69km, one of the most impressive engineering feats
	"traiana",     // built by Trajan, served Trastevere
	"julia",       // built by Agrippa under Augustus
	"appia",       // oldest of Rome's aqueducts, 312 BC
	"anio",        // two branches: Anio Vetus and Anio Novus
	"tepula",      // warm spring water, 126 BC
	"gier",        // 85km aqueduct serving Lyon, France
	"eifel",       // 130km, one of the longest Roman aqueducts, Germany
	"alexandrina", // last of Rome's great aqueducts, 3rd century AD
	"barbegal",    // Arles — powered an ancient grain mill complex
}

func defaultAqueductNames(n int) []string {
	if n <= 0 {
		n = 1
	}
	names := make([]string, n)
	for i := range names {
		if i < len(romanAqueducts) {
			names[i] = romanAqueducts[i]
		} else {
			names[i] = fmt.Sprintf("operator-%d", i)
		}
	}
	return names
}

// Run starts the scheduler loop. It blocks until ctx is cancelled.
func (s *Castellarius) Run(ctx context.Context) error {
	s.logger.Info("Cistern online. Aqueducts open.",
		"repos", len(s.config.Repos),
		"cataractae", s.config.MaxCataractae,
	)

	// Integrity check: regenerate any missing or corrupt CLAUDE.md files before
	// accepting work. A corrupted CLAUDE.md (e.g. "test\n\nold instructions") means
	// the agent runs with no role instructions — silent and catastrophic.
	s.ensureCataractaeIntegrity()

	s.recoverInProgress()

	if s.cleanupInterval > 0 {
		go func() {
			ticker := time.NewTicker(s.cleanupInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.purgeOldItems()
				}
			}
		}()
	}

	// Heartbeat goroutine — runs independently of the main poll loop.
	// Detects orphaned in-progress droplets (dead sessions with no outcome)
	// and resets them to open so they can be re-dispatched.
	go func() {
		hbTicker := time.NewTicker(s.heartbeatInterval)
		defer hbTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-hbTicker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							stack := debug.Stack()
							s.logger.Error("heartbeat: panic recovered",
								"panic", r,
								"stack", string(stack),
							)
						}
					}()
					s.heartbeatInProgress(ctx)
				}()
			}
		}
	}()

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Aqueducts closed.")
			return ctx.Err()
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// purgeOldItems deletes closed/escalated items older than retention_days across all repos.
func (s *Castellarius) purgeOldItems() {
	retentionDays := s.config.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 90
	}
	olderThan := time.Duration(retentionDays) * 24 * time.Hour

	total := 0
	for _, repo := range s.config.Repos {
		client := s.clients[repo.Name]
		n, err := client.Purge(olderThan, false)
		if err != nil {
			s.logger.Error("purge failed", "repo", repo.Name, "error", err)
			continue
		}
		if n > 0 {
			s.logger.Info("purged items", "repo", repo.Name, "count", n)
		}
		total += n
	}
	s.logger.Info("purge complete", "total", total)
}

// Tick runs a single poll cycle across all repos. Exported for testing.
func (s *Castellarius) Tick(ctx context.Context) {
	s.tick(ctx)
}

func (s *Castellarius) tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			s.logger.Error("tick: panic recovered",
				"panic", r,
				"stack", string(stack),
			)
		}
	}()

	for _, repo := range s.config.Repos {
		if err := ctx.Err(); err != nil {
			return
		}
		// Phase 1: route items that have signaled an outcome via ct droplet commands.
		s.observeRepo(ctx, repo)
		// Phase 2: dispatch new work to idle workers.
		s.dispatchRepo(ctx, repo)
	}

	// Drought edge detection: fire hooks on transition from busy → drought.
	isDrought := s.totalBusy() == 0
	if isDrought && !s.wasDrought {
		if len(s.config.DroughtHooks) > 0 {
			s.logger.Info("Drought protocols running.")
			go RunDroughtHooks(s.config.DroughtHooks, &s.config, s.dbPath, s.sandboxRoot, s.logger)
		}
	}
	s.wasDrought = isDrought
}

// observeRepo routes all in_progress items that have signaled an outcome.
// Agents write outcomes via `ct droplet pass/recirculate/block <id>`, which sets
// the outcome column in the DB. This phase finds those items and advances them.
func (s *Castellarius) observeRepo(_ context.Context, repo aqueduct.RepoConfig) {
	client := s.clients[repo.Name]
	wf := s.workflows[repo.Name]
	pool := s.pools[repo.Name]

	items, err := client.List(repo.Name, "in_progress")
	if err != nil {
		s.logger.Error("observe: list in_progress failed", "repo", repo.Name, "error", err)
		return
	}

	for _, item := range items {
		if item.Outcome == "" {
			continue // still running
		}

		step := currentCataracta(item, wf)

		// Park the worktree (detach HEAD) before releasing the worker, so the
		// feature branch is free for any aqueduct to check out on the next step.
		// This is the fix for "already used by worktree" errors between cataractae.
		if item.Assignee != "" {
			if w := pool.FindByName(item.Assignee); w != nil {
				sandboxDir := filepath.Join(s.sandboxRoot, repo.Name, w.Name)
				parkWorktree(sandboxDir)
				pool.Release(w)
			}
		}

		if step == nil {
			s.logger.Warn("observe: no step found, resetting",
				"repo", repo.Name, "droplet", item.ID, "cataracta", item.CurrentCataracta)
			cataracta := item.CurrentCataracta
			if cataracta == "" && len(wf.Cataractae) > 0 {
				cataracta = wf.Cataractae[0].Name
			}
			if err := client.Assign(item.ID, "", cataracta); err != nil {
				s.logger.Error("observe: reset (no step) failed", "droplet", item.ID, "error", err)
			}
			continue
		}

		result, recirculateTo := parseOutcome(item.Outcome)

		switch result {
		case ResultPass:
			s.logger.Info("Droplet cleared cataracta", "droplet", item.ID, "cataracta", step.Name)
		case ResultRecirculate:
			s.logger.Info("Droplet recirculated", "droplet", item.ID, "cataracta", step.Name)
		default:
			s.logger.Info("Droplet stagnant at cataracta", "droplet", item.ID, "cataracta", step.Name, "outcome", item.Outcome)
		}

		var next string
		if recirculateTo != "" {
			// Agent specified an explicit target step (e.g. recirculate:implement).
			next = recirculateTo
		} else {
			next = route(*step, result)
		}

		if next == "" {
			reason := fmt.Sprintf("no route from step %q for outcome %q", step.Name, item.Outcome)
			s.logger.Warn("observe: no route", "droplet", item.ID)
			if err := client.Escalate(item.ID, reason); err != nil {
				s.logger.Error("observe: escalate failed", "droplet", item.ID, "error", err)
			}
			continue
		}

		// Apply complexity skip rules.
		skipSteps := wf.SkipCataractaeForLevel(item.Complexity)
		next = advanceSkippedCataractae(next, wf, skipSteps)

		// For critical droplets, insert a human gate before delivery.
		if wf.Complexity.RequireHumanForLevel(item.Complexity) && next == "delivery" {
			next = "human"
		}

		if isTerminal(next) {
			s.handleTerminal(client, item.ID, next, step.Name)
			continue
		}

		// Advance item to next step (open for the next dispatch cycle).
		if err := client.Assign(item.ID, "", next); err != nil {
			s.logger.Error("observe: advance step failed", "droplet", item.ID, "next", next, "error", err)
		}
	}
}

// dispatchRepo assigns open items to idle workers and spawns their steps.
// For agent steps, Spawn returns immediately (tmux session started).
// For automated steps, Spawn runs synchronously and writes the outcome to the DB.
// In both cases the worker stays busy until the observe phase processes the outcome.
func (s *Castellarius) dispatchRepo(ctx context.Context, repo aqueduct.RepoConfig) {
	pool := s.pools[repo.Name]
	client := s.clients[repo.Name]
	wf := s.workflows[repo.Name]

	for {
		worker := pool.AvailableAqueduct()
		if worker == nil {
			return
		}

		if s.totalBusy() >= s.config.MaxCataractae {
			return
		}

		item, err := client.GetReady(repo.Name)
		if err != nil {
			s.logger.Error("poll failed", "repo", repo.Name, "error", err)
			return
		}
		if item == nil {
			return
		}

		step := currentCataracta(item, wf)
		if step == nil {
			s.logger.Error("no step found", "repo", repo.Name, "droplet", item.ID)
			return
		}

		pool.Assign(worker, item.ID, step.Name)

		notes, err := client.GetNotes(item.ID)
		if err != nil {
			s.logger.Error("get notes failed", "droplet", item.ID, "error", err)
			notes = nil
		}

		if err := client.Assign(item.ID, worker.Name, step.Name); err != nil {
			s.logger.Error("assign failed", "droplet", item.ID, "error", err)
			pool.Release(worker)
			continue
		}

		s.logger.Info("Droplet entering cataracta",
			"droplet", item.ID,
			"operator", worker.Name,
			"cataracta", step.Name,
		)

		req := CataractaRequest{
			Item:       item,
			Step:       *step,
			Workflow:   wf,
			RepoConfig: repo,
			AqueductName: worker.Name,
			Notes:      notes,
		}

		w := worker // capture for goroutine
		go func() {
			if err := s.runner.Spawn(ctx, req); err != nil {
				s.logger.Error("spawn failed",
					"repo", repo.Name,
					"droplet", req.Item.ID,
					"cataracta", req.Step.Name,
					"error", err,
				)
				// Detach HEAD before releasing so the branch isn't left locked
				// in this sandbox, which would block any other aqueduct from
				// checking out the same branch on retry.
				parkWorktree(filepath.Join(s.sandboxRoot, repo.Name, w.Name))
				// Reset to open so the item can be re-dispatched.
				if err2 := client.Assign(req.Item.ID, "", req.Step.Name); err2 != nil {
					s.logger.Error("reset after spawn failure",
						"droplet", req.Item.ID, "error", err2)
				}
				pool.Release(w)
			}
			// On success: worker stays busy; observe phase releases it when the
			// outcome is written to the DB.
		}()
	}
}

func (s *Castellarius) totalBusy() int {
	total := 0
	for _, pool := range s.pools {
		total += pool.FlowingCount()
	}
	return total
}

// parseOutcome parses a DB outcome string into a Result and optional target step.
// "pass"               → (ResultPass, "")
// "recirculate"        → (ResultRecirculate, "")
// "recirculate:impl"   → (ResultRecirculate, "impl")
// "block"              → (ResultFail, "")
func parseOutcome(outcome string) (Result, string) {
	if strings.HasPrefix(outcome, "recirculate:") {
		return ResultRecirculate, strings.TrimPrefix(outcome, "recirculate:")
	}
	switch outcome {
	case "pass":
		return ResultPass, ""
	case "recirculate":
		return ResultRecirculate, ""
	case "block":
		return ResultFail, ""
	default:
		return ResultFail, ""
	}
}

// currentCataracta determines which workflow step a work item is at.
// If the item has a current_step, look up that step.
// Otherwise, start at the first step in the aqueduct.
func currentCataracta(item *cistern.Droplet, wf *aqueduct.Workflow) *aqueduct.WorkflowCataracta {
	if item.CurrentCataracta != "" {
		return lookupCataracta(wf, item.CurrentCataracta)
	}
	if len(wf.Cataractae) > 0 {
		return &wf.Cataractae[0]
	}
	return nil
}

func lookupCataracta(wf *aqueduct.Workflow, name string) *aqueduct.WorkflowCataracta {
	for i := range wf.Cataractae {
		if wf.Cataractae[i].Name == name {
			return &wf.Cataractae[i]
		}
	}
	return nil
}

// route determines the next step name based on the outcome result.
func route(step aqueduct.WorkflowCataracta, result Result) string {
	switch result {
	case ResultPass:
		return step.OnPass
	case ResultFail:
		return step.OnFail
	case ResultRecirculate:
		return step.OnRecirculate
	case ResultEscalate:
		return step.OnEscalate
	default:
		return step.OnFail
	}
}

// advanceSkippedCataractae walks the workflow from nextStep, skipping any step whose name
// appears in skipSteps. It follows on_pass links to find the next non-skipped step.
// Returns "done" if all remaining steps are skipped.
func advanceSkippedCataractae(nextStep string, wf *aqueduct.Workflow, skipSteps []string) string {
	if len(skipSteps) == 0 {
		return nextStep
	}
	skip := make(map[string]bool, len(skipSteps))
	for _, s := range skipSteps {
		skip[s] = true
	}
	current := nextStep
	for skip[current] {
		step := lookupCataracta(wf, current)
		if step == nil || step.OnPass == "" {
			return "done"
		}
		current = step.OnPass
	}
	return current
}

// isTerminal returns true if the target is a terminal state.
func isTerminal(name string) bool {
	switch strings.ToLower(name) {
	case "done", "blocked", "human", "escalate":
		return true
	}
	return false
}

func (s *Castellarius) handleTerminal(client CisternClient, itemID, terminal, fromStep string) {
	switch strings.ToLower(terminal) {
	case "done":
		s.logger.Info("Droplet delivered", "droplet", itemID)
		if err := client.CloseItem(itemID); err != nil {
			s.logger.Error("close failed", "droplet", itemID, "error", err)
		}
	case "blocked", "human", "escalate":
		s.logger.Info("Droplet stagnant at terminal", "droplet", itemID, "terminal", terminal, "from_cataracta", fromStep)
		reason := fmt.Sprintf("reached terminal %q from cataracta %q", terminal, fromStep)
		if err := client.Escalate(itemID, reason); err != nil {
			s.logger.Error("escalate at terminal failed", "droplet", itemID, "error", err)
		}
		if strings.ToLower(terminal) == "human" {
			if err := client.SetCataracta(itemID, "human"); err != nil {
				s.logger.Error("set cataracta human failed", "droplet", itemID, "error", err)
			}
		}
	}
}

// recoverInProgress handles items left in_progress after a process restart.
// Items with a non-null outcome are left as-is — the first observe tick will route them.
// Items with a null outcome are reset to open so they can be re-dispatched.
// (Agent sessions that were running will no longer be monitored, but the
// feature branch preserves their work; the new session picks up incrementally.)
func (s *Castellarius) recoverInProgress() {
	for _, repo := range s.config.Repos {
		client := s.clients[repo.Name]
		wf := s.workflows[repo.Name]

		items, err := client.List(repo.Name, "in_progress")
		if err != nil {
			s.logger.Error("recovery: list in_progress failed", "repo", repo.Name, "error", err)
			continue
		}

		for _, item := range items {
			if item.Outcome != "" {
				// Outcome already written — leave as in_progress.
				// The first observe tick will route this item.
				s.logger.Info("recovery: item has outcome, will be routed on first tick",
					"repo", repo.Name, "droplet", item.ID, "outcome", item.Outcome)
				continue
			}

			// No outcome: reset to open for re-dispatch.
			cataracta := item.CurrentCataracta
			if cataracta == "" {
				step := currentCataracta(item, wf)
				if step != nil {
					cataracta = step.Name
				} else if len(wf.Cataractae) > 0 {
					cataracta = wf.Cataractae[0].Name
				}
			}

			s.logger.Info("recovery: resetting in_progress item to open",
				"repo", repo.Name, "droplet", item.ID, "cataracta", cataracta)
			if err := client.Assign(item.ID, "", cataracta); err != nil {
				s.logger.Error("recovery: reset failed", "droplet", item.ID, "error", err)
			}
		}
	}
}

// heartbeatInProgress scans for orphaned in_progress droplets whose agent
// sessions have died without writing an outcome. Resets them to open so the
// main dispatch loop re-queues them on the next tick.
func (s *Castellarius) heartbeatInProgress(ctx context.Context) {
	for _, repo := range s.config.Repos {
		if ctx.Err() != nil {
			return
		}
		s.heartbeatRepo(ctx, repo)
	}
}

func (s *Castellarius) heartbeatRepo(_ context.Context, repo aqueduct.RepoConfig) {
	client := s.clients[repo.Name]
	pool := s.pools[repo.Name]

	items, err := client.List(repo.Name, "in_progress")
	if err != nil {
		s.logger.Error("heartbeat: list in_progress failed", "repo", repo.Name, "error", err)
		return
	}

	for _, item := range items {
		// Items with outcomes are handled by the observe phase — skip them.
		if item.Outcome != "" {
			continue
		}

		// Check if the tmux session is still alive — this is the authoritative
		// liveness signal. Do NOT rely on pool.IsWorkerBusy: the in-memory busy
		// state is never cleared when a tmux server crash kills the session, so
		// an item can stay "busy" in memory indefinitely while the agent is dead.
		if item.Assignee != "" {
			sessionID := repo.Name + "-" + item.Assignee
			if isTmuxAlive(sessionID) {
				// Agent is still running; leave it alone.
				continue
			}
		}

		// Dead/missing session, no outcome — reset to open for re-dispatch.
		s.logger.Info("heartbeat: resetting stalled droplet",
			"repo", repo.Name, "droplet", item.ID, "cataracta", item.CurrentCataracta)

		if item.Assignee != "" {
			if w := pool.FindByName(item.Assignee); w != nil {
				pool.Release(w)
			}
		}

		if err := client.Assign(item.ID, "", item.CurrentCataracta); err != nil {
			s.logger.Error("heartbeat: reset failed", "droplet", item.ID, "error", err)
		}
	}
}

// isTmuxAlive returns true if a tmux session with the given name is running.
func isTmuxAlive(sessionID string) bool {
	return exec.Command("tmux", "has-session", "-t", sessionID).Run() == nil
}

// WriteContext writes a CONTEXT.md file with notes from previous steps.
// Call this before spawning the next agent to provide context from prior steps.
func WriteContext(dir string, notes []cistern.CataractaNote) error {
	if len(notes) == 0 {
		return nil
	}

	var b []byte
	b = append(b, "# Context from Previous Steps\n\n"...)
	for _, n := range notes {
		header := n.CataractaName
		if header == "" {
			header = "unknown"
		}
		b = append(b, fmt.Sprintf("## Step: %s\n\n%s\n\n", header, n.Content)...)
	}

	return os.WriteFile(filepath.Join(dir, "CONTEXT.md"), b, 0o644)
}

// parkWorktree detaches HEAD in a worker's sandbox so the feature branch is
// not held by any worktree between steps. This allows any aqueduct to check
// out the same branch on the next cataracta without a "already used by worktree" error.
// Inlined here to avoid an import cycle with the cataracta package.
func parkWorktree(dir string) {
	cmd := exec.Command("git", "checkout", "--detach", "HEAD")
	cmd.Dir = dir
	_ = cmd.Run() // best-effort; failure means next checkout may conflict
}

// ensureCataractaeIntegrity checks each agent cataracta's CLAUDE.md for the
// sentinel string that proves it was generated from the YAML (not corrupted).
// If any file is missing or lacks the sentinel, it is regenerated automatically.
// This runs at Castellarius startup so corrupted prompts never silently persist.
func (s *Castellarius) ensureCataractaeIntegrity() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	cataractaeDir := filepath.Join(home, ".cistern", "cataractae")
	sentinel := "ct droplet pass" // present in every correctly-generated CLAUDE.md

	needsRegen := false
	// Collect all unique identities across all repo workflows.
	seen := map[string]bool{}
	for _, wf := range s.workflows {
		for _, step := range wf.Cataractae {
			if step.Identity == "" || seen[step.Identity] {
				continue
			}
			seen[step.Identity] = true
			claudePath := filepath.Join(cataractaeDir, step.Identity, "CLAUDE.md")
			content, err := os.ReadFile(claudePath)
			if err != nil || !strings.Contains(string(content), sentinel) {
				s.logger.Warn("CLAUDE.md missing or corrupt — will regenerate",
					"identity", step.Identity, "path", claudePath)
				needsRegen = true
			}
		}
	}

	if needsRegen {
		s.logger.Info("Regenerating cataractae CLAUDE.md files")
		if err := hookCataractaeGenerate(&s.config, s.logger); err != nil {
			s.logger.Error("Failed to regenerate CLAUDE.md files", "error", err)
		} else {
			s.logger.Info("Cataractae CLAUDE.md files regenerated successfully")
		}
	}
}
