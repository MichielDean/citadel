package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/MichielDean/citadel/internal/workflow"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var rolesCmd = &cobra.Command{
	Use:   "roles",
	Short: "Manage agent role definitions",
}

// --- roles generate ---

var rolesGenerateWorkflow string

var rolesGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate CLAUDE.md files from workflow role definitions",
	RunE:  runRolesGenerate,
}

func runRolesGenerate(cmd *cobra.Command, args []string) error {
	wfPath := rolesGenerateWorkflow
	if wfPath == "" {
		// Try to find workflow from config.
		cfgPath := resolveConfigPath()
		cfg, err := workflow.ParseFarmConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("loading config: %w (use --workflow to specify a workflow file directly)", err)
		}
		if len(cfg.Repos) == 0 {
			return fmt.Errorf("no repos configured")
		}
		// Use the first repo's workflow.
		wfPath = cfg.Repos[0].WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(filepath.Dir(cfgPath), wfPath)
		}
	}

	w, err := workflow.ParseWorkflow(wfPath)
	if err != nil {
		return fmt.Errorf("parse workflow: %w", err)
	}

	if len(w.Roles) == 0 {
		fmt.Println("no roles defined in workflow")
		return nil
	}

	rolesDir := citadelRolesDir()
	written, err := workflow.GenerateRoleFiles(w, rolesDir)
	if err != nil {
		return err
	}

	for _, path := range written {
		fmt.Printf("wrote %s\n", path)
	}
	fmt.Printf("\n%d role(s) generated in %s\n", len(written), rolesDir)
	return nil
}

// --- roles list ---

var rolesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all roles defined in the workflow YAML",
	RunE:  runRolesList,
}

func runRolesList(cmd *cobra.Command, args []string) error {
	wfPath := rolesGenerateWorkflow
	if wfPath == "" {
		cfgPath := resolveConfigPath()
		cfg, err := workflow.ParseFarmConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if len(cfg.Repos) == 0 {
			return fmt.Errorf("no repos configured")
		}
		wfPath = cfg.Repos[0].WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(filepath.Dir(cfgPath), wfPath)
		}
	}

	w, err := workflow.ParseWorkflow(wfPath)
	if err != nil {
		return fmt.Errorf("parse workflow: %w", err)
	}

	if len(w.Roles) == 0 {
		fmt.Println("no roles defined in workflow")
		return nil
	}

	// Sort keys for stable output.
	keys := make([]string, 0, len(w.Roles))
	for k := range w.Roles {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		role := w.Roles[k]
		desc := role.Description
		if len(desc) > 70 {
			desc = desc[:67] + "..."
		}
		fmt.Printf("  %-20s %s\n", k, desc)
	}
	return nil
}

// --- roles edit ---

var rolesEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit a role's instructions in $EDITOR",
	RunE:  runRolesEdit,
}

func runRolesEdit(cmd *cobra.Command, args []string) error {
	wfPath := rolesGenerateWorkflow
	if wfPath == "" {
		cfgPath := resolveConfigPath()
		cfg, err := workflow.ParseFarmConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if len(cfg.Repos) == 0 {
			return fmt.Errorf("no repos configured")
		}
		wfPath = cfg.Repos[0].WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(filepath.Dir(cfgPath), wfPath)
		}
	}

	// Read the raw YAML to preserve structure.
	data, err := os.ReadFile(wfPath)
	if err != nil {
		return fmt.Errorf("read workflow: %w", err)
	}

	w, err := workflow.ParseWorkflow(wfPath)
	if err != nil {
		return fmt.Errorf("parse workflow: %w", err)
	}

	if len(w.Roles) == 0 {
		fmt.Println("no roles defined in workflow")
		return nil
	}

	// Sort keys for stable ordering.
	keys := make([]string, 0, len(w.Roles))
	for k := range w.Roles {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Print numbered list.
	fmt.Println("Select a role to edit:")
	for i, k := range keys {
		fmt.Printf("  %d. %s — %s\n", i+1, k, w.Roles[k].Name)
	}
	fmt.Print("\nEnter number: ")

	var input string
	fmt.Scanln(&input)
	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(keys) {
		return fmt.Errorf("invalid selection: %q", input)
	}

	selectedKey := keys[idx-1]
	role := w.Roles[selectedKey]

	// Write instructions to temp file.
	tmpFile, err := os.CreateTemp("", "citadel-role-*.md")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(role.Instructions); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	// Open in $EDITOR.
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	editorCmd := exec.Command(editor, tmpPath)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr
	if err := editorCmd.Run(); err != nil {
		return fmt.Errorf("editor: %w", err)
	}

	// Read back edited content.
	edited, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("read edited file: %w", err)
	}

	// Update role in the workflow.
	role.Instructions = string(edited)
	w.Roles[selectedKey] = role

	// Re-parse the raw data into a generic structure to preserve
	// non-role fields, then update roles and re-serialize.
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse raw yaml: %w", err)
	}
	raw["roles"] = w.Roles

	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	if err := os.WriteFile(wfPath, out, 0o644); err != nil {
		return fmt.Errorf("write workflow: %w", err)
	}

	// Regenerate CLAUDE.md.
	rolesDir := citadelRolesDir()
	written, err := workflow.GenerateRoleFiles(w, rolesDir)
	if err != nil {
		return err
	}

	fmt.Printf("\nUpdated %s and regenerated:\n", wfPath)
	for _, path := range written {
		fmt.Printf("  %s\n", path)
	}
	return nil
}

// citadelRolesDir returns ~/.citadel/roles.
func citadelRolesDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "roles")
	}
	return filepath.Join(home, ".citadel", "roles")
}

func init() {
	rolesGenerateCmd.Flags().StringVar(&rolesGenerateWorkflow, "workflow", "", "path to workflow YAML file")
	rolesListCmd.Flags().StringVar(&rolesGenerateWorkflow, "workflow", "", "path to workflow YAML file")
	rolesEditCmd.Flags().StringVar(&rolesGenerateWorkflow, "workflow", "", "path to workflow YAML file")

	rolesCmd.AddCommand(rolesGenerateCmd, rolesListCmd, rolesEditCmd)
	rootCmd.AddCommand(rolesCmd)
}
