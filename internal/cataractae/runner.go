// Package runner manages named workers, persistent sandboxes, and Claude Code
// sessions for executing workflow steps against work items.
package cataractae

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/provider"
	"github.com/MichielDean/cistern/internal/skills"
)

// Worker is a named execution slot with a git worktree.
// All workers for a repo share a single upstream clone; each worker has its
// own worktree at SandboxDir so they can work on independent branches in parallel.
type Worker struct {
	Name       string
	Repo       string
	SandboxDir string // ~/.cistern/sandboxes/<repo>/<worker>/ — git worktree
	SessionID  string // tmux session name: <repo>-<worker>
	Busy       bool
}

// Runner manages the worker pool and step execution for a single repo.
type Runner struct {
	repo     aqueduct.RepoConfig
	workflow *aqueduct.Workflow
	queue    *cistern.Client
	preset   provider.ProviderPreset

	workers          []*Worker
	handoffThreshold int

	mu sync.Mutex
}

// Config holds the parameters for creating a cataractae.
type Config struct {
	Repo             aqueduct.RepoConfig
	Workflow         *aqueduct.Workflow
	CisternClient    *cistern.Client
	SandboxRoot      string                  // Override for sandbox root dir (default: ~/.cistern/sandboxes)
	HandoffThreshold int                     // Token threshold for session handoff (default: 150000)
	SkipInitialClone bool                    // Skip the startup clone (for tests with fake repo URLs)
	Preset           provider.ProviderPreset // Resolved provider preset; zero-value falls back to legacy claude path
}

// New creates a Runner for the given repo, initializing named workers from the
// config namepool. Workers are assigned names from RepoConfig.Names; if no names
// are configured, workers are numbered worker-0, worker-1, etc.
func New(cfg Config) (*Runner, error) {
	if cfg.Workflow == nil {
		return nil, fmt.Errorf("cataractae: workflow is required")
	}
	if cfg.CisternClient == nil {
		return nil, fmt.Errorf("cataractae: queue client is required")
	}

	sandboxRoot := cfg.SandboxRoot
	if sandboxRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cataractae: home dir: %w", err)
		}
		sandboxRoot = filepath.Join(home, ".cistern", "sandboxes")
	}

	repoSandboxDir := filepath.Join(sandboxRoot, cfg.Repo.Name)

	handoff := cfg.HandoffThreshold
	if handoff == 0 {
		handoff = 150000
	}

	workers, err := initWorkers(cfg.Repo, repoSandboxDir)
	if err != nil {
		return nil, err
	}

	// Ensure the primary clone (shared object store) and per-aqueduct worktrees exist.
	// SkipInitialClone is set in tests that use fake repo URLs.
	if !cfg.SkipInitialClone {
		primaryDir := filepath.Join(repoSandboxDir, "_primary")
		if err := EnsurePrimaryClone(primaryDir, cfg.Repo.URL); err != nil {
			return nil, fmt.Errorf("cataractae: primary clone for %q: %w", cfg.Repo.Name, err)
		}
		for _, w := range workers {
			if err := EnsureWorktree(primaryDir, w.SandboxDir); err != nil {
				return nil, fmt.Errorf("cataractae: worktree for %q/%s: %w", cfg.Repo.Name, w.Name, err)
			}
		}
	}

	return &Runner{
		repo:             cfg.Repo,
		workflow:         cfg.Workflow,
		queue:            cfg.CisternClient,
		preset:           cfg.Preset,
		workers:          workers,
		handoffThreshold: handoff,
	}, nil
}

// initWorkers creates worker structs from the repo config namepool.
func initWorkers(repo aqueduct.RepoConfig, sandboxBase string) ([]*Worker, error) {
	count := repo.Cataractae
	if count <= 0 {
		return nil, fmt.Errorf("cataractae: repo %q has no workers configured", repo.Name)
	}

	workers := make([]*Worker, count)
	for i := 0; i < count; i++ {
		name := workerName(repo, i)
		workers[i] = &Worker{
			Name:       name,
			Repo:       repo.Name,
			SandboxDir: filepath.Join(sandboxBase, name),
			SessionID:  repo.Name + "-" + name,
		}
	}
	return workers, nil
}

// workerName returns the name for worker at index i.
// Uses the config namepool if available, otherwise "worker-N".
func workerName(repo aqueduct.RepoConfig, i int) string {
	if i < len(repo.Names) {
		return repo.Names[i]
	}
	return fmt.Sprintf("worker-%d", i)
}

// Claim finds an idle worker and marks it busy. Returns nil if all workers are busy.
func (r *Runner) Claim() *Worker {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.workers {
		if !w.Busy {
			w.Busy = true
			return w
		}
	}
	return nil
}

// Release marks a worker as idle.
func (r *Runner) Release(w *Worker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.Busy = false
}

// IdleCount returns the number of workers not currently running a step.
func (r *Runner) IdleCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, w := range r.workers {
		if !w.Busy {
			n++
		}
	}
	return n
}

// findWorkerByName returns the named worker without changing its state.
func (r *Runner) findWorkerByName(name string) *Worker {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.workers {
		if w.Name == name {
			return w
		}
	}
	return nil
}

// SpawnStep prepares the sandbox and context for a step, then spawns the agent
// session in tmux and returns immediately. The agent signals completion by calling
// `ct droplet pass/recirculate/pool <id>`, which the Castellarius observe loop
// detects on its next tick.
//
// sandboxDirOverride, if non-empty, is used as the sandbox directory instead of
// w.SandboxDir. The Castellarius sets this to the per-droplet worktree path.
func (r *Runner) SpawnStep(w *Worker, item *cistern.Droplet, step *aqueduct.WorkflowCataractae, sandboxDirOverride string) error {
	slog.Default().Info("cataractae: spawning step",
		"repo", r.repo.Name,
		"worker", w.Name,
		"step", step.Name,
		"droplet", item.ID,
	)

	sandboxDir := w.SandboxDir
	if sandboxDirOverride != "" {
		sandboxDir = sandboxDirOverride
	}

	// diff_only steps require the per-droplet worktree path so generateDiff
	// reads the feature branch — not the worker's own sandbox (which is on
	// main and has no changes). The Castellarius always provides this via
	// sandboxDirOverride. If it's missing, fail loudly rather than silently
	// producing an empty diff.patch.
	if step.Context == aqueduct.ContextDiffOnly && sandboxDirOverride == "" {
		return fmt.Errorf("%s step %q: per-droplet SandboxDir not set — Castellarius must provide worktree path", step.Context, step.Name)
	}

	// 1. Prepare context directory and CONTEXT.md.
	// Branch setup is owned by the Castellarius and happens before this call.
	notes, err := r.queue.GetNotes(item.ID)
	if err != nil {
		slog.Default().Warn("cataractae: could not fetch notes", "droplet", item.ID, "error", err)
	}

	openIssues, err := r.queue.ListIssues(item.ID, true, "")
	if err != nil {
		slog.Default().Warn("cataractae: could not fetch open issues", "droplet", item.ID, "error", err)
	}

	ctxDir, cleanup, err := PrepareContext(ContextParams{
		Level:       step.Context,
		SandboxDir:  sandboxDir,
		Item:        item,
		Step:        step,
		Notes:       notes,
		OpenIssues:  openIssues,
		QueueClient: r.queue,
	})
	if err != nil {
		return fmt.Errorf("context: %w", err)
	}

	// 2. Verify skills are installed in ~/.cistern/skills/. Claude reads them
	// directly via --add-dir ~/.cistern/skills (injected in session.go) using
	// the absolute paths written into CONTEXT.md by context.go. No file copying.
	for _, skill := range step.Skills {
		if !skills.IsInstalled(skill.Name) {
			cleanup()
			return fmt.Errorf("cataractae: skill %q not installed — run `ct skills install %s <url>`", skill.Name, skill.Name)
		}
	}

	// 3. Spawn agent session in tmux. Returns immediately.
	modelVal := resolveModelVal(step.Model, r.preset)
	skillNames := make([]string, len(step.Skills))
	for i, sk := range step.Skills {
		skillNames[i] = sk.Name
	}
	sess := &Session{
		ID:             w.SessionID,
		WorkDir:        ctxDir,
		Model:          modelVal,
		Identity:       step.Identity,
		TimeoutMinutes: step.TimeoutMinutes,
		Preset:         r.preset,
		Skills:         skillNames,
		TemplateCtx: aqueduct.TemplateContext{
			Step: aqueduct.BuildStepTemplateContext(r.workflow, step),
			Droplet: aqueduct.DropletTemplateContext{
				ID:          item.ID,
				Title:       item.Title,
				Description: item.Description,
				Complexity:  item.Complexity,
			},
			Pipeline: aqueduct.BuildPipeline(r.workflow),
		},
	}
	if err := sess.Spawn(); err != nil {
		cleanup()
		return fmt.Errorf("session spawn: %w", err)
	}

	// For diff_only/spec_only, ctxDir is a tmpdir. Schedule cleanup once the
	// session dies so the tmpdir is not left behind indefinitely.
	if ctxDir != w.SandboxDir {
		sessionID := w.SessionID
		go func() {
			for {
				time.Sleep(30 * time.Second)
				if exec.Command("tmux", "has-session", "-t", sessionID).Run() != nil {
					break // session is dead
				}
			}
			cleanup()
		}()
	}

	return nil
}

// resolveModelVal returns the model string to pass to the agent. It prefers
// the step-level override (step.Model) and falls back to preset.DefaultModel
// when the step does not specify a model.
func resolveModelVal(stepModel *string, preset provider.ProviderPreset) string {
	if stepModel != nil {
		return *stepModel
	}
	return preset.DefaultModel
}

// CataractaeByName looks up a workflow step by name.
func (r *Runner) CataractaeByName(name string) *aqueduct.WorkflowCataractae {
	for i := range r.workflow.Cataractae {
		if r.workflow.Cataractae[i].Name == name {
			return &r.workflow.Cataractae[i]
		}
	}
	return nil
}

// Workers returns the worker pool (read-only snapshot).
func (r *Runner) Workers() []Worker {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Worker, len(r.workers))
	for i, w := range r.workers {
		out[i] = *w
	}
	return out
}
