package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/provider"
	"github.com/spf13/cobra"
)

// claudeJSONOutput is the envelope returned by claude --print --output-format json.
// The Result field contains the assistant's raw text response; SessionID identifies
// the conversation so it can be resumed.
type claudeJSONOutput struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
}

// filterSessionResult holds the parsed output from a filtration LLM invocation.
type filterSessionResult struct {
	SessionID string
	Proposals []DropletProposal
}

// filterFinalizePrompt is sent to the session when --file is used to retrieve
// the final accepted proposals in JSON form so they can be persisted.
const filterFinalizePrompt = "Output the final accepted droplet proposal(s) as a JSON array only, no additional text."

var (
	filterTitle        string
	filterDescription  string
	filterResume       string
	filterFile         bool
	filterRepo         string
	filterOutputFormat string
	filterSkipContext  bool
)

var filterCmd = &cobra.Command{
	Use:   "filter",
	Short: "Run filtration LLM pass — refine ideas without persisting to the cistern",
	Long: `ct filter runs the same LLM filtration pass used by ct droplet add --filter,
but does not persist anything to the database until you are ready.

New session:
  ct filter --title 'rough idea' [--description '...']

Continue refinement:
  ct filter --resume <session-id> 'your feedback here'

Persist final result:
  ct filter --resume <session-id> --file --repo <repo>

Use --output-format json for scriptable output (session_id + proposals).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Canonicalize repo name before passing to resolveFilterPreset so that
		// repo-specific provider overrides are resolved correctly even when the
		// user supplies a wrong-case name (e.g. "portfoliowebsite" → "PortfolioWebsite").
		if filterRepo != "" {
			canonical, err := resolveCanonicalRepo(filterRepo)
			if err != nil {
				return err
			}
			filterRepo = canonical
		}
		preset := resolveFilterPreset(filterRepo)

		if filterResume != "" {
			if filterFile {
				// --resume --file: finalize and persist to DB.
				if filterRepo == "" {
					return fmt.Errorf("--repo is required with --file")
				}
				// filterRepo is already canonical; use it directly.
				result, err := invokeFilterResume(preset, filterResume, filterFinalizePrompt)
				if err != nil {
					return err
				}
				c, err := cistern.New(resolveDBPath(), inferPrefix(filterRepo))
				if err != nil {
					return err
				}
				defer c.Close()
				return addProposals(c, result.Proposals, filterRepo, 2)
			}

			// --resume without --file: feedback refinement pass.
			if len(args) == 0 {
				return fmt.Errorf("feedback argument required: ct filter --resume <id> '<feedback>'")
			}
			result, err := invokeFilterResume(preset, filterResume, strings.Join(args, " "))
			if err != nil {
				return err
			}
			return printFilterResult(result, filterOutputFormat)
		}

		// New session: --title is required.
		if filterTitle == "" {
			return fmt.Errorf("--title is required (or use --resume to continue an existing session)")
		}
		// Compute the repo worktree path unconditionally so it can be passed to
		// the agent via --add-dir regardless of whether static context is injected.
		var repoPath string
		if filterRepo != "" {
			if home, err := os.UserHomeDir(); err == nil {
				repoPath = filepath.Join(home, ".cistern", "sandboxes", filterRepo, "_primary")
			}
		}
		var contextBlock string
		if !filterSkipContext {
			contextBlock = gatherFilterContext(filterContextConfig{
				DBPath:   resolveDBPath(),
				RepoPath: repoPath,
				Title:    filterTitle,
				Desc:     filterDescription,
			})
		}
		result, err := invokeFilterNew(preset, filterTitle, filterDescription, contextBlock, repoPath)
		if err != nil {
			return err
		}
		return printFilterResult(result, filterOutputFormat)
	},
}

// invokeFilterNew starts a new filtration session and returns proposals with session_id.
// contextBlock, when non-empty, is prepended before the system prompt so the LLM
// sees codebase context first. Pass empty string to omit context injection.
// repoPath, when non-empty and the preset defines AddDirFlag, is passed via
// --add-dir so the agent can use read-only file tools to explore the repository.
func invokeFilterNew(preset provider.ProviderPreset, title, description, contextBlock, repoPath string) (filterSessionResult, error) {
	userPrompt := "Title: " + title
	if description != "" {
		userPrompt += "\nDescription: " + description
	}
	return callFilterAgent(preset, nil, buildFilterPrompt(contextBlock, userPrompt), repoPath)
}

// invokeFilterResume resumes an existing filtration session with the given message
// and returns updated proposals with session_id.
func invokeFilterResume(preset provider.ProviderPreset, sessionID, message string) (filterSessionResult, error) {
	resumeFlag := preset.ResumeFlag
	if resumeFlag == "" {
		resumeFlag = "--resume"
	}
	extraArgs := []string{resumeFlag, sessionID}
	return callFilterAgent(preset, extraArgs, message, "")
}

// callFilterAgent invokes the preset command with --print --output-format json,
// optional extraArgs (e.g. --resume <id>), and the given prompt.
// When repoPath is non-empty and the preset defines AddDirFlag, --add-dir repoPath
// is appended so the agent can use file tools to explore the repository.
// When the preset defines NonInteractive.AllowedToolsFlag, read-only file tools
// (Glob, Grep, Read) are enabled so the agent can discover context on demand.
// It returns parsed proposals and the session_id from the JSON envelope.
// If the agent does not support --output-format json, it falls back to parsing
// the raw output as proposals (session_id will be empty in that case).
func callFilterAgent(preset provider.ProviderPreset, extraArgs []string, prompt, repoPath string) (filterSessionResult, error) {
	for _, key := range preset.EnvPassthrough {
		if os.Getenv(key) == "" {
			return filterSessionResult{}, fmt.Errorf("%s is not set", key)
		}
	}

	// Build args: [Subcommand] [preset.Args...] [--add-dir repoPath] [--allowedTools ...] [extraArgs...] [PrintFlag] [--output-format json] [PromptFlag prompt]
	var args []string
	if preset.NonInteractive.Subcommand != "" {
		args = append(args, preset.NonInteractive.Subcommand)
	}
	args = append(args, preset.Args...)
	if repoPath != "" && preset.AddDirFlag != "" {
		args = append(args, preset.AddDirFlag, repoPath)
	}
	if preset.NonInteractive.AllowedToolsFlag != "" {
		args = append(args, preset.NonInteractive.AllowedToolsFlag, "Glob,Grep,Read")
	}
	args = append(args, extraArgs...)
	if preset.NonInteractive.PrintFlag != "" {
		args = append(args, preset.NonInteractive.PrintFlag)
	}
	args = append(args, "--output-format", "json")
	if preset.NonInteractive.PromptFlag != "" {
		args = append(args, preset.NonInteractive.PromptFlag)
	}
	args = append(args, prompt)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, preset.Command, args...)
	if len(preset.ExtraEnv) > 0 {
		env := os.Environ()
		for k, v := range preset.ExtraEnv {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return filterSessionResult{}, fmt.Errorf("agent exec failed (exit %d): %s", ee.ExitCode(), strings.TrimSpace(string(ee.Stderr)))
		}
		return filterSessionResult{}, fmt.Errorf("agent exec failed: %w", err)
	}

	var envelope claudeJSONOutput
	if err := json.Unmarshal(out, &envelope); err != nil {
		// Fallback: the preset may not support --output-format json; try raw.
		proposals, perr := extractProposals(string(out))
		if perr != nil {
			return filterSessionResult{}, fmt.Errorf("failed to parse agent output: %w", perr)
		}
		return filterSessionResult{Proposals: proposals}, nil
	}
	if envelope.IsError {
		return filterSessionResult{}, fmt.Errorf("agent returned error: %s", envelope.Result)
	}

	proposals, err := extractProposals(envelope.Result)
	if err != nil {
		return filterSessionResult{}, fmt.Errorf("failed to parse proposals from agent response: %w", err)
	}

	return filterSessionResult{
		SessionID: envelope.SessionID,
		Proposals: proposals,
	}, nil
}

// printFilterResult writes the filtration result to stdout (proposals) and stderr
// (session_id). Human format is the default; --output-format json emits a single
// JSON object with session_id, title, description, and proposals.
func printFilterResult(result filterSessionResult, outputFormat string) error {
	if outputFormat == "json" {
		type jsonOut struct {
			SessionID   string           `json:"session_id"`
			Title       string           `json:"title,omitempty"`
			Description string           `json:"description,omitempty"`
			Proposals   []DropletProposal `json:"proposals,omitempty"`
		}
		out := jsonOut{
			SessionID: result.SessionID,
			Proposals: result.Proposals,
		}
		if len(result.Proposals) > 0 {
			out.Title = result.Proposals[0].Title
			out.Description = result.Proposals[0].Description
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal output: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	// Human-readable: print each proposal to stdout, session_id to stderr.
	for i, p := range result.Proposals {
		if len(result.Proposals) > 1 {
			fmt.Printf("--- Proposal %d ---\n", i+1)
		}
		fmt.Printf("Title:       %s\n", p.Title)
		fmt.Printf("Description: %s\n", p.Description)
		if p.Complexity != "" {
			fmt.Printf("Complexity:  %s\n", p.Complexity)
		}
		if i < len(result.Proposals)-1 {
			fmt.Println()
		}
	}
	if result.SessionID != "" {
		fmt.Fprintln(os.Stderr, result.SessionID)
	}
	return nil
}

func init() {
	filterCmd.Flags().StringVar(&filterTitle, "title", "", "rough idea title (required for new sessions)")
	filterCmd.Flags().StringVar(&filterDescription, "description", "", "rough idea description")
	filterCmd.Flags().StringVar(&filterResume, "resume", "", "resume an existing filtration session by ID")
	filterCmd.Flags().BoolVar(&filterFile, "file", false, "persist the refined result to the cistern (requires --repo)")
	filterCmd.Flags().StringVar(&filterRepo, "repo", "", "target repository (required with --file)")
	filterCmd.Flags().StringVar(&filterOutputFormat, "output-format", "human", "output format: human or json")
	filterCmd.Flags().BoolVar(&filterSkipContext, "skip-context", false, "skip codebase context injection (for testing and comparison)")
	rootCmd.AddCommand(filterCmd)
}
