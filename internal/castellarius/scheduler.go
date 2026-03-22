// Package castellarius implements the Castellarius — the overseer of all aqueducts.
//
// It polls the work cistern for each configured repo, assigns droplets to
// named operators, runs workflow cataractae via an injected CataractaeRunner, reads
// outcomes, and routes to the next cataractae via deterministic workflow rules.
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
	GetNotes(id string) ([]cistern.CataractaeNote, error)
	Escalate(id, reason string) error
	CloseItem(id string) error
	List(repo, status string) ([]*cistern.Droplet, error)
	Purge(olderThan time.Duration, dryRun bool) (int, error)
	SetCataractae(id, cataractae string) error
	// GetLastReviewedCommit returns the HEAD commit hash recorded when the last
	// review diff was generated. Used to detect phantom commits.
	GetLastReviewedCommit(dropletID string) (string, error)
	// SetOutcome records the agent outcome on a droplet. Used by stuck-delivery
	// recovery to write an outcome directly so the observe phase can route it.
	SetOutcome(id, outcome string) error
}

// CataractaeRunner executes a single workflow step.
// Spawn is non-blocking for agent steps (spawns tmux, returns immediately)
// and synchronous for automated steps (runs the gate, writes outcome to DB).
// The observe phase of the scheduler reads outcomes written to the DB on each tick.
type CataractaeRunner interface {
	Spawn(ctx context.Context, req CataractaeRequest) error
}

// CataractaeRequest contains everything needed to execute a workflow step.
type CataractaeRequest struct {
	Item         *cistern.Droplet
	Step         aqueduct.WorkflowCataractae
	Workflow     *aqueduct.Workflow
	RepoConfig   aqueduct.RepoConfig
	AqueductName string
	Notes        []cistern.CataractaeNote // context from previous steps
	// SandboxDir is the per-droplet worktree path created by the Castellarius.
	// Set for full_codebase agent steps; empty otherwise.
	SandboxDir string
}

// Castellarius is the core loop that polls for work, assigns it to operators,
// and routes outcomes through workflow cataractae.
type Castellarius struct {
	config            aqueduct.AqueductConfig
	workflows         map[string]*aqueduct.Workflow
	clients           map[string]CisternClient
	pools             map[string]*AqueductPool
	runner            CataractaeRunner
	logger            *slog.Logger
	pollInterval      time.Duration
	// heartbeatInterval controls how often orphaned in-progress droplets are
	// checked. Independent of pollInterval so it fires even when the main tick
	// is busy. Defaults to 30s.
	heartbeatInterval   time.Duration
	sandboxRoot         string
	cleanupInterval     time.Duration
	dbPath              string
	wasDrought          bool
	startupBinaryMtime  time.Time // mtime of the binary at startup; used to detect updates
	supervised          bool      // true if managed by systemd/supervisord/etc.
	reloadCh            chan struct{} // signals Tick() to hot-reload workflows from disk

	// Stuck delivery recovery — injectable for testing.
	findPRFn        func(ctx context.Context, repoName, dropletID, sandboxDir string) (prURL, state, mergeStateStatus string, err error)
	killSessionFn   func(sessionID string) error
	rebaseAndPushFn func(ctx context.Context, sandboxDir string) error
	ghMergeFn       func(ctx context.Context, sandboxDir, prURL string, autoMerge bool) error
}

// isSupervisedProcess returns true when the Castellarius is being managed by
// a process supervisor that will restart it after a clean exit.
// Checks (in order):
//   - CT_SUPERVISED=1   — explicit user override for custom supervisors
//   - INVOCATION_ID      — set by systemd for every managed unit
//   - SUPERVISOR_ENABLED — set by supervisord
//   - parent PID == 1    — running as direct child of init (Docker, etc.)
func isSupervisedProcess() bool {
	if os.Getenv("CT_SUPERVISED") == "1" {
		return true
	}
	if os.Getenv("INVOCATION_ID") != "" {
		return true // systemd
	}
	if os.Getenv("SUPERVISOR_ENABLED") == "1" {
		return true // supervisord
	}
	if os.Getppid() == 1 {
		return true // direct child of init/PID1
	}
	return false
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
func New(config aqueduct.AqueductConfig, dbPath string, runner CataractaeRunner, opts ...Option) (*Castellarius, error) {
	// Capture binary mtime at construction time for update detection.
	var startupBinaryMtime time.Time
	if exe, err := os.Executable(); err == nil {
		if info, err := os.Stat(exe); err == nil {
			startupBinaryMtime = info.ModTime()
		}
	}

	s := &Castellarius{
		config:             config,
		workflows:          make(map[string]*aqueduct.Workflow),
		clients:            make(map[string]CisternClient),
		pools:              make(map[string]*AqueductPool),
		runner:             runner,
		logger:             slog.Default(),
		pollInterval:       10 * time.Second,
		heartbeatInterval:  30 * time.Second,
		dbPath:             dbPath,
		startupBinaryMtime: startupBinaryMtime,
		supervised:         isSupervisedProcess(),
		reloadCh:           make(chan struct{}, 1),
		findPRFn:           defaultFindPR,
		killSessionFn:      defaultKillSession,
		rebaseAndPushFn:    defaultRebaseAndPush,
		ghMergeFn:          defaultGhMerge,
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
	runner CataractaeRunner,
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
		findPRFn:          defaultFindPR,
		killSessionFn:     defaultKillSession,
		rebaseAndPushFn:   defaultRebaseAndPush,
		ghMergeFn:         defaultGhMerge,
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
	supervisorStatus := "unsupervised (manual restart required for binary updates)"
	if s.supervised {
		supervisorStatus = "supervised (will self-restart via supervisor)"
	}
	s.logger.Info("Cistern online. Aqueducts open.",
		"repos", len(s.config.Repos),
		"cataractae", s.config.MaxCataractae,
		"supervisor", supervisorStatus,
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

	// Stuck delivery goroutine — checks every 5 minutes for delivery agents
	// that have been running past 1.5× their configured timeout. Kills the
	// stuck session and sets an appropriate outcome so the observe phase can
	// route the droplet without human intervention.
	go func() {
		sdTicker := time.NewTicker(stuckDeliveryCheckInterval)
		defer sdTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sdTicker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							stack := debug.Stack()
							s.logger.Error("stuck delivery check: panic recovered",
								"panic", r,
								"stack", string(stack),
							)
						}
					}()
					s.checkStuckDeliveries(ctx)
				}()
			}
		}
	}()

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

// doReloadWorkflows re-parses workflow YAMLs from disk and updates the in-memory
// map. Called on the main goroutine so no locking is needed.
func (s *Castellarius) doReloadWorkflows() {
	for _, repo := range s.config.Repos {
		wfPath := repo.WorkflowPath
		if !filepath.IsAbs(wfPath) {
			home, err := os.UserHomeDir()
			if err != nil {
				s.logger.Error("hot-reload: home dir", "error", err)
				continue
			}
			wfPath = filepath.Join(home, ".cistern", wfPath)
		}
		wf, err := aqueduct.ParseWorkflow(wfPath)
		if err != nil {
			s.logger.Error("hot-reload: failed to load workflow", "repo", repo.Name, "path", wfPath, "error", err)
			continue
		}
		s.workflows[repo.Name] = wf
		s.logger.Info("hot-reload: workflow reloaded", "repo", repo.Name, "cataractae", len(wf.Cataractae))
	}
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

	// Drain the reload channel: if a drought hook signaled a workflow change
	// and we're not supervised, apply the hot-reload here on the main goroutine
	// (safe — no concurrent map writes).
	select {
	case <-s.reloadCh:
		s.doReloadWorkflows()
	default:
	}

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
			reloadFn := func() {
				select {
				case s.reloadCh <- struct{}{}:
				default: // already pending
				}
			}
			go RunDroughtHooks(s.config.DroughtHooks, &s.config, s.dbPath, s.sandboxRoot, s.logger, s.startupBinaryMtime, s.supervised, reloadFn)
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
		assignee := item.Assignee

		// cleanupBranch removes the per-droplet worktree.
		// Called at terminal states and no-route escalation — non-terminal routes keep
		// the worktree so the next dispatch cycle can resume incrementally.
		cleanupBranch := func() {
			if s.sandboxRoot != "" {
				primaryDir := filepath.Join(s.sandboxRoot, repo.Name, "_primary")
				removeDropletWorktree(primaryDir, s.sandboxRoot, repo.Name, item.ID)
			}
		}

		// Release the aqueduct worker unconditionally — it is free for other droplets
		// regardless of where this one routes next.
		if assignee != "" {
			if w := pool.FindByName(assignee); w != nil {
				pool.Release(w)
			}
		}

		if step == nil {
			s.logger.Warn("observe: no step found, resetting",
				"repo", repo.Name, "droplet", item.ID, "cataractae", item.CurrentCataractae)
			cataractaeName := item.CurrentCataractae
			if cataractaeName == "" && len(wf.Cataractae) > 0 {
				cataractaeName = wf.Cataractae[0].Name
			}
			if err := client.Assign(item.ID, "", cataractaeName); err != nil {
				s.logger.Error("observe: reset (no step) failed", "droplet", item.ID, "error", err)
			}
			continue
		}

		result, recirculateTo := parseOutcome(item.Outcome)

		switch result {
		case ResultPass:
			s.logger.Info("Droplet cleared cataractae", "droplet", item.ID, "cataractae", step.Name)
		case ResultRecirculate:
			s.logger.Info("Droplet recirculated", "droplet", item.ID, "cataractae", step.Name)
		default:
			s.logger.Info("Droplet stagnant at cataractae", "droplet", item.ID, "cataractae", step.Name, "outcome", item.Outcome)
		}

		// Phantom commit prevention: when implement passes, verify that HEAD has
		// advanced since the last review. If not, the implementer signed pass without
		// committing — auto-recirculate with a diagnostic message.
		if result == ResultPass && step.Name == "implement" {
			if lastCommit, err := client.GetLastReviewedCommit(item.ID); err == nil && lastCommit != "" {
				sandboxDir := filepath.Join(s.sandboxRoot, repo.Name, item.ID)
				if head, err := sandboxHead(sandboxDir); err == nil && head == lastCommit {
					note := fmt.Sprintf(
						"Implement pass rejected: HEAD has not advanced since last review (commit: %s). No new commits were found. You must commit your changes before signaling pass.",
						lastCommit,
					)
					s.logger.Warn("Phantom commit detected — recirculating to implement",
						"droplet", item.ID, "commit", lastCommit)
					_ = client.AddNote(item.ID, "scheduler", note)
					if err := client.Assign(item.ID, "", "implement"); err != nil {
						s.logger.Error("observe: phantom commit recirculate failed", "droplet", item.ID, "error", err)
					}
					continue
				}
			}
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
			cleanupBranch()
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
			cleanupBranch()
			s.handleTerminal(client, item.ID, next, step.Name)
			continue
		}

		// Advance item to next step (open for the next dispatch cycle).
		// The feature branch is kept so the next cycle can resume incrementally.
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
		if s.totalBusy() >= s.config.MaxCataractae {
			return
		}

		worker := pool.AvailableAqueduct()
		if worker == nil {
			return
		}

		// Each repo has its own pool — just get the next ready droplet for this repo.
		// No sticky aqueduct matching needed: each aqueduct has its own worktree,
		// so any aqueduct in the pool can work on any droplet in the repo.
		item, err := client.GetReady(repo.Name)
		if err != nil {
			s.logger.Error("poll failed", "repo", repo.Name, "error", err)
			pool.Release(worker)
			return
		}
		if item == nil {
			pool.Release(worker)
			return
		}

		step := currentCataracta(item, wf)
		if step == nil {
			s.logger.Error("no step found", "repo", repo.Name, "droplet", item.ID)
			pool.Release(worker)
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

		s.logger.Info("Droplet entering cataractae",
			"droplet", item.ID,
			"operator", worker.Name,
			"cataractae", step.Name,
		)

		req := CataractaeRequest{
			Item:       item,
			Step:       *step,
			Workflow:   wf,
			RepoConfig: repo,
			AqueductName: worker.Name,
			Notes:      notes,
		}

		w := worker // capture for goroutine
		go func() {
			// Prepare the per-droplet worktree before spawning the agent.
			// Castellarius owns worktree lifecycle — agents never call git worktree add.
			// Skipped when sandboxRoot is unset (test environments without real repos).
			if s.sandboxRoot != "" &&
				req.Step.Type == aqueduct.CataractaeTypeAgent &&
				(req.Step.Context == aqueduct.ContextFullCodebase || req.Step.Context == "") {
				primaryDir := filepath.Join(s.sandboxRoot, req.RepoConfig.Name, "_primary")
				sandboxDir, err := prepareDropletWorktree(primaryDir, s.sandboxRoot, req.RepoConfig.Name, req.Item.ID)
				if err != nil {
					s.logger.Error("prepare worktree failed",
						"repo", req.RepoConfig.Name,
						"droplet", req.Item.ID,
						"error", err,
					)
					if err2 := client.Assign(req.Item.ID, "", req.Step.Name); err2 != nil {
						s.logger.Error("reset after worktree failure", "droplet", req.Item.ID, "error", err2)
					}
					pool.Release(w)
					return
				}

				// Dirty state check: if non-CONTEXT.md files are uncommitted,
				// recirculate with a diagnostic note rather than spawning into dirty state.
				if dirtyFiles := dirtyNonContextFiles(sandboxDir); len(dirtyFiles) > 0 {
					note := fmt.Sprintf(
						"Dispatch blocked: worktree has uncommitted files from a prior session: %s. "+
							"These must be committed or discarded before proceeding.",
						strings.Join(dirtyFiles, ", "),
					)
					s.logger.Warn("dirty worktree — recirculating",
						"droplet", req.Item.ID,
						"files", dirtyFiles,
					)
					_ = client.AddNote(req.Item.ID, "scheduler", note)
					if err2 := client.Assign(req.Item.ID, "", req.Step.Name); err2 != nil {
						s.logger.Error("reset after dirty check", "droplet", req.Item.ID, "error", err2)
					}
					pool.Release(w)
					return
				}

				req.SandboxDir = sandboxDir
			}

			if err := s.runner.Spawn(ctx, req); err != nil {
				s.logger.Error("spawn failed",
					"repo", repo.Name,
					"droplet", req.Item.ID,
					"cataractae", req.Step.Name,
					"error", err,
				)
				// Reset to open so the item can be re-dispatched to same aqueduct.
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
func currentCataracta(item *cistern.Droplet, wf *aqueduct.Workflow) *aqueduct.WorkflowCataractae {
	if item.CurrentCataractae != "" {
		return lookupCataracta(wf, item.CurrentCataractae)
	}
	if len(wf.Cataractae) > 0 {
		return &wf.Cataractae[0]
	}
	return nil
}

func lookupCataracta(wf *aqueduct.Workflow, name string) *aqueduct.WorkflowCataractae {
	for i := range wf.Cataractae {
		if wf.Cataractae[i].Name == name {
			return &wf.Cataractae[i]
		}
	}
	return nil
}

// route determines the next step name based on the outcome result.
func route(step aqueduct.WorkflowCataractae, result Result) string {
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
		s.logger.Info("Droplet stagnant at terminal", "droplet", itemID, "terminal", terminal, "from_cataractae", fromStep)
		reason := fmt.Sprintf("reached terminal %q from cataractae %q", terminal, fromStep)
		if err := client.Escalate(itemID, reason); err != nil {
			s.logger.Error("escalate at terminal failed", "droplet", itemID, "error", err)
		}
		if strings.ToLower(terminal) == "human" {
			if err := client.SetCataractae(itemID, "human"); err != nil {
				s.logger.Error("set cataractae human failed", "droplet", itemID, "error", err)
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
			cataractaeName := item.CurrentCataractae
			if cataractaeName == "" {
				step := currentCataracta(item, wf)
				if step != nil {
					cataractaeName = step.Name
				} else if len(wf.Cataractae) > 0 {
					cataractaeName = wf.Cataractae[0].Name
				}
			}

			s.logger.Info("recovery: resetting in_progress item to open",
				"repo", repo.Name, "droplet", item.ID, "cataractae", cataractaeName)
			if err := client.Assign(item.ID, "", cataractaeName); err != nil {
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
			"repo", repo.Name, "droplet", item.ID, "cataractae", item.CurrentCataractae)

		if item.Assignee != "" {
			if w := pool.FindByName(item.Assignee); w != nil {
				pool.Release(w)
			}
		}

		if err := client.Assign(item.ID, "", item.CurrentCataractae); err != nil {
			s.logger.Error("heartbeat: reset failed", "droplet", item.ID, "error", err)
		}
	}
}

// isTmuxAlive returns true if a tmux session with the given name is running.
func isTmuxAlive(sessionID string) bool {
	return exec.Command("tmux", "has-session", "-t", sessionID).Run() == nil
}

// sandboxHead returns the current HEAD commit hash in the given directory.
func sandboxHead(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// prepareBranchInSandbox creates or resumes the feature branch for a droplet
// in the given sandbox (worktree) directory. Castellarius calls this before
// spawning an agent — agents never manage branches directly.
func prepareBranchInSandbox(dir, itemID string) error {
	branch := "feat/" + itemID

	// Configure git identity so commits don't fail.
	for _, args := range [][]string{
		{"git", "config", "user.name", "Cistern Agent"},
		{"git", "config", "user.email", "agent@cistern.local"},
	} {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("%v in %s: %w: %s", args, dir, err, out)
		}
	}

	// Check whether the branch already exists locally.
	listCmd := exec.Command("git", "branch", "--list", branch)
	listCmd.Dir = dir
	out, err := listCmd.Output()
	if err != nil {
		return fmt.Errorf("git branch --list in %s: %w", dir, err)
	}

	if strings.TrimSpace(string(out)) != "" {
		// Branch exists — check it out to resume incrementally.
		checkout := exec.Command("git", "checkout", branch)
		checkout.Dir = dir
		if co, err := checkout.CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout %s in %s: %w: %s", branch, dir, err, co)
		}
		return nil
	}

	// New branch — fetch and start from a clean origin/main.
	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = dir
	if fo, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch in %s: %w: %s", dir, err, fo)
	}

	reset := exec.Command("git", "reset", "--hard", "origin/main")
	reset.Dir = dir
	if ro, err := reset.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset in %s: %w: %s", dir, err, ro)
	}

	clean := exec.Command("git", "clean", "-fdx")
	clean.Dir = dir
	if co, err := clean.CombinedOutput(); err != nil {
		return fmt.Errorf("git clean in %s: %w: %s", dir, err, co)
	}

	create := exec.Command("git", "checkout", "-b", branch)
	create.Dir = dir
	if co, err := create.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b %s in %s: %w: %s", branch, dir, err, co)
	}

	return nil
}

// prepareDropletWorktree creates (or resumes) a per-droplet git worktree at
// sandboxRoot/<repoName>/<dropletID>/ on branch feat/<dropletID>.
//
// If the directory already exists (recirculation), the branch is checked out
// without resetting — preserving all prior agent commits.
// If new, it is created via `git worktree add -b feat/<id> <path> origin/main`.
func prepareDropletWorktree(primaryDir, sandboxRoot, repoName, dropletID string) (string, error) {
	worktreePath := filepath.Join(sandboxRoot, repoName, dropletID)
	branch := "feat/" + dropletID

	if _, err := os.Stat(worktreePath); err == nil {
		// Worktree exists — resume by checking out the branch.
		checkout := exec.Command("git", "checkout", branch)
		checkout.Dir = worktreePath
		if out, err := checkout.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git checkout %s in %s: %w: %s", branch, worktreePath, err, out)
		}
		// Discard CONTEXT.md left modified from prior dispatch — it is rewritten
		// fresh by PrepareContext before each spawn.
		discard := exec.Command("git", "checkout", "--", "CONTEXT.md")
		discard.Dir = worktreePath
		_ = discard.Run()
		return worktreePath, nil
	}

	// Fetch latest before creating.
	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = primaryDir
	if out, err := fetch.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git fetch in %s: %w: %s", primaryDir, err, out)
	}

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir for worktree %s: %w", worktreePath, err)
	}

	// First try attaching to an existing branch (handles crash-between-branch-create-and-worktree-add).
	addExisting := exec.Command("git", "worktree", "add", worktreePath, branch)
	addExisting.Dir = primaryDir
	if out, err := addExisting.CombinedOutput(); err != nil {
		// Branch doesn't exist yet — create it fresh from origin/main.
		addNew := exec.Command("git", "worktree", "add", "-b", branch, worktreePath, "origin/main")
		addNew.Dir = primaryDir
		if out2, err2 := addNew.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("git worktree add %s in %s: %w: %s", worktreePath, primaryDir, err2, out2)
		}
		_ = out // first attempt output discarded; only the second failure matters
	}

	for _, args := range [][]string{
		{"git", "config", "user.name", "Cistern Agent"},
		{"git", "config", "user.email", "agent@cistern.local"},
	} {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = worktreePath
		if out, err := c.CombinedOutput(); err != nil {
			return "", fmt.Errorf("%v in %s: %w: %s", args, worktreePath, err, out)
		}
	}

	// Hard-reset to origin/main to guarantee a clean baseline — the worktree
	// may inherit local modifications from the primary clone.
	reset := exec.Command("git", "reset", "--hard", "origin/main")
	reset.Dir = worktreePath
	if out, err := reset.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git reset in %s: %w: %s", worktreePath, err, out)
	}
	clean := exec.Command("git", "clean", "-fd")
	clean.Dir = worktreePath
	_ = clean.Run()

	return worktreePath, nil
}

// removeDropletWorktree removes the per-droplet worktree directory and
// unregisters it from git. Errors are ignored — best-effort cleanup.
func removeDropletWorktree(primaryDir, sandboxRoot, repoName, dropletID string) {
	worktreePath := filepath.Join(sandboxRoot, repoName, dropletID)
	rm := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	rm.Dir = primaryDir
	_ = rm.Run()
}

// dirtyNonContextFiles returns uncommitted non-CONTEXT.md files in dir.
// An empty slice means the worktree is clean for dispatch.
func dirtyNonContextFiles(dir string) []string {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var dirty []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// Format: "XY filename" — XY is always exactly 2 chars, then a space.
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		// Skip untracked files ("??" prefix) — gitignored binaries (ct, install)
		// show as untracked and should never block dispatch.
		if xy == "??" {
			continue
		}
		name := strings.TrimSpace(line[3:])
		if name != "CONTEXT.md" {
			dirty = append(dirty, name)
		}
	}
	return dirty
}

// cleanupBranchInSandbox detaches HEAD in the worktree and deletes the feature
// branch. Called by the Castellarius after a droplet completes or recirculates.
// Errors are ignored — this is best-effort cleanup.
//
// Deprecated: use removeDropletWorktree for per-droplet worktrees.
// Kept for backwards compatibility with existing tests.
func cleanupBranchInSandbox(dir, branch string) {
	// Detach HEAD so we can delete the branch.
	detach := exec.Command("git", "checkout", "--detach", "HEAD")
	detach.Dir = dir
	_ = detach.Run()

	del := exec.Command("git", "branch", "-D", branch)
	del.Dir = dir
	_ = del.Run()
}

// WriteContext writes a CONTEXT.md file with notes from previous steps.
// Call this before spawning the next agent to provide context from prior steps.
func WriteContext(dir string, notes []cistern.CataractaeNote) error {
	if len(notes) == 0 {
		return nil
	}

	var b []byte
	b = append(b, "# Context from Previous Steps\n\n"...)
	for _, n := range notes {
		header := n.CataractaeName
		if header == "" {
			header = "unknown"
		}
		b = append(b, fmt.Sprintf("## Step: %s\n\n%s\n\n", header, n.Content)...)
	}

	return os.WriteFile(filepath.Join(dir, "CONTEXT.md"), b, 0o644)
}

// ensureCataractaeIntegrity checks each agent cataractae's CLAUDE.md for the
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
