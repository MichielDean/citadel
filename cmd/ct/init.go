package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/spf13/cobra"
)

//go:embed assets/cistern.yaml
var defaultCisternConfig []byte

//go:embed assets/aqueduct/aqueduct.yaml
var defaultAqueductWorkflow []byte

//go:embed assets/start-castellarius.sh
var defaultStartCastellarius []byte

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
	if _, err := aqueduct.GenerateCataractaeFiles(w, cataractaeDir, ""); err != nil {
		return fmt.Errorf("generate cataractae: %w", err)
	}

	// 5. Create ~/.cistern/env credential file (chmod 600) if absent.
	envFilePath := filepath.Join(cisternDir, "env")
	if err := fixCisternEnvFile(envFilePath); err != nil {
		return fmt.Errorf("create env file: %w", err)
	}

	// 6. Add "env" to ~/.cistern/.gitignore so the credential file is never committed.
	gitignorePath := filepath.Join(cisternDir, ".gitignore")
	if err := addLineToGitignore(gitignorePath, "env"); err != nil {
		return fmt.Errorf("update .gitignore: %w", err)
	}

	// 7. Write start-castellarius.sh from embedded template.
	startScriptDst := filepath.Join(cisternDir, "start-castellarius.sh")
	if err := writeFileIfAbsent(startScriptDst, defaultStartCastellarius, initForce); err != nil {
		return err
	}
	if err := os.Chmod(startScriptDst, 0o755); err != nil {
		return fmt.Errorf("chmod start-castellarius.sh: %w", err)
	}

	// 8. Print next-steps message.
	fmt.Printf(`Cistern initialized.
  Config          : ~/.cistern/cistern.yaml
  Aqueduct        : ~/.cistern/aqueduct/aqueduct.yaml
  Cataractae      : ~/.cistern/cataractae/
  Credentials     : ~/.cistern/env  (chmod 600)
  Startup script  : ~/.cistern/start-castellarius.sh

Next:
  1. Edit ~/.cistern/cistern.yaml — add your repos
  2. Add your credentials to ~/.cistern/env:
       echo 'ANTHROPIC_API_KEY=sk-ant-...' >> ~/.cistern/env
       chmod 600 ~/.cistern/env
  3. ct droplet add --title "Your first droplet" --repo yourrepo
  4. ct castellarius start
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

// addLineToGitignore appends line to the file at gitignorePath, creating the
// file if necessary. If line is already present the file is not modified.
func addLineToGitignore(gitignorePath, line string) error {
	raw, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", gitignorePath, err)
	}
	content := string(raw)
	for _, existing := range strings.Split(content, "\n") {
		if strings.TrimSpace(existing) == line {
			return nil // already present
		}
	}
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", gitignorePath, err)
	}
	defer f.Close()
	// Ensure the new line starts on its own line.
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		if _, err := fmt.Fprint(f, "\n"); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(f, line)
	return err
}

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "overwrite existing files")
	rootCmd.AddCommand(initCmd)
}
