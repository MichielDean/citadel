package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/skills"
	"github.com/spf13/cobra"
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage locally installed skills for cataractae",
	Long: `Skills extend cataractae with reusable instructions and protocols.

Skills are stored locally in ~/.cistern/skills/<name>/SKILL.md — that is the
only place the runtime reads from. Install skills explicitly before use; the
Castellarius never fetches skills automatically during agent spawn.

To use a skill in a cataractae, add it to the cataractae's skills: list in your
aqueduct YAML, then run ct cataractae generate to rebuild CLAUDE.md files.

In-repo skills (located under skills/ in the repository) are deployed
automatically into ~/.cistern/skills/ by the git_sync drought hook.`,
}

// --- ct skills install <name> <url> ---

var skillsInstallCmd = &cobra.Command{
	Use:   "install <name> <url>",
	Short: "Install a skill from a URL into the local skill store",
	Long: `Download a SKILL.md from <url> and save it to ~/.cistern/skills/<name>/SKILL.md.

The URL can be any publicly accessible SKILL.md — from SkillsMP, GitHub,
or any direct link. Once installed, reference the skill by name only in
your aqueduct YAML. GH_TOKEN is injected automatically if set.

  ct skills install github-workflow https://raw.githubusercontent.com/callstackincubator/agent-skills/main/skills/github/SKILL.md
  ct skills install security-audit  https://skillsmp.com/skills/security-audit/SKILL.md`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, url := args[0], args[1]
		if err := skills.Install(name, url); err != nil {
			return err
		}
		fmt.Printf("installed %s\n  source : %s\n  path   : %s\n", name, url, skills.LocalPath(name))
		return nil
	},
}

// --- ct skills update [name] ---

var skillsUpdateCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Re-fetch a skill (or all skills) from their source URLs",
	Long: `Re-download a skill from its recorded source URL, overwriting the local copy.
If no name is given, all skills in the manifest are updated.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := skills.ListInstalled()
		if err != nil {
			return fmt.Errorf("reading manifest: %w", err)
		}
		if len(entries) == 0 {
			fmt.Println("no skills installed")
			return nil
		}

		// Filter to named skill if specified.
		if len(args) == 1 {
			name := args[0]
			found := false
			for _, e := range entries {
				if e.Name == name {
					entries = []skills.ManifestEntry{e}
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("skill %q not found in manifest — install it first with ct skills install", name)
			}
		}

		for _, e := range entries {
			if e.SourceURL == "" {
				fmt.Fprintf(os.Stderr, "skip %s: no source URL recorded\n", e.Name)
				continue
			}
			if e.SourceURL == "local" {
				fmt.Fprintf(os.Stderr, "skip %s: managed by git_sync (run drought hook to update)\n", e.Name)
				continue
			}
			if err := skills.Update(e.Name, e.SourceURL); err != nil {
				fmt.Fprintf(os.Stderr, "error updating %s: %v\n", e.Name, err)
				continue
			}
			fmt.Printf("updated %s\n", e.Name)
		}
		return nil
	},
}

// --- ct skills remove <name> ---

var skillsRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an installed skill from the local store",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := skills.Remove(name); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", name)
		return nil
	},
}

// --- ct skills list ---

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List locally installed skills and which cataractae reference them",
	RunE:  runSkillsList,
}

func runSkillsList(cmd *cobra.Command, args []string) error {
	installed, _ := skills.ListInstalled()
	installedMap := map[string]skills.ManifestEntry{}
	for _, e := range installed {
		installedMap[e.Name] = e
	}

	// Collect all skill refs from configured workflows.
	wfPaths, err := resolveWorkflowPaths()
	if err != nil {
		// Non-fatal: show installed skills even if config is missing.
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	type usage struct{ cataractae []string }
	usedBy := map[string]*usage{}
	for _, wfPath := range wfPaths {
		w, wErr := aqueduct.ParseWorkflow(wfPath)
		if wErr != nil {
			fmt.Fprintf(os.Stderr, "warning: parse %s: %v\n", wfPath, wErr)
			continue
		}
		for _, cat := range w.Cataractae {
			for _, sk := range cat.Skills {
				if usedBy[sk.Name] == nil {
					usedBy[sk.Name] = &usage{}
				}
				usedBy[sk.Name].cataractae = append(usedBy[sk.Name].cataractae, cat.Name)
			}
		}
	}

	// Merge: skills installed but not referenced, and referenced but not installed.
	allNames := map[string]bool{}
	for _, e := range installed {
		allNames[e.Name] = true
	}
	for name := range usedBy {
		allNames[name] = true
	}

	if len(allNames) == 0 {
		fmt.Println("no skills installed")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tUSED BY\tDESCRIPTION\tSOURCE")
	fmt.Fprintln(tw, "────\t──────\t───────\t───────────\t──────")
	for name := range allNames {
		entry, isInstalled := installedMap[name]
		var statusStr string
		if isInstalled {
			statusStr = col(colorGreen, "✓") + " installed"
		} else {
			statusStr = col(colorRed, "✗") + " missing"
		}
		used := "—"
		if u := usedBy[name]; u != nil {
			used = ""
			for i, c := range u.cataractae {
				if i > 0 {
					used += ", "
				}
				used += c
			}
		}
		desc := skillDesc(skills.LocalPath(name))
		if desc == "" {
			desc = "—"
		}
		source := entry.SourceURL
		if source == "" {
			source = "—"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", name, statusStr, used, desc, source)
	}
	tw.Flush()
	return nil
}

// --- helpers ---

// skillsWorkflow is shared across skills subcommands to optionally override the workflow path.
var skillsWorkflow string

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

func init() {
	skillsListCmd.Flags().StringVar(&skillsWorkflow, "workflow", "", "path to aqueduct YAML file")
	skillsInstallCmd.Flags().StringVar(&skillsWorkflow, "workflow", "", "path to aqueduct YAML file")
	skillsUpdateCmd.Flags().StringVar(&skillsWorkflow, "workflow", "", "path to aqueduct YAML file")

	skillsCmd.AddCommand(skillsListCmd, skillsInstallCmd, skillsUpdateCmd, skillsRemoveCmd)
	rootCmd.AddCommand(skillsCmd)
}
