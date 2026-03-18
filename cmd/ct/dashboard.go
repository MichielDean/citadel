package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	dashboardInnerWidth      = 56 // inner content width (between ║ borders)
	refreshInterval          = 2 * time.Second
	recentEventLimit         = 5
	defaultDashboardHTMLPort = 5737

	// ANSI color codes
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorDim    = "\033[2m"
	colorReset  = "\033[0m"

	// ANSI cursor/screen
	clearScreen = "\033[2J\033[H"
)

const dashboardEasterEggText = `Four letters guard the gate you seek,
Each one counted in a way that’s unique.
Not by their place in the alphabet’s line,
But by where they stand among numbers prime.

Take each letter’s secret prime,
Then trim away what’s second in time.
What’s left behind, when placed in a row,
Reveals the port where you must go.`

// CataractaInfo describes the state of a single aqueduct — its name, which droplet it carries, and where in the cataracta chain that droplet is.
type CataractaInfo struct {
	Name       string
	DropletID  string
	Step       string
	Elapsed    time.Duration
	CataractaIndex  int // 1-based index of current cataracta; 0 if unknown
	TotalCataractae int
}

// DashboardData holds all data required to render the dashboard.
type DashboardData struct {
	CataractaCount  int
	FlowingCount int
	QueuedCount  int
	DoneCount    int
	Cataractae      []CataractaInfo
	CisternItems []*cistern.Droplet // flowing + queued
	RecentItems  []*cistern.Droplet // recently closed/escalated
	FarmRunning  bool
	FetchedAt    time.Time
}

// fetchDashboardData loads config and queue state into a DashboardData.
// On any error (missing config, missing DB) it returns a partial/drought result
// rather than an error, so the dashboard degrades gracefully.
func fetchDashboardData(cfgPath, dbPath string) *DashboardData {
	data := &DashboardData{FetchedAt: time.Now()}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		// Config not found — show aqueducts closed.
		return data
	}

	// Build aqueduct list and load cataracta chain for each repo.
	type cataractaEntry struct {
		name string
		repo string
	}
	var configCataractae []cataractaEntry
	allSteps := map[string][]aqueduct.WorkflowCataracta{}
	cfgDir := filepath.Dir(cfgPath)
	for _, repo := range cfg.Repos {
		names := repoWorkerNames(repo)
		for _, name := range names {
			configCataractae = append(configCataractae, cataractaEntry{name, repo.Name})
		}
		data.CataractaCount += len(names)

		wfPath := repo.WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(cfgDir, wfPath)
		}
		if wf, wfErr := aqueduct.ParseWorkflow(wfPath); wfErr == nil {
			allSteps[repo.Name] = wf.Cataractae
		}
	}

	// Open queue — if it fails, show aqueducts as idle.
	c, err := cistern.New(dbPath, "")
	if err != nil {
		cataractae := make([]CataractaInfo, len(configCataractae))
		for i, ch := range configCataractae {
			cataractae[i] = CataractaInfo{Name: ch.name}
		}
		data.Cataractae = cataractae
		return data
	}
	defer c.Close()

	allItems, err := c.List("", "")
	if err != nil {
		cataractae := make([]CataractaInfo, len(configCataractae))
		for i, ch := range configCataractae {
			cataractae[i] = CataractaInfo{Name: ch.name}
		}
		data.Cataractae = cataractae
		return data
	}

	// Tally counts and build assignee map.
	assigneeMap := map[string]*cistern.Droplet{}
	for _, item := range allItems {
		switch item.Status {
		case "in_progress":
			data.FlowingCount++
			if item.Assignee != "" {
				assigneeMap[item.Assignee] = item
			}
		case "open":
			data.QueuedCount++
		case "delivered":
			data.DoneCount++
		}
	}

	// Build cataracta infos.
	cataractae := make([]CataractaInfo, len(configCataractae))
	for i, ch := range configCataractae {
		ci := CataractaInfo{Name: ch.name}
		if item, ok := assigneeMap[ch.name]; ok {
			ci.DropletID = item.ID
			ci.Step = item.CurrentCataracta
			ci.Elapsed = time.Since(item.UpdatedAt)
			wfCataractae := allSteps[ch.repo]
			ci.TotalCataractae = len(wfCataractae)
			ci.CataractaIndex = cataractaIndexInWorkflow(item.CurrentCataracta, wfCataractae)
		}
		cataractae[i] = ci
	}
	data.Cataractae = cataractae

	// Cistern: in_progress and open items.
	for _, item := range allItems {
		if item.Status == "in_progress" || item.Status == "open" {
			data.CisternItems = append(data.CisternItems, item)
		}
	}

	// Recent flow: most recently updated delivered/stagnant items.
	var recent []*cistern.Droplet
	for _, item := range allItems {
		if item.Status == "delivered" || item.Status == "stagnant" {
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

// cataractaIndexInWorkflow returns the 1-based index of stepName in the cataracta list, or 0 if not found.
func cataractaIndexInWorkflow(stepName string, cataractae []aqueduct.WorkflowCataracta) int {
	for i, s := range cataractae {
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
	title := " CISTERN "
	padTotal := totalWidth - 2 - len(title)
	leftPad := padTotal / 2
	rightPad := padTotal - leftPad
	sb.WriteString("╔" + strings.Repeat("═", leftPad) + title + strings.Repeat("═", rightPad) + "╗\n")

	// Summary line.
	summary := fmt.Sprintf("%d flowing  •  %d queued  •  %d delivered",
		data.FlowingCount, data.QueuedCount, data.DoneCount)
	sb.WriteString(contentLine(summary) + "\n")

	// AQUEDUCTS section.
	sb.WriteString(borderLine() + "\n")
	sb.WriteString(contentLine("AQUEDUCTS") + "\n")

	if len(data.Cataractae) == 0 {
		sb.WriteString(contentLine("  No aqueducts configured") + "\n")
	} else {
		for _, ch := range data.Cataractae {
			sb.WriteString(contentLine(renderCataractaLine(ch)) + "\n")
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

// renderCataractaLine builds the aqueduct row string (without borders).
func renderCataractaLine(ch CataractaInfo) string {
	if ch.DropletID == "" {
		// Idle aqueduct.
		name := padRight(ch.Name, 10)
		return colorDim + "  " + name + "idle" + colorReset
	}

	name := padRight(ch.Name, 10)
	id := padRight(ch.DropletID, 10)
	step := "[" + ch.Step + "]"
	elapsed := formatElapsed(ch.Elapsed)
	bar := progressBar(ch.CataractaIndex, ch.TotalCataractae, 6)

	// Line: "  name → id  [cataracta]  elapsed  bar"
	line := fmt.Sprintf("%s%s→ %s%s  %-18s  %-8s  %s%s",
		colorGreen, "  "+name, id, colorReset, step, elapsed, bar, colorReset)
	return line
}

// renderCisternLine builds a cistern row string.
func renderCisternLine(item *cistern.Droplet) string {
	id := padRight(item.ID, 10)
	cx := padRight(complexityName(item.Complexity), 9)
	status := displayStatus(item.Status)
	step := item.CurrentCataracta
	if step == "" {
		step = "—"
	}

	var statusColor string
	switch item.Status {
	case "in_progress":
		statusColor = colorGreen
	case "open":
		statusColor = colorYellow
	case "stagnant":
		statusColor = colorRed
	default:
		statusColor = colorDim
	}

	return fmt.Sprintf("  %s%s%s%s%-12s  %s%s%s  %s",
		colorDim, id, colorReset,
		cx,
		statusColor+status+colorReset,
		"",
		"", "",
		step)
}

// renderRecentLine builds a recent-flow row string.
func renderRecentLine(item *cistern.Droplet) string {
	t := item.UpdatedAt.Format("15:04")
	id := padRight(item.ID, 10)
	step := item.CurrentCataracta
	if step == "" {
		step = "—"
	}
	status := displayStatus(item.Status)

	var icon string
	switch item.Status {
	case "delivered":
		icon = colorGreen + "✓" + colorReset
	case "stagnant":
		icon = colorRed + "✗" + colorReset
	default:
		icon = "·"
	}

	return fmt.Sprintf("  %s  %s  %-20s  %s  %s",
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
	Short: "Live dashboard showing cataractae, cistern, and flow events",
	RunE:  runDashboard,
}

var feedCmd = &cobra.Command{
	Use:   "feed",
	Short: "Alias for dashboard",
	RunE:  runDashboard,
}

var dashboardHTML bool
var dashboardPort int

func dashboardListenAddr(port int) string {
	return fmt.Sprintf(":%d", port)
}

func renderDashboardHTML(snapshot inspectOutput) string {
	var sb strings.Builder
	sb.WriteString("<!doctype html><html><head><meta charset=\"utf-8\">")
	sb.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	sb.WriteString("<meta http-equiv=\"refresh\" content=\"2\">")
	sb.WriteString("<title>CT Dashboard</title>")
	sb.WriteString(`<style>
body{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;margin:0;background:#0b1020;color:#d6deeb}
.wrap{max-width:980px;margin:24px auto;padding:0 16px}
.card{background:#121a2b;border:1px solid #25314f;border-radius:12px;padding:14px 16px;margin-bottom:12px}
.muted{color:#8fa1c7}.ok{color:#57d57a}.warn{color:#f0c86b}
table{width:100%;border-collapse:collapse}th,td{padding:6px 8px;border-bottom:1px solid #22304d;text-align:left}
h1,h2{margin:0 0 10px}h1{font-size:20px}h2{font-size:15px;color:#9db1db}
#easter-egg{position:fixed;right:10px;bottom:8px;opacity:.28;cursor:default;user-select:none;font-size:11px}
#easter-egg .hint{display:none;position:absolute;right:0;bottom:16px;white-space:pre-line;width:300px;padding:10px;border-radius:8px;background:#0f1728;border:1px solid #31436b;color:#ced9f0;opacity:.96}
#easter-egg:hover .hint{display:block}
</style></head><body>`)
	sb.WriteString(`<div class="wrap">`)
	sb.WriteString(`<div class="card"><h1>CT Dashboard</h1>`)
	sb.WriteString(fmt.Sprintf(`<div class="muted"><span class="ok">%d flowing</span> • <span class="warn">%d queued</span> • %d delivered</div>`,
		snapshot.Counts.Flowing, snapshot.Counts.Queued, snapshot.Counts.Delivered))
	sb.WriteString(fmt.Sprintf(`<div class="muted" style="margin-top:6px">last update: %s</div>`, time.Now().Format("15:04:05")))
	sb.WriteString(`</div>`)

	sb.WriteString(`<div class="card"><h2>Aqueducts</h2><table><thead><tr><th>Aqueduct</th><th>Droplet</th><th>Cataracta</th><th>Elapsed</th></tr></thead><tbody>`)
	if len(snapshot.Cataractae) == 0 {
		sb.WriteString(`<tr><td colspan="4" class="muted">No aqueducts configured</td></tr>`)
	} else {
		for _, ch := range snapshot.Cataractae {
			droplet := "-"
			stage := "idle"
			elapsed := "-"
			if ch.DropletID != nil {
				droplet = html.EscapeString(*ch.DropletID)
			}
			if ch.Stage != nil {
				stage = html.EscapeString(*ch.Stage)
			}
			if ch.ElapsedSeconds != nil {
				elapsed = formatElapsed(time.Duration(*ch.ElapsedSeconds) * time.Second)
			}
			sb.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				html.EscapeString(ch.Name), droplet, stage, elapsed))
		}
	}
	sb.WriteString(`</tbody></table></div>`)

	sb.WriteString(`<div class="card"><h2>Cistern</h2><table><thead><tr><th>Droplet</th><th>Status</th><th>Stage</th></tr></thead><tbody>`)
	if len(snapshot.Droplets) == 0 {
		sb.WriteString(`<tr><td colspan="3" class="muted">Cistern dry.</td></tr>`)
	} else {
		for _, d := range snapshot.Droplets {
			step := d.Stage
			if step == "" {
				step = "-"
			}
			sb.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td></tr>`,
				html.EscapeString(d.ID), html.EscapeString(displayStatus(d.Status)), html.EscapeString(step)))
		}
	}
	sb.WriteString(`</tbody></table></div>`)

	sb.WriteString(`<div class="card"><h2>Recent Flow</h2><table><thead><tr><th>Time</th><th>Droplet</th><th>Event</th></tr></thead><tbody>`)
	if len(snapshot.RecentEvents) == 0 {
		sb.WriteString(`<tr><td colspan="3" class="muted">No recent flow.</td></tr>`)
	} else {
		for _, evt := range snapshot.RecentEvents {
			sb.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td></tr>`,
				html.EscapeString(evt.Time.Format("15:04")), html.EscapeString(evt.Droplet), html.EscapeString(evt.Event)))
		}
	}
	sb.WriteString(`</tbody></table></div></div>`)

	sb.WriteString(`<div id="easter-egg" aria-hidden="true">◈<span class="hint">`)
	sb.WriteString(html.EscapeString(dashboardEasterEggText))
	sb.WriteString(`</span></div>`)
	sb.WriteString(`</body></html>`)

	return sb.String()
}

func runDashboardHTML(cfgPath, dbPath string, out io.Writer) error {
	addr := dashboardListenAddr(dashboardPort)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := buildInspectOutput(cfgPath, dbPath)
		if err != nil {
			http.Error(w, "failed to build dashboard snapshot", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, renderDashboardHTML(snapshot))
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	fmt.Fprintf(out, "Dashboard available at http://localhost:%d\n", dashboardPort)
	fmt.Fprintln(out, "Press Ctrl-C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	case <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}

func runDashboard(cmd *cobra.Command, args []string) error {
	cfgPath := resolveConfigPath()
	dbPath := resolveDBPath()

	if dashboardHTML {
		return runDashboardHTML(cfgPath, dbPath, os.Stdout)
	}

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
	dashboardCmd.Flags().BoolVar(&dashboardHTML, "html", false, "serve dashboard as HTML instead of terminal UI")
	dashboardCmd.Flags().IntVar(&dashboardPort, "port", defaultDashboardHTMLPort, "port for --html dashboard server")
	feedCmd.Flags().BoolVar(&dashboardHTML, "html", false, "serve dashboard as HTML instead of terminal UI")
	feedCmd.Flags().IntVar(&dashboardPort, "port", defaultDashboardHTMLPort, "port for --html dashboard server")

	rootCmd.AddCommand(dashboardCmd)
	rootCmd.AddCommand(feedCmd)
}
