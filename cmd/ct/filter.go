package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
		preset := resolveFilterPreset(filterRepo)

		if filterResume != "" {
			if filterFile {
				// --resume --file: finalize and persist to DB.
				if filterRepo == "" {
					return fmt.Errorf("--repo is required with --file")
				}
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
		result, err := invokeFilterNew(preset, filterTitle, filterDescription)
		if err != nil {
			return err
		}
		return printFilterResult(result, filterOutputFormat)
	},
}

// invokeFilterNew starts a new filtration session and returns proposals with session_id.
func invokeFilterNew(preset provider.ProviderPreset, title, description string) (filterSessionResult, error) {
	userPrompt := "Title: " + title
	if description != "" {
		userPrompt += "\nDescription: " + description
	}
	combinedPrompt := filterSystemPrompt + "\n\n" + userPrompt
	return callFilterAgent(preset, nil, combinedPrompt)
}

// invokeFilterResume resumes an existing filtration session with the given message
// and returns updated proposals with session_id.
func invokeFilterResume(preset provider.ProviderPreset, sessionID, message string) (filterSessionResult, error) {
	resumeFlag := preset.ResumeFlag
	if resumeFlag == "" {
		resumeFlag = "--resume"
	}
	extraArgs := []string{resumeFlag, sessionID}
	return callFilterAgent(preset, extraArgs, message)
}

// callFilterAgent invokes the preset command with --print --output-format json,
// optional extraArgs (e.g. --resume <id>), and the given prompt.
// It returns parsed proposals and the session_id from the JSON envelope.
// If the agent does not support --output-format json, it falls back to parsing
// the raw output as proposals (session_id will be empty in that case).
func callFilterAgent(preset provider.ProviderPreset, extraArgs []string, prompt string) (filterSessionResult, error) {
	for _, key := range preset.EnvPassthrough {
		if os.Getenv(key) == "" {
			return filterSessionResult{}, fmt.Errorf("%s is not set", key)
		}
	}

	// Build args: [Subcommand] [preset.Args...] [extraArgs...] [PrintFlag] [--output-format json] [PromptFlag prompt]
	var args []string
	if preset.NonInteractive.Subcommand != "" {
		args = append(args, preset.NonInteractive.Subcommand)
	}
	args = append(args, preset.Args...)
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
	rootCmd.AddCommand(filterCmd)
}
