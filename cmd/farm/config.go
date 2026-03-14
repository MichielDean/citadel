package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichielDean/bullet-farm/internal/workflow"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Config management commands",
}

var configValidateCmd = &cobra.Command{
	Use:   "validate [path]",
	Short: "Validate a farm config and all referenced workflow files",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runConfigValidate,
}

func init() {
	configCmd.AddCommand(configValidateCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigValidate(cmd *cobra.Command, args []string) error {
	path := cfgPath
	if len(args) > 0 {
		path = args[0]
	}

	cfg, err := workflow.ParseFarmConfig(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return err
	}

	cfgDir := filepath.Dir(path)
	var errs []error
	for _, repo := range cfg.Repos {
		if repo.Name == "" {
			e := fmt.Errorf("repo entry missing name")
			fmt.Fprintf(os.Stderr, "  error: %v\n", e)
			errs = append(errs, e)
			continue
		}
		if repo.WorkflowPath == "" {
			e := fmt.Errorf("repo %q: workflow_path is required", repo.Name)
			fmt.Fprintf(os.Stderr, "  error: %v\n", e)
			errs = append(errs, e)
			continue
		}

		wfPath := repo.WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(cfgDir, wfPath)
		}

		if _, err := workflow.ParseWorkflow(wfPath); err != nil {
			e := fmt.Errorf("repo %q workflow %q: %w", repo.Name, repo.WorkflowPath, err)
			fmt.Fprintf(os.Stderr, "  error: %v\n", e)
			errs = append(errs, e)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("validation found %d error(s)", len(errs))
	}

	fmt.Println("config valid:", path)
	return nil
}
