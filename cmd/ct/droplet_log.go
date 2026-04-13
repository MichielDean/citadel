package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/spf13/cobra"
)

var logFmt string

type logEntry struct {
	Time       string `json:"time"`
	Cataractae string `json:"cataractae"`
	Event      string `json:"event"`
	Detail     string `json:"detail"`
}

var dropletLogCmd = &cobra.Command{
	Use:   "log <id>",
	Short: "Show chronological activity log for a droplet",
	Long: `Display a timeline of events for a droplet, including stage
transitions, outcome signals, scheduler events, and notes.

Output modes:
  --format text   Tab-aligned table with timestamps (default)
  --format json   One JSON object per line (NDJSON)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLog(os.Stdout, args[0])
	},
}

func runLog(out io.Writer, id string) error {
	if logFmt != "text" && logFmt != "json" {
		return fmt.Errorf("--format must be text or json")
	}

	c, err := cistern.New(resolveDBPath(), "")
	if err != nil {
		return err
	}
	defer c.Close()

	item, err := c.Get(id)
	if err != nil {
		return err
	}

	changes, err := c.GetDropletChanges(id, 10000)
	if err != nil {
		return err
	}

	entries := buildLogEntries(item, changes)

	if logFmt == "json" {
		return printLogJSON(out, entries)
	}
	return printLogText(out, item, entries)
}

func buildLogEntries(item *cistern.Droplet, changes []cistern.DropletChange) []logEntry {
	var entries []logEntry

	entries = append(entries, logEntry{
		Time:       item.CreatedAt.Format("2006-01-02 15:04:05"),
		Cataractae: "",
		Event:      "created",
		Detail:     fmt.Sprintf("status=open title=%q priority=%d", item.Title, item.Priority),
	})

	for _, ch := range changes {
		var evt, detail string
		var cataractae string

		if ch.Kind == "note" {
			before, after, found := strings.Cut(ch.Value, ": ")
			if found {
				cataractae = before
				detail = after
			} else {
				detail = ch.Value
			}
			evt = "note"
		} else {
			before, after, found := strings.Cut(ch.Value, ": ")
			if found {
				evt = before
				detail = after
			} else {
				evt = ch.Value
			}
			switch evt {
			case "pool":
				evt = "pooled"
				if detail != "" {
					detail = "reason: " + detail
				}
			}
		}

		entries = append(entries, logEntry{
			Time:       ch.Time.Format("2006-01-02 15:04:05"),
			Cataractae: cataractae,
			Event:      evt,
			Detail:     detail,
		})
	}

	if !item.LastHeartbeatAt.IsZero() {
		entries = append(entries, logEntry{
			Time:       item.LastHeartbeatAt.Format("2006-01-02 15:04:05"),
			Cataractae: item.CurrentCataractae,
			Event:      "heartbeat",
			Detail:     "last heartbeat recorded",
		})
	}

	return entries
}

func printLogText(out io.Writer, item *cistern.Droplet, entries []logEntry) error {
	fmt.Fprintf(out, "Droplet: %s  Title: %s  Status: %s\n\n", item.ID, item.Title, displayStatus(item.Status))

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tCATARACTAE\tEVENT\tDETAIL")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Time, e.Cataractae, e.Event, e.Detail)
	}
	return tw.Flush()
}

func printLogJSON(out io.Writer, entries []logEntry) error {
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("json marshal error: %w", err)
		}
		fmt.Fprintln(out, string(line))
	}
	return nil
}
