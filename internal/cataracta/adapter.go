package cataracta

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichielDean/cistern/internal/gates"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/castellarius"
	"github.com/MichielDean/cistern/internal/aqueduct"
)

// Adapter wraps Runner instances to implement castellarius.CataractaRunner.
type Adapter struct {
	runners      map[string]*Runner // keyed by repo name
	executor     *gates.Executor
	queueClients map[string]*cistern.Client
}

// NewAdapter creates an Adapter with a Runner for each configured repo.
func NewAdapter(configs []aqueduct.RepoConfig, workflows map[string]*aqueduct.Workflow, queueClients map[string]*cistern.Client) (*Adapter, error) {
	runners := make(map[string]*Runner, len(configs))
	for _, repo := range configs {
		wf, ok := workflows[repo.Name]
		if !ok {
			return nil, fmt.Errorf("adapter: no workflow for repo %q", repo.Name)
		}
		client, ok := queueClients[repo.Name]
		if !ok {
			return nil, fmt.Errorf("adapter: no queue client for repo %q", repo.Name)
		}
		r, err := New(Config{
			Repo:          repo,
			Workflow:      wf,
			CisternClient: client,
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
	}, nil
}

// Spawn implements castellarius.CataractaRunner.
// For automated steps, runs synchronously and writes the outcome to the DB.
// For agent steps, spawns a tmux session and returns immediately; the agent
// signals completion by calling `ct droplet pass/recirculate/block <id>`.
func (a *Adapter) Spawn(ctx context.Context, req castellarius.CataractaRequest) error {
	if req.Step.Type == aqueduct.CataractaTypeAutomated {
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
	return r.SpawnStep(worker, req.Item, &step)
}

// spawnAutomated runs an automated (gate) step synchronously, then writes the
// outcome and any metadata notes directly to the DB so the observe phase can
// route the item on the next tick.
func (a *Adapter) spawnAutomated(ctx context.Context, req castellarius.CataractaRequest) error {
	client, ok := a.queueClients[req.RepoConfig.Name]
	if !ok {
		return fmt.Errorf("adapter: no queue client for repo %q", req.RepoConfig.Name)
	}

	home, _ := os.UserHomeDir()
	sandboxDir := filepath.Join(home, ".cistern", "sandboxes", req.RepoConfig.Name, req.AqueductName)
	branch := "feat/" + req.Item.ID

	// Build metadata from prior annotations stored as step notes with "meta:" prefix.
	metadata := make(map[string]any)
	for _, n := range req.Notes {
		if len(n.Content) > 5 && n.Content[:5] == "meta:" {
			kv := n.Content[5:]
			for i := 0; i < len(kv); i++ {
				if kv[i] == '=' {
					metadata[kv[:i]] = kv[i+1:]
					break
				}
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
	if result.Notes != "" {
		if err := client.AddNote(req.Item.ID, req.Step.Name, result.Notes); err != nil {
			// Log but continue — the outcome is more important than the note.
			_ = err
		}
	}
	for k, v := range result.Annotations {
		if err := client.AddNote(req.Item.ID, req.Step.Name, fmt.Sprintf("meta:%s=%s", k, v)); err != nil {
			_ = err
		}
	}

	// Write outcome to DB. The observe phase routes the item on the next tick.
	if err := client.SetOutcome(req.Item.ID, string(result.Result)); err != nil {
		return fmt.Errorf("adapter: set outcome for %s: %w", req.Item.ID, err)
	}
	return nil
}
