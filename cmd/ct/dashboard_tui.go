package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MichielDean/cistern/internal/cistern"
)

// --- Lip Gloss styles ---

var (
	tuiStyleGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#57d57a"))
	tuiStyleYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#f0c86b"))
	tuiStyleRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e06c75"))
	tuiStyleDim    = lipgloss.NewStyle().Faint(true)
	tuiStyleHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9db1db"))
	tuiStyleFooter = lipgloss.NewStyle().Faint(true)
)

// --- Messages ---

type tuiTickMsg time.Time
type tuiDataMsg *DashboardData

// --- Model ---

type dashboardTUIModel struct {
	cfgPath   string
	dbPath    string
	data      *DashboardData
	logoLines []string
	width     int
	height    int
}

func newDashboardTUIModel(cfgPath, dbPath string) dashboardTUIModel {
	return dashboardTUIModel{
		cfgPath:   cfgPath,
		dbPath:    dbPath,
		logoLines: loadLogoLines(),
		width:     100,
		height:    24,
	}
}

func (m dashboardTUIModel) Init() tea.Cmd {
	return tea.Batch(m.fetchDataCmd(), tuiTick())
}

func tuiTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tuiTickMsg(t)
	})
}

func (m dashboardTUIModel) fetchDataCmd() tea.Cmd {
	cfgPath, dbPath := m.cfgPath, m.dbPath
	return func() tea.Msg {
		return tuiDataMsg(fetchDashboardData(cfgPath, dbPath))
	}
}

func (m dashboardTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tuiTickMsg:
		return m, tea.Batch(m.fetchDataCmd(), tuiTick())

	case tuiDataMsg:
		m.data = (*DashboardData)(msg)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		case "r", "R":
			return m, m.fetchDataCmd()
		}
	}
	return m, nil
}

func (m dashboardTUIModel) View() string {
	if m.width < 100 || m.height < 24 {
		return fmt.Sprintf("Terminal too small — need at least 100×24 (current: %d×%d)\n", m.width, m.height)
	}
	if m.data == nil {
		return "  Loading…\n"
	}

	sep := tuiStyleDim.Render(strings.Repeat("─", m.width))
	var parts []string

	// 1. Logo header.
	parts = append(parts, m.viewLogo()...)
	parts = append(parts, sep)

	// 2. Aqueduct arch diagram — one arch per aqueduct.
	parts = append(parts, m.viewAqueductArches()...)
	parts = append(parts, sep)

	// 3. Cistern counts.
	parts = append(parts, m.viewStatusBar())
	parts = append(parts, sep)

	// 4. Cistern — queued droplets waiting.
	parts = append(parts, tuiStyleHeader.Render("  CISTERN"))
	parts = append(parts, m.viewCistern()...)
	parts = append(parts, sep)

	// 5. Recent flow.
	parts = append(parts, tuiStyleHeader.Render("  RECENT FLOW"))
	parts = append(parts, m.viewRecentFlow()...)
	parts = append(parts, sep)

	// 5. Footer.
	parts = append(parts, tuiStyleFooter.Render("  q quit  r refresh  ? help"))

	return strings.Join(parts, "\n")
}

func (m dashboardTUIModel) viewLogo() []string {
	logoHeight := len(m.logoLines)
	if logoHeight > 0 && m.height >= logoHeight+16 {
		// Full logo — truncate each line to terminal width.
		lines := make([]string, 0, logoHeight)
		for _, line := range m.logoLines {
			r := []rune(line)
			if len(r) > m.width {
				line = string(r[:m.width])
			}
			lines = append(lines, line)
		}
		return lines
	}
	// Condensed 3-line banner.
	return []string{
		tuiStyleDim.Render(strings.Repeat("▓", m.width)),
		tuiStyleHeader.Bold(true).Render(tuiPadCenter("◈  C I S T E R N  ◈", m.width)),
		tuiStyleDim.Render(strings.Repeat("▓", m.width)),
	}
}

func (m dashboardTUIModel) viewStatusBar() string {
	d := m.data
	flowing := tuiStyleGreen.Render(fmt.Sprintf("● %d flowing", d.FlowingCount))
	queued := tuiStyleYellow.Render(fmt.Sprintf("○ %d queued", d.QueuedCount))
	done := tuiStyleGreen.Render(fmt.Sprintf("✓ %d delivered", d.DoneCount))
	ts := tuiStyleDim.Render("— last update " + d.FetchedAt.Format("15:04:05"))
	return fmt.Sprintf("  %s  %s  %s  %s", flowing, queued, done, ts)
}

// viewAqueductArches renders each aqueduct as a Roman arch diagram.
// Each cataracta is a pier supporting the water channel above.
func (m dashboardTUIModel) viewAqueductArches() []string {
	if len(m.data.Cataractae) == 0 {
		return []string{tuiStyleDim.Render("  No aqueducts configured")}
	}
	var lines []string
	for i, ch := range m.data.Cataractae {
		if i > 0 {
			lines = append(lines, "") // gap between aqueducts
		}
		lines = append(lines, m.tuiAqueductRow(ch)...)
	}
	return lines
}

// tuiAqueductRow renders a single aqueduct as an 8-line arch diagram:
//
//	  virgo       ╔══════════════════════════════════════════════════════╗
//	              ║  ≈ ≈  ci-pqz1q  implement  2m 14s  ████░░░░  ≈ ≈   ║
//	              ╚═══════╤══════════════╤══════════════╤════════════════╝
//	                      │              │              │              │
//	                   ╔══╧══╗       ╔══╧══╗        ╔══╧══╗       ╔══╧══╗
//	                   ║  ●  ║       ║  ○  ║        ║  ○  ║       ║  ○  ║
//	                   ╚═════╝       ╚═════╝        ╚═════╝       ╚═════╝
//	                 implement    adv-review           qa          delivery
// tuiAqueductRow renders a single aqueduct as a Roman arch diagram.
//
// Each cataracta is drawn as a tapered solid pier (▓ chars). The pier is
// widest at the top where it meets the channel, and narrows across three taper
// rows, creating arch-shaped openings between adjacent piers:
//
//	  virgo   ╔══════════════════════════════════════════════════════╗
//	          ║  ≈ ≈  ci-abc  implement  2m 14s  ████░░░░  ≈ ≈ ≈   ║
//	          ╚══════════════════════════════════════════════════════╝
//	          ▓▓▓▓▓▓▓▓▓▓▓▓    ▓▓▓▓▓▓▓▓▓▓▓▓    ▓▓▓▓▓▓▓▓▓▓▓▓    ▓▓▓▓▓▓▓▓▓▓▓▓
//	           ██████████        ██████████      ██████████      ██████████
//	            ████████          ████████        ████████        ████████
//	             ██●███            ██○███          ██○███          ██○███
//	             ██████            ██████          ██████          ██████
//	             ██████            ██████          ██████          ██████
//	           implement        adv-review           qa           delivery
func (m dashboardTUIModel) tuiAqueductRow(ch CataractaInfo) []string {
	const (
		colW      = 16  // width per cataracta column, including inter-arch space
		archTopW  = 12  // arch body width at the top row (colW-archTopW = gap budget)
		taperRows = 3   // rows of tapering: archTopW → pierW (2 chars narrower each row)
		pierRows  = 3   // rows of constant-width pier beneath the arch
		nameW     = 10  // prefix name column
	)
	// pierW = archTopW - taperRows*2 = 12 - 6 = 6 chars at minimum.
	// Must be ≥ 3 to fit the ●/○ indicator.
	pierW := archTopW - taperRows*2

	g := tuiStyleGreen
	dim := tuiStyleDim

	steps := ch.Steps
	if len(steps) == 0 {
		steps = []string{"—"}
	}
	n := len(steps)

	prefix := "  " + padRight(ch.Name, nameW) + "  "
	indent := strings.Repeat(" ", len([]rune(prefix)))

	isActive := func(step string) bool {
		return step == ch.Step && ch.DropletID != ""
	}

	// Channel inner width exactly spans arch tops: from left edge of arch 0
	// to right edge of arch n-1.
	// Each arch top is archTopW wide, centred in colW (padL = (colW-archTopW)/2).
	// Span = (n-1)*colW + archTopW.
	padL := (colW - archTopW) / 2
	chanW := (n-1)*colW + archTopW
	chanPad := strings.Repeat(" ", padL)

	// Line 1 — channel top.
	l1 := prefix + chanPad + dim.Render("╔"+strings.Repeat("═", chanW)+"╗")

	// Line 2 — water / idle.
	var water string
	if ch.DropletID != "" {
		bar := progressBar(ch.CataractaIndex, ch.TotalCataractae, 8)
		content := fmt.Sprintf(" ≈ ≈  %s  %s  %s  ≈ ≈ ", ch.DropletID, formatElapsed(ch.Elapsed), bar)
		water = g.Render(padOrTruncCenter(content, chanW))
	} else {
		water = dim.Render(padOrTruncCenter(" — idle — ", chanW))
	}
	l2 := indent + chanPad + dim.Render("║") + water + dim.Render("║")

	// Line 3 — channel bottom: same width as top, no ╤ joints.
	l3 := indent + chanPad + dim.Render("╚"+strings.Repeat("═", chanW)+"╝")

	// Arch rows: taper then pier.
	// Each row, every arch column is: padL spaces + body + padR spaces.
	// padL/padR grow as bodyW shrinks, widening the visible gap between piers
	// and creating the arch-opening silhouette.
	totalRows := taperRows + pierRows
	archLines := make([]string, totalRows)
	for row := 0; row < totalRows; row++ {
		bodyW := archTopW - row*2
		if bodyW < pierW {
			bodyW = pierW
		}
		rowPadL := (colW - bodyW) / 2
		rowPadR := colW - bodyW - rowPadL

		var sb strings.Builder
		sb.WriteString(indent)
		for i, step := range steps {
			active := isActive(step)

			sb.WriteString(strings.Repeat(" ", rowPadL))

			// Build arch body. Add ●/○ indicator in centre of middle pier row.
			body := []rune(strings.Repeat("▓", bodyW))
			if row == taperRows+1 {
				mid := len(body) / 2
				if active {
					body[mid] = '●'
				} else {
					body[mid] = '○'
				}
			}
			rendered := string(body)
			if active {
				sb.WriteString(g.Render(rendered))
			} else {
				sb.WriteString(dim.Render(rendered))
			}

			if i < n-1 {
				sb.WriteString(strings.Repeat(" ", rowPadR))
			}
		}
		archLines[row] = sb.String()
	}

	// Label line.
	var lblLine strings.Builder
	lblLine.WriteString(indent)
	for _, step := range steps {
		lbl := step
		if len([]rune(lbl)) > colW-1 {
			lbl = string([]rune(lbl)[:colW-2]) + "…"
		}
		centered := padOrTruncCenter(lbl, colW)
		if isActive(step) {
			lblLine.WriteString(g.Bold(true).Render(centered))
		} else {
			lblLine.WriteString(dim.Render(centered))
		}
	}

	result := []string{l1, l2, l3}
	result = append(result, archLines...)
	result = append(result, lblLine.String())
	return result
}

// tuiFlowGraphRow renders a single aqueduct as a styled flow graph row.
// The aqueduct name is shown as a left-column prefix so every row is labelled.
// Returns graphLine (the pipeline) and infoLine (↑ pointer with droplet info, or empty).
// Visual column tracking is kept separate from the ANSI-escaped string builder.
func (m dashboardTUIModel) tuiFlowGraphRow(ch CataractaInfo) (graphLine, infoLine string) {
	const namePad = 12 // fixed visual width for the name column
	namePfx := padRight(ch.Name, namePad)
	const pfxWidth = namePad + 4 // "  <name>  " = 2 + namePad + 2

	if len(ch.Steps) == 0 {
		if ch.DropletID == "" {
			return "  " + tuiStyleDim.Render(namePfx+"  (idle)"), ""
		}
		return "  " + tuiStyleGreen.Render(namePfx) + "  " + ch.Step, ""
	}

	var g strings.Builder
	g.WriteString("  ")
	g.WriteString(tuiStyleDim.Render(namePfx))
	g.WriteString("  ")
	activeVisualCol := -1
	visualCol := pfxWidth

	for i, step := range ch.Steps {
		if i > 0 {
			// " ──" = 3 visual chars, "●"/"○" = 1, "──▶ " = 4 → total 8
			if step == ch.Step && ch.DropletID != "" {
				g.WriteString(tuiStyleDim.Render(" ──"))
				g.WriteString(tuiStyleGreen.Render("●"))
				g.WriteString(tuiStyleDim.Render("──▶ "))
			} else {
				g.WriteString(tuiStyleDim.Render(" ──○──▶ "))
			}
			visualCol += 8
		}
		if step == ch.Step && ch.DropletID != "" {
			g.WriteString(tuiStyleGreen.Bold(true).Render(step))
			activeVisualCol = visualCol // step name starts here (after any incoming edge)
		} else {
			g.WriteString(tuiStyleDim.Render(step))
		}
		visualCol += len([]rune(step))
	}

	graphLine = g.String()
	if activeVisualCol >= 0 {
		bar := progressBar(ch.CataractaIndex, ch.TotalCataractae, 8)
		infoLine = strings.Repeat(" ", activeVisualCol) +
			tuiStyleDim.Render("↑ ") +
			tuiStyleGreen.Render(ch.Name) +
			tuiStyleDim.Render(" · "+ch.DropletID) +
			"  " + formatElapsed(ch.Elapsed) +
			"  " + tuiStyleGreen.Render(bar)
	}
	return
}

func (m dashboardTUIModel) viewCistern() []string {
	// Show open (queued) droplets — things waiting to be picked up.
	// In-progress items are already visible in the aqueduct diagram above.
	var queued []*cistern.Droplet
	for _, item := range m.data.CisternItems {
		if item.Status == "open" {
			queued = append(queued, item)
		}
	}
	if len(queued) == 0 {
		return []string{tuiStyleDim.Render("  Cistern is empty.")}
	}

	lines := make([]string, 0, len(queued))
	for _, item := range queued {
		lines = append(lines, m.viewCisternRow(item))
	}
	return lines
}

func (m dashboardTUIModel) viewCisternRow(item *cistern.Droplet) string {
	age := time.Since(item.CreatedAt).Round(time.Minute)
	id  := padRight(item.ID, 10)

	// Blocked?
	blockedBy, isBlocked := m.data.BlockedByMap[item.ID]
	var statusStr string
	if isBlocked {
		statusStr = tuiStyleRed.Render(fmt.Sprintf("blocked by %s", blockedBy))
	} else {
		statusStr = tuiStyleYellow.Render("queued")
	}

	// Priority indicator.
	prio := ""
	switch item.Priority {
	case 1:
		prio = tuiStyleRed.Render("↑")
	case 2:
		prio = tuiStyleDim.Render("·")
	case 3:
		prio = tuiStyleDim.Render("↓")
	}

	// Truncate title to fit.
	fixedW := 2 + 10 + 2 + 1 + 1 + 7 + 2 + 20
	titleW := m.width - fixedW
	if titleW < 8 {
		titleW = 8
	}
	title := item.Title
	r := []rune(title)
	if len(r) > titleW {
		title = string(r[:titleW-1]) + "…"
	}

	elapsed := tuiStyleDim.Render(formatElapsed(age))
	return fmt.Sprintf("  %s %s  %s  %s  %s",
		prio,
		tuiStyleDim.Render(id),
		elapsed,
		statusStr,
		title,
	)
}

func (m dashboardTUIModel) viewRecentFlow() []string {
	if len(m.data.RecentItems) == 0 {
		return []string{tuiStyleDim.Render("  No recent flow.")}
	}
	lines := make([]string, 0, len(m.data.RecentItems))
	for _, item := range m.data.RecentItems {
		lines = append(lines, m.viewRecentRow(item))
	}
	return lines
}

func (m dashboardTUIModel) viewRecentRow(item *cistern.Droplet) string {
	t := item.UpdatedAt.Format("15:04")
	step := item.CurrentCataracta
	if step == "" {
		step = "—"
	}

	var icon string
	switch item.Status {
	case "delivered":
		icon = tuiStyleGreen.Render("✓")
	case "stagnant":
		icon = tuiStyleRed.Render("✗")
	default:
		icon = tuiStyleDim.Render("·")
	}

	// Truncate title to fit terminal width.
	fixedWidth := 2 + 5 + 2 + 10 + 2 + 20 + 2 + 2 + 2
	titleWidth := m.width - fixedWidth
	if titleWidth < 8 {
		titleWidth = 8
	}
	title := item.Title
	r := []rune(title)
	if len(r) > titleWidth {
		title = string(r[:titleWidth-3]) + "..."
	}

	return fmt.Sprintf("  %s  %-10s  %-20s  %s  %s",
		tuiStyleDim.Render(t),
		item.ID,
		step,
		icon,
		title,
	)
}

// tuiPadCenter centers s within width using spaces.
func tuiPadCenter(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return s
	}
	total := width - len(r)
	left := total / 2
	right := total - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

// loadLogoLines loads the ASCII logo from well-known search paths,
// using the same search order as displayASCIILogo in root.go.
func loadLogoLines() []string {
	var candidates []string
	if env := os.Getenv("CT_ASCII_LOGO"); env != "" {
		candidates = append(candidates, env)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".cistern", "cistern_logo_ascii.txt"))
	}
	candidates = append(candidates, "cistern_logo_ascii.txt")

	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	return nil
}

// RunDashboardTUI runs the Bubble Tea TUI dashboard using the alternate screen.
func RunDashboardTUI(cfgPath, dbPath string) error {
	m := newDashboardTUIModel(cfgPath, dbPath)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
