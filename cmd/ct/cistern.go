package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/spf13/cobra"
)

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
	addRefine      bool
	addYes         bool
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

		if addRefine {
			proposals, err := callRefineAPI(addTitle, addDescription)
			if err != nil {
				return err
			}
			c, err := cistern.New(resolveDBPath(), inferPrefix(addRepo))
			if err != nil {
				return err
			}
			defer c.Close()
			if addYes {
				return runRefineNonInteractive(c, proposals, addRepo, addPriority)
			}
			return runRefineInteractive(c, proposals, addRepo, addPriority)
		}

		c, err := cistern.New(resolveDBPath(), inferPrefix(addRepo))
		if err != nil {
			return err
		}
		defer c.Close()

		cx, err := parseComplexity(addComplexity)
		if err != nil {
			return err
		}
		item, err := c.Add(addRepo, addTitle, addDescription, addPriority, cx, addDependsOn...)
		if err != nil {
			return err
		}
		fmt.Printf("Droplet added to cistern. %s: %s\n", item.ID, item.Title)
		return nil
	},
}

// --- cistern list ---

var (
	listRepo   string
	listStatus string
	listOutput string
	listAll    bool
)

var dropletListCmd = &cobra.Command{
	Use:   "list",
	Short: "List droplets in the cistern",
	RunE: func(cmd *cobra.Command, args []string) error {
		if listOutput != "table" && listOutput != "json" {
			return fmt.Errorf("--output must be table or json")
		}
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		items, err := c.List(listRepo, listStatus)
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
		filterDelivered := listStatus == "" && !listAll
		var active, dimmed []*cistern.Droplet
		for _, item := range items {
			if filterDelivered && item.Status == "delivered" {
				dimmed = append(dimmed, item)
			} else {
				active = append(active, item)
			}
		}

		if len(active) == 0 && (!listAll || len(dimmed) == 0) {
			fmt.Println("Cistern dry.")
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
			ds := displayStatus(item.Status)
			cataracta := item.CurrentCataracta
			if cataracta == "" {
				cataracta = "\u2014"
			}
			if item.Status == "open" {
				blockedBy, _ := c.GetBlockedBy(item.ID)
				if len(blockedBy) > 0 {
					ds = "\u2298 blocked"
					cataracta = "waiting: " + blockedBy[0]
				}
			}
			elapsed := "\u2014"
			if item.Status == "in_progress" {
				elapsed = formatElapsed(time.Since(item.UpdatedAt))
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				item.ID, complexityName(item.Complexity), truncate(item.Title, titleMax),
				ds, elapsed, cataracta)
		}
		if listAll && len(dimmed) > 0 {
			fmt.Fprintln(tw, "— delivered —")
			for _, item := range dimmed {
				age := formatElapsed(time.Since(item.UpdatedAt))
				cataracta := item.CurrentCataracta
				if cataracta == "" {
					cataracta = "\u2014"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					item.ID, complexityName(item.Complexity), truncate(item.Title, titleMax),
					"delivered", age, cataracta)
			}
		}
		return tw.Flush()
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
		ds := displayStatus(item.Status)
		cataracta := item.CurrentCataracta
		if cataracta == "" {
			cataracta = "—"
		}
		elapsed := "—"
		if item.Status == "in_progress" {
			elapsed = formatElapsed(time.Since(item.UpdatedAt))
		}
		title := padRight(truncate(item.Title, titleMax), titleMax)
		sc := statusCell(ds, colSt)
		fmt.Printf("  %-*s  %-*s  %s  %s  %-*s  %s\n",
			colID, item.ID, colCX, complexityName(item.Complexity),
			title, sc, colEl, elapsed, cataracta)
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
	case "escalated", "stagnant":
		return "stagnant"
	case "closed", "delivered":
		return "delivered"
	default:
		return status
	}
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
		fmt.Printf("Stage:       %s\n", item.CurrentCataracta)

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
				fmt.Printf("  [%s] %s\n", n.CataractaName, n.Content)
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

		if err := c.AddNote(args[0], "manual", args[1]); err != nil {
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

// --- cistern escalate ---

var escalateReason string

var dropletEscalateCmd = &cobra.Command{
	Use:   "escalate <id>",
		Short: "Mark a droplet stagnant — escalate for human attention",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if escalateReason == "" {
			return fmt.Errorf("--reason is required")
		}
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.Escalate(args[0], escalateReason); err != nil {
			return err
		}
			fmt.Printf("droplet %s stagnant\n", args[0])
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
		Short: "Delete closed/stagnant droplets older than a threshold",
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
		fmt.Fprintf(tw, "  stagnant\t%d\n", s.Stagnant)
		fmt.Fprintln(tw, "  ──────────────")
		fmt.Fprintf(tw, "  total\t%d\n", s.Flowing+s.Queued+s.Delivered+s.Stagnant)
		return tw.Flush()
	},
}

// --- cistern pass ---

var passNotes string

var dropletPassCmd = &cobra.Command{
	Use:   "pass <id>",
	Short: "Signal pass outcome — work complete, advance to next cataracta",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if passNotes != "" {
			if err := c.AddNote(args[0], "manual", passNotes); err != nil {
				return err
			}
		}
		if err := c.SetOutcome(args[0], "pass"); err != nil {
			return err
		}
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
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if recirculateNotes != "" {
			if err := c.AddNote(args[0], "manual", recirculateNotes); err != nil {
				return err
			}
		}
		outcome := "recirculate"
		if recirculateTo != "" {
			outcome = "recirculate:" + recirculateTo
		}
		if err := c.SetOutcome(args[0], outcome); err != nil {
			return err
		}
		fmt.Printf("droplet %s: outcome=%s\n", args[0], outcome)
		return nil
	},
}

// --- cistern block ---

var blockNotes string

var dropletBlockCmd = &cobra.Command{
	Use:   "block <id>",
	Short: "Signal block outcome — genuinely blocked, cannot proceed",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if blockNotes != "" {
			if err := c.AddNote(args[0], "manual", blockNotes); err != nil {
				return err
			}
		}
		if err := c.SetOutcome(args[0], "block"); err != nil {
			return err
		}
		fmt.Printf("droplet %s: outcome=block\n", args[0])
		return nil
	},
}

func init() {
	dropletAddCmd.Flags().StringVar(&addTitle, "title", "", "droplet title (required)")
	dropletAddCmd.Flags().StringVar(&addDescription, "description", "", "droplet description")
	dropletAddCmd.Flags().IntVar(&addPriority, "priority", 2, "priority (1=highest)")
	dropletAddCmd.Flags().StringVar(&addRepo, "repo", "", "target repository (required)")
	dropletAddCmd.Flags().StringVarP(&addComplexity, "complexity", "x", "3", "droplet complexity: 1/trivial, 2/standard, 3/full (default), 4/critical")
	dropletAddCmd.Flags().StringArrayVar(&addDependsOn, "depends-on", nil, "dependency droplet ID (repeatable)")
	dropletAddCmd.Flags().BoolVar(&addRefine, "refine", false, "run LLM-assisted refinement before creating droplet(s)")
	dropletAddCmd.Flags().BoolVar(&addYes, "yes", false, "skip confirmation prompts (for non-interactive/agent use)")

	dropletDepsCmd.Flags().StringVar(&depsAdd, "add", "", "add a dependency (dep ID)")
	dropletDepsCmd.Flags().StringVar(&depsRemove, "remove", "", "remove a dependency (dep ID)")

	dropletListCmd.Flags().StringVar(&listRepo, "repo", "", "filter by repo")
	dropletListCmd.Flags().StringVar(&listStatus, "status", "", "filter by status (open|in_progress|delivered|stagnant)")
	dropletListCmd.Flags().StringVar(&listOutput, "output", "table", "output format: table or json")
	dropletListCmd.Flags().BoolVar(&listAll, "all", false, "include delivered droplets in a dimmed section below active ones")

	dropletEscalateCmd.Flags().StringVar(&escalateReason, "reason", "", "escalation reason (required)")

	dropletPurgeCmd.Flags().StringVar(&purgeOlderThan, "older-than", "", "delete droplets older than this duration (e.g. 30d, 24h) (required)")
	dropletPurgeCmd.Flags().BoolVar(&purgeDryRun, "dry-run", false, "show what would be deleted without deleting")

	dropletPassCmd.Flags().StringVar(&passNotes, "notes", "", "add a note before signaling pass")
	dropletRecirculateCmd.Flags().StringVar(&recirculateTo, "to", "", "named cataracta to recirculate to (e.g. --to implement)")
	dropletRecirculateCmd.Flags().StringVar(&recirculateNotes, "notes", "", "add a note before signaling recirculate")
	dropletBlockCmd.Flags().StringVar(&blockNotes, "notes", "", "add a note before signaling block")

	dropletCmd.AddCommand(dropletAddCmd, dropletListCmd, dropletShowCmd, dropletNoteCmd,
		dropletCloseCmd, dropletReopenCmd, dropletEscalateCmd, dropletPurgeCmd,
		dropletPassCmd, dropletRecirculateCmd, dropletBlockCmd, dropletStatsCmd,
		dropletDepsCmd)
	rootCmd.AddCommand(dropletCmd)
}

// parseComplexity accepts "1"-"4" or names "trivial","standard","full","critical".
func parseComplexity(s string) (int, error) {
	switch s {
	case "1", "trivial":
		return 1, nil
	case "2", "standard":
		return 2, nil
	case "3", "full", "":
		return 3, nil
	case "4", "critical":
		return 4, nil
	}
	return 0, fmt.Errorf("invalid complexity %q: use 1/trivial, 2/standard, 3/full, 4/critical", s)
}

// complexityName returns the human name for a complexity level.
func complexityName(cx int) string {
	switch cx {
	case 1:
		return "trivial"
	case 2:
		return "standard"
	case 4:
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
