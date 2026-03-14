package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/MichielDean/bullet-farm/internal/workflow"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show repos, workers, and global worker count",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, err := workflow.ParseFarmConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	cfgDir := filepath.Dir(cfgPath)
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "REPO\tWORKFLOW\tWORKERS\tSTATUS")

	totalWorkers := 0
	for _, repo := range cfg.Repos {
		wfName := "-"
		status := "ok"

		wfPath := repo.WorkflowPath
		if wfPath != "" {
			if !filepath.IsAbs(wfPath) {
				wfPath = filepath.Join(cfgDir, wfPath)
			}
			w, err := workflow.ParseWorkflow(wfPath)
			if err != nil {
				wfName = repo.WorkflowPath
				status = "error: " + err.Error()
			} else {
				wfName = w.Name
			}
		}

		totalWorkers += repo.Workers
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", repo.Name, wfName, repo.Workers, status)
	}
	tw.Flush()

	fmt.Printf("\ntotal workers: %d / %d (max)\n", totalWorkers, cfg.MaxTotalWorkers)
	return nil
}
