package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/MichielDean/bullet-farm/internal/queue"
	"github.com/spf13/cobra"
)

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Manage work items in the queue",
}

// --- queue add ---

var (
	addTitle       string
	addDescription string
	addPriority    int
	addRepo        string
)

var queueAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new work item to the queue",
	RunE: func(cmd *cobra.Command, args []string) error {
		if addTitle == "" {
			return fmt.Errorf("--title is required")
		}
		if addRepo == "" {
			return fmt.Errorf("--repo is required")
		}
		c, err := queue.New(resolveDBPath(), inferPrefix(addRepo))
		if err != nil {
			return err
		}
		defer c.Close()

		item, err := c.Add(addRepo, addTitle, addDescription, addPriority)
		if err != nil {
			return err
		}
		fmt.Printf("created %s: %s\n", item.ID, item.Title)
		return nil
	},
}

// --- queue list ---

var (
	listRepo   string
	listStatus string
)

var queueListCmd = &cobra.Command{
	Use:   "list",
	Short: "List work items",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := queue.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		items, err := c.List(listRepo, listStatus)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println("no items found")
			return nil
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATUS\tPRI\tSTEP\tTITLE")
		for _, item := range items {
			step := item.CurrentStep
			if step == "" {
				step = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
				item.ID, item.Status, item.Priority, step, item.Title)
		}
		return tw.Flush()
	},
}

// --- queue show ---

var queueShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show details of a work item",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := queue.New(resolveDBPath(), "")
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
		fmt.Printf("Status:      %s\n", item.Status)
		fmt.Printf("Priority:    %d\n", item.Priority)
		fmt.Printf("Assignee:    %s\n", item.Assignee)
		fmt.Printf("Step:        %s\n", item.CurrentStep)
		fmt.Printf("Attempts:    %d\n", item.AttemptCount)
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
				fmt.Printf("  [%s] %s\n", n.StepName, n.Content)
			}
		}

		return nil
	},
}

// --- queue note ---

var queueNoteCmd = &cobra.Command{
	Use:   "note <id> <content>",
	Short: "Add a note to a work item",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := queue.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.AddNote(args[0], "manual", args[1]); err != nil {
			return err
		}
		fmt.Printf("note added to %s\n", args[0])
		return nil
	},
}

// --- queue close ---

var queueCloseCmd = &cobra.Command{
	Use:   "close <id>",
	Short: "Close a work item",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := queue.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.CloseItem(args[0]); err != nil {
			return err
		}
		fmt.Printf("closed %s\n", args[0])
		return nil
	},
}

// --- queue reopen ---

var queueReopenCmd = &cobra.Command{
	Use:   "reopen <id>",
	Short: "Reopen a closed or escalated work item",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := queue.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.UpdateStatus(args[0], "open"); err != nil {
			return err
		}
		fmt.Printf("reopened %s\n", args[0])
		return nil
	},
}

// --- queue escalate ---

var escalateReason string

var queueEscalateCmd = &cobra.Command{
	Use:   "escalate <id>",
	Short: "Escalate a work item for human attention",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if escalateReason == "" {
			return fmt.Errorf("--reason is required")
		}
		c, err := queue.New(resolveDBPath(), "")
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.Escalate(args[0], escalateReason); err != nil {
			return err
		}
		fmt.Printf("escalated %s\n", args[0])
		return nil
	},
}

func init() {
	queueAddCmd.Flags().StringVar(&addTitle, "title", "", "work item title (required)")
	queueAddCmd.Flags().StringVar(&addDescription, "description", "", "work item description")
	queueAddCmd.Flags().IntVar(&addPriority, "priority", 2, "priority (1=highest)")
	queueAddCmd.Flags().StringVar(&addRepo, "repo", "", "target repository (required)")

	queueListCmd.Flags().StringVar(&listRepo, "repo", "", "filter by repo")
	queueListCmd.Flags().StringVar(&listStatus, "status", "", "filter by status (open|in_progress|closed|escalated)")

	queueEscalateCmd.Flags().StringVar(&escalateReason, "reason", "", "escalation reason (required)")

	queueCmd.AddCommand(queueAddCmd, queueListCmd, queueShowCmd, queueNoteCmd,
		queueCloseCmd, queueReopenCmd, queueEscalateCmd)
	rootCmd.AddCommand(queueCmd)
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
		return "bf"
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
