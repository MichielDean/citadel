// Package runner manages named workers, persistent sandboxes, and Claude Code
// sessions for executing workflow steps against work items.
package cataracta

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
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

	workers          []*Worker
	sharedCloneDir   string // ~/.cistern/sandboxes/<repo>/ — single shared git clone
	sandboxBase      string // kept for compat; same as sharedCloneDir
	handoffThreshold int

	mu sync.Mutex
}

// Config holds the parameters for creating a cataracta.
type Config struct {
	Repo             aqueduct.RepoConfig
	Workflow         *aqueduct.Workflow
	CisternClient    *cistern.Client
	SandboxRoot      string // Override for sandbox root dir (default: ~/.cistern/sandboxes)
	HandoffThreshold int    // Token threshold for session handoff (default: 150000)
	SkipInitialClone bool   // Skip the startup clone (for tests with fake repo URLs)
}

// New creates a Runner for the given repo, initializing named workers from the
// config namepool. Workers are assigned names from RepoConfig.Names; if no names
// are configured, workers are numbered worker-0, worker-1, etc.
func New(cfg Config) (*Runner, error) {
	if cfg.Workflow == nil {
		return nil, fmt.Errorf("cataracta: workflow is required")
	}
	if cfg.CisternClient == nil {
		return nil, fmt.Errorf("cataracta: queue client is required")
	}

	sandboxRoot := cfg.SandboxRoot
	if sandboxRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cataracta: home dir: %w", err)
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

	// Ensure the shared clone exists once at startup — workers share it via worktrees.
	// SkipInitialClone is set in tests that use fake repo URLs.
	if !cfg.SkipInitialClone {
		if err := EnsureSharedClone(repoSandboxDir, cfg.Repo.URL); err != nil {
			return nil, fmt.Errorf("cataracta: initial clone for %q: %w", cfg.Repo.Name, err)
		}
	}

	return &Runner{
		repo:             cfg.Repo,
		workflow:         cfg.Workflow,
		queue:            cfg.CisternClient,
		workers:          workers,
		sharedCloneDir:   repoSandboxDir,
		sandboxBase:      repoSandboxDir,
		handoffThreshold: handoff,
	}, nil
}

// initWorkers creates worker structs from the repo config namepool.
func initWorkers(repo aqueduct.RepoConfig, sandboxBase string) ([]*Worker, error) {
	count := repo.Cataractae
	if count <= 0 {
		return nil, fmt.Errorf("cataracta: repo %q has no workers configured", repo.Name)
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
// `ct droplet pass/recirculate/block <id>`, which the Castellarius observe loop
// detects on its next tick.
func (r *Runner) SpawnStep(w *Worker, item *cistern.Droplet, step *aqueduct.WorkflowCataracta) error {
	log.Printf("cataracta: %s/%s: spawning step %q for item %s", r.repo.Name, w.Name, step.Name, item.ID)

	// 1. Fetch latest from remote (shared clone already exists), then ensure this worker's worktree.
	if err := fetchSandbox(r.sharedCloneDir); err != nil {
		log.Printf("cataracta: warning: git fetch failed for %s: %v", r.repo.Name, err)
	}
	if err := EnsureWorktree(w.SandboxDir, r.sharedCloneDir); err != nil {
		return fmt.Errorf("worktree: %w", err)
	}

	// For full_codebase agent steps (implement), position the worktree on the
	// item's persistent feature branch so revision cycles are incremental.
	if step.Context == aqueduct.ContextFullCodebase || step.Context == "" {
		if step.Type == aqueduct.CataractaTypeAgent {
			if err := PrepareBranch(w.SandboxDir, item.ID); err != nil {
				return fmt.Errorf("sandbox branch: %w", err)
			}
		}
	}

	// 2. Prepare context directory and CONTEXT.md.
	notes, err := r.queue.GetNotes(item.ID)
	if err != nil {
		log.Printf("cataracta: warning: could not fetch notes for %s: %v", item.ID, err)
	}

	ctxDir, cleanup, err := PrepareContext(ContextParams{
		Level:      step.Context,
		SandboxDir: w.SandboxDir,
		Item:       item,
		Step:       step,
		Notes:      notes,
	})
	if err != nil {
		return fmt.Errorf("context: %w", err)
	}

	// 3. Install and copy skills into sandbox/.claude/skills/<name>/SKILL.md.
	for _, skill := range step.Skills {
		if err := skills.Install(skill.Name, skill.URL); err != nil {
			log.Printf("cataracta: warning: failed to install skill %q: %v", skill.Name, err)
			continue
		}
		dest := filepath.Join(w.SandboxDir, ".claude", "skills", skill.Name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			log.Printf("cataracta: warning: mkdir skills dir for %q: %v", skill.Name, err)
			continue
		}
		if err := copyFile(skills.CachePath(skill.Name), dest); err != nil {
			log.Printf("cataracta: warning: copy skill %q: %v", skill.Name, err)
		}
	}

	// 4. Spawn Claude Code session in tmux. Returns immediately.
	sess := &Session{
		ID:             w.SessionID,
		WorkDir:        ctxDir,
		Model:          step.Model,
		Identity:       step.Identity,
		TimeoutMinutes: step.TimeoutMinutes,
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

	// Park the worktree so it's not left "in use by another worktree" in the event
	// the agent exits without the scheduler noticing for a long time.
	// Note: ParkWorktree is called on exit from this goroutine context by the
	// caller (adapter/scheduler) after the step is fully complete. We do NOT park
	// here because the agent's session is still running.

	return nil
}

// CataractaByName looks up a workflow step by name.
func (r *Runner) CataractaByName(name string) *aqueduct.WorkflowCataracta {
	for i := range r.workflow.Cataractae {
		if r.workflow.Cataractae[i].Name == name {
			return &r.workflow.Cataractae[i]
		}
	}
	return nil
}

// copyFile copies src to dst, creating dst if it does not exist.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
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
