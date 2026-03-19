package gates

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DefaultPollInterval is the default interval between CI check polls.
const DefaultPollInterval = 30 * time.Second

// checkRun represents a single CI check from gh pr checks --json.
type checkRun struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
}

// CIGate polls PR checks until all pass or the context deadline is exceeded.
// The PR URL is read from bc.Metadata[MetaPRURL]. pollInterval controls how
// often checks are polled; zero uses DefaultPollInterval.
func (e *Executor) CIGate(ctx context.Context, bc DropletContext, pollInterval time.Duration) (*StepOutcome, error) {
	prURL := metaString(bc.Metadata, MetaPRURL)
	if prURL == "" {
		return &StepOutcome{
			Result: ResultFail,
			Notes:  "no pr_url in droplet metadata",
		}, nil
	}

	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}

	startTime := time.Now()
	noChecksCount := 0

	for {
		// Detect merge conflicts early — CI won't run on a conflicting PR.
		if e.prHasConflicts(ctx, bc.WorkDir, prURL) {
			return &StepOutcome{
				Result: ResultRecirculate,
				Notes:  "PR has merge conflicts with base branch — rebase onto main and force-push to resolve",
			}, nil
		}

		checks, err := e.fetchChecks(ctx, bc.WorkDir, prURL)
		if err != nil {
			return &StepOutcome{
				Result: ResultFail,
				Notes:  fmt.Sprintf("fetch checks failed: %s", err),
			}, nil
		}

		allDone, anyFailed, summary := evaluateChecks(checks)

		if anyFailed {
			notes := e.extractCIFailure(ctx, bc)
			return &StepOutcome{
				Result: ResultRecirculate,
				Notes:  fmt.Sprintf("%s (checks: %s)", notes, summary),
			}, nil
		}

		if allDone {
			if len(checks) == 0 {
				// No checks configured or runner offline.
				// After several polls with no checks, treat as passed (no CI configured).
				noChecksCount++
				if noChecksCount >= 3 {
					return &StepOutcome{
						Result: ResultPass,
						Notes:  "no CI checks configured — proceeding to merge",
					}, nil
				}
			} else {
				return &StepOutcome{
					Result: ResultPass,
					Notes:  fmt.Sprintf("CI passed: %s", summary),
				}, nil
			}
		}

		select {
		case <-ctx.Done():
			elapsed := int(time.Since(startTime).Minutes())
			return &StepOutcome{
				Result: ResultFail,
				Notes:  fmt.Sprintf("CI pending after %d minutes — manual check required", elapsed),
			}, nil
		case <-time.After(pollInterval):
		}
	}
}

// extractCIFailure fetches failure details from GitHub Actions logs.
func (e *Executor) extractCIFailure(ctx context.Context, bc DropletContext) string {
	branchOut, err := e.ExecFn(ctx, bc.WorkDir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "CI failed"
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		return "CI failed"
	}

	runListOut, err := e.ExecFn(ctx, bc.WorkDir, "gh", "run", "list", "--branch", branch, "--json", "databaseId,status", "--limit", "3")
	if err != nil {
		return "CI failed"
	}

	var runs []struct {
		DatabaseID int64  `json:"databaseId"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(runListOut, &runs); err != nil || len(runs) == 0 {
		return "CI failed"
	}

	runID := fmt.Sprintf("%d", runs[0].DatabaseID)
	logOut, _ := e.ExecFn(ctx, bc.WorkDir, "gh", "run", "view", runID, "--log-failed")
	logStr := strings.TrimSpace(string(logOut))
	if len(logStr) > 500 {
		logStr = logStr[:500]
	}
	if logStr == "" {
		return fmt.Sprintf("CI failed — run %s", runID)
	}
	return fmt.Sprintf("CI failed — %s", logStr)
}

func (e *Executor) fetchChecks(ctx context.Context, dir, prURL string) ([]checkRun, error) {
	out, err := e.ExecFn(ctx, dir, "gh", "pr", "checks", prURL, "--json", "name,bucket")
	if err != nil {
		outStr := string(out)
		// gh pr checks exits 1 when no checks are configured or the runner is
		// offline — both are "no checks yet", not a fatal error.
		if len(outStr) == 0 || outStr == "[]\n" || outStr == "[]" ||
			strings.Contains(outStr, "no checks reported") ||
			strings.Contains(outStr, "no checks") {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %s", err, out)
	}

	var checks []checkRun
	if err := json.Unmarshal(out, &checks); err != nil {
		return nil, fmt.Errorf("parse checks: %w", err)
	}
	return checks, nil
}

// prHasConflicts returns true if the PR has a merge conflict with its base branch.
func (e *Executor) prHasConflicts(ctx context.Context, dir, prURL string) bool {
	out, err := e.ExecFn(ctx, dir, "gh", "pr", "view", prURL, "--json", "mergeable", "--jq", ".mergeable")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "CONFLICTING"
}

// evaluateChecks returns whether all checks are done, whether any failed,
// and a human-readable summary.
func evaluateChecks(checks []checkRun) (allDone, anyFailed bool, summary string) {
	if len(checks) == 0 {
		return true, false, "no checks configured"
	}

	allDone = true
	var passed, failed, pending int

	for _, c := range checks {
		switch c.Bucket {
		case "pass", "skipping":
			passed++
		case "fail":
			failed++
			anyFailed = true
		default:
			pending++
			allDone = false
		}
	}

	summary = fmt.Sprintf("%d passed, %d failed, %d pending", passed, failed, pending)
	return allDone, anyFailed, summary
}
