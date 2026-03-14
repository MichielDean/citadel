// Package scheduler implements the core scheduling loop for bullet farm.
//
// It polls the work queue for each configured repo, assigns work to
// idle named workers, runs workflow steps via an injected StepRunner, reads
// outcomes, and routes to the next step via deterministic workflow rules.
// No AI in the scheduler — pure state machine.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichielDean/bullet-farm/internal/queue"
	"github.com/MichielDean/bullet-farm/internal/workflow"
)

// QueueClient is the interface for interacting with the work queue.
// *queue.Client satisfies this interface.
type QueueClient interface {
	GetReady(repo string) (*queue.WorkItem, error)
	Assign(id, worker, step string) error
	IncrementAttempts(id string) (int, error)
	AddNote(id, step, content string) error
	GetNotes(id string) ([]queue.StepNote, error)
	Escalate(id, reason string) error
	CloseItem(id string) error
	List(repo, status string) ([]*queue.WorkItem, error)
}

// StepRunner executes a single workflow step.
// The scheduler calls Run and reads the returned Outcome to decide routing.
// Implementations handle agent spawning, automated commands, etc.
type StepRunner interface {
	Run(ctx context.Context, req StepRequest) (*Outcome, error)
}

// StepRequest contains everything needed to execute a workflow step.
type StepRequest struct {
	Item       *queue.WorkItem
	Step       workflow.WorkflowStep
	Workflow   *workflow.Workflow
	RepoConfig workflow.RepoConfig
	WorkerName string
	Notes      []queue.StepNote // context from previous steps
}

// Scheduler is the core loop that polls for work, assigns it to workers,
// and routes outcomes through workflow steps.
type Scheduler struct {
	config       workflow.FarmConfig
	workflows    map[string]*workflow.Workflow
	clients      map[string]QueueClient
	pools        map[string]*WorkerPool
	runner       StepRunner
	logger       *slog.Logger
	pollInterval time.Duration
	sandboxRoot  string
}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option {
	return func(s *Scheduler) { s.logger = l }
}

// WithPollInterval sets how often the scheduler polls for work.
func WithPollInterval(d time.Duration) Option {
	return func(s *Scheduler) { s.pollInterval = d }
}

// WithSandboxRoot sets the root directory for worker sandboxes.
func WithSandboxRoot(root string) Option {
	return func(s *Scheduler) { s.sandboxRoot = root }
}

// New creates a Scheduler from a FarmConfig.
// Workflows are loaded from each RepoConfig.WorkflowPath.
// Each repo gets its own queue.Client scoped by prefix.
func New(config workflow.FarmConfig, dbPath string, runner StepRunner, opts ...Option) (*Scheduler, error) {
	s := &Scheduler{
		config:       config,
		workflows:    make(map[string]*workflow.Workflow),
		clients:      make(map[string]QueueClient),
		pools:        make(map[string]*WorkerPool),
		runner:       runner,
		logger:       slog.Default(),
		pollInterval: 10 * time.Second,
	}
	for _, o := range opts {
		o(s)
	}

	if s.sandboxRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("scheduler: home dir: %w", err)
		}
		s.sandboxRoot = filepath.Join(home, ".bullet-farm", "sandboxes")
	}

	for _, repo := range config.Repos {
		wf, err := workflow.ParseWorkflow(repo.WorkflowPath)
		if err != nil {
			return nil, fmt.Errorf("load workflow for %s: %w", repo.Name, err)
		}
		s.workflows[repo.Name] = wf

		client, err := queue.New(dbPath, repo.Prefix)
		if err != nil {
			return nil, fmt.Errorf("queue for %s: %w", repo.Name, err)
		}
		s.clients[repo.Name] = client

		names := repo.Names
		if len(names) == 0 {
			names = defaultWorkerNames(repo.Workers)
		}
		s.pools[repo.Name] = NewWorkerPool(repo.Name, names)
	}

	return s, nil
}

// NewFromParts creates a Scheduler with pre-built components (for testing).
func NewFromParts(
	config workflow.FarmConfig,
	workflows map[string]*workflow.Workflow,
	clients map[string]QueueClient,
	runner StepRunner,
	opts ...Option,
) *Scheduler {
	s := &Scheduler{
		config:       config,
		workflows:    workflows,
		clients:      clients,
		pools:        make(map[string]*WorkerPool),
		runner:       runner,
		logger:       slog.Default(),
		pollInterval: 10 * time.Second,
	}
	for _, o := range opts {
		o(s)
	}

	for _, repo := range config.Repos {
		names := repo.Names
		if len(names) == 0 {
			names = defaultWorkerNames(repo.Workers)
		}
		s.pools[repo.Name] = NewWorkerPool(repo.Name, names)
	}

	return s
}

func defaultWorkerNames(n int) []string {
	if n <= 0 {
		n = 1
	}
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("worker-%d", i)
	}
	return names
}

// Run starts the scheduler loop. It blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) error {
	s.logger.Info("scheduler starting",
		"repos", len(s.config.Repos),
		"max_total_workers", s.config.MaxTotalWorkers,
	)

	s.recoverInProgress()

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopping")
			return ctx.Err()
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// Tick runs a single poll cycle across all repos. Exported for testing.
func (s *Scheduler) Tick(ctx context.Context) {
	s.tick(ctx)
}

func (s *Scheduler) tick(ctx context.Context) {
	for _, repo := range s.config.Repos {
		if err := ctx.Err(); err != nil {
			return
		}
		s.tickRepo(ctx, repo)
	}
}

func (s *Scheduler) tickRepo(ctx context.Context, repo workflow.RepoConfig) {
	pool := s.pools[repo.Name]
	client := s.clients[repo.Name]
	wf := s.workflows[repo.Name]

	for {
		worker := pool.IdleWorker()
		if worker == nil {
			return
		}

		if s.totalBusy() >= s.config.MaxTotalWorkers {
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

		step := currentStep(item, wf)
		if step == nil {
			s.logger.Error("no step found", "repo", repo.Name, "item", item.ID)
			return
		}

		pool.Assign(worker, item.ID, step.Name)
		go s.runStep(ctx, worker, pool, item, *step, repo)
	}
}

func (s *Scheduler) totalBusy() int {
	total := 0
	for _, pool := range s.pools {
		total += pool.BusyCount()
	}
	return total
}

// currentStep determines which workflow step a work item is at.
// If the item has a current_step, look up that step.
// Otherwise, start at the first step in the workflow.
func currentStep(item *queue.WorkItem, wf *workflow.Workflow) *workflow.WorkflowStep {
	if item.CurrentStep != "" {
		return lookupStep(wf, item.CurrentStep)
	}
	if len(wf.Steps) > 0 {
		return &wf.Steps[0]
	}
	return nil
}

func lookupStep(wf *workflow.Workflow, name string) *workflow.WorkflowStep {
	for i := range wf.Steps {
		if wf.Steps[i].Name == name {
			return &wf.Steps[i]
		}
	}
	return nil
}

func (s *Scheduler) runStep(
	ctx context.Context,
	worker *Worker,
	pool *WorkerPool,
	item *queue.WorkItem,
	step workflow.WorkflowStep,
	repo workflow.RepoConfig,
) {
	defer pool.Release(worker)

	client := s.clients[repo.Name]
	wf := s.workflows[repo.Name]

	s.logger.Info("step starting",
		"repo", repo.Name,
		"item", item.ID,
		"step", step.Name,
		"worker", worker.Name,
	)

	// Mark item as in-progress with the assigned worker and step.
	if err := client.Assign(item.ID, worker.Name, step.Name); err != nil {
		s.logger.Error("assign failed", "item", item.ID, "error", err)
		return
	}

	// Increment attempts and check retry budget.
	attempts, err := client.IncrementAttempts(item.ID)
	if err != nil {
		s.logger.Error("increment attempts failed", "item", item.ID, "error", err)
		return
	}

	if step.MaxIterations > 0 && attempts > step.MaxIterations {
		reason := fmt.Sprintf("step %q exceeded max iterations (%d)", step.Name, step.MaxIterations)
		s.logger.Warn("escalating", "item", item.ID, "reason", reason)
		if err := client.Escalate(item.ID, reason); err != nil {
			s.logger.Error("escalate failed", "item", item.ID, "error", err)
		}
		return
	}

	// Gather prior notes for context forwarding.
	notes, err := client.GetNotes(item.ID)
	if err != nil {
		s.logger.Error("get notes failed", "item", item.ID, "error", err)
		notes = nil
	}

	req := StepRequest{
		Item:       item,
		Step:       step,
		Workflow:   wf,
		RepoConfig: repo,
		WorkerName: worker.Name,
		Notes:      notes,
	}

	// Apply step timeout.
	stepCtx := ctx
	if step.TimeoutMinutes > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, time.Duration(step.TimeoutMinutes)*time.Minute)
		defer cancel()
	}

	// Execute the step.
	outcome, err := s.runner.Run(stepCtx, req)
	if err != nil {
		// Agent crash or timeout: item stays at current step for requeue.
		s.logger.Error("step execution failed",
			"repo", repo.Name,
			"item", item.ID,
			"step", step.Name,
			"worker", worker.Name,
			"error", err,
		)
		return
	}

	s.logger.Info("step completed",
		"repo", repo.Name,
		"item", item.ID,
		"step", step.Name,
		"result", outcome.Result,
	)

	// Attach notes from this step.
	if outcome.Notes != "" {
		if err := client.AddNote(item.ID, step.Name, outcome.Notes); err != nil {
			s.logger.Error("add note failed", "item", item.ID, "error", err)
		}
	}

	// Persist metadata notes (e.g., pr_url from pr-create) for downstream steps.
	for _, mn := range outcome.MetaNotes {
		if err := client.AddNote(item.ID, step.Name, mn); err != nil {
			s.logger.Error("add meta note failed", "item", item.ID, "error", err)
		}
	}

	// Route to next step.
	next := route(step, outcome.Result)
	if next == "" {
		reason := fmt.Sprintf("no route from step %q for result %q", step.Name, outcome.Result)
		s.logger.Warn("no route", "item", item.ID, "step", step.Name, "result", outcome.Result)
		if err := client.Escalate(item.ID, reason); err != nil {
			s.logger.Error("escalate failed", "item", item.ID, "error", err)
		}
		return
	}

	if isTerminal(next) {
		s.handleTerminal(client, item.ID, next, step.Name)
		return
	}

	// Advance item to next step (open for the next poll cycle).
	if err := client.Assign(item.ID, "", next); err != nil {
		s.logger.Error("advance step failed", "item", item.ID, "next", next, "error", err)
	}
}

// route determines the next step name based on the outcome result.
func route(step workflow.WorkflowStep, result Result) string {
	switch result {
	case ResultPass:
		return step.OnPass
	case ResultFail:
		return step.OnFail
	case ResultRevision:
		return step.OnRevision
	case ResultEscalate:
		return step.OnEscalate
	default:
		return step.OnFail
	}
}

// isTerminal returns true if the target is a terminal state.
func isTerminal(name string) bool {
	switch strings.ToLower(name) {
	case "done", "blocked", "human", "escalate":
		return true
	}
	return false
}

func (s *Scheduler) handleTerminal(client QueueClient, itemID, terminal, fromStep string) {
	s.logger.Info("reached terminal", "item", itemID, "terminal", terminal, "from_step", fromStep)

	switch strings.ToLower(terminal) {
	case "done":
		if err := client.CloseItem(itemID); err != nil {
			s.logger.Error("close failed", "item", itemID, "error", err)
		}
	case "blocked", "human", "escalate":
		reason := fmt.Sprintf("reached terminal %q from step %q", terminal, fromStep)
		if err := client.Escalate(itemID, reason); err != nil {
			s.logger.Error("escalate at terminal failed", "item", itemID, "error", err)
		}
	}
}

// recoverInProgress recovers items left in_progress after a restart.
// For each in_progress item, it checks for an outcome.json in the worker
// sandbox directory. If found, the outcome is processed and the item is
// advanced. If not found, the item is reset to open at its current step.
func (s *Scheduler) recoverInProgress() {
	for _, repo := range s.config.Repos {
		client := s.clients[repo.Name]
		wf := s.workflows[repo.Name]

		items, err := client.List(repo.Name, "in_progress")
		if err != nil {
			s.logger.Error("recovery: list in_progress failed", "repo", repo.Name, "error", err)
			continue
		}

		for _, item := range items {
			step := currentStep(item, wf)
			if step == nil {
				s.logger.Warn("recovery: no step found", "repo", repo.Name, "item", item.ID, "step", item.CurrentStep)
				continue
			}

			// Check for outcome.json in the worker's sandbox directory.
			sandboxDir := filepath.Join(s.sandboxRoot, repo.Name, item.Assignee)
			outcomePath := filepath.Join(sandboxDir, "outcome.json")

			outcome, err := ReadOutcome(outcomePath)
			if err != nil {
				// No outcome found — reset item to open at current step for retry.
				s.logger.Info("recovery: resetting to open",
					"repo", repo.Name,
					"item", item.ID,
					"step", item.CurrentStep,
				)
				if err := client.Assign(item.ID, "", item.CurrentStep); err != nil {
					s.logger.Error("recovery: reset failed", "item", item.ID, "error", err)
				}
				continue
			}

			s.logger.Info("recovery: processing leftover outcome",
				"repo", repo.Name,
				"item", item.ID,
				"step", item.CurrentStep,
				"result", outcome.Result,
			)

			// Attach notes from the recovered outcome.
			if outcome.Notes != "" {
				if err := client.AddNote(item.ID, step.Name, outcome.Notes); err != nil {
					s.logger.Error("recovery: add note failed", "item", item.ID, "error", err)
				}
			}

			// Route to next step.
			next := route(*step, outcome.Result)
			if next == "" {
				reason := fmt.Sprintf("recovery: no route from step %q for result %q", step.Name, outcome.Result)
				s.logger.Warn("recovery: no route", "item", item.ID)
				if err := client.Escalate(item.ID, reason); err != nil {
					s.logger.Error("recovery: escalate failed", "item", item.ID, "error", err)
				}
				continue
			}

			if isTerminal(next) {
				s.handleTerminal(client, item.ID, next, step.Name)
				continue
			}

			if err := client.Assign(item.ID, "", next); err != nil {
				s.logger.Error("recovery: advance failed", "item", item.ID, "next", next, "error", err)
			}
		}
	}
}

// WriteContext writes a CONTEXT.md file with notes from previous steps.
// Call this before spawning the next agent to provide context from prior steps.
func WriteContext(dir string, notes []queue.StepNote) error {
	if len(notes) == 0 {
		return nil
	}

	var b []byte
	b = append(b, "# Context from Previous Steps\n\n"...)
	for _, n := range notes {
		header := n.StepName
		if header == "" {
			header = "unknown"
		}
		b = append(b, fmt.Sprintf("## Step: %s\n\n%s\n\n", header, n.Content)...)
	}

	return os.WriteFile(filepath.Join(dir, "CONTEXT.md"), b, 0o644)
}
