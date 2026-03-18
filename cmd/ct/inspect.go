package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/spf13/cobra"
)

var inspectTable bool

type cisternStateInfo struct {
	Config      string `json:"config"`
	Running     bool   `json:"running"`
}

type cataractaInfo struct {
	Name           string  `json:"name"`
	Repo           string  `json:"repo"`
	Session        *string `json:"session"`
	SessionAlive   bool    `json:"session_alive"`
	DropletID      *string `json:"droplet_id"`
	DropletTitle   *string `json:"droplet_title"`
	Stage          *string `json:"stage"`
	ElapsedSeconds *int    `json:"elapsed_seconds"`
}

type cisternInfo struct {
	Total    int `json:"total"`
	Flowing  int `json:"flowing"`
	Queued   int `json:"queued"`
	Stagnant  int `json:"stagnant"`
	Delivered int `json:"delivered"`
}

type dropletInfo struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Complexity     int       `json:"complexity"`
	ComplexityName string    `json:"complexity_name"`
	Status         string    `json:"status"`
	Stage          string    `json:"stage"`
	Operator       string    `json:"operator"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type recentEvent struct {
	Time    time.Time `json:"time"`
	Droplet string    `json:"droplet"`
	Event   string    `json:"event"`
}

type inspectOutput struct {
	Cistern      cisternStateInfo `json:"cistern"`
	Cataractae      []cataractaInfo     `json:"cataractae"`
	Counts       cisternInfo      `json:"counts"`
	Droplets     []dropletInfo    `json:"droplets"`
	RecentEvents []recentEvent    `json:"recent_events"`
}

var flowInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Output a JSON snapshot of current Cistern state",
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
	// Running: check for lock file.
	home, _ := os.UserHomeDir()
	lockFile := filepath.Join(home, ".cistern", "cistern.lock")
	_, lockErr := os.Stat(lockFile)

	out := inspectOutput{
		Cistern: cisternStateInfo{
			Config:  cfgPath,
			Running: lockErr == nil,
		},
		Cataractae:      []cataractaInfo{},
		Droplets:     []dropletInfo{},
		RecentEvents: []recentEvent{},
	}

	// Load config best-effort — may not exist in test environments.
	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		cfg = &aqueduct.AqueductConfig{}
	}

	// Open cistern.
	c, err := cistern.New(dbPath, "")
	if err != nil {
		return out, fmt.Errorf("cistern: %w", err)
	}
	defer c.Close()

	// List all items.
	allItems, err := c.List("", "")
	if err != nil {
		return out, fmt.Errorf("list items: %w", err)
	}

	// Build assignee lookup and queue counts.
	type assignInfo struct {
		id, title, stage string
		updatedAt        time.Time
	}
	assigneeMap := map[string]assignInfo{}

	queueState := cisternInfo{}
	for _, item := range allItems {
		switch item.Status {
		case "in_progress":
			queueState.Flowing++
			queueState.Total++
		case "open":
			queueState.Queued++
			queueState.Total++
		case "stagnant":
			queueState.Stagnant++
			queueState.Total++
		case "delivered":
			queueState.Delivered++
		}
		if item.Assignee != "" {
			assigneeMap[item.Assignee] = assignInfo{
				id:        item.ID,
				title:     item.Title,
				stage:     item.CurrentCataracta,
				updatedAt: item.UpdatedAt,
			}
		}
	}
	out.Counts = queueState

	// Build cataractae from config operators.
	for _, repo := range cfg.Repos {
		for _, name := range repoWorkerNames(repo) {
			ch := cataractaInfo{
				Name: name,
				Repo: repo.Name,
			}
			if info, ok := assigneeMap[name]; ok {
				session := name + "-" + info.id
				alive := tmuxSessionAlive(session)
				elapsed := int(time.Since(info.updatedAt).Seconds())
				ch.Session = &session
				ch.SessionAlive = alive
				ch.DropletID = &info.id
				ch.DropletTitle = &info.title
				ch.Stage = &info.stage
				ch.ElapsedSeconds = &elapsed
			}
			out.Cataractae = append(out.Cataractae, ch)
		}
	}
	if out.Cataractae == nil {
		out.Cataractae = []cataractaInfo{}
	}

	// Build droplets (exclude delivered).
	for _, item := range allItems {
		if item.Status == "delivered" {
			continue
		}
		out.Droplets = append(out.Droplets, dropletInfo{
			ID:             item.ID,
			Title:          item.Title,
			Complexity:     item.Complexity,
			ComplexityName: complexityName(item.Complexity),
			Status:         item.Status,
			Stage:          item.CurrentCataracta,
			Operator:       item.Assignee,
			UpdatedAt:      item.UpdatedAt,
		})
	}
	if out.Droplets == nil {
		out.Droplets = []dropletInfo{}
	}

	// Recent events.
	events, err := c.ListRecentEvents(20)
	if err == nil && len(events) > 0 {
		for _, e := range events {
			out.RecentEvents = append(out.RecentEvents, recentEvent{
				Time:    e.Time,
				Droplet: e.Droplet,
				Event:   e.Event,
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
	fmt.Fprintf(tw, "Config:\t%s\n", out.Cistern.Config)
	fmt.Fprintf(tw, "Running:\t%v\n", out.Cistern.Running)
	fmt.Fprintf(tw, "\nQueue:\ttotal=%d  flowing=%d  queued=%d  stagnant=%d  delivered=%d\n",
		out.Counts.Total, out.Counts.Flowing, out.Counts.Queued, out.Counts.Stagnant, out.Counts.Delivered)
	if len(out.Cataractae) > 0 {
		fmt.Fprintf(tw, "\\nCataractae:\n")
		for _, ch := range out.Cataractae {
			session := "-"
			if ch.Session != nil {
				session = *ch.Session
			}
			fmt.Fprintf(tw, "  %s\t%s\talive=%v\n", ch.Name, session, ch.SessionAlive)
		}
	}
	if len(out.Droplets) > 0 {
		fmt.Fprintf(tw, "\nDroplets:\n")
		for _, d := range out.Droplets {
			fmt.Fprintf(tw, "  %s\t%s\t[%s]\t%s\n", d.ID, d.Title, d.Status, d.Operator)
		}
	}
	return nil
}

func init() {
	flowInspectCmd.Flags().BoolVar(&inspectTable, "table", false, "show human-readable table instead of JSON")
	aqueductCmd.AddCommand(flowInspectCmd)
}
