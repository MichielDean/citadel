package main

import (
	"fmt"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/spf13/cobra"
)

var cancelReason string

var dropletCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel a droplet — won't be implemented or is no longer needed",
	Long: `Cancel sets a droplet's status to 'cancelled', clears its assignee, outcome,
and assigned aqueduct, writes a scheduler note with timestamp, and records the
cancel as an event. Existing notes are preserved for the audit trail.

Works for both flowing (in_progress) and queued (open) droplets. Droplets that
are already delivered or cancelled cannot be cancelled again.

The --reason flag is required and should explain why the droplet is being
cancelled (e.g. "superseded by newer approach", "filed in error").

Examples:
  ct droplet cancel sc-uvfhw --reason "superseded by ct-abc12"
  ct droplet cancel sc-uvfhw --reason "filed in error"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cancelReason == "" {
			return fmt.Errorf("--reason is required: provide a reason for cancellation")
		}

		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		item, err := c.Get(args[0])
		if err != nil {
			return err
		}
		if item.Status == "delivered" || item.Status == "cancelled" {
			return fmt.Errorf("cannot cancel: droplet %s has terminal status %q", args[0], item.Status)
		}

		if err := c.Cancel(args[0], cancelReason); err != nil {
			return err
		}
		fmt.Printf("droplet %s: cancelled\n", args[0])
		return nil
	},
}

func init() {
	dropletCancelCmd.Flags().StringVar(&cancelReason, "reason", "", "reason for cancellation (required)")
}
