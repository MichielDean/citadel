package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
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
	addFilter      bool
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

		if addFilter {
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
				return runFilterNonInteractive(c, proposals, addRepo, addPriority)
			}
			return runFilterInteractive(c, proposals, addRepo, addPriority)
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
			ds := displayStatusForDroplet(item)
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
			if ds == "awaiting" {
				ds = "\u23f8 awaiting approval"
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
		ds := displayStatusForDroplet(item)
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

// displayStatusForDroplet returns the display status for a droplet, overriding
// for human-gated droplets to show "awaiting approval".
func displayStatusForDroplet(item *cistern.Droplet) string {
	if item.CurrentCataracta == "human" && (item.Status == "stagnant" || item.Status == "escalated") {
		return "awaiting"
	}
	return displayStatus(item.Status)
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
			fmt.Println("Cistern dry.")
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
			if ds == "awaiting" {
				ds = "\u23f8 awaiting approval"
			}
			elapsed := "\u2014"
			if item.Status == "in_progress" {
				elapsed = formatElapsed(time.Since(item.UpdatedAt))
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				item.ID, complexityName(item.Complexity), truncate(item.Title, titleMax),
				ds, elapsed, cataracta)
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
		_ = w.Write([]string{"id", "repo", "title", "description", "priority", "complexity", "status", "assignee", "current_cataracta", "outcome", "assigned_aqueduct", "last_reviewed_commit", "created_at", "updated_at"})
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
				item.CurrentCataracta,
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

		if err := c.UpdateTitle(args[0], args[1]); err != nil {
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

		issues, err := c.ListIssues(args[0], issueListOpen)
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
	Short: "Signal pass outcome — work complete, advance to next cataracta",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := cistern.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		// Refuse to pass if there are open issues.
		openCount, err := c.CountOpenIssues(args[0])
		if err != nil {
			return err
		}
		if openCount > 0 {
			issues, err2 := c.ListIssues(args[0], true)
			if err2 != nil {
				return err2
			}
			ids := make([]string, 0, len(issues))
			for _, iss := range issues {
				ids = append(ids, iss.ID)
			}
			return fmt.Errorf("cannot pass: %d open issue(s) remain: %s", openCount, strings.Join(ids, ", "))
		}

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
		if item.CurrentCataracta != "human" {
			return fmt.Errorf("%s is not awaiting human approval (cataracta: %s)", id, item.CurrentCataracta)
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
	peekLines  int
	peekRaw    bool
	peekFollow bool
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes ANSI color/style escape sequences from s.
func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// capturePane runs tmux capture-pane and returns the output.
func capturePane(session string, lines int) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-t", session, "-p",
		"-S", fmt.Sprintf("-%d", lines)).Output()
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

		// Check if tmux is available.
		if _, err := exec.LookPath("tmux"); err != nil {
			fmt.Println("tmux not installed")
			return nil
		}

		session := item.Repo + "-" + item.Assignee

		printCapture := func() {
			if err := exec.Command("tmux", "has-session", "-t", session).Run(); err != nil {
				fmt.Printf("No active tmux session found for %s — may have just completed\n", id)
				// Fall back to last 10 lines of the most recent note.
				notes, nerr := c.GetNotes(id)
				if nerr == nil && len(notes) > 0 {
					last := notes[len(notes)-1]
					lines := strings.Split(last.Content, "\n")
					if len(lines) > 10 {
						lines = lines[len(lines)-10:]
					}
					fmt.Println(strings.Join(lines, "\n"))
				}
				return
			}
			out, cerr := capturePane(session, peekLines)
			if cerr != nil {
				fmt.Fprintf(os.Stderr, "tmux capture-pane: %v\n", cerr)
				return
			}
			if !peekRaw {
				out = stripANSI(out)
			}
			fmt.Print(out)
		}

		if !peekFollow {
			printCapture()
			return nil
		}

		// --follow: re-capture every 3 seconds until Ctrl-C.
		printCapture()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			fmt.Println("───")
			printCapture()
		}
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
	dropletAddCmd.Flags().BoolVar(&addFilter, "filter", false, "run filtration — LLM-assisted pass to refine ideas into well-specified droplets")
	dropletAddCmd.Flags().BoolVar(&addYes, "yes", false, "skip confirmation prompts (for non-interactive/agent use)")

	dropletDepsCmd.Flags().StringVar(&depsAdd, "add", "", "add a dependency (dep ID)")
	dropletDepsCmd.Flags().StringVar(&depsRemove, "remove", "", "remove a dependency (dep ID)")

	dropletListCmd.Flags().StringVar(&listRepo, "repo", "", "filter by repo")
	dropletListCmd.Flags().StringVar(&listStatus, "status", "", "filter by status (open|in_progress|delivered|stagnant)")
	dropletListCmd.Flags().StringVar(&listOutput, "output", "table", "output format: table or json")
	dropletListCmd.Flags().BoolVar(&listAll, "all", false, "include delivered droplets in a dimmed section below active ones")

	dropletSearchCmd.Flags().StringVar(&searchQuery, "query", "", "filter by title substring (case-insensitive)")
	dropletSearchCmd.Flags().StringVar(&searchStatus, "status", "", "filter by status (open|in_progress|delivered|stagnant)")
	dropletSearchCmd.Flags().IntVar(&searchPriority, "priority", 0, "filter by priority (0 means no filter)")
	dropletSearchCmd.Flags().StringVar(&searchOutput, "output", "table", "output format: table or json")

	dropletExportCmd.Flags().StringVar(&exportFormat, "format", "json", "output format: json or csv")
	dropletExportCmd.Flags().StringVar(&exportQuery, "query", "", "filter by title substring (case-insensitive)")
	dropletExportCmd.Flags().StringVar(&exportStatus, "status", "", "filter by status (open|in_progress|delivered|stagnant)")
	dropletExportCmd.Flags().IntVar(&exportPriority, "priority", 0, "filter by priority (0 means no filter)")

	dropletEscalateCmd.Flags().StringVar(&escalateReason, "reason", "", "escalation reason (required)")

	dropletPurgeCmd.Flags().StringVar(&purgeOlderThan, "older-than", "", "delete droplets older than this duration (e.g. 30d, 24h) (required)")
	dropletPurgeCmd.Flags().BoolVar(&purgeDryRun, "dry-run", false, "show what would be deleted without deleting")

	dropletPassCmd.Flags().StringVar(&passNotes, "notes", "", "add a note before signaling pass")
	dropletRecirculateCmd.Flags().StringVar(&recirculateTo, "to", "", "named cataracta to recirculate to (e.g. --to implement)")
	dropletRecirculateCmd.Flags().StringVar(&recirculateNotes, "notes", "", "add a note before signaling recirculate")
	dropletBlockCmd.Flags().StringVar(&blockNotes, "notes", "", "add a note before signaling block")

	dropletPeekCmd.Flags().IntVar(&peekLines, "lines", 50, "number of lines to capture")
	dropletPeekCmd.Flags().BoolVar(&peekRaw, "raw", false, "do not strip ANSI codes")
	dropletPeekCmd.Flags().BoolVar(&peekFollow, "follow", false, "re-capture every 3 seconds (Ctrl-C to stop)")

	dropletIssueResolveCmd.Flags().StringVar(&issueResolveEvidence, "evidence", "", "command + output proving resolution")
	dropletIssueRejectCmd.Flags().StringVar(&issueRejectEvidence, "evidence", "", "command + output proving issue still present")
	_ = dropletIssueResolveCmd.MarkFlagRequired("evidence")
	_ = dropletIssueRejectCmd.MarkFlagRequired("evidence")
	dropletIssueListCmd.Flags().BoolVar(&issueListOpen, "open", false, "only show open issues")

	dropletIssueCmd.AddCommand(dropletIssueAddCmd, dropletIssueResolveCmd, dropletIssueRejectCmd, dropletIssueListCmd)

	dropletCmd.AddCommand(dropletAddCmd, dropletListCmd, dropletShowCmd, dropletNoteCmd,
		dropletCloseCmd, dropletReopenCmd, dropletEscalateCmd, dropletPurgeCmd,
		dropletPassCmd, dropletRecirculateCmd, dropletBlockCmd, dropletApproveCmd,
		dropletStatsCmd, dropletDepsCmd, dropletPeekCmd, dropletIssueCmd, dropletSearchCmd,
		dropletExportCmd, dropletRenameCmd)
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
