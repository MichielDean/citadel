package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/MichielDean/citadel/internal/queue"
	"github.com/MichielDean/citadel/internal/workflow"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	dashboardInnerWidth = 56 // inner content width (between ║ borders)
	refreshInterval     = 2 * time.Second
	recentEventLimit    = 5

	// ANSI color codes
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorDim    = "\033[2m"
	colorReset  = "\033[0m"

	// ANSI cursor/screen
	clearScreen = "\033[2J\033[H"
)

// ChannelInfo describes the state of a single channel (worker).
type ChannelInfo struct {
	Name       string
	ItemID     string
	Step       string
	Elapsed    time.Duration
	StepIndex  int // 1-based index of current step; 0 if unknown
	TotalSteps int
}

// DashboardData holds all data required to render the dashboard.
type DashboardData struct {
	ChannelCount int
	FlowingCount int
	QueuedCount  int
	DoneCount    int
	Channels     []ChannelInfo
	CisternItems []*queue.WorkItem // flowing + queued
	RecentItems  []*queue.WorkItem // recently closed/escalated
	FarmRunning  bool
	FetchedAt    time.Time
}

// fetchDashboardData loads config and queue state into a DashboardData.
// On any error (missing config, missing DB) it returns a partial/idle result
// rather than an error, so the dashboard degrades gracefully.
func fetchDashboardData(cfgPath, dbPath string) *DashboardData {
	data := &DashboardData{FetchedAt: time.Now()}

	cfg, err := workflow.ParseFarmConfig(cfgPath)
	if err != nil {
		// Config not found — show aqueducts closed.
		return data
	}

	// Build channel list and load workflow steps for each repo.
	type channelEntry struct {
		name string
		repo string
	}
	var configChannels []channelEntry
	allSteps := map[string][]workflow.WorkflowStep{}
	cfgDir := filepath.Dir(cfgPath)
	for _, repo := range cfg.Repos {
		names := repoWorkerNames(repo)
		for _, name := range names {
			configChannels = append(configChannels, channelEntry{name, repo.Name})
		}
		data.ChannelCount += len(names)

		wfPath := repo.WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(cfgDir, wfPath)
		}
		if wf, wfErr := workflow.ParseWorkflow(wfPath); wfErr == nil {
			allSteps[repo.Name] = wf.Steps
		}
	}

	// Open queue — if it fails, show channels as idle.
	c, err := queue.New(dbPath, "")
	if err != nil {
		channels := make([]ChannelInfo, len(configChannels))
		for i, ch := range configChannels {
			channels[i] = ChannelInfo{Name: ch.name}
		}
		data.Channels = channels
		return data
	}
	defer c.Close()

	allItems, err := c.List("", "")
	if err != nil {
		channels := make([]ChannelInfo, len(configChannels))
		for i, ch := range configChannels {
			channels[i] = ChannelInfo{Name: ch.name}
		}
		data.Channels = channels
		return data
	}

	// Tally counts and build assignee map.
	assigneeMap := map[string]*queue.WorkItem{}
	for _, item := range allItems {
		switch item.Status {
		case "in_progress":
			data.FlowingCount++
			if item.Assignee != "" {
				assigneeMap[item.Assignee] = item
			}
		case "open":
			data.QueuedCount++
		case "closed":
			data.DoneCount++
		}
	}

	// Build channel infos.
	channels := make([]ChannelInfo, len(configChannels))
	for i, ch := range configChannels {
		ci := ChannelInfo{Name: ch.name}
		if item, ok := assigneeMap[ch.name]; ok {
			ci.ItemID = item.ID
			ci.Step = item.CurrentStep
			ci.Elapsed = time.Since(item.UpdatedAt)
			steps := allSteps[ch.repo]
			ci.TotalSteps = len(steps)
			ci.StepIndex = stepIndexInWorkflow(item.CurrentStep, steps)
		}
		channels[i] = ci
	}
	data.Channels = channels

	// Cistern: in_progress and open items.
	for _, item := range allItems {
		if item.Status == "in_progress" || item.Status == "open" {
			data.CisternItems = append(data.CisternItems, item)
		}
	}

	// Recent flow: most recently updated closed/escalated items.
	var recent []*queue.WorkItem
	for _, item := range allItems {
		if item.Status == "closed" || item.Status == "escalated" {
			recent = append(recent, item)
		}
	}
	sort.Slice(recent, func(i, j int) bool {
		return recent[i].UpdatedAt.After(recent[j].UpdatedAt)
	})
	if len(recent) > recentEventLimit {
		recent = recent[:recentEventLimit]
	}
	data.RecentItems = recent

	data.FarmRunning = true
	return data
}

// stepIndexInWorkflow returns the 1-based index of stepName in steps, or 0 if not found.
func stepIndexInWorkflow(stepName string, steps []workflow.WorkflowStep) int {
	for i, s := range steps {
		if s.Name == stepName {
			return i + 1
		}
	}
	return 0
}

// progressBar renders a filled/empty progress bar of barWidth characters.
// E.g. stepIndex=2, total=6, barWidth=6 → "████░░"
func progressBar(stepIndex, total, barWidth int) string {
	if total <= 0 || stepIndex <= 0 {
		return strings.Repeat("░", barWidth)
	}
	filled := stepIndex * barWidth / total
	if filled > barWidth {
		filled = barWidth
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
}

// formatElapsed returns "Xm Ys" for durations >= 1 minute, "Xs" otherwise.
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < 0 {
		d = 0
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// padRight pads s to width using spaces, truncating if longer.
func padRight(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	return s + strings.Repeat(" ", width-len(r))
}

// borderLine returns a full-width double-line separator "╠═...═╣".
func borderLine() string {
	return "╠" + strings.Repeat("═", dashboardInnerWidth+2) + "╣"
}

// contentLine wraps content in ║ borders, padded to dashboardInnerWidth.
func contentLine(content string) string {
	return "║ " + padRight(content, dashboardInnerWidth) + " ║"
}

// renderDashboard produces the full dashboard string for the given data.
func renderDashboard(data *DashboardData) string {
	var sb strings.Builder
	totalWidth := dashboardInnerWidth + 4 // "║ " + content + " ║"

	// Header.
	title := " CITADEL "
	padTotal := totalWidth - 2 - len(title)
	leftPad := padTotal / 2
	rightPad := padTotal - leftPad
	sb.WriteString("╔" + strings.Repeat("═", leftPad) + title + strings.Repeat("═", rightPad) + "╗\n")

	// Summary line.
	summary := fmt.Sprintf("%d channels open  •  %d flowing  •  %d queued  •  %d done",
		data.ChannelCount, data.FlowingCount, data.QueuedCount, data.DoneCount)
	sb.WriteString(contentLine(summary) + "\n")

	// CHANNELS section.
	sb.WriteString(borderLine() + "\n")
	sb.WriteString(contentLine("CHANNELS") + "\n")

	if len(data.Channels) == 0 {
		sb.WriteString(contentLine("  Aqueducts closed") + "\n")
	} else {
		for _, ch := range data.Channels {
			sb.WriteString(contentLine(renderChannelLine(ch)) + "\n")
		}
	}

	// CISTERN section.
	sb.WriteString(borderLine() + "\n")
	sb.WriteString(contentLine("CISTERN") + "\n")

	if len(data.CisternItems) == 0 {
		sb.WriteString(contentLine("  Cistern dry.") + "\n")
	} else {
		for _, item := range data.CisternItems {
			sb.WriteString(contentLine(renderCisternLine(item)) + "\n")
		}
	}

	// RECENT FLOW section.
	sb.WriteString(borderLine() + "\n")
	sb.WriteString(contentLine("RECENT FLOW") + "\n")

	if len(data.RecentItems) == 0 {
		sb.WriteString(contentLine("  No recent flow.") + "\n")
	} else {
		for _, item := range data.RecentItems {
			sb.WriteString(contentLine(renderRecentLine(item)) + "\n")
		}
	}

	// Footer.
	sb.WriteString("╚" + strings.Repeat("═", dashboardInnerWidth+2) + "╝\n")
	sb.WriteString(fmt.Sprintf("  q to quit  •  r to refresh  •  last update: %s\n",
		data.FetchedAt.Format("15:04:05")))

	return sb.String()
}

// renderChannelLine builds the channel row string (without borders).
func renderChannelLine(ch ChannelInfo) string {
	if ch.ItemID == "" {
		// Idle channel.
		name := padRight(ch.Name, 10)
		return colorDim + "  " + name + "—         idle" + colorReset
	}

	name := padRight(ch.Name, 10)
	id := padRight(ch.ItemID, 10)
	step := "[" + ch.Step + "]"
	elapsed := formatElapsed(ch.Elapsed)
	bar := progressBar(ch.StepIndex, ch.TotalSteps, 6)

	// Line: "  name  id  [step]  elapsed  bar"
	line := fmt.Sprintf("%s%s%s  %-18s  %-8s  %s%s",
		colorGreen, "  "+name+id, colorReset, step, elapsed, bar, colorReset)
	return line
}

// renderCisternLine builds a cistern row string.
func renderCisternLine(item *queue.WorkItem) string {
	id := padRight(item.ID, 10)
	cx := padRight(complexityName(item.Complexity), 9)
	status := displayStatus(item.Status)
	step := item.CurrentStep
	if step == "" {
		step = "—"
	}

	var statusColor string
	switch item.Status {
	case "in_progress":
		statusColor = colorGreen
	case "open":
		statusColor = colorYellow
	case "escalated":
		statusColor = colorRed
	default:
		statusColor = colorDim
	}

	return fmt.Sprintf("  %s%s%s%s%-10s  %s%s%s%s",
		colorDim, id, colorReset,
		cx,
		statusColor, status, colorReset,
		"   "+step, "")
}

// renderRecentLine builds a recent-flow row string.
func renderRecentLine(item *queue.WorkItem) string {
	t := item.UpdatedAt.Format("15:04")
	id := padRight(item.ID, 10)
	step := item.CurrentStep
	if step == "" {
		step = "—"
	}
	status := displayStatus(item.Status)

	var icon string
	switch item.Status {
	case "closed":
		icon = colorGreen + "✓" + colorReset
	case "escalated":
		icon = colorRed + "✗" + colorReset
	default:
		icon = "·"
	}

	return fmt.Sprintf("  %s  %s  %-16s  %s  %s",
		t, id, step, icon, status)
}

// RunDashboard runs the refresh loop, writing to out. It reads single-byte
// events from inputCh: 'q' or 3 (Ctrl-C) to quit, 'r' to force refresh.
// The done channel is closed when the loop exits.
func RunDashboard(cfgPath, dbPath string, inputCh <-chan byte, out io.Writer) error {
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	// Initial render immediately.
	data := fetchDashboardData(cfgPath, dbPath)
	fmt.Fprint(out, clearScreen+renderDashboard(data))

	for {
		select {
		case <-ticker.C:
			data = fetchDashboardData(cfgPath, dbPath)
			fmt.Fprint(out, clearScreen+renderDashboard(data))

		case b, ok := <-inputCh:
			if !ok {
				return nil
			}
			switch b {
			case 'q', 'Q', 3: // 3 = Ctrl-C
				fmt.Fprint(out, clearScreen)
				return nil
			case 'r', 'R':
				data = fetchDashboardData(cfgPath, dbPath)
				fmt.Fprint(out, clearScreen+renderDashboard(data))
				ticker.Reset(refreshInterval)
			}
		}
	}
}

// startKeyboardReader starts a goroutine that puts stdin into raw mode and
// sends individual keystrokes to the returned channel. If raw mode fails
// (e.g., stdin is not a terminal), the channel is returned empty and only
// SIGINT will terminate the dashboard.
func startKeyboardReader() <-chan byte {
	ch := make(chan byte, 4)
	go func() {
		defer close(ch)

		fd := int(os.Stdin.Fd())
		if !term.IsTerminal(fd) {
			// Not interactive — block forever; SIGINT/signal will cancel.
			select {}
		}

		oldState, err := term.MakeRaw(fd)
		if err != nil {
			select {}
		}
		defer term.Restore(fd, oldState) //nolint:errcheck

		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			ch <- buf[0]
		}
	}()
	return ch
}

// --- commands ---

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Live dashboard showing channels, cistern, and flow events",
	RunE:  runDashboard,
}

var feedCmd = &cobra.Command{
	Use:   "feed",
	Short: "Alias for dashboard",
	RunE:  runDashboard,
}

func runDashboard(cmd *cobra.Command, args []string) error {
	cfgPath := resolveConfigPath()
	dbPath := resolveDBPath()

	// Forward SIGINT to the input channel as Ctrl-C byte.
	inputCh := startKeyboardReader()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	merged := make(chan byte, 8)
	go func() {
		defer close(merged)
		for {
			select {
			case b, ok := <-inputCh:
				if !ok {
					return
				}
				merged <- b
			case <-sigCh:
				merged <- 3 // Ctrl-C
				return
			}
		}
	}()

	return RunDashboard(cfgPath, dbPath, merged, os.Stdout)
}

func init() {
	rootCmd.AddCommand(dashboardCmd)
	rootCmd.AddCommand(feedCmd)
}
