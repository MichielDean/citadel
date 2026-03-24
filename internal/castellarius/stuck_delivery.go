package castellarius

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
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
// It looks up the associated PR, kills the stuck agent session, and sets an
// appropriate outcome so the next observe tick routes the droplet correctly.
func (s *Castellarius) recoverStuckDelivery(ctx context.Context, repo aqueduct.RepoConfig, client CisternClient, item *cistern.Droplet) {
	sandboxDir := filepath.Join(s.sandboxRoot, repo.Name, item.ID)
	sessionID := repo.Name + "-" + item.Assignee

	prURL, state, mergeStateStatus, err := s.findPRFn(ctx, repo.Name, item.ID, sandboxDir)

	// Kill the stuck agent session — every recovery path needs this.
	if killErr := s.killSessionFn(sessionID); killErr != nil {
		s.logger.Error("stuck delivery: kill session failed", "droplet", item.ID, "error", killErr)
	}

	if err != nil {
		s.logger.Error("stuck delivery: PR lookup failed", "droplet", item.ID, "error", err)
		s.addDeliveryNote(client, item.ID,
			fmt.Sprintf("Stuck delivery: PR lookup failed (%v). Recirculated.", err))
		s.logSetOutcome(client, item.ID, "recirculate", "PR lookup failed")
		return
	}

	if prURL == "" {
		s.logger.Warn("stuck delivery: no PR found", "droplet", item.ID)
		s.addDeliveryNote(client, item.ID,
			"Stuck delivery: no PR found for branch. Recirculated.")
		s.logSetOutcome(client, item.ID, "recirculate", "no PR found")
		return
	}

	s.logger.Info("stuck delivery: PR found",
		"droplet", item.ID, "prURL", prURL, "state", state, "mergeStateStatus", mergeStateStatus)

	switch state {
	case "MERGED":
		s.logger.Info("stuck delivery: PR already merged, signaling pass", "droplet", item.ID)
		s.addDeliveryNote(client, item.ID,
			fmt.Sprintf("Stuck delivery recovered: PR %s was already merged. Signaling pass.", prURL))
		s.logSetOutcome(client, item.ID, "pass", "PR already merged")

	case "CLOSED":
		s.logger.Warn("stuck delivery: PR closed without merge", "droplet", item.ID, "prURL", prURL)
		s.addDeliveryNote(client, item.ID,
			fmt.Sprintf("Stuck delivery: PR %s closed without merging. Recirculated.", prURL))
		s.logSetOutcome(client, item.ID, "recirculate", "PR closed without merge")

	case "OPEN":
		s.recoverOpenPR(ctx, client, item, sandboxDir, prURL, mergeStateStatus)

	default:
		s.logger.Warn("stuck delivery: unexpected PR state", "droplet", item.ID, "state", state)
		s.addDeliveryNote(client, item.ID,
			fmt.Sprintf("Stuck delivery: unexpected PR state %q. Recirculated.", state))
		s.logSetOutcome(client, item.ID, "recirculate", fmt.Sprintf("unexpected PR state: %s", state))
	}
}

// recoverOpenPR handles recovery for a stuck delivery where the PR is still OPEN.
// Dispatches to the appropriate recovery path based on mergeStateStatus.
func (s *Castellarius) recoverOpenPR(ctx context.Context, client CisternClient, item *cistern.Droplet, sandboxDir, prURL, mergeStateStatus string) {
	switch mergeStateStatus {
	case "BEHIND":
		// Branch is behind main — rebase, push, then enable auto-merge and signal pass.
		if err := s.rebaseAndPushFn(ctx, sandboxDir); err != nil {
			s.logger.Error("stuck delivery: rebase failed", "droplet", item.ID, "error", err)
			s.addDeliveryNote(client, item.ID,
				fmt.Sprintf("Stuck delivery: branch behind main, rebase failed (%v). Recirculated.", err))
			s.logSetOutcome(client, item.ID, "recirculate", fmt.Sprintf("rebase failed: %v", err))
			return
		}
		// Enable auto-merge (best-effort — failure does not block pass).
		if autoErr := s.ghMergeFn(ctx, sandboxDir, prURL, true); autoErr != nil {
			s.logger.Warn("stuck delivery: auto-merge enable failed after rebase",
				"droplet", item.ID, "error", autoErr)
		}
		s.addDeliveryNote(client, item.ID,
			fmt.Sprintf("Stuck delivery recovered: branch behind main. Rebased, pushed, enabled auto-merge on %s. Signaling pass.", prURL))
		s.logSetOutcome(client, item.ID, "pass", "rebased and auto-merge enabled")
		s.logger.Info("stuck delivery: recovered via rebase+auto-merge", "droplet", item.ID, "prURL", prURL)

	case "BLOCKED", "UNSTABLE":
		// CI checks are failing — recirculate so the agent can fix them.
		s.addDeliveryNote(client, item.ID,
			fmt.Sprintf("Stuck delivery: PR %s has failing CI (mergeStateStatus=%s). Recirculated.", prURL, mergeStateStatus))
		s.logSetOutcome(client, item.ID, "recirculate",
			fmt.Sprintf("CI failing: mergeStateStatus=%s", mergeStateStatus))
		s.logger.Info("stuck delivery: recirculated due to CI failure",
			"droplet", item.ID, "mergeStateStatus", mergeStateStatus)

	case "CLEAN":
		// All checks pass, branch up-to-date — try direct merge, then auto-merge.
		err := s.ghMergeFn(ctx, sandboxDir, prURL, false)
		if err == nil {
			s.addDeliveryNote(client, item.ID,
				fmt.Sprintf("Stuck delivery recovered: merged PR %s directly.", prURL))
			s.logSetOutcome(client, item.ID, "pass", "direct merge succeeded")
			s.logger.Info("stuck delivery: direct merge succeeded", "droplet", item.ID, "prURL", prURL)
			return
		}
		s.logger.Warn("stuck delivery: direct merge failed, trying auto-merge",
			"droplet", item.ID, "directErr", err)
		autoErr := s.ghMergeFn(ctx, sandboxDir, prURL, true)
		if autoErr != nil {
			s.addDeliveryNote(client, item.ID,
				fmt.Sprintf("Stuck delivery: all merge attempts failed. auto-merge error: %v. Recirculated.", autoErr))
			s.logSetOutcome(client, item.ID, "recirculate",
				fmt.Sprintf("all merge attempts failed: %v", autoErr))
			s.logger.Warn("stuck delivery: all merge attempts failed",
				"droplet", item.ID, "directErr", err, "autoErr", autoErr)
			return
		}
		s.addDeliveryNote(client, item.ID,
			fmt.Sprintf("Stuck delivery recovered: enabled auto-merge on PR %s.", prURL))
		s.logSetOutcome(client, item.ID, "pass", "auto-merge enabled")
		s.logger.Info("stuck delivery: auto-merge enabled (direct merge failed)",
			"droplet", item.ID, "prURL", prURL)

	default:
		// DIRTY, UNKNOWN, DRAFT, HAS_HOOKS, or any other unrecoverable state.
		s.addDeliveryNote(client, item.ID,
			fmt.Sprintf("Stuck delivery: PR %s in unrecoverable state %q. Recirculated.", prURL, mergeStateStatus))
		s.logSetOutcome(client, item.ID, "recirculate",
			fmt.Sprintf("unrecoverable merge state: %s", mergeStateStatus))
		s.logger.Info("stuck delivery: recirculated due to unrecoverable state",
			"droplet", item.ID, "mergeStateStatus", mergeStateStatus)
	}
}

// addDeliveryNote logs a stuck-delivery note on the droplet, warning on failure.
func (s *Castellarius) addDeliveryNote(client CisternClient, dropletID, msg string) {
	if err := client.AddNote(dropletID, "stuck-delivery", msg); err != nil {
		s.logger.Warn("stuck delivery: AddNote failed", "droplet", dropletID, "error", err)
	}
}

// logSetOutcome calls client.SetOutcome and logs at Error level if it fails.
// A SetOutcome failure after the session has been killed leaves the droplet
// stranded — logging ensures operators can detect and manually recover it.
func (s *Castellarius) logSetOutcome(client CisternClient, id, outcome, context string) {
	if err := client.SetOutcome(id, outcome); err != nil {
		s.logger.Error("stuck delivery: SetOutcome failed",
			"droplet", id,
			"outcome", outcome,
			"context", context,
			"error", err,
		)
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
	out, cmdErr := cmd.CombinedOutput()
	if cmdErr != nil {
		return "", "", "", fmt.Errorf("gh pr list --head %s: %w: %s", branch, cmdErr, out)
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

// defaultRebaseAndPush fetches origin, rebases the current branch onto
// origin/main, and force-pushes with lease. Aborts the rebase on failure.
func defaultRebaseAndPush(ctx context.Context, sandboxDir string) error {
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = sandboxDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, out)
		}
		return nil
	}

	if err := run("git", "fetch", "origin"); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if err := run("git", "rebase", "origin/main"); err != nil {
		_ = run("git", "rebase", "--abort")
		return fmt.Errorf("rebase: %w", err)
	}
	if err := run("git", "push", "--force-with-lease"); err != nil {
		return fmt.Errorf("push: %w", err)
	}
	return nil
}

// defaultGhMerge merges or enables auto-merge on a PR via the gh CLI.
// When autoMerge is true, uses --auto so GitHub merges when CI passes.
// When autoMerge is false, merges the PR immediately.
func defaultGhMerge(ctx context.Context, sandboxDir, prURL string, autoMerge bool) error {
	args := []string{"pr", "merge", prURL, "--yes"}
	if autoMerge {
		args = append(args, "--auto")
	}
	args = append(args, "--merge")
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = sandboxDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}
