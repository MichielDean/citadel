package evaluate

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// DiffSource represents where the diff comes from.
type DiffSource int

const (
	// DiffFromBranches computes the merge-base diff between two branches.
	DiffFromBranches DiffSource = iota
	// DiffFromPR fetches the diff from an existing PR.
	DiffFromPR
	// DiffFromRaw is provided directly (e.g., from a file).
	DiffFromRaw
)

// DiffInput specifies what to evaluate.
type DiffInput struct {
	Source DiffSource
	// For DiffFromBranches: base and head branch names (e.g., "main", "feat/fix-thing")
	BaseBranch string
	HeadBranch string
	// For DiffFromPR: PR number
	PRNumber int
	// For DiffFromRaw: the diff content
	RawDiff string
	// Working directory for git commands
	WorkDir string
}

// GetDiff returns the diff content based on the source.
func (d DiffInput) GetDiff() (string, error) {
	switch d.Source {
	case DiffFromBranches:
		return d.getBranchDiff()
	case DiffFromPR:
		return d.getPRDiff()
	case DiffFromRaw:
		return d.RawDiff, nil
	default:
		return "", fmt.Errorf("unknown diff source: %d", d.Source)
	}
}

func (d DiffInput) getBranchDiff() (string, error) {
	if d.BaseBranch == "" {
		d.BaseBranch = "main"
	}
	if d.HeadBranch == "" {
		return "", fmt.Errorf("head branch is required for branch diff")
	}
	// git diff $(git merge-base HEAD origin/main)..HEAD
	mergeBase, err := exec.Command("git", "merge-base", d.HeadBranch, d.BaseBranch).Output()
	if err != nil {
		return "", fmt.Errorf("git merge-base: %w", err)
	}
	cmd := exec.Command("git", "diff", strings.TrimSpace(string(mergeBase))+".."+d.HeadBranch)
	if d.WorkDir != "" {
		cmd.Dir = d.WorkDir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}

func (d DiffInput) getPRDiff() (string, error) {
	if d.PRNumber == 0 {
		return "", fmt.Errorf("PR number is required for PR diff")
	}
	cmd := exec.Command("gh", "pr", "diff", fmt.Sprintf("%d", d.PRNumber))
	if d.WorkDir != "" {
		cmd.Dir = d.WorkDir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh pr diff: %w", err)
	}
	return string(out), nil
}

// Evaluate uses an LLM to score the given diff against the rubric.
// The model parameter specifies which LLM to use (e.g., "claude-sonnet-4-20250514").
// The evaluator runs as an independent adversary, not as the code's author.
func Evaluate(diff string, model string, source string, ticket string, branch string, commit string) (*Result, error) {
	if diff == "" {
		return nil, fmt.Errorf("diff is empty — nothing to evaluate")
	}

	// For now, return a placeholder result. The actual LLM invocation
	// will be wired up in a follow-up commit when we integrate with
	// the provider system.
	//
	// The prompt is ready in ScoringPrompt — we need to:
	// 1. Format the prompt with the diff
	// 2. Call the LLM
	// 3. Parse the JSON response
	// 4. Validate and return

	result := &Result{
		Source:     source,
		Ticket:     ticket,
		Branch:     branch,
		Commit:     commit,
		Model:      model,
		Scores:     []Score{},
		TotalScore: 0,
		MaxScore:   len(AllDimensions()) * 5,
		Notes:      "Evaluation not yet implemented — rubric and scoring structure is defined",
		Timestamp:  "", // set by caller
	}

	return result, nil
}

// ParseEvaluationResult parses the LLM's JSON response into a Result.
func ParseEvaluationResult(body string) (*Result, error) {
	var result Result
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		return nil, fmt.Errorf("parsing evaluation result: %w", err)
	}

	// Validate dimensions
	validDims := make(map[Dimension]bool)
	for _, d := range AllDimensions() {
		validDims[d] = true
	}

	totalScore := 0
	for _, s := range result.Scores {
		if !validDims[s.Dimension] {
			return nil, fmt.Errorf("unknown dimension: %s", s.Dimension)
		}
		if s.Score < 0 || s.Score > 5 {
			return nil, fmt.Errorf("score for %s must be 0-5, got %d", s.Dimension, s.Score)
		}
		totalScore += s.Score
	}

	result.TotalScore = totalScore
	result.MaxScore = len(AllDimensions()) * 5

	return &result, nil
}