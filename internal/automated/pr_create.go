package automated

import (
	"context"
	"fmt"
	"strings"
)

// PRCreate creates a GitHub pull request using the gh CLI.
// Title and body come from BeadContext. The PR URL is returned in
// Annotations[AnnoPRURL] for the caller to persist to bead metadata.
func (e *Executor) PRCreate(ctx context.Context, bc BeadContext) (*StepOutcome, error) {
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
		title = fmt.Sprintf("bead %s", bc.ID)
	}

	body := bc.Description
	if body == "" {
		body = fmt.Sprintf("Automated PR for bead %s", bc.ID)
	}

	out, err := e.ExecFn(ctx, bc.WorkDir, "gh",
		"pr", "create",
		"--title", title,
		"--body", body,
		"--base", baseBranch,
		"--head", branch,
	)
	if err != nil {
		return &StepOutcome{
			Result: ResultFail,
			Notes:  fmt.Sprintf("gh pr create failed: %s: %s", err, out),
		}, nil
	}

	prURL := strings.TrimSpace(string(out))

	// Extract PR number from URL (e.g., https://github.com/owner/repo/pull/123).
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
