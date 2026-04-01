package main

import (
	"fmt"
	"strings"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/tracker"
	"github.com/spf13/cobra"
)

var (
	importRepo       string
	importFilter     bool
	importPriority   int
	importComplexity string
)

var importCmd = &cobra.Command{
	Use:   "import <provider> <issue-key>",
	Short: "Import an issue from an external tracker as a droplet",
	Long: `ct import fetches an issue from an external tracker (e.g. Jira) and
files it as a droplet in the cistern.

The provider name must match a registered TrackerProvider (e.g. "jira") and a
matching entry in the trackers section of cistern.yaml.

Examples:
  ct import jira PROJ-123 --repo myrepo
  ct import jira PROJ-456 --repo myrepo --filter
  ct import jira PROJ-789 --repo myrepo --priority 1 --complexity full`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		providerName := args[0]
		issueKey := args[1]

		if importRepo == "" {
			return fmt.Errorf("--repo is required")
		}
		repo, err := resolveCanonicalRepo(importRepo)
		if err != nil {
			return err
		}

		cfg, err := loadTrackerConfig(providerName)
		if err != nil {
			return err
		}

		constructor, ok := tracker.Resolve(providerName)
		if !ok {
			return fmt.Errorf("unknown tracker provider %q — no constructor registered", providerName)
		}
		tp, err := constructor(cfg)
		if err != nil {
			return fmt.Errorf("init tracker %s: %w", providerName, err)
		}

		issue, err := tp.FetchIssue(issueKey)
		if err != nil {
			return fmt.Errorf("fetch %s/%s: %w", providerName, issueKey, err)
		}

		priority := issue.Priority
		if cmd.Flags().Changed("priority") {
			priority = importPriority
		}

		cx, err := parseComplexity(importComplexity)
		if err != nil {
			return err
		}

		externalRef := providerName + ":" + issueKey

		if importFilter {
			// Run LLM filtration pass on the fetched title and description.
			userPrompt := "Title: " + issue.Title
			if issue.Description != "" {
				userPrompt += "\nDescription: " + issue.Description
			}
			preset := resolveFilterPreset(repo)
			proposals, err := runNonInteractive(preset, filterSystemPrompt, userPrompt)
			if err != nil {
				return err
			}
			c, err := cistern.New(resolveDBPath(), inferPrefix(repo))
			if err != nil {
				return err
			}
			defer c.Close()
			for _, p := range proposals {
				cx := complexityToInt(p.Complexity)
				item, err := c.AddDroplet(repo, p.Title, p.Description, externalRef, priority, cx)
				if err != nil {
					return fmt.Errorf("add droplet: %w", err)
				}
				fmt.Println(item.ID)
			}
			return nil
		}

		// Direct path: add the droplet immediately with the external reference.
		c, err := cistern.New(resolveDBPath(), inferPrefix(repo))
		if err != nil {
			return err
		}
		defer c.Close()

		item, err := c.AddDroplet(repo, issue.Title, issue.Description, externalRef, priority, cx)
		if err != nil {
			return fmt.Errorf("add droplet: %w", err)
		}
		fmt.Println(item.ID)
		return nil
	},
}

// loadTrackerConfig reads cistern.yaml and returns the TrackerConfig for the
// named provider. When the config cannot be loaded or the provider has no
// entry, an empty config with only the Name set is returned so the provider
// can still be constructed (it will validate its own required fields).
func loadTrackerConfig(providerName string) (tracker.TrackerConfig, error) {
	cfgPath := resolveConfigPath()
	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		// No config — return bare config; provider validates its own requirements.
		return tracker.TrackerConfig{Name: providerName}, nil
	}
	for _, tc := range cfg.Trackers {
		if strings.EqualFold(tc.Name, providerName) {
			return tc, nil
		}
	}
	// Provider not in trackers section — return bare config.
	return tracker.TrackerConfig{Name: providerName}, nil
}

func init() {
	importCmd.Flags().StringVar(&importRepo, "repo", "", "target repository (required)")
	importCmd.Flags().BoolVar(&importFilter, "filter", false, "run LLM filtration pass before filing")
	importCmd.Flags().IntVar(&importPriority, "priority", 2, "override the priority mapped from the tracker")
	importCmd.Flags().StringVarP(&importComplexity, "complexity", "x", "1", "droplet complexity: 1/standard (default), 2/full, 3/critical")
	_ = importCmd.MarkFlagRequired("repo")
	rootCmd.AddCommand(importCmd)
}
