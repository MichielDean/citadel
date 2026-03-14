// Package runner manages named workers, persistent sandboxes, and Claude Code
// sessions for executing workflow steps against work items.
package runner

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/MichielDean/bullet-farm/internal/queue"
	"github.com/MichielDean/bullet-farm/internal/workflow"
)

// Worker is a named execution slot bound to a persistent sandbox.
type Worker struct {
	Name       string
	Repo       string
	SandboxDir string // ~/.bullet-farm/sandboxes/<repo>/<worker>/
	SessionID  string // tmux session name: <repo>-<worker>
	Busy       bool
}

// Runner manages the worker pool and step execution for a single repo.
type Runner struct {
	repo     workflow.RepoConfig
	workflow *workflow.Workflow
	queue    *queue.Client

	workers          []*Worker
	sandboxBase      string // ~/.bullet-farm/sandboxes/<repo>/
	handoffThreshold int

	mu sync.Mutex
}

// Config holds the parameters for creating a Runner.
type Config struct {
	Repo              workflow.RepoConfig
	Workflow          *workflow.Workflow
	QueueClient       *queue.Client
	SandboxRoot       string // Override for sandbox root dir (default: ~/.bullet-farm/sandboxes)
	HandoffThreshold  int    // Token threshold for session handoff (default: 150000)
}

// New creates a Runner for the given repo, initializing named workers from the
// config namepool. Workers are assigned names from RepoConfig.Names; if no names
// are configured, workers are numbered worker-0, worker-1, etc.
func New(cfg Config) (*Runner, error) {
	if cfg.Workflow == nil {
		return nil, fmt.Errorf("runner: workflow is required")
	}
	if cfg.QueueClient == nil {
		return nil, fmt.Errorf("runner: queue client is required")
	}

	sandboxRoot := cfg.SandboxRoot
	if sandboxRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("runner: home dir: %w", err)
		}
		sandboxRoot = filepath.Join(home, ".bullet-farm", "sandboxes")
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

	return &Runner{
		repo:             cfg.Repo,
		workflow:         cfg.Workflow,
		queue:            cfg.QueueClient,
		workers:          workers,
		sandboxBase:      repoSandboxDir,
		handoffThreshold: handoff,
	}, nil
}

// initWorkers creates worker structs from the repo config namepool.
func initWorkers(repo workflow.RepoConfig, sandboxBase string) ([]*Worker, error) {
	count := repo.Workers
	if count <= 0 {
		return nil, fmt.Errorf("runner: repo %q has no workers configured", repo.Name)
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
func workerName(repo workflow.RepoConfig, i int) string {
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

// RunStep executes a single workflow step for an item on the given worker.
// It prepares the sandbox, builds context, spawns a Claude Code session,
// polls for outcome, and returns the result.
func (r *Runner) RunStep(w *Worker, item *queue.WorkItem, step *workflow.WorkflowStep) (*Outcome, error) {
	log.Printf("runner: %s/%s: step %q for item %s", r.repo.Name, w.Name, step.Name, item.ID)

	// 1. Ensure sandbox is ready (clone or fetch).
	if err := EnsureSandbox(w.SandboxDir, r.repo.URL); err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}

	// For full_codebase agent steps (implement), position the sandbox on the
	// item's persistent feature branch so revision cycles are incremental.
	if step.Context == workflow.ContextFullCodebase || step.Context == "" {
		if step.Type == workflow.StepTypeAgent {
			if err := PrepareBranch(w.SandboxDir, item.ID); err != nil {
				return nil, fmt.Errorf("sandbox branch: %w", err)
			}
		}
	}

	// 2. Prepare context directory and CONTEXT.md.
	notes, err := r.queue.GetNotes(item.ID)
	if err != nil {
		log.Printf("runner: warning: could not fetch notes for %s: %v", item.ID, err)
	}

	ctxDir, cleanup, err := PrepareContext(ContextParams{
		Level:      step.Context,
		SandboxDir: w.SandboxDir,
		Item:       item,
		Step:       step,
		Notes:      notes,
	})
	if err != nil {
		return nil, fmt.Errorf("context: %w", err)
	}
	defer cleanup()

	// 3. Spawn Claude Code session in tmux.
	sess := &Session{
		ID:               w.SessionID,
		WorkDir:          ctxDir,
		Model:            step.Model,
		TimeoutMinutes:   step.TimeoutMinutes,
		HandoffThreshold: r.handoffThreshold,
	}
	outcome, err := sess.Run()
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}

	return outcome, nil
}

// StepByName looks up a workflow step by name.
func (r *Runner) StepByName(name string) *workflow.WorkflowStep {
	for i := range r.workflow.Steps {
		if r.workflow.Steps[i].Name == name {
			return &r.workflow.Steps[i]
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
