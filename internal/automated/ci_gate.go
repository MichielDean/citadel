package automated

import (
	"context"
	"encoding/json"
	"fmt"
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
func (e *Executor) CIGate(ctx context.Context, bc BeadContext, pollInterval time.Duration) (*StepOutcome, error) {
	prURL := metaString(bc.Metadata, MetaPRURL)
	if prURL == "" {
		return &StepOutcome{
			Result: ResultFail,
			Notes:  "no pr_url in bead metadata",
		}, nil
	}

	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}

	for {
		checks, err := e.fetchChecks(ctx, bc.WorkDir, prURL)
		if err != nil {
			return &StepOutcome{
				Result: ResultFail,
				Notes:  fmt.Sprintf("fetch checks failed: %s", err),
			}, nil
		}

		allDone, anyFailed, summary := evaluateChecks(checks)

		if anyFailed {
			return &StepOutcome{
				Result: ResultFail,
				Notes:  fmt.Sprintf("CI failed: %s", summary),
			}, nil
		}

		if allDone {
			return &StepOutcome{
				Result: ResultPass,
				Notes:  fmt.Sprintf("CI passed: %s", summary),
			}, nil
		}

		select {
		case <-ctx.Done():
			return &StepOutcome{
				Result: ResultFail,
				Notes:  fmt.Sprintf("CI gate timed out (pending: %s)", summary),
			}, nil
		case <-time.After(pollInterval):
		}
	}
}

func (e *Executor) fetchChecks(ctx context.Context, dir, prURL string) ([]checkRun, error) {
	out, err := e.ExecFn(ctx, dir, "gh", "pr", "checks", prURL, "--json", "name,bucket")
	if err != nil {
		// gh pr checks returns exit code 1 when no checks exist; treat as empty.
		outStr := string(out)
		if len(outStr) == 0 || outStr == "[]\n" || outStr == "[]" {
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
