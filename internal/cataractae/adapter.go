package cataractae

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/castellarius"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/gates"
)

// Adapter wraps Runner instances to implement castellarius.CataractaeRunner.
type Adapter struct {
	runners      map[string]*Runner // keyed by repo name
	executor     *gates.Executor
	queueClients map[string]*cistern.Client
	logger       *slog.Logger
}

// NewAdapter creates an Adapter with a Runner for each configured repo.
// cfg is used to resolve the provider preset for each repo via ResolveProvider.
func NewAdapter(cfg *aqueduct.AqueductConfig, workflows map[string]*aqueduct.Workflow, queueClients map[string]*cistern.Client) (*Adapter, error) {
	runners := make(map[string]*Runner, len(cfg.Repos))
	for _, repo := range cfg.Repos {
		wf, ok := workflows[repo.Name]
		if !ok {
			return nil, fmt.Errorf("adapter: no workflow for repo %q", repo.Name)
		}
		client, ok := queueClients[repo.Name]
		if !ok {
			return nil, fmt.Errorf("adapter: no queue client for repo %q", repo.Name)
		}
		preset, err := cfg.ResolveProvider(repo.Name)
		if err != nil {
			return nil, fmt.Errorf("adapter: resolve provider for %q: %w", repo.Name, err)
		}
		r, err := New(Config{
			Repo:          repo,
			Workflow:      wf,
			CisternClient: client,
			Preset:        preset,
		})
		if err != nil {
			return nil, fmt.Errorf("adapter: runner for %q: %w", repo.Name, err)
		}
		runners[repo.Name] = r
	}
	return &Adapter{
		runners:      runners,
		executor:     gates.New(),
		queueClients: queueClients,
		logger:       slog.Default(),
	}, nil
}

// Spawn implements castellarius.CataractaeRunner.
// For automated steps, runs synchronously and writes the outcome to the DB.
// For agent steps, spawns a tmux session and returns immediately; the agent
// signals completion by calling `ct droplet pass/recirculate/pool <id>`.
func (a *Adapter) Spawn(ctx context.Context, req castellarius.CataractaeRequest) error {
	if req.Step.Type == aqueduct.CataractaeTypeAutomated {
		return a.spawnAutomated(ctx, req)
	}

	r, ok := a.runners[req.RepoConfig.Name]
	if !ok {
		return fmt.Errorf("adapter: no runner for repo %q", req.RepoConfig.Name)
	}

	worker := r.findWorkerByName(req.AqueductName)
	if worker == nil {
		return fmt.Errorf("adapter: worker %q not found in repo %q", req.AqueductName, req.RepoConfig.Name)
	}

	step := req.Step
	return r.SpawnStep(worker, req.Item, &step, req.SandboxDir)
}

// spawnAutomated runs an automated (gate) step synchronously, then writes the
// outcome and any metadata notes directly to the DB so the observe phase can
// route the item on the next tick.
func (a *Adapter) spawnAutomated(ctx context.Context, req castellarius.CataractaeRequest) error {
	client, ok := a.queueClients[req.RepoConfig.Name]
	if !ok {
		return fmt.Errorf("adapter: no queue client for repo %q", req.RepoConfig.Name)
	}

	// Use per-droplet sandbox if set by Castellarius, otherwise fall back to
	// aqueduct-named sandbox for automated steps (they don't use the worktree directly).
	sandboxDir := req.SandboxDir
	if sandboxDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("adapter: home dir: %w", err)
		}
		sandboxDir = filepath.Join(home, ".cistern", "sandboxes", req.RepoConfig.Name, req.AqueductName)
	}
	branch := "feat/" + req.Item.ID

	// Build metadata from prior annotations stored as step notes with "meta:" prefix.
	metadata := make(map[string]any)
	for _, n := range req.Notes {
		if after, ok := strings.CutPrefix(n.Content, "meta:"); ok {
			if k, v, ok := strings.Cut(after, "="); ok {
				metadata[k] = v
			}
		}
	}

	bc := gates.DropletContext{
		ID:          req.AqueductName + "-" + req.Item.ID,
		Title:       req.Item.Title,
		Description: req.Item.Description,
		WorkDir:     sandboxDir,
		Branch:      branch,
		BaseBranch:  "main",
		Metadata:    metadata,
	}
	result := a.executor.RunStep(ctx, req.Step.Name, bc)

	// Write notes to DB (visible to downstream steps).
	// Errors are logged at WARN — the outcome is more important than the note.
	if result.Notes != "" {
		if err := client.AddNote(req.Item.ID, req.Step.Name, result.Notes); err != nil {
			a.logger.Warn("adapter: AddNote failed", "droplet", req.Item.ID, "step", req.Step.Name, "error", err)
		}
	}
	for k, v := range result.Annotations {
		if err := client.AddNote(req.Item.ID, req.Step.Name, fmt.Sprintf("meta:%s=%s", k, v)); err != nil {
			a.logger.Warn("adapter: AddNote failed", "droplet", req.Item.ID, "step", req.Step.Name, "error", err)
		}
	}

	// Write outcome to DB. The observe phase routes the item on the next tick.
	if err := client.SetOutcome(req.Item.ID, string(result.Result)); err != nil {
		return fmt.Errorf("adapter: set outcome for %s: %w", req.Item.ID, err)
	}
	return nil
}
