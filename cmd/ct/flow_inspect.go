package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/MichielDean/citadel/internal/queue"
	"github.com/MichielDean/citadel/internal/workflow"
	"github.com/spf13/cobra"
)

var inspectTable bool

type citadelInfo struct {
	Config      string `json:"config"`
	FarmRunning bool   `json:"farm_running"`
}

type channelInfo struct {
	Name           string  `json:"name"`
	Repo           string  `json:"repo"`
	Session        *string `json:"session"`
	SessionAlive   bool    `json:"session_alive"`
	DropID         *string `json:"drop_id"`
	DropTitle      *string `json:"drop_title"`
	Valve          *string `json:"valve"`
	ElapsedSeconds *int    `json:"elapsed_seconds"`
}

type cisternInfo struct {
	Total    int `json:"total"`
	Flowing  int `json:"flowing"`
	Queued   int `json:"queued"`
	Poisoned int `json:"poisoned"`
	Closed   int `json:"closed"`
}

type dropInfo struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Complexity     int       `json:"complexity"`
	ComplexityName string    `json:"complexity_name"`
	Status         string    `json:"status"`
	Valve          string    `json:"valve"`
	Assignee       string    `json:"assignee"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type recentEvent struct {
	Time  time.Time `json:"time"`
	Drop  string    `json:"drop"`
	Event string    `json:"event"`
}

type inspectOutput struct {
	Citadel      citadelInfo   `json:"citadel"`
	Channels     []channelInfo `json:"channels"`
	Cistern      cisternInfo   `json:"cistern"`
	Drops        []dropInfo    `json:"drops"`
	RecentEvents []recentEvent `json:"recent_events"`
}

var flowInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Output a JSON snapshot of current Citadel state",
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := buildInspectOutput(resolveConfigPath(), resolveDBPath())
		if err != nil {
			return err
		}

		if inspectTable {
			return printInspectTable(out)
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	},
}

func buildInspectOutput(cfgPath, dbPath string) (inspectOutput, error) {
	// Farm running: check for lock file.
	home, _ := os.UserHomeDir()
	lockFile := filepath.Join(home, ".citadel", "citadel.lock")
	_, lockErr := os.Stat(lockFile)

	out := inspectOutput{
		Citadel: citadelInfo{
			Config:      cfgPath,
			FarmRunning: lockErr == nil,
		},
		Channels:     []channelInfo{},
		Drops:        []dropInfo{},
		RecentEvents: []recentEvent{},
	}

	// Load config best-effort — may not exist in test environments.
	cfg, err := workflow.ParseFarmConfig(cfgPath)
	if err != nil {
		cfg = &workflow.FarmConfig{}
	}

	// Open queue.
	c, err := queue.New(dbPath, "")
	if err != nil {
		return out, fmt.Errorf("queue: %w", err)
	}
	defer c.Close()

	// List all items.
	allItems, err := c.List("", "")
	if err != nil {
		return out, fmt.Errorf("list items: %w", err)
	}

	// Build assignee lookup and cistern counts.
	type assignInfo struct {
		id, title, valve string
		updatedAt        time.Time
	}
	assigneeMap := map[string]assignInfo{}

	cistern := cisternInfo{}
	for _, item := range allItems {
		switch item.Status {
		case "in_progress":
			cistern.Flowing++
			cistern.Total++
		case "open":
			cistern.Queued++
			cistern.Total++
		case "escalated":
			cistern.Poisoned++
			cistern.Total++
		case "closed":
			cistern.Closed++
		}
		if item.Assignee != "" {
			assigneeMap[item.Assignee] = assignInfo{
				id:        item.ID,
				title:     item.Title,
				valve:     item.CurrentStep,
				updatedAt: item.UpdatedAt,
			}
		}
	}
	out.Cistern = cistern

	// Build channels from config workers.
	for _, repo := range cfg.Repos {
		for _, name := range repoWorkerNames(repo) {
			ch := channelInfo{
				Name: name,
				Repo: repo.Name,
			}
			if info, ok := assigneeMap[name]; ok {
				session := name + "-" + info.id
				alive := tmuxSessionAlive(session)
				elapsed := int(time.Since(info.updatedAt).Seconds())
				ch.Session = &session
				ch.SessionAlive = alive
				ch.DropID = &info.id
				ch.DropTitle = &info.title
				ch.Valve = &info.valve
				ch.ElapsedSeconds = &elapsed
			}
			out.Channels = append(out.Channels, ch)
		}
	}
	if out.Channels == nil {
		out.Channels = []channelInfo{}
	}

	// Build drops (exclude closed).
	for _, item := range allItems {
		if item.Status == "closed" {
			continue
		}
		out.Drops = append(out.Drops, dropInfo{
			ID:             item.ID,
			Title:          item.Title,
			Complexity:     item.Complexity,
			ComplexityName: complexityName(item.Complexity),
			Status:         item.Status,
			Valve:          item.CurrentStep,
			Assignee:       item.Assignee,
			UpdatedAt:      item.UpdatedAt,
		})
	}
	if out.Drops == nil {
		out.Drops = []dropInfo{}
	}

	// Recent events.
	events, err := c.ListRecentEvents(20)
	if err == nil && len(events) > 0 {
		for _, e := range events {
			out.RecentEvents = append(out.RecentEvents, recentEvent{
				Time:  e.Time,
				Drop:  e.Drop,
				Event: e.Event,
			})
		}
	}

	return out, nil
}

func tmuxSessionAlive(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func printInspectTable(out inspectOutput) error {
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintf(tw, "Config:\t%s\n", out.Citadel.Config)
	fmt.Fprintf(tw, "Farm running:\t%v\n", out.Citadel.FarmRunning)
	fmt.Fprintf(tw, "\nCistern:\ttotal=%d  flowing=%d  queued=%d  poisoned=%d  closed=%d\n",
		out.Cistern.Total, out.Cistern.Flowing, out.Cistern.Queued, out.Cistern.Poisoned, out.Cistern.Closed)
	if len(out.Channels) > 0 {
		fmt.Fprintf(tw, "\nChannels:\n")
		for _, ch := range out.Channels {
			session := "-"
			if ch.Session != nil {
				session = *ch.Session
			}
			fmt.Fprintf(tw, "  %s\t%s\talive=%v\n", ch.Name, session, ch.SessionAlive)
		}
	}
	if len(out.Drops) > 0 {
		fmt.Fprintf(tw, "\nDrops:\n")
		for _, d := range out.Drops {
			fmt.Fprintf(tw, "  %s\t%s\t[%s]\t%s\n", d.ID, d.Title, d.Status, d.Assignee)
		}
	}
	return nil
}

func init() {
	flowInspectCmd.Flags().BoolVar(&inspectTable, "table", false, "show human-readable table instead of JSON")
	flowCmd.AddCommand(flowInspectCmd)
}
