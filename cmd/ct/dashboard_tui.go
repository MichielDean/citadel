package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MichielDean/cistern/internal/cistern"
)

// --- Lip Gloss styles ---

var (
	tuiStyleGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#4bb96e"))
	tuiStyleYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#f0c86b"))
	tuiStyleRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e06c75"))
	tuiStyleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#46465a"))
	tuiStyleHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9db1db"))
	tuiStyleFooter = lipgloss.NewStyle().Foreground(lipgloss.Color("#36364a"))
)

// --- Messages ---

type tuiTickMsg  time.Time
type tuiAnimMsg  time.Time
type tuiDataMsg  *DashboardData

const animInterval = 150 * time.Millisecond // water animation speed

// --- Model ---

type dashboardTUIModel struct {
	cfgPath    string
	dbPath     string
	data       *DashboardData
	frame      int  // animation frame counter — increments every animInterval
	scrollY    int  // scroll offset in lines (0 = top)
	width      int
	height     int
	peekActive bool
	peek       peekModel
}

func newDashboardTUIModel(cfgPath, dbPath string) dashboardTUIModel {
	return dashboardTUIModel{
		cfgPath: cfgPath,
		dbPath:  dbPath,
		width:   100,
		height:  24,
	}
}

func (m dashboardTUIModel) Init() tea.Cmd {
	return tea.Batch(m.fetchDataCmd(), tuiTick(), tuiAnimTick())
}

func tuiTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tuiTickMsg(t)
	})
}

func tuiAnimTick() tea.Cmd {
	return tea.Tick(animInterval, func(t time.Time) tea.Msg {
		return tuiAnimMsg(t)
	})
}

func (m dashboardTUIModel) fetchDataCmd() tea.Cmd {
	cfgPath, dbPath := m.cfgPath, m.dbPath
	return func() tea.Msg {
		return tuiDataMsg(fetchDashboardData(cfgPath, dbPath))
	}
}

func (m dashboardTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// When peek overlay is active, route most messages to the peek model.
	if m.peekActive {
		switch msg := msg.(type) {
		case tea.WindowSizeMsg:
			m.width = msg.Width
			m.height = msg.Height
			updated, cmd := m.peek.Update(msg)
			m.peek = updated.(peekModel)
			return m, cmd
		case tea.KeyMsg:
			switch msg.String() {
			case "q", "esc":
				m.peekActive = false
				return m, nil
			case "ctrl+c":
				return m, tea.Quit
			default:
				updated, cmd := m.peek.Update(msg)
				m.peek = updated.(peekModel)
				return m, cmd
			}
		case peekTickMsg, peekContentMsg:
			updated, cmd := m.peek.Update(msg)
			m.peek = updated.(peekModel)
			return m, cmd
		case tuiTickMsg:
			return m, tea.Batch(m.fetchDataCmd(), tuiTick())
		case tuiAnimMsg:
			m.frame++
			return m, tuiAnimTick()
		case tuiDataMsg:
			m.data = (*DashboardData)(msg)
			return m, nil
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tuiTickMsg:
		return m, tea.Batch(m.fetchDataCmd(), tuiTick())

	case tuiAnimMsg:
		m.frame++
		return m, tuiAnimTick()

	case tuiDataMsg:
		m.data = (*DashboardData)(msg)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		case "r", "R":
			return m, m.fetchDataCmd()
		case "p", "enter":
			// Open peek on the first active aqueduct.
			if m.data != nil {
				for _, ch := range m.data.Cataractae {
					if ch.DropletID != "" {
						session := ch.RepoName + "-" + ch.Name
						header := fmt.Sprintf("[%s] %s — flowing %s", ch.DropletID, ch.Step, formatElapsed(ch.Elapsed))
						pk := newPeekModel(defaultCapturer, session, header, 0)
						pk.width = m.width
						pk.height = m.height
						m.peek = pk
						m.peekActive = true
						return m, m.peek.Init()
					}
				}
			}
		case "up", "k":
			if m.scrollY > 0 {
				m.scrollY--
			}
		case "down", "j":
			m.scrollY++
		case "home", "g":
			m.scrollY = 0
		case "end", "G":
			m.scrollY = 999999 // clamped in View()
		case "pgup", "ctrl+u":
			m.scrollY -= m.height / 2
			if m.scrollY < 0 { m.scrollY = 0 }
		case "pgdown", "ctrl+d":
			m.scrollY += m.height / 2
		}

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.scrollY > 0 {
				m.scrollY -= 3
			}
			if m.scrollY < 0 { m.scrollY = 0 }
		case tea.MouseButtonWheelDown:
			m.scrollY += 3
		}
	}
	return m, nil
}

func (m dashboardTUIModel) View() string {
	if m.peekActive {
		return m.peek.View()
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

	// 4. Current flow — live narrative for active droplets.
	parts = append(parts, tuiStyleHeader.Render("  CURRENT FLOW"))
	parts = append(parts, m.viewCurrentFlow()...)
	parts = append(parts, sep)

	// 5. Cistern — queued droplets waiting.
	parts = append(parts, tuiStyleHeader.Render("  CISTERN"))
	parts = append(parts, m.viewCistern()...)
	parts = append(parts, sep)

	// 6. Recent flow.
	parts = append(parts, tuiStyleHeader.Render("  RECENT FLOW"))
	parts = append(parts, m.viewRecentFlow()...)
	parts = append(parts, sep)

	// 5. Footer — always pinned at the bottom (not scrolled).
	footer := tuiStyleFooter.Render("  q quit  r refresh  ↑↓/jk scroll  g/G top/bottom  p peek")

	// Apply scroll: render full content, slice visible window.
	full  := strings.Join(parts, "\n")
	lines := strings.Split(full, "\n")
	total := len(lines)
	// Reserve 1 line for the pinned footer.
	viewH := m.height - 1
	if viewH < 1 { viewH = 1 }
	maxScroll := total - viewH
	if maxScroll < 0 { maxScroll = 0 }
	if m.scrollY > maxScroll { m.scrollY = maxScroll }
	end := m.scrollY + viewH
	if end > total { end = total }
	visible := lines[m.scrollY:end]
	return strings.Join(visible, "\n") + "\n" + footer
}

func (m dashboardTUIModel) viewLogo() []string {
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

// viewAqueductArches renders active aqueducts as full Roman arch diagrams,
// and collapses idle aqueducts into a single compact text line each below.
// When all aqueducts are idle (drought state), renders a single dry arch instead.
func (m dashboardTUIModel) viewAqueductArches() []string {
	if len(m.data.Cataractae) == 0 {
		return []string{tuiStyleDim.Render("  No aqueducts configured")}
	}

	var active, idle []CataractaeInfo
	for _, ch := range m.data.Cataractae {
		if ch.DropletID != "" {
			active = append(active, ch)
		} else {
			idle = append(idle, ch)
		}
	}

	// Drought state: all aqueducts idle — show a single dry arch.
	if len(active) == 0 {
		return m.viewDroughtArch()
	}

	var lines []string

	// Full arch diagrams for active aqueducts only.
	for i, ch := range active {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, m.tuiAqueductRow(ch, m.frame)...)
	}

	// Compact idle section — one line per idle aqueduct.
	if len(idle) > 0 {
		lines = append(lines, "")
		for _, ch := range idle {
			lines = append(lines, m.viewIdleAqueductRow(ch))
		}
	}

	return lines
}

// viewDroughtArch renders a single unlabeled dry pillar arch centered in the terminal.
// Called when all aqueducts are idle (drought state). Shows:
//   - "drought" label centered above the arch in dim styling
//   - One 28-char-wide pillar rendered with dim grey (no water channel, no waterfall, no step labels)
//
// Returns 15 lines: 1 drought label + 14 pillar rows.
func (m dashboardTUIModel) viewDroughtArch() []string {
	const pillarW = 28

	leftPad := (m.width - pillarW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	indent := strings.Repeat(" ", leftPad)

	droughtLabel := tuiStyleDim.Render(tuiPadCenter("drought", m.width))

	buildRow := func(r int) string {
		switch r {
		case 0, 1, 2, 3, 4:
			return strings.Repeat(" ", pillarW)
		case 5:
			return tuiStyleDim.Render(strings.Repeat("▒", pillarW))
		case 6:
			return strings.Repeat(" ", 6) +
				tuiStyleDim.Render("░"+strings.Repeat("▒", 16)) +
				strings.Repeat(" ", 5)
		case 7:
			return strings.Repeat(" ", 9) +
				tuiStyleDim.Render("░"+strings.Repeat("▒", 9)) +
				strings.Repeat(" ", 9)
		case 8:
			return strings.Repeat(" ", 10) +
				tuiStyleDim.Render("░"+strings.Repeat("▒", 7)) +
				strings.Repeat(" ", 10)
		default: // rows 9–13: pier body
			return strings.Repeat(" ", 12) +
				tuiStyleDim.Render("░"+strings.Repeat("▒", 4)) +
				strings.Repeat(" ", 11)
		}
	}

	lines := make([]string, 0, 15)
	lines = append(lines, droughtLabel)
	for r := 0; r < 14; r++ {
		lines = append(lines, indent+buildRow(r))
	}
	return lines
}

// viewIdleAqueductRow renders a single idle aqueduct as a compact text line:
//
//	  virgo      cistern       ·  idle
func (m dashboardTUIModel) viewIdleAqueductRow(ch CataractaeInfo) string {
	const nameW = 12
	const repoW = 18
	name := padRight(ch.Name, nameW)
	repo := padRight(ch.RepoName, repoW)
	return fmt.Sprintf("  %s  %s  %s",
		tuiStyleDim.Render(name),
		tuiStyleDim.Render(repo),
		tuiStyleDim.Render("·  idle"),
	)
}

// tuiAqueductRow renders a single aqueduct as a durdraw pillar diagram.
// Layout (top to bottom): name → info → step labels → channel top (▀) → channel water → 9 pillar rows.
// Total: 14 lines (1 name + 1 info + 1 label + 2 channel + 9 pillar).
//
// Pillar row layout (28 chars wide):
//
//	row 5:   ▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒  (arch crown / road)
//	row 6:         ░▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒       (arch opening widens)
//	row 7:            ░▒▒▒▒▒▒▒▒▒            (arch narrowing)
//	row 8:             ░▒▒▒▒▒▒▒             (arch narrowing)
//	rows 9–13:           ░▒▒▒▒              (pier body)
//
// Water flows only to the active step — columns beyond it show a dry channel.
// Idle aqueducts (no active droplet) show no water at all.
func (m dashboardTUIModel) tuiAqueductRow(ch CataractaeInfo, frame int) []string {
	const (
		pillarW = 28 // pillar width per step (9 rows × 28 cols)
		nameW   = 10
	)

	g   := tuiStyleGreen
	dim := tuiStyleDim

	steps := ch.Steps
	if len(steps) == 0 {
		steps = []string{"—"}
	}
	n := len(steps)

	// indent is the shared left padding for channel and arch rows (name/info on own lines above).
	indent := "  " + strings.Repeat(" ", nameW) + "  "

	// Name line: aqueduct name + repo name on the same line.
	repoLabel := ch.RepoName
	if len([]rune(repoLabel)) > nameW {
		repoLabel = string([]rune(repoLabel)[:nameW-1]) + "…"
	}
	nameLine := "  " + g.Render(padRight(ch.Name, nameW)) + "  " + dim.Render(repoLabel)

	// Info line: droplet ID and elapsed time — shown only when active.
	var infoLine string
	if ch.DropletID != "" {
		info    := ch.DropletID + "  " + formatElapsed(ch.Elapsed)
		infoLine = indent + g.Render(info)
	}
	chanW      := n * pillarW

	isActive := func(step string) bool {
		return step == ch.Step && ch.DropletID != ""
	}

	// Find active step index (0-based); -1 if none.
	activeIdx := -1
	for i, step := range steps {
		if isActive(step) {
			activeIdx = i
			break
		}
	}

	// Waterfall is visible only when the droplet is on the final step.
	isLastStep := activeIdx == n-1 && activeIdx >= 0

	// Waterfall / channel-water styles — three brightness levels, black background.
	wfBright := lipgloss.NewStyle().Foreground(lipgloss.Color("#a8eeff")).Background(lipgloss.Color("0"))
	wfMid    := lipgloss.NewStyle().Foreground(lipgloss.Color("#3ec8e8")).Background(lipgloss.Color("0"))
	wfDim    := lipgloss.NewStyle().Foreground(lipgloss.Color("#1a7a96")).Background(lipgloss.Color("0"))

	// Channel wall style: dim foreground on black background.
	cStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#46465a")).Background(lipgloss.Color("0"))
	l1     := indent + cStyle.Render(strings.Repeat("▀", chanW))

	// Wave pattern: 6-char repeating unit animated each frame — water flows right.
	type waveCell struct {
		ch  string
		sty lipgloss.Style
	}
	waveCells := []waveCell{
		{"░", wfDim}, {"▒", wfMid}, {"▓", wfBright},
		{"≈", wfMid}, {"▒", wfMid}, {"░", wfDim},
	}
	const waveViz = 6

	renderWave := func(n int) string {
		var wb strings.Builder
		for i := 0; i < n; i++ {
			cell := waveCells[(i-frame%waveViz+waveViz*1000)%waveViz]
			wb.WriteString(cell.sty.Render(cell.ch))
		}
		return wb.String()
	}

	// Pillar bg used for dry channel sections and blank pillar rows.
	pillarBg := lipgloss.NewStyle().Background(lipgloss.Color("0"))

	// Compute wet/dry widths for partial-water rendering.
	// innerW is the channel content width (excluding the two █ walls).
	innerW := chanW - 2
	wetInnerW := 0
	if activeIdx >= 0 {
		// Subtract 1 to account for the left wall occupying the first column of
		// the pillar grid; without the correction the wet region extends one
		// character past the active pillar's visual right boundary.
		wetInnerW = (activeIdx+1)*pillarW - 1
		if wetInnerW > innerW {
			wetInnerW = innerW
		}
	}
	dryInnerW := innerW - wetInnerW

	// Build water: pure wave up to the active step, dry (empty) beyond.
	// Droplet info is displayed on the info line above — not embedded here.
	var water string
	if wetInnerW > 0 {
		water = renderWave(wetInnerW)
		if dryInnerW > 0 {
			water += pillarBg.Render(strings.Repeat(" ", dryInnerW))
		}
	} else {
		// No active step: fully dry channel.
		water = pillarBg.Render(strings.Repeat(" ", innerW))
	}

	// Waterfall brightness rotates with frame so ▓ appears to fall.
	wfA := func(sub int) lipgloss.Style {
		switch (sub + frame) % 3 {
		case 0:
			return wfBright
		case 1:
			return wfMid
		default:
			return wfDim
		}
	}

	wfRows := [14]string{
		wfMid.Render("▒") + wfA(0).Render("▓") + wfMid.Render("▒") + wfDim.Render("░"),
		wfDim.Render("░") + wfA(1).Render("▓") + wfMid.Render("▒"),
		" " + wfMid.Render("▒") + wfA(2).Render("▓") + wfMid.Render("▒"),
		" " + wfDim.Render("░") + wfA(0).Render("▓") + wfMid.Render("▒"),
		"  " + wfA(1).Render("▓") + wfMid.Render("▒"),
		"  " + wfA(2).Render("▓") + wfMid.Render("▒"),
		"  " + wfDim.Render("░") + wfMid.Render("▒") + wfA(0).Render("▓") + wfMid.Render("▒") + wfDim.Render("░"),
		wfDim.Render("░≈") + wfMid.Render("▒▒") + wfA(1).Render("▓▓") + wfMid.Render("▒▒") + wfDim.Render("≈░"),
		wfDim.Render("≈░") + wfMid.Render("▒▒") + wfA(2).Render("▓▓") + wfMid.Render("▒▒") + wfDim.Render("░≈"),
		" " + wfDim.Render("░") + wfMid.Render("▒") + wfA(0).Render("▓▓") + wfMid.Render("▒") + wfDim.Render("░"),
		" " + wfDim.Render("░") + wfMid.Render("▒") + wfA(1).Render("▓") + wfMid.Render("▒") + wfDim.Render("░"),
		"  " + wfDim.Render("░") + wfA(2).Render("▓") + wfDim.Render("░"),
		"  " + wfMid.Render("▒") + wfA(0).Render("▓") + wfMid.Render("▒"),
		"  " + wfDim.Render("░") + wfA(1).Render("▒") + wfDim.Render("░"),
	}

	wfExit := wfDim.Render("░") + wfMid.Render("▒") + wfA(0).Render("▓▓")
	l2 := indent + cStyle.Render("█") + water + cStyle.Render("█")
	if isLastStep {
		l2 += wfExit
	}

	// Pillar styles: fg=color3/olive on bg=black; active uses bright green for ▒.
	pillarFg     := lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Background(lipgloss.Color("0"))
	pillarActive := lipgloss.NewStyle().Foreground(lipgloss.Color("#4bb96e")).Background(lipgloss.Color("0"))

	// buildPillarRow returns the rendered 28-char content for pillar row r.
	// When active, ▒ chars use the bright green highlight color instead of olive.
	buildPillarRow := func(r int, active bool) string {
		heavy := pillarFg
		if active {
			heavy = pillarActive
		}
		switch r {
		case 0, 1, 2, 3, 4:
			return pillarBg.Render(strings.Repeat(" ", 28))
		case 5:
			return heavy.Render(strings.Repeat("▒", 28))
		case 6:
			return pillarBg.Render(strings.Repeat(" ", 6)) +
				pillarFg.Render("░") +
				heavy.Render(strings.Repeat("▒", 16)) +
				pillarBg.Render(strings.Repeat(" ", 5))
		case 7:
			return pillarBg.Render(strings.Repeat(" ", 9)) +
				pillarFg.Render("░") +
				heavy.Render(strings.Repeat("▒", 9)) +
				pillarBg.Render(strings.Repeat(" ", 9))
		case 8:
			return pillarBg.Render(strings.Repeat(" ", 10)) +
				pillarFg.Render("░") +
				heavy.Render(strings.Repeat("▒", 7)) +
				pillarBg.Render(strings.Repeat(" ", 10))
		default: // rows 9–13: pier body
			return pillarBg.Render(strings.Repeat(" ", 12)) +
				pillarFg.Render("░") +
				heavy.Render(strings.Repeat("▒", 4)) +
				pillarBg.Render(strings.Repeat(" ", 11))
		}
	}

	// Build 9 arch lines: tile one pillar column per step, then append waterfall.
	var archLines []string
	for r := 5; r < 14; r++ {
		var sb strings.Builder
		sb.WriteString(indent)
		for _, step := range steps {
			sb.WriteString(buildPillarRow(r, isActive(step)))
		}
		if isLastStep {
			sb.WriteString(wfRows[r])
		}
		archLines = append(archLines, sb.String())
	}

	// Label line: step names centered within pillarW, active step bold+green.
	// Built first so it appears above the channel rows in the result.
	var lblLine strings.Builder
	lblLine.WriteString(indent)
	for _, step := range steps {
		lbl := step
		if len([]rune(lbl)) > pillarW-1 {
			lbl = string([]rune(lbl)[:pillarW-2]) + "…"
		}
		centered := padOrTruncCenter(lbl, pillarW)
		if isActive(step) {
			lblLine.WriteString(g.Bold(true).Render(centered))
		} else {
			lblLine.WriteString(dim.Render(centered))
		}
	}

	// Return order: name → info → label → channel top → channel water → arch pillars.
	result := []string{nameLine, infoLine, lblLine.String(), l1, l2}
	result   = append(result, archLines...)
	return result
}

// tuiFlowGraphRow renders a single aqueduct as a styled flow graph row.
// The aqueduct name is shown as a left-column prefix so every row is labelled.
// Returns graphLine (the pipeline) and infoLine (↑ pointer with droplet info, or empty).
// Visual column tracking is kept separate from the ANSI-escaped string builder.
func (m dashboardTUIModel) tuiFlowGraphRow(ch CataractaeInfo) (graphLine, infoLine string) {
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
		bar := progressBar(ch.CataractaeIndex, ch.TotalCataractae, 8)
		infoLine = strings.Repeat(" ", activeVisualCol) +
			tuiStyleDim.Render("↑ ") +
			tuiStyleGreen.Render(ch.Name) +
			tuiStyleDim.Render(" · "+ch.DropletID) +
			"  " + formatElapsed(ch.Elapsed) +
			"  " + tuiStyleGreen.Render(bar)
	}
	return
}

func (m dashboardTUIModel) viewCurrentFlow() []string {
	d := m.data
	if len(d.FlowActivities) == 0 {
		return []string{tuiStyleDim.Render("  No droplets currently flowing.")}
	}

	maxW := m.width - 6 // leave room for indent + borders
	if maxW < 40 {
		maxW = 40
	}

	truncate := func(s string, n int) string {
		runes := []rune(s)
		if len(runes) <= n {
			return s
		}
		return string(runes[:n-1]) + "…"
	}

	// Collapse multi-line note content to a single meaningful line.
	firstMeaningfulLine := func(content string) string {
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "---") {
				return line
			}
		}
		return strings.TrimSpace(content)
	}

	var lines []string
	for _, fa := range d.FlowActivities {
		// Header: droplet ID + step + title.
		stepStr := tuiStyleGreen.Render(fa.Step)
		idStr   := tuiStyleHeader.Render(fa.DropletID)
		title   := tuiStyleDim.Render("  " + truncate(fa.Title, maxW-30))
		lines = append(lines, fmt.Sprintf("  %s  %s%s", idStr, stepStr, title))

		if len(fa.RecentNotes) == 0 {
			lines = append(lines, tuiStyleDim.Render("    (no notes yet — first pass)"))
		} else {
			for _, note := range fa.RecentNotes {
				// Timestamp: relative if recent, otherwise HH:MM.
				age := time.Since(note.CreatedAt)
				var ts string
				switch {
				case age < time.Minute:
					ts = "just now"
				case age < time.Hour:
					ts = fmt.Sprintf("%dm ago", int(age.Minutes()))
				default:
					ts = note.CreatedAt.Local().Format("15:04")
				}

				who  := tuiStyleDim.Render("[" + note.CataractaeName + "]")
				when := tuiStyleDim.Render(ts)
				text := firstMeaningfulLine(note.Content)
				text  = truncate(text, maxW-30)
				lines = append(lines,
					fmt.Sprintf("    › %s  %s  %s", who, tuiStyleFooter.Render(text), when),
				)
			}
		}
		lines = append(lines, "") // spacer between droplets
	}
	// Trim trailing blank line.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
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
	step := item.CurrentCataractae
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


// RunDashboardTUI runs the Bubble Tea TUI dashboard using the alternate screen.
func RunDashboardTUI(cfgPath, dbPath string) error {
	m := newDashboardTUIModel(cfgPath, dbPath)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
