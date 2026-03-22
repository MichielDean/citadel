package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/spf13/cobra"
)

var cataractaeCmd = &cobra.Command{
	Use:   "cataractae",
	Short: "Manage cataractae definitions",
}

// --- cataractae add ---

var cataractaeAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Scaffold a new cataractae directory with PERSONA.md and INSTRUCTIONS.md templates",
	Args:  cobra.ExactArgs(1),
	RunE:  runCataractaeAdd,
}

func runCataractaeAdd(cmd *cobra.Command, args []string) error {
	name := args[0]

	wfPath, err := resolveWorkflowPath()
	if err != nil {
		return err
	}

	// Derive cataractae dir from the workflow location (same as generate).
	cataractaeDir := cisternCataractaeDir(wfPath)

	// Scaffold PERSONA.md and INSTRUCTIONS.md.
	personaPath, instrPath, err := aqueduct.ScaffoldCataractaeDir(cataractaeDir, name)
	if err != nil {
		return fmt.Errorf("scaffold cataractae: %w", err)
	}

	fmt.Printf("Created: %s\n", personaPath)
	fmt.Printf("Created: %s\n", instrPath)
	fmt.Printf("\nEdit PERSONA.md and INSTRUCTIONS.md, then run: ct cataractae generate\n")
	return nil
}

// --- roles generate ---

var cataractaeGenerateWorkflow string

var cataractaeGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate CLAUDE.md files from cataractae PERSONA.md and INSTRUCTIONS.md",
	RunE:  runCataractaeGenerate,
}

func runCataractaeGenerate(cmd *cobra.Command, args []string) error {
	wfPath, err := resolveWorkflowPath()
	if err != nil {
		return err
	}

	w, err := aqueduct.ParseWorkflow(wfPath)
	if err != nil {
		return fmt.Errorf("parse aqueduct: %w", err)
	}

	// Derive cataractae dir from the workflow location: <repo>/cataractae/ sits one level
	// above the aqueduct dir that contains the workflow file.
	cataractaeDir := cisternCataractaeDir(wfPath)
	written, err := aqueduct.GenerateCataractaeFiles(w, cataractaeDir)
	if err != nil {
		return err
	}

	if len(written) == 0 {
		fmt.Println("no cataractae with PERSONA.md and INSTRUCTIONS.md found")
		return nil
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
	Short: "List all agent identities in the workflow",
	RunE:  runCataractaeList,
}

func runCataractaeList(cmd *cobra.Command, args []string) error {
	wfPath, err := resolveWorkflowPath()
	if err != nil {
		return err
	}

	w, err := aqueduct.ParseWorkflow(wfPath)
	if err != nil {
		return fmt.Errorf("parse aqueduct: %w", err)
	}

	identities := w.UniqueIdentities()
	if len(identities) == 0 {
		fmt.Println("no agent identities defined in workflow steps")
		return nil
	}

	cataractaeDir := cisternCataractaeDir(wfPath)
	for _, id := range identities {
		displayName := readPersonaName(filepath.Join(cataractaeDir, id, "PERSONA.md"), id)
		fmt.Printf("  %-20s %-40s → ct cataractae edit %s\n", id, displayName, id)
	}
	return nil
}

// readPersonaName reads the "# Role: <Name>" from the first line of PERSONA.md.
// Falls back to TitleCaseName(id) if the file cannot be read or has no such header.
func readPersonaName(personaPath, id string) string {
	data, err := os.ReadFile(personaPath)
	if err != nil {
		return aqueduct.TitleCaseName(id)
	}
	firstLine := strings.SplitN(string(data), "\n", 2)[0]
	if strings.HasPrefix(firstLine, "# Role: ") {
		return strings.TrimPrefix(firstLine, "# Role: ")
	}
	return aqueduct.TitleCaseName(id)
}

// --- roles edit ---

var cataractaeEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit a cataractae's INSTRUCTIONS.md in $EDITOR and regenerate CLAUDE.md",
	RunE:  runCataractaeEdit,
}

func runCataractaeEdit(cmd *cobra.Command, args []string) error {
	wfPath, err := resolveWorkflowPath()
	if err != nil {
		return err
	}

	w, err := aqueduct.ParseWorkflow(wfPath)
	if err != nil {
		return fmt.Errorf("parse aqueduct: %w", err)
	}

	identities := w.UniqueIdentities()
	if len(identities) == 0 {
		fmt.Println("no agent identities defined in workflow steps")
		return nil
	}

	// Print numbered list.
	fmt.Println("Select a role to edit:")
	for i, id := range identities {
		fmt.Printf("  %d. %s\n", i+1, id)
	}
	fmt.Print("\nEnter number: ")

	var input string
	fmt.Scanln(&input)
	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(identities) {
		return fmt.Errorf("invalid selection: %q", input)
	}

	selectedKey := identities[idx-1]
	cataractaeDir := cisternCataractaeDir(wfPath)
	instrPath := filepath.Join(cataractaeDir, selectedKey, "INSTRUCTIONS.md")

	// Open in $EDITOR.
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	editorCmd := exec.Command(editor, instrPath)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr
	if err := editorCmd.Run(); err != nil {
		return fmt.Errorf("editor: %w", err)
	}

	// Regenerate CLAUDE.md.
	written, err := aqueduct.GenerateCataractaeFiles(w, cataractaeDir)
	if err != nil {
		return err
	}

	fmt.Printf("\nRegenerated:\n")
	for _, path := range written {
		fmt.Printf("  %s\n", path)
	}
	return nil
}

// resolveWorkflowPath returns the workflow YAML path, either from the
// --workflow flag or by reading the first repo in the aqueduct config.
func resolveWorkflowPath() (string, error) {
	if cataractaeGenerateWorkflow != "" {
		return cataractaeGenerateWorkflow, nil
	}
	cfgPath := resolveConfigPath()
	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}
	if len(cfg.Repos) == 0 {
		return "", fmt.Errorf("no repos configured")
	}
	wfPath := cfg.Repos[0].WorkflowPath
	if !filepath.IsAbs(wfPath) {
		wfPath = filepath.Join(filepath.Dir(cfgPath), wfPath)
	}
	return wfPath, nil
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

// cisternCataractaeDir returns the cataractae directory derived from a workflow file path.
// The cataractae directory lives one level above the aqueduct directory containing the workflow.
func cisternCataractaeDir(wfPath string) string {
	return filepath.Clean(filepath.Join(filepath.Dir(wfPath), "..", "cataractae"))
}

func init() {
	cataractaeGenerateCmd.Flags().StringVar(&cataractaeGenerateWorkflow, "workflow", "", "path to workflow YAML file")
	cataractaeListCmd.Flags().StringVar(&cataractaeGenerateWorkflow, "workflow", "", "path to workflow YAML file")
	cataractaeEditCmd.Flags().StringVar(&cataractaeGenerateWorkflow, "workflow", "", "path to workflow YAML file")
	cataractaeAddCmd.Flags().StringVar(&cataractaeGenerateWorkflow, "workflow", "", "path to workflow YAML file")

	cataractaeCmd.AddCommand(cataractaeGenerateCmd, cataractaeListCmd, cataractaeEditCmd, cataractaeStatusCmd, cataractaeAddCmd)
	rootCmd.AddCommand(cataractaeCmd)
}
