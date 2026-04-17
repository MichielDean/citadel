package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/MichielDean/cistern/internal/evaluate"
	"github.com/spf13/cobra"
)

var evaluateCmd = &cobra.Command{
	Use:   "evaluate",
	Short: "Score code changes against the Cistern quality rubric",
	Long: `Evaluate scores a diff or PR against the Cistern quality rubric.

This produces structured scores across 8 dimensions, each on a 0-5 scale:
  - contract_correctness: Does every method do what its signature promises?
  - integration_coverage: Do new code paths have integration tests?
  - coupling: Is new code coupled to specific entities when it could be generic?
  - migration_safety: Do migrations follow safe practices?
  - idiom_fit: Does the code use the framework's idiomatic patterns?
  - dry: Are repeated patterns extracted into helpers?
  - naming_clarity: Are types and methods honestly named?
  - error_messages: Are error messages actionable?

Use --diff to score an existing diff, --pr to score a PR, or leave flags
empty to score the current branch against main.`,
	RunE: runEvaluate,
}

var (
	evalDiff   string
	evalBase   string
	evalHead   string
	evalPR     int
	evalTicket string
	evalSource string
	evalBranch string
	evalCommit string
	evalModel  string
	evalOutput string
	evalFormat string
)

func init() {
	rootCmd.AddCommand(evaluateCmd)

	evaluateCmd.Flags().StringVarP(&evalDiff, "diff", "d", "", "Raw diff content to evaluate")
	evaluateCmd.Flags().StringVar(&evalBase, "base", "main", "Base branch for diff (default: main)")
	evaluateCmd.Flags().StringVar(&evalHead, "head", "", "Head branch for diff (default: current branch)")
	evaluateCmd.Flags().IntVarP(&evalPR, "pr", "p", 0, "PR number to evaluate")
	evaluateCmd.Flags().StringVarP(&evalTicket, "ticket", "t", "", "Jira/ticket ID for comparative evaluation")
	evaluateCmd.Flags().StringVar(&evalSource, "source", "cistern", "Source label (e.g., 'cistern' or 'vibe-coded')")
	evaluateCmd.Flags().StringVar(&evalBranch, "branch", "", "Branch name (default: current branch)")
	evaluateCmd.Flags().StringVar(&evalCommit, "commit", "", "Commit SHA (default: HEAD)")
	evaluateCmd.Flags().StringVar(&evalModel, "model", "", "LLM model to use for evaluation (default: auto-detect)")
	evaluateCmd.Flags().StringVarP(&evalOutput, "output", "o", "", "Output file path (default: stdout)")
	evaluateCmd.Flags().StringVarP(&evalFormat, "format", "f", "json", "Output format: json or markdown")
}

func runEvaluate(cmd *cobra.Command, args []string) error {
	diff, err := resolveDiff()
	if err != nil {
		return err
	}

	if diff == "" {
		return fmt.Errorf("no diff provided -- use --diff, --base/--head, or --pr")
	}

	if evalSource == "" {
		evalSource = "unknown"
	}

	if evalBranch == "" {
		evalBranch = currentBranch()
	}

	if evalCommit == "" {
		evalCommit = "HEAD"
	}

	if evalModel == "" {
		evalModel = "auto"
	}

	result, err := evaluate.Evaluate(diff, evalModel, evalSource, evalTicket, evalBranch, evalCommit)
	if err != nil {
		return fmt.Errorf("evaluation failed: %w", err)
	}

	result.Timestamp = time.Now().UTC().Format(time.RFC3339)

	var output []byte
	switch strings.ToLower(evalFormat) {
	case "markdown", "md":
		output = []byte(formatMarkdown(result))
	case "json":
		output, err = json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling result: %w", err)
		}
	default:
		return fmt.Errorf("unknown format: %s (use json or markdown)", evalFormat)
	}

	if evalOutput != "" {
		if err := os.WriteFile(evalOutput, output, 0644); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Evaluation written to %s\n", evalOutput)
	} else {
		fmt.Println(string(output))
	}

	return nil
}

func resolveDiff() (string, error) {
	if evalDiff != "" {
		return evalDiff, nil
	}

	if evalPR > 0 {
		input := evaluate.DiffInput{
			Source:   evaluate.DiffFromPR,
			PRNumber: evalPR,
		}
		return input.GetDiff()
	}

	if evalHead == "" {
		evalHead = currentBranch()
	}
	input := evaluate.DiffInput{
		Source:     evaluate.DiffFromBranches,
		BaseBranch: evalBase,
		HeadBranch: evalHead,
	}
	return input.GetDiff()
}

func currentBranch() string {
	out, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func formatMarkdown(r *evaluate.Result) string {
	var sb strings.Builder

	sb.WriteString("# Code Quality Evaluation\n\n")
	sb.WriteString(fmt.Sprintf("- **Source:** %s\n", r.Source))
	if r.Ticket != "" {
		sb.WriteString(fmt.Sprintf("- **Ticket:** %s\n", r.Ticket))
	}
	sb.WriteString(fmt.Sprintf("- **Branch:** %s\n", r.Branch))
	sb.WriteString(fmt.Sprintf("- **Model:** %s\n", r.Model))
	sb.WriteString(fmt.Sprintf("- **Score:** %d/%d (%.0f%%)\n", r.TotalScore, r.MaxScore, r.Percentage()))
	sb.WriteString(fmt.Sprintf("- **Evaluated:** %s\n\n", r.Timestamp))

	sb.WriteString("| Dimension | Score | Evidence |\n")
	sb.WriteString("|---|---|---|\n")
	for _, s := range r.Scores {
		sb.WriteString(fmt.Sprintf("| %s | %d/5 | %s |\n", s.Dimension, s.Score, s.Evidence))
	}

	if r.Notes != "" {
		sb.WriteString(fmt.Sprintf("\n## Notes\n\n%s\n", r.Notes))
	}

	return sb.String()
}