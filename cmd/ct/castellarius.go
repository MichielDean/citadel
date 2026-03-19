package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/cataracta"
	"github.com/MichielDean/cistern/internal/castellarius"
	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/spf13/cobra"
)

var configPath string

// ─────────────────────────────────────────────────────────────────────────────
// ct castellarius — manage the Castellarius (the aqueduct overseer)
// ─────────────────────────────────────────────────────────────────────────────

var castellariusCmd = &cobra.Command{
	Use:   "castellarius",
	Short: "Manage the Castellarius — the overseer that watches the cistern and routes droplets",
}

// ct castellarius start

var castellariusStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Wake the Castellarius — validate config, then watch the cistern and route droplets automatically",
	Long: `Wake the Castellarius.

The Castellarius is a pure state machine — no AI. It watches the cistern for
droplets, routes them into named aqueducts, and advances each droplet through
its cataractae (implement → review → qa → merge) until delivered.

The Castellarius runs until Ctrl-C. As long as work is in the cistern it will
keep dispatching droplets into aqueducts automatically.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath := resolveConfigPath()
		cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		// Resolve relative workflow paths against the config file's directory so
		// that both the adapter and the scheduler see consistent absolute paths.
		cfgDir := filepath.Dir(cfgPath)
		for i := range cfg.Repos {
			if !filepath.IsAbs(cfg.Repos[i].WorkflowPath) {
				cfg.Repos[i].WorkflowPath = filepath.Join(cfgDir, cfg.Repos[i].WorkflowPath)
			}
		}

		workflows := make(map[string]*aqueduct.Workflow, len(cfg.Repos))
		for _, repo := range cfg.Repos {
			if repo.WorkflowPath == "" {
				return fmt.Errorf("repo %q: workflow_path is required", repo.Name)
			}
			w, err := aqueduct.ParseWorkflow(repo.WorkflowPath)
			if err != nil {
				return fmt.Errorf("repo %q workflow %q: %w", repo.Name, repo.WorkflowPath, err)
			}
			workflows[repo.Name] = w
		}

		dbPath := resolveDBPath()
		queueClients := make(map[string]*cistern.Client, len(cfg.Repos))
		for _, repo := range cfg.Repos {
			c, err := cistern.New(dbPath, repo.Prefix)
			if err != nil {
				return fmt.Errorf("queue for %q: %w", repo.Name, err)
			}
			queueClients[repo.Name] = c
		}

		adapter, err := cataracta.NewAdapter(cfg.Repos, workflows, queueClients)
		if err != nil {
			return fmt.Errorf("runner adapter: %w", err)
		}

		sched, err := castellarius.New(*cfg, dbPath, adapter)
		if err != nil {
			return fmt.Errorf("castellarius: %w", err)
		}

		fmt.Println("Castellarius awake. Watching the cistern.")
		for _, repo := range cfg.Repos {
			w := workflows[repo.Name]
			names := repoWorkerNames(repo)
			fmt.Printf("  %s: aqueduct=%q (%d cataractae), aqueducts=%d (%s)\n",
				repo.Name, w.Name, len(w.Cataractae), len(names), strings.Join(names, ", "))
		}
		fmt.Println("Ctrl-C to dismiss the Castellarius.")

		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		if err := sched.Run(ctx); errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	},
}

// ct castellarius status — aqueduct-centric view

var castellariusStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show aqueduct flow — which aqueducts are flowing, which are idle, and what droplet each carries",
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

		allItems, err := c.List("", "")
		if err != nil {
			return fmt.Errorf("list droplets: %w", err)
		}

		assignee := map[string]*cistern.Droplet{}
		for _, item := range allItems {
			if item.Status == "in_progress" && item.Assignee != "" {
				assignee[item.Assignee] = item
			}
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "OPERATOR\tREPO\tDROPLET\tCATARACTA\tELAPSED")
		fmt.Fprintln(tw, "────────\t────\t───────\t─────────\t───────")

		totalBusy := 0
		for _, repo := range cfg.Repos {
			for _, name := range repoWorkerNames(repo) {
				if item, ok := assignee[name]; ok {
					elapsed := int(time.Since(item.UpdatedAt).Minutes())
					fmt.Fprintf(tw, "%s\t%s\t%s\t[%s]\t%dm\n",
						name, repo.Name, item.ID, item.CurrentCataracta, elapsed)
					totalBusy++
				} else {
					fmt.Fprintf(tw, "%s\t%s\t—\t—\t—\n", name, repo.Name)
				}
			}
		}
		tw.Flush()

		totalWorkers := 0
		for _, repo := range cfg.Repos {
			totalWorkers += len(repoWorkerNames(repo))
		}
		fmt.Printf("\n%d of %d aqueducts flowing\n", totalBusy, totalWorkers)
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// ct aqueduct — inspect and validate aqueduct definitions
// ─────────────────────────────────────────────────────────────────────────────

var aqueductCmd = &cobra.Command{
	Use:   "aqueduct",
	Short: "Inspect and validate aqueducts — cataracta chains, repo bindings, and config",
}

// ct aqueduct status — aqueduct definition view

var aqueductStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show configured aqueducts — repos and their cataracta chains",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath := resolveConfigPath()
		cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		cfgDir := filepath.Dir(cfgPath)
		fmt.Printf("Aqueducts (%d configured)\n\n", len(cfg.Repos))

		for _, repo := range cfg.Repos {
			fmt.Printf("  %s\n", repo.Name)
			fmt.Printf("    URL         : %s\n", repo.URL)
			fmt.Printf("    Aqueduct    : %s\n", repo.WorkflowPath)

			wfPath := repo.WorkflowPath
			if !filepath.IsAbs(wfPath) {
				wfPath = filepath.Join(cfgDir, wfPath)
			}
			if wf, err := aqueduct.ParseWorkflow(wfPath); err == nil {
				steps := make([]string, len(wf.Cataractae))
				for i, s := range wf.Cataractae {
					steps[i] = s.Name
				}
				fmt.Printf("    Cataractae  : %s\n", strings.Join(steps, " → "))
			} else {
				fmt.Printf("    Cataractae  : (could not load: %v)\n", err)
			}

			names := repoWorkerNames(repo)
			fmt.Printf("    Aqueducts   : %s\n", strings.Join(names, ", "))
			fmt.Println()
		}
		return nil
	},
}

// ct aqueduct validate — config and aqueduct definition validation

var aqueductValidateCmd = &cobra.Command{
	Use:   "validate [path]",
	Short: "Validate cistern.yaml and all referenced aqueduct definitions",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := resolveConfigPath()
		if len(args) > 0 {
			path = args[0]
		}

		cfg, err := aqueduct.ParseAqueductConfig(path)
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

			if _, err := aqueduct.ParseWorkflow(wfPath); err != nil {
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
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// ct status — overall system status (combines all views)
// ─────────────────────────────────────────────────────────────────────────────

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Overall system status — cistern level, aqueduct flow, and cataracta chains",
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

		allItems, err := c.List("", "")
		if err != nil {
			return fmt.Errorf("list droplets: %w", err)
		}

		flowing, queued, done := 0, 0, 0
		var humanGated []*cistern.Droplet
		assignee := map[string]*cistern.Droplet{}
		for _, item := range allItems {
			switch item.Status {
			case "in_progress":
				flowing++
				if item.Assignee != "" {
					assignee[item.Assignee] = item
				}
			case "open":
				queued++
			case "delivered":
				done++
			}
			if item.CurrentCataracta == "human" && (item.Status == "stagnant" || item.Status == "escalated") {
				humanGated = append(humanGated, item)
			}
		}

		// ── Cistern summary ───────────────────────────────────────────────────
		summary := fmt.Sprintf("%s flowing · %s queued · %s delivered",
			col(colorGreen, fmt.Sprintf("%d", flowing)),
			col(colorYellow, fmt.Sprintf("%d", queued)),
			col(colorDim, fmt.Sprintf("%d", done)))
		fmt.Printf("%s\n\n", summary)

		if len(humanGated) > 0 {
			ids := make([]string, 0, len(humanGated))
			for _, d := range humanGated {
				ids = append(ids, d.ID)
			}
			fmt.Printf("%s %d droplet(s) awaiting human approval: %s\n\n",
				col(colorYellow, "⏸"),
				len(humanGated),
				strings.Join(ids, ", "))
		}

		// ── Castellarius / aqueducts ──────────────────────────────────────────
		fmt.Printf("Castellarius  watching\n")

		// Pre-load workflow step counts for progress indicators.
		cfgDir := filepath.Dir(cfgPath)
		wfSteps := map[string][]aqueduct.WorkflowCataracta{}
		for _, repo := range cfg.Repos {
			wfPath := repo.WorkflowPath
			if !filepath.IsAbs(wfPath) {
				wfPath = filepath.Join(cfgDir, wfPath)
			}
			if wf, wfErr := aqueduct.ParseWorkflow(wfPath); wfErr == nil {
				wfSteps[repo.Name] = wf.Cataractae
			}
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, repo := range cfg.Repos {
			steps := wfSteps[repo.Name]
			for _, name := range repoWorkerNames(repo) {
				if item, ok := assignee[name]; ok {
					elapsed := formatElapsed(time.Since(item.UpdatedAt))
					stage := item.CurrentCataracta
					idx := cataractaIndexInWorkflow(stage, steps)
					total := len(steps)
					var progress string
					if idx > 0 && total > 0 {
						progress = fmt.Sprintf("%s [%d/%d]", stage, idx, total)
					} else {
						progress = stage
					}
					line := fmt.Sprintf("  %s\t→ %s\t[%s]\t%s\n", name, item.ID, progress, elapsed)
					fmt.Fprint(tw, col(colorGreen, line))
				} else {
					line := fmt.Sprintf("  %s\t→ idle\t\t\n", name)
					fmt.Fprint(tw, col(colorDim, line))
				}
			}
		}
		tw.Flush()

		// ── Aqueducts ─────────────────────────────────────────────────────────
		fmt.Println()
		fmt.Printf("Aqueducts\n")
		for _, repo := range cfg.Repos {
			steps := wfSteps[repo.Name]
			stepCount := "?"
			if len(steps) > 0 {
				stepCount = fmt.Sprintf("%d", len(steps))
			}
			fmt.Printf("  %-20s  %s  (%s cataractae)\n", repo.Name, repo.WorkflowPath, stepCount)
		}

		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────

func init() {
	castellariusStartCmd.Flags().StringVar(&configPath, "config", "", "path to cistern.yaml (default: ~/.cistern/cistern.yaml)")

	castellariusCmd.AddCommand(castellariusStartCmd, castellariusStatusCmd)
	aqueductCmd.AddCommand(aqueductStatusCmd, aqueductValidateCmd)

	rootCmd.AddCommand(castellariusCmd, aqueductCmd, statusCmd)
}

func resolveConfigPath() string {
	if configPath != "" {
		return configPath
	}
	if env := os.Getenv("CT_CONFIG"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "cistern.yaml"
	}
	return filepath.Join(home, ".cistern", "cistern.yaml")
}

// repoWorkerNames returns the configured aqueduct names for a repo,
// falling back to aqueduct-0, aqueduct-1, etc.
func repoWorkerNames(repo aqueduct.RepoConfig) []string {
	if len(repo.Names) > 0 {
		return repo.Names
	}
	names := make([]string, repo.Cataractae)
	for i := range names {
		names[i] = fmt.Sprintf("aqueduct-%d", i)
	}
	return names
}
