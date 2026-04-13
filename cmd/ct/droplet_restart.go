package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/spf13/cobra"
)

var (
	restartCataractae string
	restartNotes      string
)

var dropletRestartCmd = &cobra.Command{
	Use:   "restart <id>",
	Short: "Restart a droplet from a specific cataractae",
	Long: `Restart sends a stuck or failed droplet back into the pipeline at the
named cataractae stage. Without --cataractae, it restarts from the droplet's
current stage. The command clears assignee and outcome, resets status to
'open', and writes a scheduler note with a timestamp.

The --cataractae flag, when provided, must name a valid cataractae in the
droplet's aqueduct (as defined in cistern.yaml). If the config cannot be
loaded, the validation is skipped and any cataractae name is accepted.

Examples:
  ct droplet restart sc-uvfhw --cataractae delivery
  ct droplet restart sc-uvfhw                    # restart from current stage
  ct droplet restart sc-uvfhw --cataractae delivery --notes "PR #157 conflicts resolved"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		id := args[0]

		item, err := c.Get(id)
		if err != nil {
			return err
		}

		cataractaeName := restartCataractae
		if cataractaeName == "" {
			cataractaeName = item.CurrentCataractae
		}
		if cataractaeName == "" {
			return fmt.Errorf("droplet %s has no current cataractae and --cataractae was not provided; specify one with --cataractae", id)
		}

		if err := validateRestartCataractae(restartCataractae, item.Repo); err != nil {
			return err
		}

		if restartNotes != "" {
			if err := c.AddNote(id, "restart", restartNotes); err != nil {
				return fmt.Errorf("add note: %w", err)
			}
		}

		result, err := c.Restart(id, cataractaeName)
		if err != nil {
			return fmt.Errorf("restart: %w", err)
		}

		fmt.Printf("droplet %s → restarting at cataractae %q\n", id, cataractaeName)
		if result.Status != "open" {
			fmt.Fprintf(os.Stderr, "warning: expected status 'open', got %q\n", result.Status)
		}
		if restartNotes != "" {
			fmt.Printf("  note: %s\n", restartNotes)
		}
		return nil
	},
}

func init() {
	dropletRestartCmd.Flags().StringVar(&restartCataractae, "cataractae", "", "cataractae to restart from (defaults to current stage)")
	dropletRestartCmd.Flags().StringVar(&restartNotes, "notes", "", "optional note to record before restarting")
}

func validateRestartCataractae(cataractaeName, repo string) error {
	if cataractaeName == "" {
		return nil
	}
	cfg, cfgErr := aqueduct.ParseAqueductConfig(resolveConfigPath())
	if cfgErr != nil {
		return nil
	}
	workflow := findWorkflowForRepo(cfg, repo)
	if workflow == nil {
		return nil
	}
	var validNames []string
	for _, step := range workflow.Cataractae {
		validNames = append(validNames, step.Name)
		if step.Name == cataractaeName {
			return nil
		}
	}
	return fmt.Errorf("cataractae %q is not valid for repo %s; valid cataractae: %s", cataractaeName, repo, strings.Join(validNames, ", "))
}

func findWorkflowForRepo(cfg *aqueduct.AqueductConfig, repo string) *aqueduct.Workflow {
	for _, r := range cfg.Repos {
		if !strings.EqualFold(r.Name, repo) {
			continue
		}
		if r.WorkflowPath == "" {
			return nil
		}
		wf, err := aqueduct.ParseWorkflow(r.WorkflowPath)
		if err != nil {
			return nil
		}
		return wf
	}
	return nil
}
