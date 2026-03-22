package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var cataractaeCmd = &cobra.Command{
	Use:   "cataractae",
	Short: "Manage cataractae definitions",
}

// --- roles generate ---

var cataractaeGenerateWorkflow string

var cataractaeGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate CLAUDE.md files from cataractae definitions",
	RunE:  runCataractaeGenerate,
}

func runCataractaeGenerate(cmd *cobra.Command, args []string) error {
	wfPath := cataractaeGenerateWorkflow
	if wfPath == "" {
		// Try to find workflow from config.
		cfgPath := resolveConfigPath()
		cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("loading config: %w (use --workflow to specify an aqueduct file directly)", err)
		}
		if len(cfg.Repos) == 0 {
			return fmt.Errorf("no repos configured")
		}
		// Use the first repo's aqueduct.
		wfPath = cfg.Repos[0].WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(filepath.Dir(cfgPath), wfPath)
		}
	}

	w, err := aqueduct.ParseWorkflow(wfPath)
	if err != nil {
		return fmt.Errorf("parse aqueduct: %w", err)
	}

	if len(w.CataractaeDefinitions) == 0 {
		fmt.Println("no cataractae_definitions defined in aqueduct")
		return nil
	}

	// Derive cataractae dir from the workflow location: <repo>/cataractae/ sits one level
	// above the aqueduct dir that contains the workflow file.
	cataractaeDir := filepath.Clean(filepath.Join(filepath.Dir(wfPath), "..", "cataractae"))
	written, err := aqueduct.GenerateCataractaeFiles(w, cataractaeDir)
	if err != nil {
		return err
	}

	for _, path := range written {
		fmt.Printf("wrote %s\n", path)
	}
	fmt.Printf("\n%d role(s) generated in %s\n", len(written), cataractaeDir)
	return nil
}

// --- roles list ---

var cataractaeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cataractae definitions in the aqueduct",
	RunE:  runCataractaeList,
}

func runCataractaeList(cmd *cobra.Command, args []string) error {
	wfPath := cataractaeGenerateWorkflow
	if wfPath == "" {
		cfgPath := resolveConfigPath()
		cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
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

	w, err := aqueduct.ParseWorkflow(wfPath)
	if err != nil {
		return fmt.Errorf("parse aqueduct: %w", err)
	}

	if len(w.CataractaeDefinitions) == 0 {
		fmt.Println("no cataractae_definitions defined in aqueduct")
		return nil
	}

	// Sort keys for stable output.
	keys := make([]string, 0, len(w.CataractaeDefinitions))
	for k := range w.CataractaeDefinitions {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		role := w.CataractaeDefinitions[k]
		desc := role.Description
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
		fmt.Printf("  %-20s %-40s \u2192 ct cataractae edit %s\n", k, desc, k)
	}
	return nil
}

// --- roles edit ---

var cataractaeEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit a cataractae definition's instructions in $EDITOR",
	RunE:  runCataractaeEdit,
}

func runCataractaeEdit(cmd *cobra.Command, args []string) error {
	wfPath := cataractaeGenerateWorkflow
	if wfPath == "" {
		cfgPath := resolveConfigPath()
		cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
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
		return fmt.Errorf("read aqueduct: %w", err)
	}

	w, err := aqueduct.ParseWorkflow(wfPath)
	if err != nil {
		return fmt.Errorf("parse aqueduct: %w", err)
	}

	if len(w.CataractaeDefinitions) == 0 {
		fmt.Println("no cataractae_definitions defined in aqueduct")
		return nil
	}

	// Sort keys for stable ordering.
	keys := make([]string, 0, len(w.CataractaeDefinitions))
	for k := range w.CataractaeDefinitions {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Print numbered list.
	fmt.Println("Select a role to edit:")
	for i, k := range keys {
		fmt.Printf("  %d. %s — %s\n", i+1, k, w.CataractaeDefinitions[k].Name)
	}
	fmt.Print("\nEnter number: ")

	var input string
	fmt.Scanln(&input)
	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(keys) {
		return fmt.Errorf("invalid selection: %q", input)
	}

	selectedKey := keys[idx-1]
	role := w.CataractaeDefinitions[selectedKey]

	// Write instructions to temp file.
	tmpFile, err := os.CreateTemp("", "cistern-role-*.md")
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

	// Update role in the aqueduct.
	role.Instructions = string(edited)
	w.CataractaeDefinitions[selectedKey] = role

	// Re-parse the raw data into a generic structure to preserve
	// non-role fields, then update roles and re-serialize.
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse raw yaml: %w", err)
	}
	raw["cataractae"] = w.CataractaeDefinitions

	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	if err := os.WriteFile(wfPath, out, 0o644); err != nil {
		return fmt.Errorf("write aqueduct: %w", err)
	}

	// Regenerate CLAUDE.md.
	cataractaeDir := cisternCataractaeDir()
	written, err := aqueduct.GenerateCataractaeFiles(w, cataractaeDir)
	if err != nil {
		return err
	}

	fmt.Printf("\nUpdated %s and regenerated:\n", wfPath)
	for _, path := range written {
		fmt.Printf("  %s\n", path)
	}
	return nil
}

// --- roles reset ---

var cataractaeResetCmd = &cobra.Command{
	Use:   "reset [role]",
	Short: "Restore a cataractae definition to its built-in default (with confirmation)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runCataractaeReset,
}

func runCataractaeReset(cmd *cobra.Command, args []string) error {
	wfPath := cataractaeGenerateWorkflow
	if wfPath == "" {
		cfgPath := resolveConfigPath()
		cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
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
		return fmt.Errorf("read aqueduct: %w", err)
	}

	w, err := aqueduct.ParseWorkflow(wfPath)
	if err != nil {
		return fmt.Errorf("parse aqueduct: %w", err)
	}

	cataractaeDir := cisternCataractaeDir()

	if len(args) == 1 {
		// Reset a single role.
		roleName := args[0]
		builtin, ok := aqueduct.BuiltinCataractaeDefinitions[roleName]
		if !ok {
			return fmt.Errorf("no built-in default for role %q", roleName)
		}

		fmt.Printf("Reset %s to built-in default? [y/N] ", roleName)
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		if input != "y" && input != "yes" {
			fmt.Println("aborted")
			return nil
		}

		// Update in aqueduct.
		role := w.CataractaeDefinitions[roleName]
		role.Name = builtin.Name
		role.Description = builtin.Description
		role.Instructions = builtin.Instructions
		w.CataractaeDefinitions[roleName] = role

		if err := writeWorkflowCataractaeDefinitions(wfPath, data, w); err != nil {
			return err
		}

		written, err := aqueduct.GenerateCataractaeFiles(w, cataractaeDir)
		if err != nil {
			return err
		}
		for _, path := range written {
			if strings.Contains(path, roleName) {
				fmt.Printf("Drop %s back to source. %s refreshed.\n", roleName, path)
			}
		}
		return nil
	}

	// No arg — list all resettable roles and prompt for all.
	resettable := make([]string, 0)
	for k := range aqueduct.BuiltinCataractaeDefinitions {
		resettable = append(resettable, k)
	}
	sort.Strings(resettable)

	if len(resettable) == 0 {
		fmt.Println("no built-in defaults available")
		return nil
	}

	fmt.Println("Resettable roles:")
	for _, k := range resettable {
		b := aqueduct.BuiltinCataractaeDefinitions[k]
		fmt.Printf("  %-20s %s\n", k, b.Description)
	}
	fmt.Print("\nReset all to defaults? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	if input != "y" && input != "yes" {
		fmt.Println("aborted")
		return nil
	}

	if w.CataractaeDefinitions == nil {
		w.CataractaeDefinitions = make(map[string]aqueduct.CataractaeDefinition)
	}
	for _, k := range resettable {
		b := aqueduct.BuiltinCataractaeDefinitions[k]
		w.CataractaeDefinitions[k] = aqueduct.CataractaeDefinition{
			Name:         b.Name,
			Description:  b.Description,
			Instructions: b.Instructions,
		}
	}

	if err := writeWorkflowCataractaeDefinitions(wfPath, data, w); err != nil {
		return err
	}

	written, err := aqueduct.GenerateCataractaeFiles(w, cataractaeDir)
	if err != nil {
		return err
	}
	for _, path := range written {
		fmt.Printf("Drop back to source. %s refreshed.\n", path)
	}
	return nil
}

// writeWorkflowCataractaeDefinitions updates the cataractae_definitions section of an aqueduct YAML file.
func writeWorkflowCataractaeDefinitions(wfPath string, originalData []byte, w *aqueduct.Workflow) error {
	var raw map[string]interface{}
	if err := yaml.Unmarshal(originalData, &raw); err != nil {
		return fmt.Errorf("parse raw yaml: %w", err)
	}
	raw["cataractae"] = w.CataractaeDefinitions

	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	if err := os.WriteFile(wfPath, out, 0o644); err != nil {
		return fmt.Errorf("write aqueduct: %w", err)
	}
	return nil
}

// cisternCataractaeDir returns ~/.cistern/cataractae.
func cisternCataractaeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "cataractae")
	}
	return filepath.Join(home, ".cistern", "cataractae")
}

// --- cataractae status ---

// cataractaeStatusCmd shows which aqueducts are flowing and by
// which operator and droplet. This is the pipeline view — steps on the left,
// what's flowing through each on the right.
var cataractaeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show which cataractae are active — steps, operators, and droplets in flight",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath := resolveConfigPath()
		cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		dbPath := resolveDBPath()
		c, err := cistern.New(dbPath, "")
		if err != nil {
			return fmt.Errorf("cistern: %w", err)
		}
		defer c.Close()

		allItems, err := c.List("", "in_progress")
		if err != nil {
			return fmt.Errorf("list in-progress droplets: %w", err)
		}

		// Index in-progress items by current cataractae step (per repo).
		type stepKey struct{ repo, step string }
		active := map[stepKey]*cistern.Droplet{}
		for _, item := range allItems {
			active[stepKey{item.Repo, item.CurrentCataractae}] = item
		}

		cfgDir := filepath.Dir(cfgPath)
		for _, repo := range cfg.Repos {
			wfPath := repo.WorkflowPath
			if !filepath.IsAbs(wfPath) {
				wfPath = filepath.Join(cfgDir, wfPath)
			}
			wf, err := aqueduct.ParseWorkflow(wfPath)
			if err != nil {
				fmt.Printf("%s  (workflow could not be loaded: %v)\n\n", repo.Name, err)
				continue
			}

			fmt.Printf("%s  (%s)\n", repo.Name, wf.Name)
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			for i, step := range wf.Cataractae {
				marker := fmt.Sprintf("%d.", i+1)
				item, ok := active[stepKey{repo.Name, step.Name}]
				if ok {
					elapsed := int(time.Since(item.UpdatedAt).Minutes())
					fmt.Fprintf(tw, "  %s\t%-22s\t← %s\t(%s)\t%dm\n",
						marker, step.Name, item.ID, item.Assignee, elapsed)
				} else {
					fmt.Fprintf(tw, "  %s\t%-22s\t—\n", marker, step.Name)
				}
			}
			tw.Flush()
			fmt.Println()
		}
		return nil
	},
}

func init() {
	cataractaeGenerateCmd.Flags().StringVar(&cataractaeGenerateWorkflow, "workflow", "", "path to workflow YAML file")
	cataractaeListCmd.Flags().StringVar(&cataractaeGenerateWorkflow, "workflow", "", "path to workflow YAML file")
	cataractaeEditCmd.Flags().StringVar(&cataractaeGenerateWorkflow, "workflow", "", "path to workflow YAML file")

	cataractaeResetCmd.Flags().StringVar(&cataractaeGenerateWorkflow, "workflow", "", "path to workflow YAML file")

	cataractaeCmd.AddCommand(cataractaeGenerateCmd, cataractaeListCmd, cataractaeEditCmd, cataractaeResetCmd, cataractaeStatusCmd)
	rootCmd.AddCommand(cataractaeCmd)
}
