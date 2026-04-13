package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/spf13/cobra"
)

var (
	tailFmt    string
	tailCount  int
	tailFollow bool
)

const tailMaxChanges = 10000

func isTailTerminal(status string) bool {
	switch status {
	case "delivered", "pooled", "cancelled":
		return true
	}
	return false
}

var dropletTailCmd = &cobra.Command{
	Use:   "tail <id>",
	Short: "Stream droplet status change events in real time",
	Long: `Watch a droplet and stream status change events to stdout.

Shows the last N events on start (default 20), then polls for new events
when used with --follow. Exits when the droplet reaches a terminal state
(delivered, pooled, cancelled) unless --follow is used.

Output modes:
  --format text   Timestamped one-line events (default)
  --format json   One JSON object per line (NDJSON)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTail(os.Stdout, args[0])
	},
}

func runTail(out io.Writer, id string) error {
	if tailFmt != "text" && tailFmt != "json" {
		return fmt.Errorf("--format must be text or json")
	}
	if tailCount < 1 {
		return fmt.Errorf("--lines must be >= 1")
	}

	c, err := cistern.New(resolveDBPath(), "")
	if err != nil {
		return err
	}
	defer c.Close()

	if _, err := c.Get(id); err != nil {
		return err
	}

	printChange := func(ch cistern.DropletChange) {
		ts := ch.Time.Format("2006-01-02 15:04:05")
		if tailFmt == "json" {
			line, err := json.Marshal(ch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "json marshal error: %v\n", err)
				return
			}
			fmt.Fprintln(out, string(line))
		} else {
			fmt.Fprintf(out, "%s [%s] %s\n", ts, ch.Kind, ch.Value)
		}
	}

	changes, err := c.GetDropletChanges(id, tailCount)
	if err != nil {
		return err
	}
	for _, ch := range changes {
		printChange(ch)
	}

	if !tailFollow {
		return nil
	}

	seenCount := len(changes)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			return nil
		case <-ticker.C:
			current, err := c.Get(id)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error polling droplet: %v\n", err)
				continue
			}

			allChanges, err := c.GetDropletChanges(id, tailMaxChanges)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error polling changes: %v\n", err)
				continue
			}

			for i := seenCount; i < len(allChanges); i++ {
				printChange(allChanges[i])
			}
			seenCount = len(allChanges)

			if isTailTerminal(current.Status) {
				fmt.Fprintf(out, "\n─── droplet %s: %s ───\n", id, displayStatus(current.Status))
				return nil
			}
		}
	}
}
