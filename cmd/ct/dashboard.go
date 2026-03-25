package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	refreshInterval          = 2 * time.Second
	idleRefreshInterval      = 5 * time.Second // slow rate when Castellarius is idle
	recentEventLimit         = 5


	// ANSI color codes
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorDim    = "\033[2m"
	colorReset  = "\033[0m"

	// ANSI cursor/screen
	clearScreen = "\033[2J\033[H"
)



// CataractaeInfo describes the state of a single aqueduct — its name, which droplet it carries, and where in the cataractae chain that droplet is.
type CataractaeInfo struct {
	Name            string        `json:"name"`
	RepoName        string        `json:"repo_name"`   // repository this aqueduct belongs to
	DropletID       string        `json:"droplet_id"`
	Title           string        `json:"title"`       // human-readable title of the flowing droplet
	Step            string        `json:"step"`
	Steps           []string      `json:"steps"`       // workflow step names in order
	Elapsed         time.Duration `json:"elapsed"`     // nanoseconds; use elapsed/1e9 for seconds
	CataractaeIndex  int          `json:"cataractae_index"` // 1-based index; 0 if unknown
	TotalCataractae int           `json:"total_cataractae"`
}

// FlowActivity holds the live narrative for one in-progress droplet —
// its current stage and the most recent notes exchanged between cataractae.
type FlowActivity struct {
	DropletID   string                   `json:"droplet_id"`
	Title       string                   `json:"title"`
	Step        string                   `json:"step"`
	RecentNotes []cistern.CataractaeNote `json:"recent_notes"` // last 3, newest first
}

// DashboardData holds all data required to render the dashboard.
type DashboardData struct {
	CataractaeCount int                `json:"cataractae_count"`
	FlowingCount    int                `json:"flowing_count"`
	QueuedCount     int                `json:"queued_count"`
	DoneCount       int                `json:"done_count"`
	Cataractae      []CataractaeInfo   `json:"cataractae"`
	CisternItems    []*cistern.Droplet `json:"cistern_items"`  // flowing + queued
	RecentItems     []*cistern.Droplet `json:"recent_items"`   // recently closed/escalated
	BlockedByMap    map[string]string  `json:"blocked_by_map"` // droplet ID -> first blocking dep ID
	FlowActivities  []FlowActivity     `json:"flow_activities"` // live narrative for in-progress droplets
	FarmRunning     bool               `json:"farm_running"`
	FetchedAt       time.Time          `json:"fetched_at"`
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

	// Build aqueduct list and load cataractae chain for each repo.
	type cataractaeEntry struct {
		name string
		repo string
	}
	var configCataractae []cataractaeEntry
	allSteps := map[string][]aqueduct.WorkflowCataractae{}
	cfgDir := filepath.Dir(cfgPath)
	for _, repo := range cfg.Repos {
		names := repoWorkerNames(repo)
		for _, name := range names {
			configCataractae = append(configCataractae, cataractaeEntry{name, repo.Name})
		}
		data.CataractaeCount += len(names)

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
		cataractae := make([]CataractaeInfo, len(configCataractae))
		for i, ch := range configCataractae {
			ci := CataractaeInfo{Name: ch.name, RepoName: ch.repo}
			if wf, ok := allSteps[ch.repo]; ok {
				ci.Steps = stepNames(wf)
			}
			cataractae[i] = ci
		}
		data.Cataractae = cataractae
		return data
	}
	defer c.Close()

	allItems, err := c.List("", "")
	if err != nil {
		cataractae := make([]CataractaeInfo, len(configCataractae))
		for i, ch := range configCataractae {
			ci := CataractaeInfo{Name: ch.name, RepoName: ch.repo}
			if wf, ok := allSteps[ch.repo]; ok {
				ci.Steps = stepNames(wf)
			}
			cataractae[i] = ci
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

	// Build cataractae infos.
	cataractae := make([]CataractaeInfo, len(configCataractae))
	for i, ch := range configCataractae {
		ci := CataractaeInfo{Name: ch.name, RepoName: ch.repo}
		if wf, ok := allSteps[ch.repo]; ok {
			ci.Steps = stepNames(wf)
		}
		if item, ok := assigneeMap[ch.name]; ok {
			ci.DropletID = item.ID
			ci.Title = item.Title
			ci.Step = item.CurrentCataractae
			ci.Elapsed = time.Since(item.UpdatedAt)
			wfCataractae := allSteps[ch.repo]
			activeSteps := activeStepNames(wfCataractae, item.Complexity)
			ci.Steps = activeSteps
			ci.TotalCataractae = len(activeSteps)
			ci.CataractaeIndex = slices.Index(activeSteps, item.CurrentCataractae) + 1
		}
		cataractae[i] = ci
	}
	data.Cataractae = cataractae

	// Cistern: in_progress and open items; build blocked-by map.
	data.BlockedByMap = map[string]string{}
	for _, item := range allItems {
		if item.Status == "in_progress" || item.Status == "open" {
			data.CisternItems = append(data.CisternItems, item)
		}
		if item.Status == "open" {
			if blockedBy, err := c.GetBlockedBy(item.ID); err == nil && len(blockedBy) > 0 {
				data.BlockedByMap[item.ID] = blockedBy[0]
			}
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

	// Current flow: build live narrative for each in-progress droplet.
	for _, item := range allItems {
		if item.Status != "in_progress" {
			continue
		}
		notes, err := c.GetNotes(item.ID)
		if err != nil {
			notes = nil
		}
		// Keep last 3 notes (most recent activity, newest first).
		recent := notes
		if len(recent) > 3 {
			recent = recent[:3]
		}
		data.FlowActivities = append(data.FlowActivities, FlowActivity{
			DropletID:   item.ID,
			Title:       item.Title,
			Step:        item.CurrentCataractae,
			RecentNotes: recent,
		})
	}

	data.FarmRunning = true
	return data
}

// dashboardStateHash returns a string fingerprint of the key fields in d that
// indicate Castellarius activity. The hash changes whenever flowing/queued/done
// counts change, or when aqueduct assignments or steps change. It is used by
// the polling loop to detect idle state for adaptive refresh rate backoff.
// Returns "" for nil input so the first comparison never triggers idle mode.
func dashboardStateHash(d *DashboardData) string {
	if d == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d/%d/%d/%v", d.FlowingCount, d.QueuedCount, d.DoneCount, d.FarmRunning)
	for _, ch := range d.Cataractae {
		fmt.Fprintf(&b, "|%s:%s:%s", ch.Name, ch.DropletID, ch.Step)
	}
	return b.String()
}

// cataractaeIndexInWorkflow returns the 1-based index of stepName in the cataractae list, or 0 if not found.
func cataractaeIndexInWorkflow(stepName string, cataractae []aqueduct.WorkflowCataractae) int {
	for i, s := range cataractae {
		if s.Name == stepName {
			return i + 1
		}
	}
	return 0
}

// stepNames extracts step names from a workflow cataractae slice.
func stepNames(wf []aqueduct.WorkflowCataractae) []string {
	names := make([]string, len(wf))
	for i, s := range wf {
		names[i] = s.Name
	}
	return names
}

// activeStepNames returns the names of workflow steps that will actually run
// for the given complexity level, filtering out any step whose SkipFor list
// contains that complexity.
func activeStepNames(wf []aqueduct.WorkflowCataractae, complexity int) []string {
	var names []string
	for _, step := range wf {
		if !slices.Contains(step.SkipFor, complexity) {
			names = append(names, step.Name)
		}
	}
	return names
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

// renderAqueductRow renders a single aqueduct as a Roman aqueduct arch diagram.
// Each cataractae is an arch pier. The channel on top carries the flowing droplet.
// Returns a multi-line string (7 lines) suitable for the TUI dashboard.
//
// Example output (active in green, idle piers dim):
//
//	virgo  ╔════════════════════════════════════════════════════════╗
//	       ║ ≈ ≈ ≈  ci-pqz1q  implement  2m 14s  ████░░░░  ≈ ≈ ≈  ║
//	       ╚════════╤═══════════════╤═══════════════╤══════════════╝
//	                │               │               │               │
//	             ╔══╧══╗         ╔══╧══╗         ╔══╧══╗        ╔══╧══╗
//	             ║  ●  ║         ║  ○  ║         ║  ○  ║        ║  ○  ║
//	             ╚═════╝         ╚═════╝         ╚═════╝        ╚═════╝
//	           implement      adv-review            qa          delivery
func renderAqueductRow(ch CataractaeInfo) string {
	const (
		colW    = 15 // visual width per cataractae column (label + spacing)
		pierInW = 5  // inner width of pier box: "  ●  " or " impl"
		nameW   = 10 // left label column width
	)

	steps := ch.Steps
	if len(steps) == 0 {
		steps = []string{"(empty)"}
	}
	n := len(steps)

	// Channel total inner width = n columns of colW, separated by ┬ joints.
	chanW := n*colW - 1

	// ── Line 1: channel top ────────────────────────────────────────────────
	prefix := "  " + padRight(ch.Name, nameW) + "  "
	indent := strings.Repeat(" ", len([]rune(prefix)))

	chanTop := prefix + colorDim + "╔" + strings.Repeat("═", chanW) + "╗" + colorReset

	// ── Line 2: water / droplet info ───────────────────────────────────────
	var waterInner string
	if ch.DropletID != "" {
		bar := progressBar(ch.CataractaeIndex, ch.TotalCataractae, 8)
		content := fmt.Sprintf(" ≈ ≈  %s  %s  %s  ≈ ≈ ", ch.DropletID, formatElapsed(ch.Elapsed), bar)
		waterInner = padOrTruncCenter(content, chanW)
		waterInner = colorGreen + waterInner + colorReset
	} else {
		waterInner = colorDim + padOrTruncCenter(" — idle — ", chanW) + colorReset
	}
	chanMid := indent + colorDim + "║" + colorReset + waterInner + colorDim + "║" + colorReset

	// ── Line 3: channel bottom with ┬ connectors at each pier ──────────────
	var chanBot strings.Builder
	chanBot.WriteString(indent)
	chanBot.WriteString(colorDim + "╚" + colorReset)
	for range steps {
		half := (colW - 1) / 2
		rest := colW - 1 - half
		chanBot.WriteString(colorDim + strings.Repeat("═", half) + "╤" + strings.Repeat("═", rest-1) + colorReset)
	}
	chanBot.WriteString(colorDim + "═╝" + colorReset)

	// ── Line 4: vertical stems from channel to pier caps ───────────────────
	var stems strings.Builder
	stems.WriteString(indent)
	for range steps {
		half := (colW - 1) / 2
		stems.WriteString(strings.Repeat(" ", half))
		stems.WriteString(colorDim + "│" + colorReset)
		stems.WriteString(strings.Repeat(" ", colW-half-1))
	}

	// ── Line 5: pier tops ╔══╧══╗ ─────────────────────────────────────────
	var pierTop strings.Builder
	pierTop.WriteString(indent)
	for _, step := range steps {
		half := (colW - 1) / 2
		pad := half - (pierInW/2 + 1)
		pierTop.WriteString(strings.Repeat(" ", pad))
		active := step == ch.Step && ch.DropletID != ""
		box := "╔" + strings.Repeat("═", pierInW) + "╗"
		if active {
			pierTop.WriteString(colorGreen + box + colorReset)
		} else {
			pierTop.WriteString(colorDim + box + colorReset)
		}
		pierTop.WriteString(strings.Repeat(" ", colW-pad-pierInW-2))
	}

	// ── Line 6: pier middle ║  ●  ║ ─────────────────────────────────────
	var pierMid strings.Builder
	pierMid.WriteString(indent)
	for _, step := range steps {
		half := (colW - 1) / 2
		pad := half - (pierInW/2 + 1)
		pierMid.WriteString(strings.Repeat(" ", pad))
		active := step == ch.Step && ch.DropletID != ""
		sym := "  ○  "
		if active {
			sym = "  ●  "
		}
		var body string
		if active {
			body = colorGreen + "║" + sym + "║" + colorReset
		} else {
			body = colorDim + "║" + sym + "║" + colorReset
		}
		pierMid.WriteString(body)
		pierMid.WriteString(strings.Repeat(" ", colW-pad-pierInW-2))
	}

	// ── Line 7: pier bottoms ╚═════╝ ─────────────────────────────────────
	var pierBot strings.Builder
	pierBot.WriteString(indent)
	for _, step := range steps {
		half := (colW - 1) / 2
		pad := half - (pierInW/2 + 1)
		pierBot.WriteString(strings.Repeat(" ", pad))
		active := step == ch.Step && ch.DropletID != ""
		box := "╚" + strings.Repeat("═", pierInW) + "╝"
		if active {
			pierBot.WriteString(colorGreen + box + colorReset)
		} else {
			pierBot.WriteString(colorDim + box + colorReset)
		}
		pierBot.WriteString(strings.Repeat(" ", colW-pad-pierInW-2))
	}

	// ── Line 8: labels ────────────────────────────────────────────────────
	var labels strings.Builder
	labels.WriteString(indent)
	for _, step := range steps {
		lbl := step
		if len([]rune(lbl)) > colW-1 {
			runes := []rune(lbl)
			lbl = string(runes[:colW-2]) + "…"
		}
		active := step == ch.Step && ch.DropletID != ""
		centered := padOrTruncCenter(lbl, colW)
		if active {
			labels.WriteString(colorGreen + centered + colorReset)
		} else {
			labels.WriteString(colorDim + centered + colorReset)
		}
	}

	return strings.Join([]string{
		chanTop,
		chanMid,
		chanBot.String(),
		stems.String(),
		pierTop.String(),
		pierMid.String(),
		pierBot.String(),
		labels.String(),
	}, "\n")
}

// padOrTruncCenter centers s within width w, padding with spaces.
// Truncates with … if s is too long.
func padOrTruncCenter(s string, w int) string {
	runes := []rune(s)
	if len(runes) > w {
		return string(runes[:w-1]) + "…"
	}
	total := w - len(runes)
	left := total / 2
	right := total - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

// renderFlowGraphRow is kept for tests; the TUI now uses renderAqueductRow.
func renderFlowGraphRow(ch CataractaeInfo) (graphLine, infoLine string) {
	const namePad = 12
	namePfx := padRight(ch.Name, namePad)
	const pfxWidth = namePad + 4

	if len(ch.Steps) == 0 {
		if ch.DropletID == "" {
			return "  " + namePfx + "  " + colorDim + "(idle)" + colorReset, ""
		}
		return "  " + colorGreen + namePfx + colorReset + "  " + ch.Step, ""
	}

	var g strings.Builder
	g.WriteString("  ")
	g.WriteString(colorDim + namePfx + colorReset)
	g.WriteString("  ")
	activeCol := -1
	visualCol := pfxWidth

	for i, step := range ch.Steps {
		if i > 0 {
			if step == ch.Step && ch.DropletID != "" {
				g.WriteString(colorDim + " ──" + colorReset + colorGreen + "●" + colorReset + colorDim + "──▶ " + colorReset)
			} else {
				g.WriteString(colorDim + " ──○──▶ " + colorReset)
			}
			visualCol += 8
		}
		if step == ch.Step && ch.DropletID != "" {
			g.WriteString(colorGreen + step + colorReset)
			activeCol = visualCol
		} else {
			g.WriteString(colorDim + step + colorReset)
		}
		visualCol += len([]rune(step))
	}

	graphLine = g.String()
	if activeCol >= 0 {
		bar := progressBar(ch.CataractaeIndex, ch.TotalCataractae, 8)
		infoLine = strings.Repeat(" ", activeCol) + "↑ " + ch.Name + " · " + ch.DropletID + "  " + formatElapsed(ch.Elapsed) + "  " + bar
	}
	return
}

// renderDashboard produces the full dashboard string for the given data.
func renderDashboard(data *DashboardData) string {
	var sb strings.Builder
	sep := strings.Repeat("─", 70)

	// Aqueduct arch visualization — one arch diagram per aqueduct.
	if len(data.Cataractae) == 0 {
		sb.WriteString("  No aqueducts configured\n")
	} else {
		for i, ch := range data.Cataractae {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(renderAqueductRow(ch))
			sb.WriteString("\n")
		}
	}
	sb.WriteString(sep + "\n")

	// Cistern counts.
	sb.WriteString(fmt.Sprintf("  ● %d flowing  ○ %d queued  ✓ %d delivered\n",
		data.FlowingCount, data.QueuedCount, data.DoneCount))
	sb.WriteString(sep + "\n")

	// Cistern — queued droplets.
	sb.WriteString("  CISTERN\n")
	var queued []*cistern.Droplet
	for _, item := range data.CisternItems {
		if item.Status == "open" {
			queued = append(queued, item)
		}
	}
	if len(queued) == 0 {
		sb.WriteString("  Cistern is empty.\n")
	} else {
		for _, item := range queued {
			age := time.Since(item.CreatedAt).Round(time.Minute)
			blocked := ""
			if dep, ok := data.BlockedByMap[item.ID]; ok {
				blocked = fmt.Sprintf(" [blocked by %s]", dep)
			}
			sb.WriteString(fmt.Sprintf("  ○ %-10s  %s  %s%s\n",
				item.ID, formatElapsed(age), item.Title, blocked))
		}
	}
	sb.WriteString(sep + "\n")

	// Recent flow.
	sb.WriteString("  RECENT FLOW\n")
	if len(data.RecentItems) == 0 {
		sb.WriteString("  No recent flow.\n")
	} else {
		for _, item := range data.RecentItems {
			sb.WriteString("  " + renderRecentLine(item) + "\n")
		}
	}
	sb.WriteString(sep + "\n")

	// Footer.
	sb.WriteString(fmt.Sprintf("  q to quit  •  r to refresh  •  last update: %s\n",
		data.FetchedAt.Format("15:04:05")))

	return sb.String()
}

// renderRecentLine builds a recent-flow row string.
func renderRecentLine(item *cistern.Droplet) string {
	t := item.UpdatedAt.Format("15:04")
	id := padRight(item.ID, 10)
	step := item.CurrentCataractae
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

// runDashboardWith is the testable core of RunDashboard. fetch is called to
// load dashboard data; fastInterval is used when droplets are active or the
// state has just changed, slowInterval is used when the Castellarius is idle
// (FlowingCount == 0 and state hash unchanged since the last poll).
func runDashboardWith(cfgPath, dbPath string, inputCh <-chan byte, out io.Writer,
	fetch func(string, string) *DashboardData,
	fastInterval, slowInterval time.Duration) error {

	ticker := time.NewTicker(fastInterval)
	defer ticker.Stop()

	// Initial render immediately.
	data := fetch(cfgPath, dbPath)
	prevHash := dashboardStateHash(data)
	fmt.Fprint(out, clearScreen+renderDashboard(data))

	for {
		select {
		case <-ticker.C:
			data = fetch(cfgPath, dbPath)
			newHash := dashboardStateHash(data)

			// Adaptive backoff: slow down when Castellarius is idle.
			idle := newHash == prevHash && data.FlowingCount == 0
			prevHash = newHash
			fmt.Fprint(out, clearScreen+renderDashboard(data))

			next := fastInterval
			if idle {
				next = slowInterval
			}
			ticker.Reset(next)

		case b, ok := <-inputCh:
			if !ok {
				return nil
			}
			switch b {
			case 'q', 'Q', 3: // 3 = Ctrl-C
				fmt.Fprint(out, clearScreen)
				return nil
			case 'r', 'R':
				data = fetch(cfgPath, dbPath)
				prevHash = dashboardStateHash(data)
				fmt.Fprint(out, clearScreen+renderDashboard(data))
				ticker.Reset(fastInterval) // manual refresh always resets to fast
			}
		}
	}
}

// RunDashboard runs the refresh loop, writing to out. It reads single-byte
// events from inputCh: 'q' or 3 (Ctrl-C) to quit, 'r' to force refresh.
// The polling rate adapts: fast (refreshInterval) when droplets are flowing or
// state changes, slow (idleRefreshInterval) when the Castellarius is idle.
func RunDashboard(cfgPath, dbPath string, inputCh <-chan byte, out io.Writer) error {
	return runDashboardWith(cfgPath, dbPath, inputCh, out, fetchDashboardData,
		refreshInterval, idleRefreshInterval)
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

var (
	dashboardWebFlag  bool
	dashboardAddrFlag string
)

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

func runDashboard(cmd *cobra.Command, args []string) error {
	cfgPath := resolveConfigPath()
	dbPath := resolveDBPath()

	if dashboardWebFlag {
		return RunDashboardWeb(cfgPath, dbPath, dashboardAddrFlag)
	}
	return RunDashboardTUI(cfgPath, dbPath)
}

func init() {
	rootCmd.AddCommand(dashboardCmd)
	rootCmd.AddCommand(feedCmd)

	dashboardCmd.Flags().BoolVar(&dashboardWebFlag, "web", false, "Start HTTP web dashboard instead of TUI")
	dashboardCmd.Flags().StringVar(&dashboardAddrFlag, "addr", "127.0.0.1:5737", "Address for web dashboard (default 127.0.0.1:5737)")
}
