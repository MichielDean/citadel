package gates

import (
	"context"
	"fmt"
	"strings"
)

// PRCreate creates a GitHub pull request using the gh CLI.
//
// Before opening the PR it rebases the feature branch onto baseBranch to
// prevent conflicts from accumulating. If the PR already exists it extracts
// the URL and continues (idempotent). Conflict errors during push are
// recirculated to implement with an actionable note.
func (e *Executor) PRCreate(ctx context.Context, bc DropletContext) (*StepOutcome, error) {
	branch := bc.Branch
	if branch == "" {
		out, err := e.ExecFn(ctx, bc.WorkDir, "git", "branch", "--show-current")
		if err != nil {
			return &StepOutcome{
				Result: ResultFail,
				Notes:  fmt.Sprintf("detect branch: %s: %s", err, out),
			}, nil
		}
		branch = strings.TrimSpace(string(out))
	}
	if branch == "" {
		return &StepOutcome{
			Result: ResultFail,
			Notes:  "could not determine head branch",
		}, nil
	}

	baseBranch := bc.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	title := bc.Title
	if title == "" {
		title = fmt.Sprintf("droplet %s", bc.ID)
	}

	body := bc.Description
	if body == "" {
		body = fmt.Sprintf("Automated PR for droplet %s", bc.ID)
	}

	// Fetch latest base to rebase against.
	fetchOut, fetchErr := e.ExecFn(ctx, bc.WorkDir, "git", "fetch", "origin", baseBranch)
	if fetchErr != nil {
		// Non-fatal — rebase will fail below if truly unreachable.
		_ = fetchOut
	}

	// Stash any uncommitted changes (e.g. CONTEXT.md, .claude/) before the
	// merge-base check and any potential rebase so the worktree is clean.
	// We pop the stash on exit regardless of whether a rebase was needed.
	stashOut, _ := e.ExecFn(ctx, bc.WorkDir, "git", "stash", "--include-untracked", "--message", "pre-rebase-stash")
	didStash := !strings.Contains(string(stashOut), "No local changes")
	defer func() {
		if didStash {
			e.ExecFn(ctx, bc.WorkDir, "git", "stash", "pop") //nolint:errcheck
		}
	}()

	// Rebase only when the branch has diverged from origin/baseBranch.
	// If either check fails, fall back to rebasing unconditionally.
	mergeBaseOut, mergeBaseErr := e.ExecFn(ctx, bc.WorkDir, "git", "merge-base", "HEAD", "origin/"+baseBranch)
	originTipOut, originTipErr := e.ExecFn(ctx, bc.WorkDir, "git", "rev-parse", "origin/"+baseBranch)
	needsRebase := mergeBaseErr != nil || originTipErr != nil ||
		strings.TrimSpace(string(mergeBaseOut)) != strings.TrimSpace(string(originTipOut))

	if needsRebase {
		// Rebase onto base branch before pushing to avoid merge conflicts in the PR.
		rebaseOut, rebaseErr := e.ExecFn(ctx, bc.WorkDir, "git", "rebase", "origin/"+baseBranch)
		if rebaseErr != nil {
			// Rebase conflict — abort so the worktree stays clean, then recirculate.
			e.ExecFn(ctx, bc.WorkDir, "git", "rebase", "--abort") //nolint:errcheck
			return &StepOutcome{
				Result: ResultRecirculate,
				Notes: fmt.Sprintf(
					"rebase conflict with %s — resolve conflicts and re-commit before this can be merged: %s",
					baseBranch, strings.TrimSpace(string(rebaseOut)),
				),
			}, nil
		}
	}

	// Push the (now-rebased) feature branch.
	pushOut, pushErr := e.ExecFn(ctx, bc.WorkDir, "git", "push", "-u", "origin", branch, "--force-with-lease")
	if pushErr != nil {
		return &StepOutcome{
			Result: ResultFail,
			Notes:  fmt.Sprintf("git push failed: %s: %s", pushErr, pushOut),
		}, nil
	}

	// Create the PR. If one already exists, extract its URL instead of failing.
	out, err := e.ExecFn(ctx, bc.WorkDir, "gh",
		"pr", "create",
		"--title", title,
		"--body", body,
		"--base", baseBranch,
		"--head", branch,
	)
	outStr := strings.TrimSpace(string(out))

	var prURL string
	if err != nil {
		// "already exists" is the one recoverable error — extract the URL.
		if strings.Contains(outStr, "already exists") {
			prURL = extractExistingPRURL(outStr)
			if prURL == "" {
				// Fallback: look it up directly.
				lookupOut, lookupErr := e.ExecFn(ctx, bc.WorkDir, "gh",
					"pr", "view", branch, "--json", "url", "--jq", ".url")
				if lookupErr == nil {
					prURL = strings.TrimSpace(string(lookupOut))
				}
			}
			if prURL == "" {
				return &StepOutcome{
					Result: ResultFail,
					Notes:  fmt.Sprintf("PR already exists but could not extract URL: %s", outStr),
				}, nil
			}
		} else {
			return &StepOutcome{
				Result: ResultFail,
				Notes:  fmt.Sprintf("gh pr create failed: %s: %s", err, outStr),
			}, nil
		}
	} else {
		prURL = outStr
	}

	// Extract PR number from URL (e.g. https://github.com/owner/repo/pull/123).
	prNumber := ""
	if parts := strings.Split(prURL, "/"); len(parts) > 0 {
		prNumber = parts[len(parts)-1]
	}

	return &StepOutcome{
		Result: ResultPass,
		Notes:  fmt.Sprintf("created PR: %s", prURL),
		Annotations: map[string]string{
			AnnoPRURL:    prURL,
			AnnoPRNumber: prNumber,
		},
	}, nil
}

// extractExistingPRURL pulls the PR URL out of gh's "already exists" error
// message, which embeds it on the last line.
func extractExistingPRURL(msg string) string {
	lines := strings.Split(msg, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "https://github.com/") && strings.Contains(line, "/pull/") {
			return line
		}
	}
	return ""
}
