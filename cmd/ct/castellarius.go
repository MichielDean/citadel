package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cataractae"
	"github.com/MichielDean/cistern/internal/castellarius"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/skills"
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

		if err := validateWorkflowSkills(workflows); err != nil {
			return err
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

		adapter, err := cataractae.NewAdapter(cfg, workflows, queueClients)
		if err != nil {
			return fmt.Errorf("runner adapter: %w", err)
		}

		sched, err := castellarius.New(*cfg, dbPath, adapter, castellarius.WithConfigPath(cfgPath))
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

		// Tmux keepalive: ensure the tmux server stays running for the lifetime of
		// the Castellarius. If the server dies (socket cleaned up, idle timeout, etc.)
		// all subsequent session spawns fail silently. We maintain a dedicated
		// keepalive session so the server is always available, and restart it if it dies.
		go func() {
			const keepaliveSession = "castellarius-keepalive"
			ensureTmuxKeepalive(keepaliveSession)
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					// Clean up keepalive session on shutdown (best-effort).
					exec.Command("tmux", "kill-session", "-t", keepaliveSession).Run() //nolint:errcheck
					return
				case <-ticker.C:
					ensureTmuxKeepalive(keepaliveSession)
				}
			}
		}()

		if err := sched.Run(ctx); errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	},
}

// ensureTmuxKeepalive creates the named tmux session if it does not exist,
// starting the tmux server in the process. This prevents the server from
// dying when all agent sessions have exited.
func ensureTmuxKeepalive(sessionName string) {
	if exec.Command("tmux", "has-session", "-t", sessionName).Run() == nil {
		return // already alive
	}
	exec.Command("tmux", "new-session", "-d", "-s", sessionName).Run() //nolint:errcheck
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
						name, repo.Name, item.ID, item.CurrentCataractae, elapsed)
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

		fmt.Println()
		for _, repo := range cfg.Repos {
			fmt.Printf("  %s\n", repoQueueSummary(repo.Name, allItems))
		}

		hf, hErr := castellarius.ReadHealthFile(filepath.Dir(dbPath))
		fmt.Printf("\nlast tick: %s\n", formatLastTick(hf, hErr))
		return nil
	},
}

// formatLastTick returns the health display string for ct castellarius status.
// If the health file is readable, it returns the age of the last tick (e.g. "5s ago").
// If err is non-nil, it returns the missing-file warning.
func formatLastTick(hf *castellarius.HealthFile, err error) string {
	if err != nil {
		return "unknown (health file missing)"
	}
	age := time.Since(hf.LastTickAt).Round(time.Second)
	return age.String() + " ago"
}

// ─────────────────────────────────────────────────────────────────────────────
// ct aqueduct — inspect and validate aqueduct definitions
// ─────────────────────────────────────────────────────────────────────────────

var aqueductCmd = &cobra.Command{
	Use:   "aqueduct",
	Short: "Inspect and validate aqueducts — cataractae chains, repo bindings, and config",
}

// ct aqueduct status — aqueduct definition view

var aqueductStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show configured aqueducts — repos and their cataractae chains",
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

var (
	statusWatch    bool
	statusInterval int
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Overall system status — cistern level, aqueduct flow, and cataractae chains",
	RunE: func(cmd *cobra.Command, args []string) error {
		if statusInterval < 1 {
			return fmt.Errorf("--interval must be at least 1")
		}

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

		render := func() error {
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
				if item.CurrentCataractae == "human" && (item.Status == "stagnant" || item.Status == "escalated") {
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
			wfSteps := map[string][]aqueduct.WorkflowCataractae{}
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
						stage := item.CurrentCataractae
						idx := cataractaeIndexInWorkflow(stage, steps)
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
		}

		if !statusWatch {
			return render()
		}

		// Watch mode: clear screen and re-render every statusInterval seconds. Ctrl-C to exit.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		ticker := time.NewTicker(time.Duration(statusInterval) * time.Second)
		defer ticker.Stop()

		fmt.Print(clearScreen)
		if err := render(); err != nil {
			return err
		}
		for {
			select {
			case <-ticker.C:
				fmt.Print(clearScreen)
				if err := render(); err != nil {
					return err
				}
			case <-sigCh:
				return nil
			}
		}
	},
}

// ─────────────────────────────────────────────────────────────────────────────

// validateWorkflowSkills checks that every skill referenced in any workflow
// cataractae step is installed in ~/.cistern/skills/<name>/SKILL.md.
// Returns a descriptive error listing all missing skills if any are absent,
// so the operator can install them before the Castellarius starts accepting work.
func validateWorkflowSkills(workflows map[string]*aqueduct.Workflow) error {
	seen := map[string]bool{}
	var missing []string
	for _, w := range workflows {
		for _, step := range w.Cataractae {
			for _, skill := range step.Skills {
				if skill.Name == "" || seen[skill.Name] {
					continue
				}
				seen[skill.Name] = true
				if !skills.IsInstalled(skill.Name) {
					missing = append(missing, skill.Name)
				}
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	noun := "skill"
	if len(missing) != 1 {
		noun = "skills"
	}
	return fmt.Errorf("castellarius cannot start: %d required %s not installed"+
		" — run git_sync or: ct skills install <name> <url>:\n  %s",
		len(missing), noun, strings.Join(missing, "\n  "))
}

func init() {
	castellariusStartCmd.Flags().StringVar(&configPath, "config", "", "path to cistern.yaml (default: ~/.cistern/cistern.yaml)")

	statusCmd.Flags().BoolVar(&statusWatch, "watch", false, "continuously refresh status every N seconds")
	statusCmd.Flags().IntVar(&statusInterval, "interval", 5, "refresh interval in seconds (min 1, used with --watch)")

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

// startupRequiredEnvVars parses the aqueduct config at cfgPath and returns the
// deduplicated list of env vars required by the configured provider preset(s),
// along with whether any repo uses the claude provider.
//
// If cfgPath is empty or the config cannot be parsed, it returns nil env vars
// and usesClaude=true — claude authenticates via its own OAuth credentials file
// and requires no ANTHROPIC_API_KEY env var.
func startupRequiredEnvVars(cfgPath string) (requiredVars []string, usesClaude bool) {
	if cfgPath == "" {
		return nil, true
	}
	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		return nil, true
	}
	seen := map[string]bool{}
	resolved := false
	for _, repo := range cfg.Repos {
		preset, presErr := cfg.ResolveProvider(repo.Name)
		if presErr != nil {
			continue
		}
		resolved = true
		if preset.Name == "claude" {
			usesClaude = true
		}
		for _, envVar := range preset.EnvPassthrough {
			if !seen[envVar] {
				seen[envVar] = true
				requiredVars = append(requiredVars, envVar)
			}
		}
	}
	if !resolved {
		// No repos resolved (empty list or all failed) — default to claude.
		return nil, true
	}
	return requiredVars, usesClaude
}

// repoQueueSummary returns a one-line summary of queue depth and active
// sessions for a single repo, given the full list of droplets from the cistern.
//
// Format:
//
//	"<repo>: N queued, M flowing"
//	"<repo>: N queued, M flowing (assignee: id/cataractae, ...)"
//
// "queued" counts droplets with status "open".
// "flowing" counts droplets with status "in_progress".
// Other statuses (stagnant, delivered, cancelled, …) are ignored.
func repoQueueSummary(repoName string, allItems []*cistern.Droplet) string {
	queued := 0
	var flowing []*cistern.Droplet
	for _, item := range allItems {
		if item.Repo != repoName {
			continue
		}
		switch item.Status {
		case "open":
			queued++
		case "in_progress":
			flowing = append(flowing, item)
		}
	}

	summary := fmt.Sprintf("%s: %d queued, %d flowing", repoName, queued, len(flowing))
	if len(flowing) == 0 {
		return summary
	}

	parts := make([]string, 0, len(flowing))
	for _, item := range flowing {
		entry := item.ID + "/" + item.CurrentCataractae
		if item.Assignee != "" {
			entry = item.Assignee + ": " + entry
		}
		parts = append(parts, entry)
	}
	return summary + " (" + strings.Join(parts, ", ") + ")"
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
