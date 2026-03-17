package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/spf13/cobra"
)

//go:embed assets/cistern.yaml
var defaultCisternConfig []byte

//go:embed assets/aqueduct/feature.yaml
var defaultFeatureWorkflow []byte

//go:embed assets/aqueduct/bug.yaml
var defaultBugWorkflow []byte

var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Bootstrap a new Cistern installation",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")

	// 1. Create directory structure.
	for _, dir := range []string{cisternDir, aqueductDir, cataractaeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// 2. Write cistern.yaml from embedded template.
	configDst := filepath.Join(cisternDir, "cistern.yaml")
	if err := writeFileIfAbsent(configDst, defaultCisternConfig, initForce); err != nil {
		return err
	}

	// 3. Copy default workflow files.
	workflows := []struct {
		name string
		data []byte
	}{
		{"feature.yaml", defaultFeatureWorkflow},
		{"bug.yaml", defaultBugWorkflow},
	}
	for _, wf := range workflows {
		dst := filepath.Join(aqueductDir, wf.name)
		if err := writeFileIfAbsent(dst, wf.data, initForce); err != nil {
			return err
		}
	}

	// 4. Generate role files from the feature aqueduct.
	featureWfPath := filepath.Join(aqueductDir, "feature.yaml")
	w, err := aqueduct.ParseWorkflow(featureWfPath)
	if err != nil {
		return fmt.Errorf("parse feature workflow: %w", err)
	}
	if len(w.CataractaDefinitions) > 0 {
		if _, err := aqueduct.GenerateCataractaFiles(w, cataractaeDir); err != nil {
			return fmt.Errorf("generate cataractae: %w", err)
		}
	}

	// 5. Print next-steps message.
	fmt.Printf(`Cistern initialized.
  Config     : ~/.cistern/cistern.yaml
  Aqueduct   : ~/.cistern/aqueduct/
  Cataractae : ~/.cistern/cataractae/

Next:
  1. Edit ~/.cistern/cistern.yaml — add your repos
  2. ct droplet add --title "Your first droplet" --repo yourrepo
  3. ct castellarius start
`)
	return nil
}

// writeFileIfAbsent writes data to path. If the file already exists and force
// is false, it prints a warning to stderr and skips the write.
func writeFileIfAbsent(path string, data []byte, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(os.Stderr, "warning: %s already exists, skipping (use --force to overwrite)\n", path)
			return nil
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "overwrite existing files")
	rootCmd.AddCommand(initCmd)
}
