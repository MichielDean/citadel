package castellarius

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

const (
	defaultDeliveryTimeoutMinutes = 45
	stuckDeliveryThresholdFactor  = 1.5
	// stuckDeliveryCheckInterval controls how often the stuck-delivery check runs.
	stuckDeliveryCheckInterval = 5 * time.Minute
)

// stuckDeliveryThreshold returns the duration after which a delivery agent is
// considered stuck: 1.5× the delivery step's timeout_minutes (default 45m → 67.5m).
func stuckDeliveryThreshold(wf *aqueduct.Workflow) time.Duration {
	mins := defaultDeliveryTimeoutMinutes
	for _, step := range wf.Cataractae {
		if step.Name == "delivery" && step.TimeoutMinutes > 0 {
			mins = step.TimeoutMinutes
			break
		}
	}
	return time.Duration(float64(mins) * stuckDeliveryThresholdFactor * float64(time.Minute))
}

// checkStuckDeliveries scans all repos for delivery agents that have been running
// past their stuck threshold and recovers them.
func (s *Castellarius) checkStuckDeliveries(ctx context.Context) {
	for _, repo := range s.config.Repos {
		if ctx.Err() != nil {
			return
		}
		s.checkStuckDeliveriesForRepo(ctx, repo)
	}
}

func (s *Castellarius) checkStuckDeliveriesForRepo(ctx context.Context, repo aqueduct.RepoConfig) {
	client := s.clients[repo.Name]
	wf := s.workflows[repo.Name]
	threshold := stuckDeliveryThreshold(wf)

	items, err := client.List(repo.Name, "in_progress")
	if err != nil {
		s.logger.Error("stuck delivery: list in_progress failed", "repo", repo.Name, "error", err)
		return
	}

	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		// Only recover delivery-step items.
		if item.CurrentCataractae != "delivery" {
			continue
		}
		// Outcome already written — observe phase handles it.
		if item.Outcome != "" {
			continue
		}
		// Need an assignee to identify the session.
		if item.Assignee == "" {
			continue
		}
		// Not yet past the stuck threshold.
		if time.Since(item.UpdatedAt) < threshold {
			continue
		}
		// Session is dead — heartbeat already handles orphans.
		sessionID := repo.Name + "-" + item.Assignee
		if !isTmuxAlive(sessionID) {
			continue
		}

		s.logger.Info("stuck delivery detected",
			"droplet", item.ID,
			"repo", repo.Name,
			"assignee", item.Assignee,
			"age", time.Since(item.UpdatedAt).Round(time.Minute),
			"threshold", threshold,
		)
		s.recoverStuckDelivery(ctx, repo, client, item)
	}
}

// recoverStuckDelivery recovers a single stuck delivery droplet.
// It looks up the associated PR, kills the stuck agent session, and escalates
// for human attention. The Castellarius never writes pass — only agents do that.
func (s *Castellarius) recoverStuckDelivery(ctx context.Context, repo aqueduct.RepoConfig, client CisternClient, item *cistern.Droplet) {
	sandboxDir := filepath.Join(s.sandboxRoot, repo.Name, item.ID)
	sessionID := repo.Name + "-" + item.Assignee

	prURL, state, _, err := s.findPRFn(ctx, repo.Name, item.ID, sandboxDir)

	// Kill the stuck agent session — every recovery path needs this.
	if killErr := s.killSessionFn(sessionID); killErr != nil {
		s.logger.Error("stuck delivery: kill session failed", "droplet", item.ID, "error", killErr)
	}

	if err != nil {
		s.logger.Error("stuck delivery: PR lookup failed", "droplet", item.ID, "error", err)
		s.addDeliveryNote(client, item.ID,
			fmt.Sprintf("Stuck delivery: PR lookup failed (%v). Escalated.", err))
		if escErr := client.Escalate(item.ID, fmt.Sprintf("stuck delivery: PR lookup failed: %v", err)); escErr != nil {
			s.logger.Error("stuck delivery: escalate failed", "droplet", item.ID, "error", escErr)
		}
		return
	}

	if prURL == "" {
		s.logger.Warn("stuck delivery: no PR found", "droplet", item.ID)
		s.addDeliveryNote(client, item.ID,
			"Stuck delivery: no PR found for branch. Escalated.")
		if escErr := client.Escalate(item.ID, "stuck delivery: no PR found"); escErr != nil {
			s.logger.Error("stuck delivery: escalate failed", "droplet", item.ID, "error", escErr)
		}
		return
	}

	s.logger.Info("stuck delivery: PR found",
		"droplet", item.ID, "prURL", prURL, "state", state)

	s.logger.Warn("stuck delivery: escalating",
		"droplet", item.ID, "prURL", prURL, "state", state)
	s.addDeliveryNote(client, item.ID,
		fmt.Sprintf("Stuck delivery: PR %s in state %q. Escalated for human attention.", prURL, state))
	if escErr := client.Escalate(item.ID, fmt.Sprintf("stuck delivery: PR %s in state %s", prURL, state)); escErr != nil {
		s.logger.Error("stuck delivery: escalate failed", "droplet", item.ID, "error", escErr)
	}
}

// addDeliveryNote logs a stuck-delivery note on the droplet, warning on failure.
func (s *Castellarius) addDeliveryNote(client CisternClient, dropletID, msg string) {
	if err := client.AddNote(dropletID, "stuck-delivery", msg); err != nil {
		s.logger.Warn("stuck delivery: AddNote failed", "droplet", dropletID, "error", err)
	}
}

// --- Default injectable implementations ---

// defaultFindPR searches GitHub for the PR associated with the droplet's branch
// (feat/<id>). Returns the PR URL, state ("OPEN"/"CLOSED"/"MERGED"), and
// mergeStateStatus ("BEHIND", "BLOCKED", "CLEAN", "DIRTY", "UNKNOWN", "UNSTABLE").
// Returns ("", "", "", nil) when no PR is found.
func defaultFindPR(ctx context.Context, _ string, dropletID, sandboxDir string) (prURL, state, mergeStateStatus string, err error) {
	branch := "feat/" + dropletID
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--head", branch,
		"--state", "all",
		"--json", "url,state,mergeStateStatus",
		"--limit", "1",
	)
	cmd.Dir = sandboxDir
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	out, cmdErr := cmd.Output()
	if cmdErr != nil {
		return "", "", "", fmt.Errorf("gh pr list --head %s: %w: %s", branch, cmdErr, stderrBuf.String())
	}
	var prs []struct {
		URL             string `json:"url"`
		State           string `json:"state"`
		MergeStateStatus string `json:"mergeStateStatus"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return "", "", "", fmt.Errorf("parse gh pr list output: %w", err)
	}
	if len(prs) == 0 {
		return "", "", "", nil
	}
	return prs[0].URL, prs[0].State, prs[0].MergeStateStatus, nil
}

// defaultKillSession sends a kill signal to the named tmux session.
func defaultKillSession(sessionID string) error {
	cmd := exec.Command("tmux", "kill-session", "-t", sessionID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session %s: %w: %s", sessionID, err, out)
	}
	return nil
}
