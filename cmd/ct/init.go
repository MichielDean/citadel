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

//go:embed assets/aqueduct/aqueduct.yaml
var defaultAqueductWorkflow []byte

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

	// 3. Copy default workflow file.
	aqueductDst := filepath.Join(aqueductDir, "aqueduct.yaml")
	if err := writeFileIfAbsent(aqueductDst, defaultAqueductWorkflow, initForce); err != nil {
		return err
	}

	// 4. Generate role files from the aqueduct workflow.
	w, err := aqueduct.ParseWorkflow(aqueductDst)
	if err != nil {
		return fmt.Errorf("parse aqueduct workflow: %w", err)
	}
	// Seed PERSONA.md + INSTRUCTIONS.md for each identity in the workflow so
	// GenerateCataractaeFiles can write CLAUDE.md.
	if err := initCataractaeDir(w, cataractaeDir); err != nil {
		return fmt.Errorf("init cataractae dir: %w", err)
	}
	if _, err := aqueduct.GenerateCataractaeFiles(w, cataractaeDir); err != nil {
		return fmt.Errorf("generate cataractae: %w", err)
	}

	// 5. Print next-steps message.
	fmt.Printf(`Cistern initialized.
  Config     : ~/.cistern/cistern.yaml
  Aqueduct   : ~/.cistern/aqueduct/aqueduct.yaml
  Cataractae : ~/.cistern/cataractae/

Next:
  1. Edit ~/.cistern/cistern.yaml — add your repos
  2. ct droplet add --title "Your first droplet" --repo yourrepo
  3. ct castellarius start
`)
	return nil
}

// initCataractaeDir writes PERSONA.md and INSTRUCTIONS.md for each unique agent
// identity in the workflow. Skips identities that already have both files.
func initCataractaeDir(w *aqueduct.Workflow, cataractaeDir string) error {
	for _, id := range w.UniqueIdentities() {
		dir := filepath.Join(cataractaeDir, id)
		personaPath := filepath.Join(dir, "PERSONA.md")
		instrPath := filepath.Join(dir, "INSTRUCTIONS.md")

		// Skip if both source files already exist.
		_, personaErr := os.Stat(personaPath)
		_, instrErr := os.Stat(instrPath)
		if personaErr == nil && instrErr == nil {
			continue
		}

		if _, _, err := aqueduct.ScaffoldCataractaeDir(cataractaeDir, id); err != nil {
			return fmt.Errorf("scaffold %s: %w", id, err)
		}
	}
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
