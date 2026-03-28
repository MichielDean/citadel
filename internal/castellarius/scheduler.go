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
	// Set for full_codebase and diff_only agent steps; empty otherwise.
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
	cfgPath             string    // path to cistern.yaml; used to detect config-file updates
	startupCfgMtime     time.Time // mtime of cistern.yaml at startup; used to detect updates
	supervised          bool      // true if managed by systemd/supervisord/etc.
	reloadCh            chan struct{} // signals Tick() to hot-reload workflows from disk

	// Stuck delivery recovery — injectable for testing.
	findPRFn        func(ctx context.Context, repoName, dropletID, sandboxDir string) (prURL, state, mergeStateStatus string, err error)
	killSessionFn   func(sessionID string) error
	rebaseAndPushFn func(ctx context.Context, sandboxDir string) error
	ghMergeFn       func(ctx context.Context, sandboxDir, prURL string, autoMerge bool) error

	// drainTimeout is the maximum duration to wait for in-flight sessions to
	// signal an outcome after SIGTERM. Defaults to 5 minutes.
	drainTimeout time.Duration

	// Dispatch-loop detection — tracks per-droplet failure counts to detect and
	// recover from tight loops where no agent session ever starts.
	dispatchLoop *dispatchLoopTracker

	// lastStallNoted tracks the most recent signal time at which a stall note
	// was appended for each droplet (keyed by droplet ID). Used to debounce
	// repeated stall notes: no new note is written until at least one progress
	// signal advances past the stored time.
	lastStallNoted map[string]time.Time

	// sessionLogRoot is the directory containing per-session output logs.
	// Defaults to ~/.cistern/session-logs when empty.
	sessionLogRoot string
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

// WithSessionLogRoot overrides the directory used to locate per-session output
// logs. Primarily for testing; production code uses ~/.cistern/session-logs.
func WithSessionLogRoot(dir string) Option {
	return func(s *Castellarius) { s.sessionLogRoot = dir }
}

// WithConfigPath records the path to cistern.yaml so the scheduler can detect
// when the config file has been updated on disk and trigger a clean restart.
// The mtime is captured in New() after all options are applied.
func WithConfigPath(path string) Option {
	return func(s *Castellarius) { s.cfgPath = path }
}

// WithDrainTimeout overrides the graceful-shutdown drain timeout. Primarily
// used in tests to avoid multi-minute waits.
func WithDrainTimeout(d time.Duration) Option {
	return func(s *Castellarius) { s.drainTimeout = d }
}

// WithHeartbeatInterval overrides how often the heartbeat scans for stalled
// in-progress droplets. Defaults to 30s; pass a shorter duration in tests.
func WithHeartbeatInterval(d time.Duration) Option {
	return func(s *Castellarius) { s.heartbeatInterval = d }
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
		drainTimeout:       5 * time.Minute,
		dbPath:             dbPath,
		startupBinaryMtime: startupBinaryMtime,
		supervised:         isSupervisedProcess(),
		reloadCh:           make(chan struct{}, 1),
		findPRFn:           defaultFindPR,
		killSessionFn:      defaultKillSession,
		rebaseAndPushFn:    defaultRebaseAndPush,
		ghMergeFn:          defaultGhMerge,
		dispatchLoop:       newDispatchLoopTracker(),
		lastStallNoted:     make(map[string]time.Time),
	}
	for _, o := range opts {
		o(s)
	}

	// Capture config mtime at construction time for update detection (mirrors
	// the binary mtime capture above).
	if s.cfgPath != "" {
		if info, err := os.Stat(s.cfgPath); err == nil {
			s.startupCfgMtime = info.ModTime()
		} else {
			s.logger.Warn("cannot stat cistern.yaml — config-update detection disabled",
				"path", s.cfgPath, "err", err)
		}
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

	if config.DrainTimeoutMinutes > 0 {
		s.drainTimeout = time.Duration(config.DrainTimeoutMinutes) * time.Minute
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
		drainTimeout:      5 * time.Minute,
		findPRFn:          defaultFindPR,
		killSessionFn:     defaultKillSession,
		rebaseAndPushFn:   defaultRebaseAndPush,
		ghMergeFn:         defaultGhMerge,
		dispatchLoop:      newDispatchLoopTracker(),
		lastStallNoted:    make(map[string]time.Time),
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
	totalSlots := 0
	for _, repo := range s.config.Repos {
		totalSlots += repo.Cataractae
	}
	s.logger.Info("Cistern online. Aqueducts open.",
		"repos", len(s.config.Repos),
		"cataractae", totalSlots,
		"supervisor", supervisorStatus,
	)

	// Startup credential check: log which env vars are set (names only, never values)
	// and whether gh is authenticated. Helps diagnose auth failures without leaking secrets.
	s.logStartupCredentials(ctx)

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
			return s.drainInFlight()
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// drainInFlight waits for all in-progress droplets (sessions with no outcome
// yet) to signal before returning. It runs observe-only ticks — no new work is
// dispatched during the drain. Three paths:
//
//   - Zero in-flight: exits immediately, logs "Aqueducts closed."
//   - Clean drain:    all sessions complete within drainTimeout, logs "drain complete"
//   - Timeout:        forces exit after drainTimeout, logs stuck IDs
//
// If stuckSessionIDs returns an error, the drain conservatively assumes sessions
// are still running and keeps waiting until the timeout fires.
//
// Always returns context.Canceled so the caller (cmd layer) treats it as a
// clean exit from a signal.
func (s *Castellarius) drainInFlight() error {
	ids, err := s.stuckSessionIDs()
	if err != nil {
		// Conservative: treat a query failure as sessions still running.
		s.logger.Info("draining in-flight sessions before shutdown (count unknown due to query error)")
	} else if len(ids) == 0 {
		s.logger.Info("Aqueducts closed.")
		return context.Canceled
	} else {
		s.logger.Info("draining in-flight sessions before shutdown", "sessions", len(ids))
	}

	deadline := time.NewTimer(s.drainTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadline.C:
			stuck, err := s.stuckSessionIDs()
			var sessions, ids any = len(stuck), stuck
			if err != nil {
				sessions, ids = "unknown (query error)", "unavailable"
			}
			s.logger.Warn("drain timeout — forcing exit with sessions still running",
				"sessions", sessions,
				"ids", ids,
			)
			return context.Canceled
		case <-ticker.C:
			for _, repo := range s.config.Repos {
				s.observeRepo(context.Background(), repo)
			}
			ids, err := s.stuckSessionIDs()
			if err != nil {
				// Conservative: keep draining on query error.
				continue
			}
			if len(ids) == 0 {
				s.logger.Info("drain complete")
				return context.Canceled
			}
		}
	}
}

// stuckSessionIDs returns the IDs of in-progress droplets with no outcome set.
// Returns an error if any repo's client.List call fails; callers must treat
// errors conservatively (assume sessions are still running).
func (s *Castellarius) stuckSessionIDs() ([]string, error) {
	var ids []string
	for _, repo := range s.config.Repos {
		client := s.clients[repo.Name]
		items, err := client.List(repo.Name, "in_progress")
		if err != nil {
			s.logger.Warn("stuckSessionIDs: failed to list in-progress droplets",
				"repo", repo.Name,
				"err", err,
			)
			return nil, err
		}
		for _, item := range items {
			if item.Outcome == "" {
				ids = append(ids, item.ID)
			}
		}
	}
	return ids, nil
}

// logStartupCredentials logs which credential-related environment variables are
// set (names only — values are never logged) and whether gh is authenticated.
// Called once at Castellarius startup to surface auth problems early.
func (s *Castellarius) logStartupCredentials(ctx context.Context) {
	// Collect the names of set env vars across all repo presets. Values are
	// intentionally never logged to prevent credential leakage.
	seenVars := map[string]bool{}
	setVars := []string{}
	for _, repo := range s.config.Repos {
		preset, err := s.config.ResolveProvider(repo.Name)
		if err != nil {
			continue
		}
		for _, envVar := range preset.EnvPassthrough {
			if seenVars[envVar] {
				continue
			}
			seenVars[envVar] = true
			if os.Getenv(envVar) != "" {
				setVars = append(setVars, envVar)
			} else {
				s.logger.Warn("startup credentials: required env var not set",
					"var", envVar, "repo", repo.Name)
			}
		}
	}
	if len(setVars) > 0 {
		s.logger.Info("startup credentials: env vars set", "vars", setVars)
	}

	// gh auth status output is NOT logged (may contain token fragments).
	// Use a 10s timeout so a hung credential helper cannot block startup indefinitely.
	ghCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if exec.CommandContext(ghCtx, "gh", "auth", "status").Run() == nil {
		s.logger.Info("startup credentials: gh authenticated")
	} else {
		s.logger.Warn("startup credentials: gh auth status failed — sessions may fail on GitHub operations")
	}
}

// addNote writes a note via client.AddNote and logs a warning if the write fails.
// Used throughout dispatch, recovery, and observe paths where AddNote failure
// should not derail the primary operation.
func (s *Castellarius) addNote(client CisternClient, dropletID, source, msg string) {
	if err := client.AddNote(dropletID, source, msg); err != nil {
		s.logger.Warn("AddNote failed", "droplet", dropletID, "source", source, "error", err)
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
			go RunDroughtHooks(DroughtHookParams{
				Hooks:              s.config.DroughtHooks,
				Config:             &s.config,
				DBPath:             s.dbPath,
				SandboxRoot:        s.sandboxRoot,
				Logger:             s.logger,
				StartupBinaryMtime: s.startupBinaryMtime,
				CfgPath:            s.cfgPath,
				StartupCfgMtime:    s.startupCfgMtime,
				Supervised:         s.supervised,
				OnReload:           reloadFn,
			})
		}
	}
	s.wasDrought = isDrought

	s.writeHealthFile()
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
					s.addNote(client, item.ID, "scheduler", note)
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

		// Auto-promote: when a step signals recirculate but has no on_recirculate route,
		// treat it as pass. The work is almost certainly complete — the agent chose the
		// wrong signal. Log at WARN so the pattern is visible without failing anything.
		if next == "" && result == ResultRecirculate {
			if passNext := route(*step, ResultPass); passNext != "" {
				note := fmt.Sprintf(
					"Auto-promoted: cataractae %q signaled recirculate but has no on_recirculate route — treated as pass. Review agent behavior if this recurs.",
					step.Name,
				)
				s.logger.Warn("observe: auto-promoting recirculate to pass",
					"droplet", item.ID, "step", step.Name)
				s.addNote(client, item.ID, "scheduler", note)
				next = passNext
			}
		}

		if next == "" {
			if result == ResultRecirculate {
				note := fmt.Sprintf(
					"cataractae %q signaled recirculate but has no on_recirculate route configured — this is likely an agent error (recirculate used instead of pass or block). Manual intervention required.",
					step.Name,
				)
				s.addNote(client, item.ID, "scheduler", note)
			}
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
			// Dispatch-loop detection: if this droplet has been failing repeatedly
			// without ever spawning an agent, attempt ordered recovery before retrying.
			failCount := s.dispatchLoop.recentFailureCount(req.Item.ID)
			if failCount >= dispatchLoopThreshold {
				s.logger.Warn("dispatch-loop threshold reached — triggering recovery",
					"droplet", req.Item.ID,
					"failures", failCount,
					"threshold", dispatchLoopThreshold,
					"window", dispatchLoopWindow.String(),
				)
				s.recoverDispatchLoop(client, req.Item, req.RepoConfig)
				if err2 := client.Assign(req.Item.ID, "", req.Step.Name); err2 != nil {
					s.logger.Error("dispatch-loop recovery: reset failed",
						"droplet", req.Item.ID, "error", err2)
				}
				pool.Release(w)
				return
			}

			// Prepare the per-droplet worktree before spawning the agent.
			// Castellarius owns worktree lifecycle — agents never call git worktree add.
			// Skipped when sandboxRoot is unset (test environments without real repos).
			//
			// Invariant: every agent context type except spec_only requires a
			// per-droplet worktree.
			//   - full_codebase / "": agent reads and writes the repo directly.
			//   - diff_only: generateDiff reads committed changes from the worktree
			//     to produce diff.patch; the agent's working dir is a separate tmpdir.
			//   - spec_only: agent receives only spec.md in an isolated tmpdir —
			//     no repo access at all; no worktree needed.
			//
			// If a new context type is added that does NOT need a worktree, this
			// condition must be updated (do NOT just add another != clause without
			// understanding the full_codebase/diff_only requirements above).
			if s.sandboxRoot != "" &&
				req.Step.Type == aqueduct.CataractaeTypeAgent &&
				req.Step.Context != aqueduct.ContextSpecOnly {
				primaryDir := filepath.Join(s.sandboxRoot, req.RepoConfig.Name, "_primary")
				sandboxDir, err := prepareDropletWorktree(primaryDir, s.sandboxRoot, req.RepoConfig.Name, req.Item.ID)
				if err != nil {
					s.logger.Error("prepare worktree failed",
						"repo", req.RepoConfig.Name,
						"droplet", req.Item.ID,
						"error", err,
					)
					s.dispatchLoop.recordFailure(req.Item.ID)
					if err2 := client.Assign(req.Item.ID, "", req.Step.Name); err2 != nil {
						s.logger.Error("reset after worktree failure", "droplet", req.Item.ID, "error", err2)
					}
					pool.Release(w)
					return
				}

				// Dirty state check: if non-CONTEXT.md files are uncommitted,
				// recirculate with a diagnostic note rather than spawning into dirty state.
				// If git status itself fails (transient error, disk full, permissions),
				// recirculate conservatively rather than letting an unknown dirty state advance.
				dirtyFiles, dirtyErr := dirtyNonContextFiles(sandboxDir)
				if dirtyErr != nil {
					s.logger.Error("dirty check: git status failed — recirculating conservatively",
						"droplet", req.Item.ID, "error", dirtyErr)
					s.dispatchLoop.recordFailure(req.Item.ID)
					s.addNote(client, req.Item.ID, "scheduler",
						fmt.Sprintf("Dispatch blocked: could not check worktree state: %v", dirtyErr))
					if err2 := client.Assign(req.Item.ID, "", req.Step.Name); err2 != nil {
						s.logger.Error("reset after dirty-check error", "droplet", req.Item.ID, "error", err2)
					}
					pool.Release(w)
					return
				}
				if len(dirtyFiles) > 0 {
					note := fmt.Sprintf(
						"Dispatch blocked: worktree has uncommitted files from a prior session: %s. "+
							"These must be committed or discarded before proceeding.",
						strings.Join(dirtyFiles, ", "),
					)
					s.logger.Warn("dirty worktree — recirculating",
						"droplet", req.Item.ID,
						"files", dirtyFiles,
					)
					s.dispatchLoop.recordFailure(req.Item.ID)
					s.addNote(client, req.Item.ID, "scheduler", note)
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
				s.dispatchLoop.recordFailure(req.Item.ID)
				// Reset to open so the item can be re-dispatched to same aqueduct.
				if err2 := client.Assign(req.Item.ID, "", req.Step.Name); err2 != nil {
					s.logger.Error("reset after spawn failure",
						"droplet", req.Item.ID, "error", err2)
				}
				pool.Release(w)
				return
			}
			// Successful spawn — reset the dispatch-loop counter.
			s.dispatchLoop.reset(req.Item.ID)
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

func (s *Castellarius) heartbeatRepo(ctx context.Context, repo aqueduct.RepoConfig) {
	client := s.clients[repo.Name]
	wf := s.workflows[repo.Name]

	items, err := client.List(repo.Name, "in_progress")
	if err != nil {
		s.logger.Error("heartbeat: list in_progress failed", "repo", repo.Name, "error", err)
		return
	}

	threshold := stallThresholdDuration(s.config)
	logDir := s.resolveSessionLogRoot()

	// Prune lastStallNoted entries for droplets that are no longer in_progress.
	activeIDs := make(map[string]struct{}, len(items))
	for _, it := range items {
		activeIDs[it.ID] = struct{}{}
	}
	for id := range s.lastStallNoted {
		if _, ok := activeIDs[id]; !ok {
			delete(s.lastStallNoted, id)
		}
	}

	for _, item := range items {
		// Items with outcomes are handled by the observe phase — skip them.
		if item.Outcome != "" {
			continue
		}

		// Fast liveness check: if the tmux session is dead, re-spawn immediately
		// using --continue so the agent resumes where it left off. This runs on
		// every heartbeat tick (~30s) — no threshold, no waiting.
		if item.Assignee != "" {
			sessionID := repo.Name + "-" + item.Assignee
			if !isTmuxAlive(sessionID) {
				// Minimum age guard: ignore sessions dispatched < 2× pollInterval ago
				// (tmux may not yet be visible immediately after spawn).
				if time.Since(item.UpdatedAt) < 2*s.pollInterval {
					continue
				}
				step := currentCataracta(item, wf)
				if step == nil {
					s.logger.Error("heartbeat: no step for dead session — skipping",
						"repo", repo.Name, "droplet", item.ID)
					continue
				}
				notes, _ := client.GetNotes(item.ID)
				req := CataractaeRequest{
					Item:         item,
					Step:         *step,
					Workflow:     wf,
					RepoConfig:   repo,
					AqueductName: item.Assignee,
					Notes:        notes,
				}
				if s.sandboxRoot != "" && step.Type == aqueduct.CataractaeTypeAgent && step.Context != aqueduct.ContextSpecOnly {
					req.SandboxDir = filepath.Join(s.sandboxRoot, repo.Name, item.ID)
				}
				s.logger.Info("heartbeat: dead session — re-spawning with --continue",
					"repo", repo.Name,
					"droplet", item.ID,
					"assignee", item.Assignee,
					"cataractae", step.Name,
					"session_age", time.Since(item.UpdatedAt).Round(time.Second).String(),
				)
				if err := s.runner.Spawn(ctx, req); err != nil {
					s.logger.Error("heartbeat: re-spawn failed",
						"repo", repo.Name, "droplet", item.ID, "error", err)
				}
				continue // handled — skip progress check for this item
			}
		}

		// Evaluate three activity signals.
		noteSig, noteLabel := latestNoteSignal(client, item.ID)
		worktreeSig, worktreeLabel := latestWorktreeSignal(s.sandboxRoot, repo.Name, item.ID)
		logSig, logLabel := sessionLogSignal(logDir, repo.Name, item.Assignee)

		maxSig := latestTime(noteSig, worktreeSig, logSig)

		// Clear debounce if any signal has advanced past the recorded stall time.
		if prev, ok := s.lastStallNoted[item.ID]; ok && maxSig.After(prev) {
			delete(s.lastStallNoted, item.ID)
		}

		// Not stalled — nothing to do.
		if time.Since(maxSig) <= threshold {
			continue
		}

		// Stalled. Suppress if we already noted this stall and no signal has advanced.
		if _, ok := s.lastStallNoted[item.ID]; ok {
			continue
		}

		// First detection — append a stall note and emit a warning.
		note := fmt.Sprintf(
			"Droplet appears stalled (no activity for %v, threshold %v).\n  note signal:        %s\n  worktree signal:    %s\n  session log signal: %s",
			time.Since(maxSig).Round(time.Second),
			threshold,
			noteLabel, worktreeLabel, logLabel,
		)
		if err := client.AddNote(item.ID, "scheduler", note); err != nil {
			s.logger.Warn("heartbeat: AddNote failed", "droplet", item.ID, "error", err)
			continue // do not arm debounce — note was never written
		}
		s.logger.Warn("heartbeat: stall detected",
			"repo", repo.Name,
			"droplet", item.ID,
			"cataractae", item.CurrentCataractae,
			"stall_duration", time.Since(maxSig).Round(time.Second).String(),
			"threshold", threshold.String(),
		)
		s.lastStallNoted[item.ID] = maxSig

		// Re-spawn the stalled session if an assignee is present.
		// session.Spawn() detects prior Claude session files and uses --continue
		// when they exist, or spawns fresh when priorSessionCount == 0.
		// Status and assignee are intentionally left unchanged.
		if item.Assignee != "" {
			if err := s.respawnStalledDroplet(ctx, client, repo, item); err != nil {
				// Clear the debounce so the next heartbeat re-detects the stall
				// and retries — spawn failures are often transient.
				delete(s.lastStallNoted, item.ID)
			}
		}
	}
}

// respawnStalledDroplet calls runner.Spawn for a stalled in-progress droplet
// whose session has gone quiet. It reuses the existing worktree and assignee;
// session.Spawn() selects --continue or a fresh spawn based on prior session
// files under ~/.claude/projects/<worktree>/.
func (s *Castellarius) respawnStalledDroplet(ctx context.Context, client CisternClient, repo aqueduct.RepoConfig, item *cistern.Droplet) error {
	wf, ok := s.workflows[repo.Name]
	if !ok {
		s.logger.Warn("heartbeat: no workflow for repo — cannot respawn stalled session",
			"repo", repo.Name, "droplet", item.ID)
		return nil
	}

	step := currentCataracta(item, wf)
	if step == nil {
		s.logger.Warn("heartbeat: no step found — cannot respawn stalled session",
			"repo", repo.Name, "droplet", item.ID, "cataractae", item.CurrentCataractae)
		return nil
	}

	notes, err := client.GetNotes(item.ID)
	if err != nil {
		s.logger.Warn("heartbeat: GetNotes failed (continuing without notes)", "droplet", item.ID, "error", err)
	}

	req := CataractaeRequest{
		Item:         item,
		Step:         *step,
		Workflow:     wf,
		RepoConfig:   repo,
		AqueductName: item.Assignee,
		Notes:        notes,
	}

	// Use the existing worktree — it was created by the original dispatch.
	if s.sandboxRoot != "" &&
		step.Type == aqueduct.CataractaeTypeAgent &&
		step.Context != aqueduct.ContextSpecOnly {
		req.SandboxDir = filepath.Join(s.sandboxRoot, repo.Name, item.ID)
	}

	s.logger.Info("heartbeat: respawning stalled session",
		"repo", repo.Name,
		"droplet", item.ID,
		"assignee", item.Assignee,
		"cataractae", item.CurrentCataractae,
	)

	if err := s.runner.Spawn(ctx, req); err != nil {
		s.logger.Error("heartbeat: respawn failed",
			"repo", repo.Name, "droplet", item.ID, "error", err)
		return err
	}
	return nil
}

// stallThresholdDuration returns the configured stall threshold, defaulting to 45 minutes.
func stallThresholdDuration(cfg aqueduct.AqueductConfig) time.Duration {
	if cfg.StallThresholdMinutes > 0 {
		return time.Duration(cfg.StallThresholdMinutes) * time.Minute
	}
	return 45 * time.Minute
}

// latestNoteSignal returns the most recent note timestamp for a droplet, along
// with a human-readable label. Returns a zero time if the droplet has no notes
// or the lookup fails.
func latestNoteSignal(client CisternClient, dropletID string) (time.Time, string) {
	notes, err := client.GetNotes(dropletID)
	if err != nil || len(notes) == 0 {
		return time.Time{}, "none"
	}
	var latest time.Time
	for _, n := range notes {
		if n.CataractaeName == "scheduler" {
			continue // exclude scheduler-generated notes to prevent self-clearing debounce loop
		}
		if n.CreatedAt.After(latest) {
			latest = n.CreatedAt
		}
	}
	if latest.IsZero() {
		return time.Time{}, "none"
	}
	return latest, latest.Format(time.RFC3339)
}

// latestWorktreeSignal returns the most recent file mtime under the droplet's
// worktree directory, along with a human-readable label. Returns a zero time if
// the directory does not exist, cannot be read, or sandboxRoot is empty.
func latestWorktreeSignal(sandboxRoot, repoName, dropletID string) (time.Time, string) {
	if sandboxRoot == "" {
		return time.Time{}, "none (no sandbox root)"
	}
	dir := filepath.Join(sandboxRoot, repoName, dropletID)
	var latest time.Time
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil // directories change on every commit — only count files
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	if latest.IsZero() {
		return time.Time{}, "none (no files)"
	}
	return latest, latest.Format(time.RFC3339)
}

// sessionLogSignal returns the mtime of the session output log for the given
// assignee, along with a human-readable label. Returns a zero time if the
// assignee is empty, logDir is empty, or the log file does not exist.
func sessionLogSignal(logDir, repoName, assignee string) (time.Time, string) {
	if logDir == "" || assignee == "" {
		return time.Time{}, "none (no assignee)"
	}
	logPath := filepath.Join(logDir, repoName+"-"+assignee+".log")
	info, err := os.Stat(logPath)
	if err != nil {
		return time.Time{}, "none (log not found)"
	}
	t := info.ModTime()
	return t, t.Format(time.RFC3339)
}

// latestTime returns the most recent non-zero time among the provided values.
// Returns a zero time if all inputs are zero.
func latestTime(times ...time.Time) time.Time {
	var latest time.Time
	for _, t := range times {
		if t.After(latest) {
			latest = t
		}
	}
	return latest
}

// resolveSessionLogRoot returns the effective session log directory.
// Uses s.sessionLogRoot if set; otherwise derives ~/.cistern/session-logs.
func (s *Castellarius) resolveSessionLogRoot() string {
	if s.sessionLogRoot != "" {
		return s.sessionLogRoot
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cistern", "session-logs")
}

// isTmuxAliveFn is a variable so tests can substitute a fake implementation
// without requiring a real tmux server.
var isTmuxAliveFn = func(sessionID string) bool {
	return exec.Command("tmux", "has-session", "-t", sessionID).Run() == nil
}

// isTmuxAlive returns true if a tmux session with the given name is running.
func isTmuxAlive(sessionID string) bool {
	return isTmuxAliveFn(sessionID)
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
	return prepareDropletWorktreeWithLogger(slog.Default(), primaryDir, sandboxRoot, repoName, dropletID)
}

// prepareDropletWorktreeWithLogger is the logger-parameterized implementation
// of prepareDropletWorktree, used directly in tests.
func prepareDropletWorktreeWithLogger(logger *slog.Logger, primaryDir, sandboxRoot, repoName, dropletID string) (string, error) {
	worktreePath := filepath.Join(sandboxRoot, repoName, dropletID)
	branch := "feat/" + dropletID
	t0 := time.Now()

	if _, err := os.Stat(worktreePath); err == nil {
		// Worktree exists — resume by checking out the branch, then hard-reset
		// to guarantee a clean state. Any uncommitted changes from prior manual
		// work or prior cataractae are discarded — agents must commit their work.

		// Abort any in-progress rebase or merge left by a prior interrupted
		// dispatch (e.g. Castellarius restart, timeout). Both commands exit
		// non-zero when nothing is in progress — errors are ignored.
		abortRebase := exec.Command("git", "rebase", "--abort")
		abortRebase.Dir = worktreePath
		_ = abortRebase.Run()
		abortMerge := exec.Command("git", "merge", "--abort")
		abortMerge.Dir = worktreePath
		_ = abortMerge.Run()

		logger.Info("git checkout",
			"path", worktreePath, "branch", branch)
		checkout := exec.Command("git", "checkout", branch)
		checkout.Dir = worktreePath
		if out, err := checkout.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git checkout %s in %s: %w: %s", branch, worktreePath, err, out)
		}
		reset := exec.Command("git", "reset", "--hard", "HEAD")
		reset.Dir = worktreePath
		if out, err := reset.CombinedOutput(); err != nil {
			logger.Warn("git reset --hard HEAD failed", "path", worktreePath, "error", err, "output", string(out))
		}
		clean := exec.Command("git", "clean", "-fd")
		clean.Dir = worktreePath
		_ = clean.Run()

		logger.Info("worktree resumed",
			"droplet", dropletID, "path", worktreePath,
			"duration", time.Since(t0).Round(time.Millisecond).String())
		return worktreePath, nil
	}

	// Fetch latest before creating.
	logger.Info("git fetch", "dir", primaryDir)
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

	logger.Info("worktree created",
		"droplet", dropletID, "path", worktreePath,
		"duration", time.Since(t0).Round(time.Millisecond).String())
	return worktreePath, nil
}

// removeDropletWorktree removes the per-droplet worktree directory,
// unregisters it from git, and deletes the feature branch from the primary
// clone. Errors are ignored — best-effort cleanup.
func removeDropletWorktree(primaryDir, sandboxRoot, repoName, dropletID string) {
	removeDropletWorktreeWithLogger(slog.Default(), primaryDir, sandboxRoot, repoName, dropletID)
}

// removeDropletWorktreeWithLogger is the logger-parameterized implementation
// of removeDropletWorktree, used directly in tests.
func removeDropletWorktreeWithLogger(logger *slog.Logger, primaryDir, sandboxRoot, repoName, dropletID string) {
	worktreePath := filepath.Join(sandboxRoot, repoName, dropletID)
	rm := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	rm.Dir = primaryDir
	rmErr := rm.Run()

	del := exec.Command("git", "branch", "-D", "feat/"+dropletID)
	del.Dir = primaryDir
	_ = del.Run()

	if rmErr != nil {
		logger.Warn("worktree deletion failed", "droplet", dropletID, "path", worktreePath, "error", rmErr)
	} else {
		logger.Info("worktree deleted", "droplet", dropletID, "path", worktreePath)
	}
}

// dirtyNonContextFiles returns uncommitted non-CONTEXT.md files in dir.
// An empty slice means the worktree is clean for dispatch.
// An error is returned when git status itself fails (non-git dir, disk error,
// permissions) — callers must treat this as an unknown dirty state and
// recirculate conservatively rather than proceeding to spawn.
func dirtyNonContextFiles(dir string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status in %s: %w", dir, err)
	}
	var dirty []string
	for _, line := range strings.Split(string(out), "\n") {
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
	return dirty, nil
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

// parkWorktree detaches HEAD in a worker's sandbox so the feature branch is
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
		for _, identity := range wf.UniqueIdentities() {
			if seen[identity] {
				continue
			}
			seen[identity] = true
			claudePath := filepath.Join(cataractaeDir, identity, "CLAUDE.md")
			content, err := os.ReadFile(claudePath)
			if err != nil || !strings.Contains(string(content), sentinel) {
				s.logger.Warn("CLAUDE.md missing or corrupt — will regenerate",
					"identity", identity, "path", claudePath)
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
