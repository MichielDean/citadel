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
	"sync/atomic"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/proc"
)

const (
	// stallNotePrefix is the structured prefix for all scheduler stall notes.
	// It allows stall notes to be identified and filtered programmatically.
	stallNotePrefix = "[scheduler:stall]"
)

// CisternClient is the interface for interacting with the work cistern.
// *cistern.Client satisfies this interface.
type CisternClient interface {
	GetReady(repo string) (*cistern.Droplet, error)
	Assign(id, worker, step string) error
	Get(id string) (*cistern.Droplet, error)

	AddNote(id, step, content string) error
	GetNotes(id string) ([]cistern.CataractaeNote, error)
	Pool(id, reason string) error
	CloseItem(id string) error
	List(repo, status string) ([]*cistern.Droplet, error)
	Purge(olderThan time.Duration, dryRun bool) (int, error)
	SetCataractae(id, cataractae string) error
	// SetOutcome records the agent outcome on a droplet. Used by stuck-delivery
	// recovery to write an outcome directly so the observe phase can route it.
	SetOutcome(id, outcome string) error
	// ListIssues returns issues for a droplet. If openOnly is true only open
	// issues are returned; if flaggedBy is non-empty only matching issues are
	// returned.
	ListIssues(dropletID string, openOnly bool, flaggedBy string) ([]cistern.DropletIssue, error)
	// SetAssignedAqueduct records the aqueduct operator currently holding this
	// droplet. Called after each dispatch so in_progress droplets always carry a
	// non-empty assigned_aqueduct for status display.
	SetAssignedAqueduct(id, aqueductName string) error
	// Cancel marks a droplet as cancelled. Used by the Architecti for
	// irrecoverable droplets.
	Cancel(id, reason string) error
	// FileDroplet creates a new droplet in the given repo. Used by the Architecti
	// to file structural/code fix work items.
	FileDroplet(repo, title, description string, priority, complexity int) (*cistern.Droplet, error)
	// Heartbeat records the current time as the agent's most recent activity
	// timestamp. Called by agents via `ct droplet heartbeat <id>` every 60 seconds.
	Heartbeat(id string) error
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
	config       aqueduct.AqueductConfig
	workflows    map[string]*aqueduct.Workflow
	clients      map[string]CisternClient
	pools        map[string]*AqueductPool
	runner       CataractaeRunner
	logger       *slog.Logger
	pollInterval time.Duration
	// heartbeatInterval controls how often orphaned in-progress droplets are
	// checked. Independent of pollInterval so it fires even when the main tick
	// is busy. Defaults to 30s.
	heartbeatInterval  time.Duration
	sandboxRoot        string
	cleanupInterval    time.Duration
	dbPath             string
	wasDrought         bool
	startupBinaryMtime time.Time     // mtime of the binary at startup; used to detect updates
	cfgPath            string        // path to cistern.yaml; used to detect config-file updates
	startupCfgMtime    time.Time     // mtime of cistern.yaml at startup; used to detect updates
	supervised         bool          // true if managed by systemd/supervisord/etc.
	reloadCh           chan struct{} // signals Tick() to hot-reload workflows from disk

	// drainTimeout is the maximum duration to wait for in-flight sessions to
	// signal an outcome after SIGTERM. Defaults to 5 minutes.
	drainTimeout time.Duration

	// droughtRunning and droughtStartedAt are written by the drought goroutine
	// (via OnDroughtStart/OnDroughtEnd callbacks) and read by writeHealthFile on
	// the main tick goroutine. atomic fields ensure safe concurrent access.
	droughtRunning   atomic.Bool
	droughtStartedAt atomic.Pointer[time.Time]
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
		config:                 config,
		workflows:              make(map[string]*aqueduct.Workflow),
		clients:                make(map[string]CisternClient),
		pools:                  make(map[string]*AqueductPool),
		runner:                 runner,
		logger:                 slog.Default(),
		pollInterval:           10 * time.Second,
		heartbeatInterval:      30 * time.Second,
		drainTimeout:           5 * time.Minute,
		dbPath:                 dbPath,
		startupBinaryMtime:     startupBinaryMtime,
		supervised:             isSupervisedProcess(),
		reloadCh:               make(chan struct{}, 1),
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
		config:                 config,
		workflows:              workflows,
		clients:                clients,
		pools:                  make(map[string]*AqueductPool),
		runner:                 runner,
		logger:                 slog.Default(),
		pollInterval:           10 * time.Second,
		heartbeatInterval:      30 * time.Second,
		drainTimeout:           5 * time.Minute,
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
					s.checkHungDrought()
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
	drainStart := time.Now()

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

	// Use the time remaining from the shared drainTimeout budget rather than
	// starting a fresh drainTimeout for the session drain.  Clamp to a minimum
	// of 1s so sessions always get a fair chance to signal an outcome.
	remaining := s.drainTimeout - time.Since(drainStart)
	if remaining < time.Second {
		remaining = time.Second
	}
	deadline := time.NewTimer(remaining)
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

// purgeOldItems deletes closed/pooled items older than retention_days across all repos.
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
				OnDroughtStart: func(t time.Time) {
					s.droughtRunning.Store(true)
					s.droughtStartedAt.Store(&t)
				},
				OnDroughtEnd: func() {
					s.droughtRunning.Store(false)
					s.droughtStartedAt.Store(nil)
				},
			})
		}
	}
	s.wasDrought = isDrought

	s.writeHealthFile()
}

// observeRepo routes all in_progress items that have signaled an outcome.
// Agents write outcomes via `ct droplet pass/recirculate/pool <id>`, which sets
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
		// Called at terminal states and no-route pool — non-terminal routes keep
		// the worktree so the next dispatch cycle can resume incrementally.
		// keepBranch=true preserves the feature branch ref (stagnant/blocked/pooled);
		// keepBranch=false deletes it (done/cancelled).
		cleanupBranch := func(keepBranch bool) {
			if s.sandboxRoot != "" {
				primaryDir := filepath.Join(s.sandboxRoot, repo.Name, "_primary")
				removeDropletWorktree(primaryDir, s.sandboxRoot, repo.Name, item.ID, keepBranch)
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
			s.logger.Info("Droplet pooled at cataractae", "droplet", item.ID, "cataractae", step.Name, "outcome", item.Outcome)
		}

		var next string
		if recirculateTo != "" {
			// Agent specified an explicit target step (e.g. recirculate:implement).
			next = recirculateTo
		} else {
			next = route(*step, result)
		}

		// Reviewer-issue loop recovery: when a non-reviewer step recirculates back to
		// itself while a reviewer-opened issue remains open, detect the loop and break
		// it by routing directly to the reviewer cataractae so it can verify the fix
		// and close the issue.
		if result == ResultRecirculate && next == step.Name {
			issues, issErr := client.ListIssues(item.ID, true, "")
			if issErr != nil {
				s.logger.Warn("observe: list issues failed for loop detection",
					"droplet", item.ID, "error", issErr)
			} else if notes, notesErr := client.GetNotes(item.ID); notesErr != nil {
				s.logger.Warn("observe: get notes failed for loop detection",
					"droplet", item.ID, "error", notesErr)
			} else {
				for _, issue := range issues {
					if issue.FlaggedBy == step.Name {
						continue // skip issues filed by the current step
					}
					if lookupCataracta(wf, issue.FlaggedBy) == nil {
						continue // reviewer step not in workflow
					}
					pendingCount := loopRecoveryPendingCount(notes, issue.ID)
					if pendingCount >= loopDetectN-1 {
						// Loop confirmed — route to the reviewer cataractae.
						recoveryNote := fmt.Sprintf(
							"[scheduler:loop-recovery] detected %s→%s loop on reviewer issue %s — routing to reviewer",
							step.Name, step.Name, issue.ID,
						)
						s.logger.Info("observe: loop recovery — routing to reviewer",
							"droplet", item.ID, "step", step.Name,
							"reviewer", issue.FlaggedBy, "issue", issue.ID)
						s.addNote(client, item.ID, "scheduler", recoveryNote)
						next = issue.FlaggedBy
						break
					}
					// First (or sub-threshold) recirculation with open reviewer issue:
					// record a pending marker so the next cycle can confirm the loop.
					pendingNote := fmt.Sprintf(
						"[scheduler:loop-recovery-pending] issue=%s — open reviewer issue found at %s, routing back to %s (cycle %d/%d)",
						issue.ID, step.Name, step.Name, pendingCount+1, loopDetectN,
					)
					s.addNote(client, item.ID, "scheduler", pendingNote)
					break // handle one qualifying issue per observation
				}
			}
		}

		// When a step signals recirculate but has no on_recirculate route, either
		// auto-promote to pass (routing via on_pass) or pool with a diagnostic note.
		if next == "" && result == ResultRecirculate {
			if step.OnPass != "" {
				// Auto-promote: treat recirculate as pass and route via on_pass.
				note := fmt.Sprintf(
					"[scheduler:routing] Auto-promoted: cataractae=%s signaled recirculate but has no on_recirculate route — routing via on_pass to %s",
					step.Name, step.OnPass,
				)
				s.logger.Warn("observe: recirculate auto-promoted to pass via on_pass",
					"droplet", item.ID, "step", step.Name, "next", step.OnPass)
				s.addNote(client, item.ID, "scheduler", note)
				next = step.OnPass
			} else {
				// No on_recirculate and no on_pass: pool the droplet with a diagnostic note.
				note := fmt.Sprintf(
					"[scheduler:routing] cataractae=%s signaled recirculate but has no on_recirculate route and no on_pass route — droplet pooled",
					step.Name,
				)
				s.logger.Warn("observe: recirculate with no on_recirculate or on_pass route — pooling",
					"droplet", item.ID, "step", step.Name)
				s.addNote(client, item.ID, "scheduler", note)
				cleanupBranch(true)
				if err := client.Pool(item.ID, note); err != nil {
					s.logger.Error("observe: pool (no route) failed", "droplet", item.ID, "error", err)
				}
				continue
			}
		}

		if next == "" {
			reason := fmt.Sprintf("no route from step %q for outcome %q", step.Name, item.Outcome)
			s.logger.Warn("observe: no route", "droplet", item.ID)
			cleanupBranch(true)
			if err := client.Pool(item.ID, reason); err != nil {
				s.logger.Error("observe: pool failed", "droplet", item.ID, "error", err)
			}
			continue
		}

		// For critical droplets, insert a human gate before delivery.
		if wf.Complexity.RequireHumanForLevel(item.Complexity) && next == "delivery" {
			next = "human"
		}

		if isTerminal(next) {
			keepBranch := strings.ToLower(next) != "done"
			cleanupBranch(keepBranch)
			s.handleTerminal(client, item.ID, next, step.Name)
			continue
		}

		// Advance item to next step (open for the next dispatch cycle).
		// The feature branch is kept so the next cycle can resume incrementally.
		if err := client.Assign(item.ID, "", next); err != nil {
			s.logger.Error("observe: advance step failed", "droplet", item.ID, "next", next, "error", err)
		}
	}

	// Secondary: release pool slots for droplets whose status was changed to
	// 'cancelled' or 'pooled' externally while in_progress (i.e., without
	// going through the normal outcome path). This happens when
	// `ct droplet cancel` is called on a running droplet.
	for _, extStatus := range []string{"cancelled", "pooled"} {
		changed, err := client.List(repo.Name, extStatus)
		if err != nil {
			s.logger.Error("observe: list externally-changed failed",
				"repo", repo.Name, "status", extStatus, "error", err)
			continue
		}
		for _, item := range changed {
			if item.Assignee == "" {
				continue
			}
			w := pool.FindByName(item.Assignee)
			if w == nil || w.Status != AqueductFlowing || w.DropletID != item.ID {
				continue
			}
			pool.Release(w)
			if s.sandboxRoot != "" {
				primaryDir := filepath.Join(s.sandboxRoot, repo.Name, "_primary")
				removeDropletWorktree(primaryDir, s.sandboxRoot, repo.Name, item.ID, extStatus == "pooled")
			}
			s.logger.Info("aqueduct freed: droplet changed externally",
				"aqueduct", item.Assignee, "droplet", item.ID, "status", extStatus)
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

		if err := client.SetAssignedAqueduct(item.ID, worker.Name); err != nil {
			s.logger.Warn("SetAssignedAqueduct failed", "droplet", item.ID, "operator", worker.Name, "error", err)
		}

		s.logger.Info("Droplet entering cataractae",
			"droplet", item.ID,
			"operator", worker.Name,
			"cataractae", step.Name,
		)

		req := CataractaeRequest{
			Item:         item,
			Step:         *step,
			Workflow:     wf,
			RepoConfig:   repo,
			AqueductName: worker.Name,
			Notes:        notes,
		}

		w := worker // capture for goroutine
		go func() {
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
					if err2 := client.Assign(req.Item.ID, "", req.Step.Name); err2 != nil {
						s.logger.Error("reset after worktree failure", "droplet", req.Item.ID, "error", err2)
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
				if err2 := client.Assign(req.Item.ID, "", req.Step.Name); err2 != nil {
					s.logger.Error("reset after spawn failure",
						"droplet", req.Item.ID, "error", err2)
				}
				pool.Release(w)
				return
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

// loopDetectN is the number of consecutive recirculate-to-self cycles with an
// open reviewer issue that triggers automatic routing to the reviewer cataractae.
const loopDetectN = 2

// loopRecoveryPendingCount returns the number of [scheduler:loop-recovery-pending]
// notes for the given issue ID previously written by the scheduler.
func loopRecoveryPendingCount(notes []cistern.CataractaeNote, issueID string) int {
	marker := "[scheduler:loop-recovery-pending] issue=" + issueID + " "
	count := 0
	for _, n := range notes {
		if n.CataractaeName == "scheduler" && strings.Contains(n.Content, marker) {
			count++
		}
	}
	return count
}

// parseOutcome parses a DB outcome string into a Result and optional target step.
// "pass"               → (ResultPass, "")
// "recirculate"        → (ResultRecirculate, "")
// "recirculate:impl"   → (ResultRecirculate, "impl")
// "pool"               → (ResultPool, "")
func parseOutcome(outcome string) (Result, string) {
	if strings.HasPrefix(outcome, "recirculate:") {
		return ResultRecirculate, strings.TrimPrefix(outcome, "recirculate:")
	}
	switch outcome {
	case "pass":
		return ResultPass, ""
	case "recirculate":
		return ResultRecirculate, ""
	case "pool":
		return ResultPool, ""
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
	case ResultPool:
		return step.OnPool
	default:
		return step.OnFail
	}
}

// isTerminal returns true if the target is a terminal state.
func isTerminal(name string) bool {
	switch strings.ToLower(name) {
	case "done", "pooled", "human", "pool":
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
	case "pooled", "human", "pool":
		s.logger.Info("Droplet pooled at terminal", "droplet", itemID, "terminal", terminal, "from_cataractae", fromStep)
		reason := fmt.Sprintf("reached terminal %q from cataractae %q", terminal, fromStep)
		if err := client.Pool(itemID, reason); err != nil {
			s.logger.Error("pool at terminal failed", "droplet", itemID, "error", err)
		}
		if strings.ToLower(terminal) == "human" {
			if err := client.SetCataractae(itemID, "human"); err != nil {
				s.logger.Error("set cataractae human failed", "droplet", itemID, "error", err)
			}
		}
	}
}

// recoverInProgress handles items left in_progress after a Castellarius restart.
//
// If an outcome is already written, the first observe tick will route it.
// If the tmux session is still alive, leave the droplet alone — the agent
// will signal its outcome.
// Otherwise, reset to open for re-dispatch. The circuit breaker in
// heartbeatRepo will pool the droplet if it keeps dying.
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
				s.logger.Info("recovery: item has outcome, will be routed on first tick",
					"repo", repo.Name, "droplet", item.ID, "outcome", item.Outcome)
				continue
			}

			if item.Assignee != "" {
				sessionID := repo.Name + "-" + item.Assignee
				if isTmuxAlive(sessionID) {
					s.logger.Info("recovery: session still alive, leaving for agent to signal",
						"repo", repo.Name, "droplet", item.ID, "session", sessionID)
					continue
				}
				if pool := s.pools[repo.Name]; pool != nil {
					if w := pool.FindByName(item.Assignee); w != nil {
						pool.Release(w)
					}
				}
			}

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

// checkHungDrought reads the health file and emits a warning log if the drought
// goroutine has been running for more than 5 minutes without completing. A hung
// drought is invisible otherwise because the goroutine runs in the background.
func (s *Castellarius) checkHungDrought() {
	if s.dbPath == "" {
		return
	}
	hf, err := ReadHealthFile(filepath.Dir(s.dbPath))
	if err != nil {
		return // health file absent — nothing to check
	}
	if !hf.DroughtRunning || hf.DroughtStartedAt == nil {
		return
	}
	elapsed := time.Since(*hf.DroughtStartedAt)
	if elapsed > 5*time.Minute {
		s.logger.Warn("drought goroutine may be hung",
			"started_at", hf.DroughtStartedAt.Format(time.RFC3339),
			"elapsed", elapsed.Round(time.Second).String(),
		)
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

	for _, item := range items {
		if item.Outcome != "" {
			continue
		}

		// Dead session detection: tmux dead or agent process dead.
		// Reset to open for re-dispatch. The circuit breaker below
		// will pool the droplet if it keeps dying.
		zombieGuard := 4 * s.heartbeatInterval
		if item.Assignee != "" {
			sessionID := repo.Name + "-" + item.Assignee
			dispatchedAt := item.StageDispatchedAt
			if dispatchedAt.IsZero() {
				dispatchedAt = item.UpdatedAt
			}
			if time.Since(dispatchedAt) < zombieGuard {
				continue
			}

			dead := false
			tmuxDead := false
			if !isTmuxAlive(sessionID) {
				dead = true
				tmuxDead = true
				s.logger.Info("heartbeat: tmux dead — resetting to open",
					"repo", repo.Name, "droplet", item.ID,
					"assignee", item.Assignee, "session_age", time.Since(dispatchedAt).Round(time.Second).String())
			} else if !isAgentAlive(sessionID) {
				dead = true
				exec.Command("tmux", "kill-session", "-t", sessionID).Run()
				s.logger.Info("heartbeat: agent dead — killing session, resetting to open",
					"repo", repo.Name, "droplet", item.ID,
					"assignee", item.Assignee, "session", sessionID)
			}

			if dead {
				// Re-read the item from the DB before resetting. The agent may
				// have called ct droplet pass/recirculate/pool between when we
				// fetched the list and now. If an outcome exists, the observe
				// cycle will handle it. Even if the outcome was already consumed
				// by the observe cycle (which clears it via Assign), we can detect
				// that by checking if the cataractae stage advanced — if so, the
				// droplet has already moved on and we must not reset it.
				fresh, err := client.Get(item.ID)
				if err == nil {
					if fresh.Outcome != "" {
						s.logger.Info("heartbeat: dead session but outcome already written — skipping reset",
							"repo", repo.Name, "droplet", item.ID, "outcome", fresh.Outcome)
						continue
					}
					if fresh.CurrentCataractae != item.CurrentCataractae {
						s.logger.Info("heartbeat: dead session but cataractae already advanced — skipping reset",
							"repo", repo.Name, "droplet", item.ID,
							"was", item.CurrentCataractae, "now", fresh.CurrentCataractae)
						continue
					}
				}

				step := currentCataracta(item, wf)
				if step == nil {
					s.logger.Error("heartbeat: no step for dead session — skipping",
						"repo", repo.Name, "droplet", item.ID)
					continue
				}
				if pool := s.pools[repo.Name]; pool != nil {
					if w := pool.FindByName(item.Assignee); w != nil {
						pool.Release(w)
					}
				}
				var noteMsg string
				if tmuxDead {
					noteMsg = fmt.Sprintf("[scheduler:zombie] Session %s died without outcome (worker=%s, cataractae=%s). [%s]",
						sessionID, item.Assignee, step.Name, time.Now().UTC().Format(time.RFC3339))
				} else {
					noteMsg = fmt.Sprintf("Session killed. Re-dispatching (worker=%s, cataractae=%s). [%s]",
						item.Assignee, step.Name, time.Now().UTC().Format(time.RFC3339))
				}
				s.addNote(client, item.ID, "scheduler", noteMsg)
				if err := client.Assign(item.ID, "", step.Name); err != nil {
					s.logger.Error("heartbeat: reset failed", "droplet", item.ID, "error", err)
				}
				continue
			}
		}

		// Stall detection: agent heartbeat older than threshold.
		stallSig := item.LastHeartbeatAt
		if stallSig.IsZero() {
			stallSig = item.UpdatedAt
		}
		if time.Since(stallSig) <= threshold {
			continue
		}

		// Stalled — write an escalation note.
		heartbeatStatus := "none"
		if !item.LastHeartbeatAt.IsZero() {
			heartbeatStatus = item.LastHeartbeatAt.UTC().Format(time.RFC3339)
		}
		note := fmt.Sprintf("%s elapsed=%s heartbeat=%s",
			stallNotePrefix, formatStallDuration(time.Since(stallSig)), heartbeatStatus)
		s.addNote(client, item.ID, "scheduler", note)
		s.logger.Warn("heartbeat: stall detected",
			"repo", repo.Name, "droplet", item.ID,
			"cataractae", item.CurrentCataractae,
			"stall_duration", time.Since(stallSig).Round(time.Second).String(),
			"threshold", threshold.String())

		// Orphan: no assignee, no session. Reset to open.
		if item.Assignee == "" {
			stepName := item.CurrentCataractae
			if stepName == "" {
				if step := currentCataracta(item, wf); step != nil {
					stepName = step.Name
				}
			}
			s.addNote(client, item.ID, "scheduler",
				fmt.Sprintf("[scheduler:recovery] Orphan reset to open (cataractae=%s).", stepName))
			s.logger.Info("heartbeat: orphan reset to open",
				"repo", repo.Name, "droplet", item.ID, "cataractae", stepName)
			if err := client.Assign(item.ID, "", stepName); err != nil {
				s.logger.Error("heartbeat: orphan reset failed", "droplet", item.ID, "error", err)
			}
		}
	}

	// Circuit breaker: pool droplets that have been dispatched too many
	// times without producing an outcome. This is the only path that
	// pools — everything else resets to open for re-dispatch.
	s.circuitBreaker(repo, items)
}

// stallThresholdDuration returns the configured stall threshold, defaulting to 45 minutes.
func stallThresholdDuration(cfg aqueduct.AqueductConfig) time.Duration {
	if cfg.StallThresholdMinutes > 0 {
		return time.Duration(cfg.StallThresholdMinutes) * time.Minute
	}
	return 45 * time.Minute
}

// formatStallDuration formats a stall duration as a compact human-readable string
// for use in structured stall note fields (e.g. "45m", "2h", "2h30m").
func formatStallDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "0m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, minutes)
}

// circuitBreakerMaxDispatches is the maximum number of dispatch attempts within
// circuitBreakerWindow that a droplet can have without producing an outcome
// before being pooled. This is the sole mechanism that transitions a droplet
// to pooled — everything else resets to open.
const (
	circuitBreakerMaxDispatches = 5
	circuitBreakerWindow        = 15 * time.Minute
)

// circuitBreaker checks in-progress droplets for a tight respawn loop:
// a droplet that has been dispatched many times within a short window
// without ever producing an outcome. If detected, the droplet is pooled
// to stop the token burn. This is the only path that pools.
func (s *Castellarius) circuitBreaker(repo aqueduct.RepoConfig, items []*cistern.Droplet) {
	client := s.clients[repo.Name]

	for _, item := range items {
		if item.Outcome != "" {
			continue
		}
		if item.StageDispatchedAt.IsZero() {
			continue
		}
		// Only check items that have been around long enough to accumulate cycles.
		if time.Since(item.StageDispatchedAt) < circuitBreakerWindow {
			continue
		}

		notes, err := client.GetNotes(item.ID)
		if err != nil {
			continue
		}

		cutoff := time.Now().Add(-circuitBreakerWindow)
		dispatchCount := 0
		for _, n := range notes {
			if n.CataractaeName == "scheduler" &&
				strings.Contains(n.Content, "Session died without outcome") &&
				n.CreatedAt.After(cutoff) {
				dispatchCount++
			}
		}

		if dispatchCount >= circuitBreakerMaxDispatches {
			reason := fmt.Sprintf("[circuit-breaker] %d dead sessions in %s with no outcome — pooling",
				dispatchCount, circuitBreakerWindow)
			s.logger.Warn("circuit breaker: pooling droplet",
				"repo", repo.Name, "droplet", item.ID,
				"dead_sessions", dispatchCount, "window", circuitBreakerWindow)
			s.addNote(client, item.ID, "scheduler", reason)

			// Release the pool slot.
			if item.Assignee != "" {
				if pool := s.pools[repo.Name]; pool != nil {
					if w := pool.FindByName(item.Assignee); w != nil {
						pool.Release(w)
					}
				}
			}

			if s.sandboxRoot != "" {
				primaryDir := filepath.Join(s.sandboxRoot, repo.Name, "_primary")
				removeDropletWorktree(primaryDir, s.sandboxRoot, repo.Name, item.ID, true)
			}

			if err := client.Pool(item.ID, reason); err != nil {
				s.logger.Error("circuit breaker: pool failed", "droplet", item.ID, "error", err)
			}
		}
	}
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

// isAgentAliveFn returns true when a claude process is alive inside the tmux
// session. It uses tmux display-message to find the pane root PID and then
// walks /proc to find a claude descendant of that PID.
// Injectable for testing without a real tmux server or process tree.
var isAgentAliveFn = func(sessionID string) bool {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", sessionID, "#{pane_pid}").Output()
	if err != nil {
		return false
	}
	return proc.ClaudeAliveUnderPIDIn(strings.TrimSpace(string(out)), "/proc")
}

// isAgentAlive returns true when the tmux session contains a live claude process.
func isAgentAlive(sessionID string) bool {
	return isAgentAliveFn(sessionID)
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
// and hard-reset — uncommitted changes are discarded, committed work is preserved.
// If new, it first tries `git worktree add <path> feat/<id>` (attach existing branch);
// if that fails (branch does not exist), falls back to
// `git worktree add -b feat/<id> <path> origin/main` to create a fresh branch.
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
		// Worktree exists — resume the branch, preserving any uncommitted work
		// from the prior session. The agent will be told to commit its changes.

		// Abort any in-progress rebase or merge left by a prior interrupted
		// dispatch (e.g. Castellarius restart, timeout). These are corrupted
		// git state, not meaningful uncommitted work.
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

	// First try attaching to an existing branch (handles crash-between-branch-create-and-worktree-add,
	// and resumes stagnant droplets whose worktree was removed but branch preserved).
	addExisting := exec.Command("git", "worktree", "add", worktreePath, branch)
	addExisting.Dir = primaryDir
	freshBranch := false
	if out, err := addExisting.CombinedOutput(); err != nil {
		// Branch doesn't exist yet — create it fresh from origin/main.
		addNew := exec.Command("git", "worktree", "add", "-b", branch, worktreePath, "origin/main")
		addNew.Dir = primaryDir
		if out2, err2 := addNew.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("git worktree add %s in %s: %w: %s", worktreePath, primaryDir, err2, out2)
		}
		_ = out // first attempt output discarded; only the second failure matters
		freshBranch = true
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

	if freshBranch {
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
	}

	logger.Info("worktree created",
		"droplet", dropletID, "path", worktreePath,
		"duration", time.Since(t0).Round(time.Millisecond).String())
	return worktreePath, nil
}

// removeDropletWorktree removes the per-droplet worktree directory,
// unregisters it from git, and (when keepBranch is false) deletes the feature
// branch from the primary clone. Errors are ignored — best-effort cleanup.
// keepBranch=true preserves the branch ref for stagnant/blocked/pooled droplets
// so they can resume; keepBranch=false deletes it for done/cancelled droplets.
func removeDropletWorktree(primaryDir, sandboxRoot, repoName, dropletID string, keepBranch bool) {
	removeDropletWorktreeWithLogger(slog.Default(), primaryDir, sandboxRoot, repoName, dropletID, keepBranch)
}

// removeDropletWorktreeWithLogger is the logger-parameterized implementation
// of removeDropletWorktree, used directly in tests.
func removeDropletWorktreeWithLogger(logger *slog.Logger, primaryDir, sandboxRoot, repoName, dropletID string, keepBranch bool) {
	worktreePath := filepath.Join(sandboxRoot, repoName, dropletID)
	rm := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	rm.Dir = primaryDir
	rmErr := rm.Run()

	if !keepBranch {
		del := exec.Command("git", "branch", "-D", "feat/"+dropletID)
		del.Dir = primaryDir
		_ = del.Run()
	}

	if rmErr != nil {
		logger.Warn("worktree deletion failed", "droplet", dropletID, "path", worktreePath, "error", rmErr)
	} else {
		logger.Info("worktree deleted", "droplet", dropletID, "path", worktreePath)
	}
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
