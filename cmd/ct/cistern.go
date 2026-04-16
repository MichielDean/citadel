package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/provider"
	"github.com/spf13/cobra"
)

// cataractaeName returns the cataractae identity for note attribution.
// It reads CT_CATARACTA_NAME (injected by the pipeline into agent sessions),
// falling back to "manual" for direct CLI invocations.
func cataractaeName() string {
	if name := os.Getenv("CT_CATARACTA_NAME"); name != "" {
		return name
	}
	return "manual"
}

var dropletCmd = &cobra.Command{
	Use:   "droplet",
	Short: "Manage droplets in the cistern",
}

// --- cistern add ---

var (
	addTitle       string
	addDescription string
	addPriority    int
	addRepo        string
	addComplexity  string
	addDependsOn   []string
)

var dropletAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new droplet to the cistern",
	RunE: func(cmd *cobra.Command, args []string) error {
		if addTitle == "" {
			return fmt.Errorf("--title is required")
		}
		if addRepo == "" {
			return fmt.Errorf("--repo is required")
		}
		repo, err := resolveCanonicalRepo(addRepo)
		if err != nil {
			return err
		}

		c, err := cistern.New(resolveDBPath(), inferPrefix(repo))
		if err != nil {
			return err
		}
		defer c.Close()

		cx, err := parseComplexity(addComplexity)
		if err != nil {
			return err
		}
		item, err := c.Add(repo, addTitle, addDescription, addPriority, cx, addDependsOn...)
		if err != nil {
			return err
		}
		fmt.Printf("Droplet added to cistern. %s: %s\n", item.ID, item.Title)
		return nil
	},
}

// resolveFilterPreset returns the ProviderPreset to use for filtration.
// It tries to load the AqueductConfig and resolve the preset for repo.
// On any error (missing config, unknown repo, etc.) it falls back to the
// built-in claude preset.
func resolveFilterPreset(repo string) provider.ProviderPreset {
	cfgPath := resolveConfigPath()
	if cfg, err := aqueduct.ParseAqueductConfig(cfgPath); err == nil {
		if preset, err := cfg.ResolveProvider(repo); err == nil {
			return preset
		}
	}
	// Fallback: built-in claude preset.
	for _, p := range provider.Builtins() {
		if p.Name == "claude" {
			return p
		}
	}
	return provider.ProviderPreset{}
}

// resolveCanonicalRepo looks up input against configured repo names case-insensitively
// and returns the canonical (configured) name on a match. If the config cannot be
// loaded, input is returned unchanged (validation is skipped). If the config loads
// but no repo matches, an error is returned listing the configured repo names.
func resolveCanonicalRepo(input string) (string, error) {
	cfgPath := resolveConfigPath()
	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		// No config available; cannot validate — pass through unchanged.
		return input, nil
	}
	names := make([]string, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		if strings.EqualFold(r.Name, input) {
			return r.Name, nil
		}
		names = append(names, r.Name)
	}
	return "", fmt.Errorf("unknown repo %s — configured repos are: %s", input, strings.Join(names, ", "))
}

// --- cistern list ---

var (
	listRepo      string
	listStatus    string
	listOutput    string
	listAll       bool
	listWatch     bool
	listCancelled bool
)

var dropletListCmd = &cobra.Command{
	Use:   "list",
	Short: "List droplets in the cistern",
	RunE: func(cmd *cobra.Command, args []string) error {
		if listOutput != "table" && listOutput != "json" {
			return fmt.Errorf("--output must be table or json")
		}
		if listWatch && listOutput != "table" {
			return fmt.Errorf("--watch requires --output table")
		}
		if listWatch && !isTerminal() {
			return fmt.Errorf("--watch requires an interactive terminal")
		}

		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		render := func() error {
			// --cancelled overrides --status and shows only cancelled droplets.
			effectiveStatus := listStatus
			if listCancelled {
				effectiveStatus = "cancelled"
			}
			items, err := c.List(listRepo, effectiveStatus)
			if err != nil {
				return err
			}

			if listOutput == "json" {
				if items == nil {
					items = []*cistern.Droplet{}
				}
				out, err := json.MarshalIndent(items, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(out))
				return nil
			}

			// TABLE output: split into active and delivered.
			// Default hides delivered items; --all shows them in a dimmed section.
			// If --status is set explicitly, honour it and don't split.
			filterDelivered := listStatus == "" && !listAll && !listCancelled
			var active, dimmed []*cistern.Droplet
			for _, item := range items {
				if filterDelivered && item.Status == "delivered" {
					dimmed = append(dimmed, item)
				} else {
					active = append(active, item)
				}
			}

			if len(active) == 0 && (!listAll || len(dimmed) == 0) {
				printEmptyMessage(c)
				return nil
			}

			// Title truncation width.
			titleMax := 40
			if isTerminal() {
				if w := termWidth(); w-55 > 15 {
					titleMax = w - 55
				}
			}

			if isTerminal() {
				printDropletListTerminal(active, dimmed, listAll, titleMax)
				return nil
			}

			// Non-terminal / piped output: plain tabwriter, no ANSI.
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tCOMPLEXITY\tTITLE\tSTATUS\tELAPSED\tCATARACTA")
			for _, item := range active {
				ds := displayStatusForDroplet(item)
				cataractae := item.CurrentCataractae
				if cataractae == "" {
					cataractae = "\u2014"
				}
				if item.Status == "open" {
					blockedBy, _ := c.GetBlockedBy(item.ID)
					if len(blockedBy) > 0 {
						ds = "\u2298 blocked"
						cataractae = "waiting: " + blockedBy[0]
					}
				}
				if ds == "awaiting" {
					ds = "\u23f8 awaiting approval"
				}
				elapsed := "\u2014"
				if item.Status == "in_progress" {
					elapsed = formatElapsed(time.Since(item.UpdatedAt))
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					item.ID, complexityName(item.Complexity), truncate(item.Title, titleMax),
					ds, elapsed, cataractae)
			}
			if listAll && len(dimmed) > 0 {
				fmt.Fprintln(tw, "— delivered —")
				for _, item := range dimmed {
					age := formatElapsed(time.Since(item.UpdatedAt))
					cataractae := item.CurrentCataractae
					if cataractae == "" {
						cataractae = "\u2014"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
						item.ID, complexityName(item.Complexity), truncate(item.Title, titleMax),
						"delivered", age, cataractae)
				}
			}
			return tw.Flush()
		}

		if !listWatch {
			return render()
		}

		// Watch mode: clear screen and re-render every 2 seconds. Ctrl-C to exit.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		ticker := time.NewTicker(2 * time.Second)
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

// printDropletListTerminal renders the droplet list with colors to a terminal.
func printDropletListTerminal(active, dimmed []*cistern.Droplet, showAll bool, titleMax int) {
	const (
		colID = 12
		colCX = 10
		colSt = 12 // STATUS cell visual width (icon + space + text)
		colEl = 10 // ELAPSED
	)
	fmt.Printf("  %-*s  %-*s  %-*s  %-*s  %-*s  %s\n",
		colID, "ID", colCX, "COMPLEXITY", titleMax, "TITLE", colSt, "STATUS", colEl, "ELAPSED", "CATARACTA")

	for _, item := range active {
		ds := displayStatusForDroplet(item)
		cataractae := item.CurrentCataractae
		if cataractae == "" {
			cataractae = "—"
		}
		elapsed := "—"
		if item.Status == "in_progress" {
			elapsed = formatElapsed(time.Since(item.UpdatedAt))
		}
		title := padRight(truncate(item.Title, titleMax), titleMax)
		sc := statusCell(ds, colSt)
		fmt.Printf("  %-*s  %-*s  %s  %s  %-*s  %s\n",
			colID, item.ID, colCX, complexityName(item.Complexity),
			title, sc, colEl, elapsed, cataractae)
	}

	if showAll && len(dimmed) > 0 {
		fmt.Println(colorDim + "  ── delivered " + strings.Repeat("─", titleMax+colID+colCX+6) + colorReset)
		for _, item := range dimmed {
			age := formatElapsed(time.Since(item.UpdatedAt))
			title := padRight(truncate(item.Title, titleMax), titleMax)
			line := fmt.Sprintf("  %-*s  %-*s  %s  ✓ %-*s  %-*s  —",
				colID, item.ID, colCX, complexityName(item.Complexity),
				title, colSt-2, "delivered", colEl, age)
			fmt.Println(colorDim + line + colorReset)
		}
	}
}

// displayStatus maps internal status names to water vocabulary.
func displayStatus(status string) string {
	switch status {
	case "in_progress":
		return "flowing"
	case "open":
		return "queued"
	case "pooled":
		return "pooled"
	case "closed", "delivered":
		return "delivered"
	default:
		return status
	}
}

func formatPeekFollowSeparator(updatedAt, stageDispatchedAt time.Time) string {
	elapsed := formatElapsed(time.Since(updatedAt))
	if !stageDispatchedAt.IsZero() {
		if se := formatStageElapsed(time.Since(stageDispatchedAt)); se != "" {
			return elapsed + " (stage " + se + ")"
		}
	}
	return elapsed
}

// displayStatusForDroplet returns the display status for a droplet, overriding
// for human-gated droplets to show "awaiting approval".
func displayStatusForDroplet(item *cistern.Droplet) string {
	if item.CurrentCataractae == "human" && item.Status == "pooled" {
		return "awaiting"
	}
	return displayStatus(item.Status)
}

func printEmptyMessage(c *cistern.Client) {
	stats, err := c.Stats()
	if err != nil {
		fmt.Println("Cistern dry.")
		return
	}
	if stats.Flowing > 0 {
		return
	}
	if stats.Pooled > 0 {
		fmt.Printf("No flowing droplets. %d droplet(s) pooled.\n", stats.Pooled)
		return
	}
	fmt.Println("Cistern dry.")
}

// --- cistern search ---

var (
	searchQuery    string
	searchStatus   string
	searchPriority int
	searchOutput   string
)

var dropletSearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search droplets by title, status, and priority",
	RunE: func(cmd *cobra.Command, args []string) error {
		if searchOutput != "table" && searchOutput != "json" {
			return fmt.Errorf("--output must be table or json")
		}
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		items, err := c.Search(searchQuery, searchStatus, searchPriority)
		if err != nil {
			return err
		}

		if searchOutput == "json" {
			if items == nil {
				items = []*cistern.Droplet{}
			}
			out, err := json.MarshalIndent(items, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(out))
			return nil
		}

		if len(items) == 0 {
			printEmptyMessage(c)
			return nil
		}

		titleMax := 40
		if isTerminal() {
			if w := termWidth(); w-55 > 15 {
				titleMax = w - 55
			}
		}

		if isTerminal() {
			printDropletListTerminal(items, nil, false, titleMax)
			return nil
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tCOMPLEXITY\tTITLE\tSTATUS\tELAPSED\tCATARACTA")
		for _, item := range items {
			ds := displayStatusForDroplet(item)
			cataractae := item.CurrentCataractae
			if cataractae == "" {
				cataractae = "\u2014"
			}
			if item.Status == "open" {
				blockedBy, _ := c.GetBlockedBy(item.ID)
				if len(blockedBy) > 0 {
					ds = "\u2298 blocked"
					cataractae = "waiting: " + blockedBy[0]
				}
			}
			if ds == "awaiting" {
				ds = "\u23f8 awaiting approval"
			}
			elapsed := "\u2014"
			if item.Status == "in_progress" {
				elapsed = formatElapsed(time.Since(item.UpdatedAt))
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				item.ID, complexityName(item.Complexity), truncate(item.Title, titleMax),
				ds, elapsed, cataractae)
		}
		return tw.Flush()
	},
}

// --- cistern export ---

var (
	exportFormat   string
	exportQuery    string
	exportStatus   string
	exportPriority int
)

var dropletExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export droplets to JSON or CSV",
	RunE: func(cmd *cobra.Command, args []string) error {
		if exportFormat != "json" && exportFormat != "csv" {
			return fmt.Errorf("--format must be json or csv")
		}
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		items, err := c.Search(exportQuery, exportStatus, exportPriority)
		if err != nil {
			return err
		}

		if exportFormat == "json" {
			if items == nil {
				items = []*cistern.Droplet{}
			}
			out, err := json.MarshalIndent(items, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(out))
			return nil
		}

		// CSV output.
		w := csv.NewWriter(os.Stdout)
		_ = w.Write([]string{"id", "repo", "title", "description", "priority", "complexity", "status", "assignee", "current_cataractae", "outcome", "assigned_aqueduct", "last_reviewed_commit", "created_at", "updated_at"})
		for _, item := range items {
			_ = w.Write([]string{
				item.ID,
				item.Repo,
				item.Title,
				item.Description,
				strconv.Itoa(item.Priority),
				strconv.Itoa(item.Complexity),
				item.Status,
				item.Assignee,
				item.CurrentCataractae,
				item.Outcome,
				item.AssignedAqueduct,
				item.LastReviewedCommit,
				item.CreatedAt.Format(time.RFC3339),
				item.UpdatedAt.Format(time.RFC3339),
			})
		}
		w.Flush()
		return w.Error()
	},
}

// --- cistern rename ---

var dropletRenameCmd = &cobra.Command{
	Use:   "rename <id> <new-title>",
	Short: "Rename a droplet — update its title",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		newTitle := args[1]
		if err := c.EditDroplet(args[0], cistern.EditDropletFields{Title: &newTitle}); err != nil {
			return err
		}
		fmt.Printf("droplet %s renamed to %q\n", args[0], args[1])
		return nil
	},
}

// --- cistern show ---

var dropletShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show details of a droplet",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		item, err := c.Get(args[0])
		if err != nil {
			return err
		}

		fmt.Printf("ID:          %s\n", item.ID)
		fmt.Printf("Title:       %s\n", item.Title)
		fmt.Printf("Repo:        %s\n", item.Repo)
		fmt.Printf("Status:      %s\n", displayStatus(item.Status))
		fmt.Printf("Priority:    %d\n", item.Priority)
		fmt.Printf("Complexity:  %s (%d)\n", complexityName(item.Complexity), item.Complexity)
		fmt.Printf("Cataracta:      %s\n", item.Assignee)
		fmt.Printf("Stage:       %s\n", item.CurrentCataractae)

		fmt.Printf("Created:     %s\n", item.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("Updated:     %s\n", item.UpdatedAt.Format("2006-01-02 15:04:05"))

		if item.Description != "" {
			fmt.Printf("\nDescription:\n%s\n", item.Description)
		}

		notes, err := c.GetNotes(item.ID)
		if err != nil {
			return err
		}
		if len(notes) > 0 {
			fmt.Printf("\nNotes:\n")
			for _, n := range notes {
				fmt.Printf("  [%s] %s\n", n.CataractaeName, n.Content)
			}
		}

		return nil
	},
}

// --- cistern note ---

var dropletNoteCmd = &cobra.Command{
	Use:   "note <id> <content>",
	Short: "Add a note to a droplet",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.AddNote(args[0], cataractaeName(), args[1]); err != nil {
			return err
		}
		fmt.Printf("note added to droplet %s\n", args[0])
		return nil
	},
}

// --- cistern close ---

var dropletCloseCmd = &cobra.Command{
	Use:   "close <id>",
	Short: "Close a droplet — mark as delivered",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.CloseItem(args[0]); err != nil {
			return err
		}
		fmt.Printf("droplet %s delivered\n", args[0])
		return nil
	},
}

// --- cistern reopen ---

var dropletReopenCmd = &cobra.Command{
	Use:   "reopen <id>",
	Short: "Return a droplet to the cistern",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.UpdateStatus(args[0], "open"); err != nil {
			return err
		}
		fmt.Printf("droplet %s returned to cistern\n", args[0])
		return nil
	},
}

// --- cistern pool ---

var poolNotes string

var dropletPoolCmd = &cobra.Command{
	Use:   "pool <id>",
	Short: "Signal pool outcome — cannot currently proceed",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		item, err := c.Get(args[0])
		if err != nil {
			return err
		}
		if item.Status == "delivered" || item.Status == "cancelled" {
			return fmt.Errorf("cannot pool: droplet %s has terminal status %q", args[0], item.Status)
		}

		if poolNotes != "" {
			if err := c.AddNote(args[0], cataractaeName(), poolNotes); err != nil {
				return err
			}
		}
		if err := c.SetOutcome(args[0], "pool"); err != nil {
			return err
		}
		notifyCastellarius()
		// When not in_progress, Castellarius will never observe this droplet.
		// Directly pool so the reason is recorded in events with a non-empty payload.
		if item.Status != "in_progress" {
			if err := c.Pool(args[0], poolNotes); err != nil {
				return err
			}
		}
		fmt.Printf("droplet %s: outcome=pool\n", args[0])
		return nil
	},
}

// --- cistern purge ---

var (
	purgeOlderThan string
	purgeDryRun    bool
)

var dropletPurgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Delete closed/pooled droplets older than a threshold",
	RunE: func(cmd *cobra.Command, args []string) error {
		if purgeOlderThan == "" {
			return fmt.Errorf("--older-than is required")
		}
		d, err := parseDuration(purgeOlderThan)
		if err != nil {
			return fmt.Errorf("invalid --older-than value: %w", err)
		}
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		n, err := c.Purge(d, purgeDryRun)
		if err != nil {
			return err
		}
		if purgeDryRun {
			fmt.Printf("dry-run: would purge %d droplet(s)\n", n)
		} else {
			fmt.Printf("purged %d droplet(s)\n", n)
		}
		return nil
	},
}

// parseDuration parses a duration string, supporting 'd' suffix for days
// in addition to standard Go duration units (e.g., "30d", "24h", "1h30m").
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid days value: %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// --- cistern deps ---

var (
	depsAdd    string
	depsRemove string
)

var dropletDepsCmd = &cobra.Command{
	Use:   "deps <id>",
	Short: "List or modify dependencies of a droplet",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if depsAdd != "" {
			if err := c.AddDependency(id, depsAdd); err != nil {
				return err
			}
			fmt.Printf("dependency added: %s depends on %s\n", id, depsAdd)
			return nil
		}

		if depsRemove != "" {
			if err := c.RemoveDependency(id, depsRemove); err != nil {
				return err
			}
			fmt.Printf("dependency removed: %s no longer depends on %s\n", id, depsRemove)
			return nil
		}

		// List dependencies and their statuses.
		deps, err := c.GetDependencies(id)
		if err != nil {
			return err
		}
		if len(deps) == 0 {
			fmt.Printf("droplet %s has no dependencies\n", id)
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "DEPENDS ON\tSTATUS")
		for _, depID := range deps {
			dep, err := c.Get(depID)
			if err != nil {
				fmt.Fprintf(tw, "%s\tunknown\n", depID)
				continue
			}
			fmt.Fprintf(tw, "%s\t%s\n", depID, displayStatus(dep.Status))
		}
		return tw.Flush()
	},
}

// --- cistern stats ---

var dropletStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show droplet counts by status",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		s, err := c.Stats()
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "  flowing\t%d\n", s.Flowing)
		fmt.Fprintf(tw, "  queued\t%d\n", s.Queued)
		fmt.Fprintf(tw, "  delivered\t%d\n", s.Delivered)
		fmt.Fprintf(tw, "  pooled\t%d\n", s.Pooled)
		fmt.Fprintln(tw, "  ──────────────")
		fmt.Fprintf(tw, "  total\t%d\n", s.Flowing+s.Queued+s.Delivered+s.Pooled)
		return tw.Flush()
	},
}

// --- cistern issue ---

var dropletIssueCmd = &cobra.Command{
	Use:   "issue",
	Short: "Manage structured issues for a droplet",
}

var dropletIssueAddCmd = &cobra.Command{
	Use:   "add <droplet-id> <description>",
	Short: "File a new open issue against a droplet",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		flaggedBy := os.Getenv("CT_CATARACTA_NAME")
		if flaggedBy == "" {
			flaggedBy = "manual"
		}
		iss, err := c.AddIssue(args[0], flaggedBy, args[1])
		if err != nil {
			return err
		}
		fmt.Printf("issue filed: %s\n", iss.ID)
		return nil
	},
}

var issueResolveEvidence string

var dropletIssueResolveCmd = &cobra.Command{
	Use:   "resolve <issue-id>",
	Short: "Mark an issue resolved (forbidden for implementer)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.ToLower(os.Getenv("CT_CATARACTA_NAME"))
		if name == "implementer" || name == "implement" {
			return fmt.Errorf("only reviewer cataractae may resolve issues")
		}
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if issueResolveEvidence == "" {
			return fmt.Errorf("--evidence is required")
		}
		if err := c.ResolveIssue(args[0], issueResolveEvidence); err != nil {
			return err
		}
		fmt.Printf("issue %s resolved\n", args[0])
		return nil
	},
}

var issueRejectEvidence string

var dropletIssueRejectCmd = &cobra.Command{
	Use:   "reject <issue-id>",
	Short: "Mark an issue unresolved — still present",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.ToLower(os.Getenv("CT_CATARACTA_NAME"))
		if name == "implementer" || name == "implement" {
			return fmt.Errorf("only reviewer cataractae may reject issues")
		}
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if issueRejectEvidence == "" {
			return fmt.Errorf("--evidence is required")
		}
		if err := c.RejectIssue(args[0], issueRejectEvidence); err != nil {
			return err
		}
		fmt.Printf("issue %s marked unresolved\n", args[0])
		return nil
	},
}

var issueListOpen bool
var issueListFlaggedBy string

var dropletIssueListCmd = &cobra.Command{
	Use:   "list <droplet-id>",
	Short: "List issues for a droplet",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		issues, err := c.ListIssues(args[0], issueListOpen, issueListFlaggedBy)
		if err != nil {
			return err
		}
		if len(issues) == 0 {
			fmt.Println("no issues found")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATUS\tFLAGGED BY\tDESCRIPTION")
		for _, iss := range issues {
			desc := iss.Description
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", iss.ID, iss.Status, iss.FlaggedBy, desc)
		}
		return tw.Flush()
	},
}

// --- cistern pass ---

var passNotes string

var dropletPassCmd = &cobra.Command{
	Use:   "pass <id>",
	Short: "Signal pass outcome — work complete, advance to next cataractae",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		item, err := c.Get(args[0])
		if err != nil {
			return err
		}
		if item.Status == "delivered" || item.Status == "cancelled" {
			return fmt.Errorf("cannot pass: droplet %s has terminal status %q", args[0], item.Status)
		}

		if passNotes != "" {
			if err := c.AddNote(args[0], cataractaeName(), passNotes); err != nil {
				return err
			}
		}
		if err := c.SetOutcome(args[0], "pass"); err != nil {
			return err
		}
		notifyCastellarius()
		fmt.Printf("droplet %s: outcome=pass\n", args[0])
		return nil
	},
}

// --- cistern recirculate ---

var recirculateTo string
var recirculateNotes string

var dropletRecirculateCmd = &cobra.Command{
	Use:   "recirculate <id>",
	Short: "Signal recirculate outcome — needs rework, send back upstream",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.ToLower(os.Getenv("CT_CATARACTA_NAME"))
		if name == "implementer" || name == "implement" {
			return fmt.Errorf("recirculate is not a valid outcome at the implement cataractae — use ct droplet pass <id> to signal completion")
		}

		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		item, err := c.Get(args[0])
		if err != nil {
			return err
		}
		if item.Status == "delivered" || item.Status == "cancelled" {
			return fmt.Errorf("cannot recirculate: droplet %s has terminal status %q", args[0], item.Status)
		}

		if recirculateNotes != "" {
			if err := c.AddNote(args[0], cataractaeName(), "♻ "+recirculateNotes); err != nil {
				return err
			}
		}
		outcome := "recirculate"
		if recirculateTo != "" {
			outcome = "recirculate:" + recirculateTo
		}
		// When not in_progress, Castellarius will never observe this droplet.
		// Directly open it for the target cataractae. Assign clears outcome.
		if item.Status != "in_progress" {
			target := recirculateTo
			if target == "" {
				target = item.CurrentCataractae
			}
			if err := c.Assign(args[0], "", target); err != nil {
				return err
			}
			fmt.Printf("droplet %s: outcome=%s\n", args[0], outcome)
			return nil
		}
		if err := c.SetOutcome(args[0], outcome); err != nil {
			return err
		}
		notifyCastellarius()
		fmt.Printf("droplet %s: outcome=%s\n", args[0], outcome)
		return nil
	},
}

// --- cistern heartbeat ---

var dropletHeartbeatCmd = &cobra.Command{
	Use:   "heartbeat <id>",
	Short: "Record agent heartbeat — signals the scheduler that work is progressing",
	Long: `Record a heartbeat timestamp for the given droplet.

Agents should call this every 60 seconds while working. The stall detector
uses the heartbeat timestamp to distinguish alive-but-slow agents from agents
that are genuinely stuck or dead. An agent that is emitting heartbeats will
not be considered stalled regardless of how long it has been running.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.Heartbeat(args[0]); err != nil {
			return err
		}
		fmt.Printf("droplet %s: heartbeat recorded\n", args[0])
		return nil
	},
}

// --- cistern approve ---

var dropletApproveCmd = &cobra.Command{
	Use:   "approve <id>",
	Short: "Approve a human-gated droplet for delivery",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		item, err := c.Get(id)
		if err != nil {
			return err
		}
		if item.CurrentCataractae != "human" {
			return fmt.Errorf("%s is not awaiting human approval (cataractae: %s)", id, item.CurrentCataractae)
		}
		if err := c.Assign(id, "", "delivery"); err != nil {
			return err
		}
		fmt.Printf("Droplet %s approved for delivery\n", id)
		return nil
	},
}

// --- cistern peek ---

var (
	peekLines     int
	peekRaw       bool
	peekFollow    bool
	peekSnapshot  bool
	sessionLogDir string // overrideable in tests; empty means ~/.cistern/session-logs
)

// tmuxHasSession reports whether the named tmux session exists.
// Replaced in tests to avoid requiring a live tmux installation.
var tmuxHasSession = func(session string) bool {
	return exec.Command("tmux", "has-session", "-t", session).Run() == nil
}

// tmuxAttachFunc attaches read-only to the named tmux session, taking over the terminal.
// Replaced in tests to capture the call without running tmux.
var tmuxAttachFunc = func(session string) error {
	cmd := exec.Command("tmux", "attach-session", "-t", session, "-r")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes ANSI color/style escape sequences from s.
func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// capturePane runs tmux capture-pane and returns the output.
// It always targets window 0, pane 0 of the session so that the correct agent
// pane is captured regardless of which window is currently active.
// When lines <= 0 the full scrollback buffer is captured (-S -).
// When lines > 0 only the last N lines of history are captured (-S -N).
func capturePane(session string, lines int) (string, error) {
	startFlag := "-" // start of scrollback buffer (full history)
	if lines > 0 {
		startFlag = fmt.Sprintf("-%d", lines)
	}
	out, err := exec.Command("tmux", "capture-pane", "-t", session+":0.0", "-p",
		"-S", startFlag).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

var dropletPeekCmd = &cobra.Command{
	Use:   "peek <id>",
	Short: "Tail live agent output from the active tmux session for a droplet",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		item, err := c.Get(id)
		if err != nil {
			return err
		}

		if item.Status != "in_progress" {
			fmt.Printf("%s is not currently flowing (status: %s)\n", id, displayStatus(item.Status))
			return nil
		}

		session := item.Repo + "-" + item.Assignee

		// --raw: read the session log file directly without requiring tmux.
		// Mutually exclusive with --snapshot and --follow.
		if peekRaw {
			if peekSnapshot {
				return fmt.Errorf("--raw is incompatible with --snapshot; use one or the other")
			}
			if peekFollow {
				return fmt.Errorf("--raw is incompatible with --follow")
			}
			logDir := sessionLogDir
			if logDir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("cannot determine home directory: %w", err)
				}
				logDir = filepath.Join(home, ".cistern", "session-logs")
			}
			logPath := filepath.Join(logDir, session+".log")
			f, err := os.Open(logPath)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Printf("No session log found at %s\n", logPath)
					return nil
				}
				return err
			}
			defer f.Close()
			_, err = io.Copy(os.Stdout, f)
			return err
		}

		// Check if tmux is available.
		if _, err := exec.LookPath("tmux"); err != nil {
			fmt.Println("tmux not installed")
			return nil
		}

		stageSuffix := ""
		if !item.StageDispatchedAt.IsZero() {
			if se := formatStageElapsed(time.Since(item.StageDispatchedAt)); se != "" {
				stageSuffix = " (stage " + se + ")"
			}
		}
		fmt.Printf("[%s] %s — flowing %s%s\n", item.ID, item.Title, formatElapsed(time.Since(item.UpdatedAt)), stageSuffix)

		// notesHint prints a no-session message and falls back to the last note.
		notesHint := func() {
			fmt.Printf("No active tmux session found for %s — may have just completed\n", id)
			notes, nerr := c.GetNotes(id)
			if nerr == nil && len(notes) > 0 {
				last := notes[0]
				lines := strings.Split(last.Content, "\n")
				if len(lines) > 10 {
					lines = lines[len(lines)-10:]
				}
				fmt.Println(strings.Join(lines, "\n"))
			}
		}

		// Default: live attach — takes over the terminal read-only.
		// --snapshot retains capture-pane polling for non-interactive use.
		if !peekSnapshot {
			if peekFollow {
				return fmt.Errorf("--follow requires --snapshot; re-run with: ct droplet peek --snapshot --follow %s", id)
			}
			if !tmuxHasSession(session) {
				notesHint()
				return nil
			}
			return tmuxAttachFunc(session)
		}

		// --snapshot mode: static capture-pane with optional --follow polling.
		printCapture := func() {
			if !tmuxHasSession(session) {
				notesHint()
				return
			}
			out, cerr := capturePane(session, peekLines)
			if cerr != nil {
				fmt.Fprintf(os.Stderr, "tmux capture-pane: %v\n", cerr)
				return
			}
			out = stripANSI(out)
			fmt.Print(out)
		}

		if !peekFollow {
			printCapture()
			return nil
		}

		// --snapshot --follow: re-capture every 3 seconds until Ctrl-C.
		printCapture()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			fmt.Printf("─── %s ───\n", formatPeekFollowSeparator(item.UpdatedAt, item.StageDispatchedAt))
			printCapture()
		}
		return nil
	},
}

// --- cistern edit ---

var (
	editTitle       string
	editDescription string
	editComplexity  string
	editPriority    int
)

var dropletEditCmd = &cobra.Command{
	Use:   "edit <id>",
	Short: "Update title, description, complexity, or priority of a queued droplet",
	Long: `Edit mutable fields on a droplet that has not yet been picked up.

If no flags are provided, opens the droplet in your editor ($EDITOR, defaults to vi).
Only the flags you pass are updated.

  ct droplet edit <id> -t "new title"
  ct droplet edit <id> -x critical -p 1

To replace the description with multi-line text from stdin:
  echo 'new description' | ct droplet edit <id> --description -

The droplet must be queued (open or pooled). Edits are rejected once a
droplet is in_progress or delivered.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		titleChanged := cmd.Flags().Changed("title")
		descChanged := cmd.Flags().Changed("description")
		cxChanged := cmd.Flags().Changed("complexity")
		prioChanged := cmd.Flags().Changed("priority")

		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if !titleChanged && !descChanged && !cxChanged && !prioChanged {
			return editInteractive(c, id)
		}

		var fields cistern.EditDropletFields

		if titleChanged {
			if editTitle == "" {
				return fmt.Errorf("title must not be empty")
			}
			fields.Title = &editTitle
		}

		if descChanged {
			desc := editDescription
			if desc == "-" {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				desc = strings.TrimSuffix(string(b), "\n")
			}
			fields.Description = &desc
		}

		if cxChanged {
			cx, err := parseComplexity(editComplexity)
			if err != nil {
				return err
			}
			fields.Complexity = &cx
		}

		if prioChanged {
			if editPriority < 1 {
				return fmt.Errorf("priority must be a positive integer, got %d", editPriority)
			}
			fields.Priority = &editPriority
		}

		if err := c.EditDroplet(id, fields); err != nil {
			return err
		}

		fmt.Printf("droplet %s updated\n", id)
		return nil
	},
}

func escapeNewlines(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unescapeNewlines(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i += 2
			case '\\':
				b.WriteByte('\\')
				i += 2
			default:
				b.WriteByte(s[i])
				i++
			}
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

func editInteractive(c *cistern.Client, id string) error {
	d, err := c.Get(id)
	if err != nil {
		return err
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	template := fmt.Sprintf(
		"# Edit droplet %s\n"+
			"# Lines starting with # are comments.\n"+
			"title: %s\n"+
			"description: %s\n"+
			"complexity: %s\n"+
			"priority: %d\n",
		id, escapeNewlines(d.Title), escapeNewlines(d.Description), complexityName(d.Complexity), d.Priority)

	tmp, err := os.CreateTemp("", "ct-edit-*.txt")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(template); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	tmp.Close()

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor: %w", err)
	}

	content, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("read temp file: %w", err)
	}

	var fields cistern.EditDropletFields

	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		switch key {
		case "title":
			unescaped := unescapeNewlines(val)
			if unescaped != d.Title && unescaped != "" {
				fields.Title = &unescaped
			}
		case "description":
			unescaped := unescapeNewlines(val)
			if unescaped != d.Description {
				fields.Description = &unescaped
			}
		case "complexity":
			cx, err := parseComplexity(val)
			if err != nil {
				return fmt.Errorf("invalid complexity %q: %w", val, err)
			}
			if cx != d.Complexity {
				fields.Complexity = &cx
			}
		case "priority":
			p, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("invalid priority %q: %w", val, err)
			}
			if p < 1 {
				return fmt.Errorf("priority must be a positive integer, got %d", p)
			}
			if p != d.Priority {
				fields.Priority = &p
			}
		}
	}

	if fields.Empty() {
		fmt.Println("no changes")
		return nil
	}

	if err := c.EditDroplet(id, fields); err != nil {
		return err
	}

	fmt.Printf("droplet %s updated\n", id)
	return nil
}

func init() {
	dropletAddCmd.Flags().StringVar(&addTitle, "title", "", "droplet title (required)")
	dropletAddCmd.Flags().StringVar(&addDescription, "description", "", "droplet description")
	dropletAddCmd.Flags().IntVar(&addPriority, "priority", 2, "priority (1=highest)")
	dropletAddCmd.Flags().StringVar(&addRepo, "repo", "", "target repository (required)")
	dropletAddCmd.Flags().StringVarP(&addComplexity, "complexity", "x", "2", "droplet complexity: 1/standard, 2/full (default), 3/critical")
	dropletAddCmd.Flags().StringArrayVar(&addDependsOn, "depends-on", nil, "dependency droplet ID (repeatable)")

	dropletDepsCmd.Flags().StringVar(&depsAdd, "add", "", "add a dependency (dep ID)")
	dropletDepsCmd.Flags().StringVar(&depsRemove, "remove", "", "remove a dependency (dep ID)")

	dropletListCmd.Flags().StringVar(&listRepo, "repo", "", "filter by repo")
	dropletListCmd.Flags().StringVar(&listStatus, "status", "", "filter by status (open|in_progress|delivered|pooled)")
	dropletListCmd.Flags().StringVar(&listOutput, "output", "table", "output format: table or json")
	dropletListCmd.Flags().BoolVar(&listAll, "all", false, "include delivered droplets in a dimmed section below active ones")
	dropletListCmd.Flags().BoolVar(&listWatch, "watch", false, "live-refresh the list every 2 seconds (Ctrl-C to stop)")
	dropletListCmd.Flags().BoolVar(&listCancelled, "cancelled", false, "show only cancelled droplets (for audit)")

	dropletSearchCmd.Flags().StringVar(&searchQuery, "query", "", "filter by title substring (case-insensitive)")
	dropletSearchCmd.Flags().StringVar(&searchStatus, "status", "", "filter by status (open|in_progress|delivered|pooled)")
	dropletSearchCmd.Flags().IntVar(&searchPriority, "priority", 0, "filter by priority (0 means no filter)")
	dropletSearchCmd.Flags().StringVar(&searchOutput, "output", "table", "output format: table or json")

	dropletExportCmd.Flags().StringVar(&exportFormat, "format", "json", "output format: json or csv")
	dropletExportCmd.Flags().StringVar(&exportQuery, "query", "", "filter by title substring (case-insensitive)")
	dropletExportCmd.Flags().StringVar(&exportStatus, "status", "", "filter by status (open|in_progress|delivered|pooled)")
	dropletExportCmd.Flags().IntVar(&exportPriority, "priority", 0, "filter by priority (0 means no filter)")

	dropletPurgeCmd.Flags().StringVar(&purgeOlderThan, "older-than", "", "delete droplets older than this duration (e.g. 30d, 24h) (required)")
	dropletPurgeCmd.Flags().BoolVar(&purgeDryRun, "dry-run", false, "show what would be deleted without deleting")

	dropletPassCmd.Flags().StringVar(&passNotes, "notes", "", "add a note before signaling pass")
	dropletRecirculateCmd.Flags().StringVar(&recirculateTo, "to", "", "named cataractae to recirculate to (e.g. --to implement)")
	dropletRecirculateCmd.Flags().StringVar(&recirculateNotes, "notes", "", "add a note before signaling recirculate")
	dropletPoolCmd.Flags().StringVar(&poolNotes, "notes", "", "add a note before signaling pool")

	dropletPeekCmd.Flags().IntVar(&peekLines, "lines", 0, "number of scrollback lines to capture; 0 means full scrollback")
	dropletPeekCmd.Flags().BoolVar(&peekRaw, "raw", false, "read the session log file directly instead of attaching to tmux")
	dropletPeekCmd.Flags().BoolVar(&peekFollow, "follow", false, "re-capture every 3 seconds (Ctrl-C to stop); use with --snapshot")
	dropletPeekCmd.Flags().BoolVar(&peekSnapshot, "snapshot", false, "capture a static snapshot instead of attaching read-only to the live session")

	dropletIssueResolveCmd.Flags().StringVar(&issueResolveEvidence, "evidence", "", "command + output proving resolution")
	dropletIssueRejectCmd.Flags().StringVar(&issueRejectEvidence, "evidence", "", "command + output proving issue still present")
	_ = dropletIssueResolveCmd.MarkFlagRequired("evidence")
	_ = dropletIssueRejectCmd.MarkFlagRequired("evidence")
	dropletIssueListCmd.Flags().BoolVar(&issueListOpen, "open", false, "only show open issues")
	dropletIssueListCmd.Flags().StringVar(&issueListFlaggedBy, "flagged-by", "", "filter by cataractae name that filed the issue")

	dropletIssueCmd.AddCommand(dropletIssueAddCmd, dropletIssueResolveCmd, dropletIssueRejectCmd, dropletIssueListCmd)

	dropletEditCmd.Flags().StringVarP(&editTitle, "title", "t", "", "new title")
	dropletEditCmd.Flags().StringVar(&editDescription, "description", "", "new description (use - to read from stdin)")
	dropletEditCmd.Flags().StringVarP(&editComplexity, "complexity", "x", "", "new complexity: standard|full|critical (or 1-3)")
	dropletEditCmd.Flags().IntVarP(&editPriority, "priority", "p", 0, "new priority (positive integer)")

	dropletTailCmd.Flags().StringVar(&tailFmt, "format", "text", "output format: text or json")
	dropletTailCmd.Flags().IntVar(&tailCount, "lines", 20, "number of historical events to show on start")
	dropletTailCmd.Flags().BoolVar(&tailFollow, "follow", false, "keep watching for new events (like tail -f)")

	dropletLogCmd.Flags().StringVar(&logFmt, "format", "text", "output format: text or json")

	dropletHistoryCmd.Flags().StringVar(&historyFmt, "format", "text", "output format: text or json")

	dropletCmd.AddCommand(dropletAddCmd, dropletListCmd, dropletShowCmd, dropletNoteCmd,
		dropletCloseCmd, dropletReopenCmd, dropletPurgeCmd,
		dropletPassCmd, dropletRecirculateCmd, dropletPoolCmd, dropletCancelCmd, dropletApproveCmd,
		dropletStatsCmd, dropletDepsCmd, dropletPeekCmd, dropletIssueCmd, dropletSearchCmd,
		dropletExportCmd, dropletRenameCmd, dropletRestartCmd, dropletEditCmd,
		dropletTailCmd, dropletHeartbeatCmd, dropletLogCmd, dropletHistoryCmd)
	rootCmd.AddCommand(dropletCmd)
}

// parseComplexity accepts "1"-"3" or names "standard","full","critical".
func parseComplexity(s string) (int, error) {
	switch s {
	case "1", "standard":
		return 1, nil
	case "2", "full", "":
		return 2, nil
	case "3", "critical":
		return 3, nil
	}
	return 0, fmt.Errorf("invalid complexity %q: use 1/standard, 2/full, 3/critical", s)
}

// complexityName returns the human name for a complexity level.
func complexityName(cx int) string {
	switch cx {
	case 1:
		return "standard"
	case 3:
		return "critical"
	default:
		return "full"
	}
}

// inferPrefix extracts a short prefix from a repo path for ID generation.
// e.g., "github.com/Org/MyRepo" → "mr" (lowercase initials of last segment),
// or just the first two chars if the name is short.
func inferPrefix(repo string) string {
	// Use last path segment.
	name := repo
	for i := len(repo) - 1; i >= 0; i-- {
		if repo[i] == '/' {
			name = repo[i+1:]
			break
		}
	}
	if len(name) == 0 {
		return "ct"
	}
	if len(name) <= 2 {
		return name
	}
	// Use first two lowercase chars.
	r := []byte{name[0], name[1]}
	for i := range r {
		if r[i] >= 'A' && r[i] <= 'Z' {
			r[i] += 32
		}
	}
	return string(r)
}
