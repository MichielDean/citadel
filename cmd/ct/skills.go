package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/skills"
	"github.com/spf13/cobra"
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage AgentSkills declared in cataractae",
}

// skillsWorkflow is shared across skills subcommands to optionally override the workflow path.
var skillsWorkflow string

// --- skills list ---

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List skills declared in cataractae (across all workflows)",
	RunE:  runSkillsList,
}

func runSkillsList(cmd *cobra.Command, args []string) error {
	wfPaths, err := resolveWorkflowPaths()
	if err != nil {
		return err
	}

	type entry struct {
		cataracta string
		skill     aqueduct.SkillRef
	}

	var entries []entry
	for _, wfPath := range wfPaths {
		w, err := aqueduct.ParseWorkflow(wfPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: parse %s: %v\n", wfPath, err)
			continue
		}
		for _, cat := range w.Cataractae {
			for _, sk := range cat.Skills {
				entries = append(entries, entry{cataracta: cat.Name, skill: sk})
			}
		}
	}

	if len(entries) == 0 {
		fmt.Println("no skills declared in cataractae")
		return nil
	}

	for _, e := range entries {
		cached := ""
		if _, err := os.Stat(skills.CachePath(e.skill.Name)); err == nil {
			cached = " [cached]"
		}
		fmt.Printf("  %-20s %-30s %s%s\n", e.cataracta, e.skill.Name, e.skill.URL, cached)
	}
	return nil
}

// --- skills install ---

var skillsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Pre-download all referenced skills to the local cache",
	RunE:  runSkillsInstall,
}

func runSkillsInstall(cmd *cobra.Command, args []string) error {
	refs, err := collectAllSkillRefs()
	if err != nil {
		return err
	}

	if len(refs) == 0 {
		fmt.Println("no skills to install")
		return nil
	}

	for _, sk := range refs {
		if err := skills.Install(sk.Name, sk.URL); err != nil {
			fmt.Fprintf(os.Stderr, "error: install %s: %v\n", sk.Name, err)
			continue
		}
		fmt.Printf("installed %s → %s\n", sk.Name, skills.CachePath(sk.Name))
	}
	return nil
}

// --- skills update ---

var skillsUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Re-fetch all skills, ignoring the local cache",
	RunE:  runSkillsUpdate,
}

func runSkillsUpdate(cmd *cobra.Command, args []string) error {
	refs, err := collectAllSkillRefs()
	if err != nil {
		return err
	}

	if len(refs) == 0 {
		fmt.Println("no skills to update")
		return nil
	}

	for _, sk := range refs {
		if err := skills.ForceUpdate(sk.Name, sk.URL); err != nil {
			fmt.Fprintf(os.Stderr, "error: update %s: %v\n", sk.Name, err)
			continue
		}
		fmt.Printf("updated %s → %s\n", sk.Name, skills.CachePath(sk.Name))
	}
	return nil
}

// --- helpers ---

// resolveWorkflowPaths returns workflow file paths to scan. If --workflow is
// set, only that path is returned; otherwise all workflows from the config.
func resolveWorkflowPaths() ([]string, error) {
	if skillsWorkflow != "" {
		return []string{skillsWorkflow}, nil
	}

	cfgPath := resolveConfigPath()
	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	cfgDir := filepath.Dir(cfgPath)
	var paths []string
	for _, repo := range cfg.Repos {
		wfPath := repo.WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(cfgDir, wfPath)
		}
		paths = append(paths, wfPath)
	}
	return paths, nil
}

// collectAllSkillRefs returns all unique SkillRefs declared across all cataractae.
// If the same skill name appears with different URLs, the first URL wins and a
// warning is printed to stderr.
func collectAllSkillRefs() ([]aqueduct.SkillRef, error) {
	wfPaths, err := resolveWorkflowPaths()
	if err != nil {
		return nil, err
	}

	seenURL := map[string]string{} // name -> first URL seen
	var refs []aqueduct.SkillRef
	for _, wfPath := range wfPaths {
		w, err := aqueduct.ParseWorkflow(wfPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: parse %s: %v\n", wfPath, err)
			continue
		}
		for _, cat := range w.Cataractae {
			for _, sk := range cat.Skills {
				if existingURL, seen := seenURL[sk.Name]; seen {
					if existingURL != sk.URL {
						fmt.Fprintf(os.Stderr, "warning: skill %q has conflicting URLs: %q vs %q; using first\n",
							sk.Name, existingURL, sk.URL)
					}
				} else {
					seenURL[sk.Name] = sk.URL
					refs = append(refs, sk)
				}
			}
		}
	}
	return refs, nil
}

func init() {
	skillsListCmd.Flags().StringVar(&skillsWorkflow, "workflow", "", "path to workflow YAML file")
	skillsInstallCmd.Flags().StringVar(&skillsWorkflow, "workflow", "", "path to workflow YAML file")
	skillsUpdateCmd.Flags().StringVar(&skillsWorkflow, "workflow", "", "path to workflow YAML file")

	skillsCmd.AddCommand(skillsListCmd, skillsInstallCmd, skillsUpdateCmd)
	rootCmd.AddCommand(skillsCmd)
}
