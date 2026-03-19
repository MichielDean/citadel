package main

import (
	"fmt"
	"math"
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
	cfgPath string
	dbPath  string
	data    *DashboardData
	frame   int  // animation frame counter — increments every animInterval
	scrollY int  // scroll offset in lines (0 = top)
	width   int
	height  int
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

	// 5. Footer — always pinned at the bottom (not scrolled).
	footer := tuiStyleFooter.Render("  q quit  r refresh  ↑↓/jk scroll  g/G top/bottom")

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
		lines = append(lines, m.tuiAqueductRow(ch, m.frame)...)
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
// tuiAqueductRow renders a single aqueduct as a V1 Roman arch diagram:
// tapered brick piers with solid arch-crown material filling the span at the
// top, creating a proper arch opening that widens as the piers narrow.
//
// Each logical row → 2 rendered sub-rows (▀ mortar cap + █▌ brick face).
// Arch crown material fills the inter-pier span in the top (taper) rows,
// using a semicircle formula to curve the arch intrados from closed at the
// keystone to fully open at the impost.
//
//	  virgo   ╔══════════════════════════════════════════════════════╗
//	          ║  ≈ ≈  ci-abc  implement  2m 14s  ████░░░░  ≈ ≈ ≈   ║
//	          ╚══════════════════════════════════════════════════════╝
//	          ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
//	          ████▌████████▀▀▀▀▀▀▀▀▀▀▀▀████▌████████▀▀▀▀▀▀▀▀▀▀▀▀████▌████████
//	           ▀▀▀▀▀▀▀▀▀▀▀▀▀▀  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀
//	           ████▌█████████  ████████████▌████  █████████▌████  █████████
//	            ▀▀▀▀▀▀▀▀▀▀▀▀▀        ▀▀▀▀▀▀▀▀▀▀▀▀▀        ▀▀▀▀▀▀▀▀▀▀▀▀▀
//	            ████▌██████████        █████████████▌        ██████████
//	              ▀▀▀▀▀▀▀▀▀             ▀▀▀▀▀▀▀▀▀             ▀▀▀▀▀▀▀▀▀
//	              █████████             █████████             █████████
//	           implement        adv-review              qa              delivery
func (m dashboardTUIModel) tuiAqueductRow(ch CataractaInfo, frame int) []string {
	const (
		colW      = 20  // wider columns → bigger arch span → more room for the curve
		archTopW  = 10  // narrower pier top → span = colW-archTopW = 10 chars at keystone
		taperRows = 3   // pier narrows by 2 per row
		pierRows  = 1   // constant-width pier body rows
		brickW    = 4   // brick face width before ▌ joint
		nameW     = 10
	)
	// pierW = archTopW - taperRows*2 = 4
	pierW := archTopW - taperRows*2

	g   := tuiStyleGreen
	dim := tuiStyleDim

	steps := ch.Steps
	if len(steps) == 0 {
		steps = []string{"—"}
	}
	n := len(steps)

	prefix  := "  " + padRight(ch.Name, nameW) + "  "
	indent  := strings.Repeat(" ", len([]rune(prefix)))
	// chanW spans the full n*colW so channel walls align with label row edges.
	// chanPadL is 0 — channel starts immediately after the name prefix.
	// The arch piers sit inside the channel with rowPadL spacing on each side.
	chanPadL := 0
	chanW    := n * colW
	chanPad  := strings.Repeat(" ", chanPadL)

	isActive := func(step string) bool {
		return step == ch.Step && ch.DropletID != ""
	}

	// archCrownAtT computes arch-crown fill at an arbitrary t in [0,1].
	// t=0: keystone (fully closed). t=1: impost (fully open).
	// Evaluating mortar and brick sub-rows at different t gives 2× curve resolution
	// without adding logical rows.
	archCrownAtT := func(t float64, gapWidth int) (lf, og, rf int) {
		if gapWidth <= 0 {
			return 0, 0, 0
		}
		r  := float64(gapWidth) / 2.0
		oh := r * math.Sin(math.Pi / 2.0 * t)
		fe := r - oh
		full := int(fe)
		frac := fe - float64(full)
		haunch := frac > 0.25 && gapWidth > 2
		lf = full
		if haunch {
			lf++
		}
		rf = lf
		og = gapWidth - lf - rf
		if og < 0 {
			og = 0
			lf = gapWidth / 2
			rf = gapWidth - lf
		}
		return lf, og, rf
	}

	// Channel rows — brick masonry style.
	// l1: ▀ mortar cap, exactly chanW wide — matches arch mortar row 0 perfectly.
	// l2: solid █ walls + water content (chanW-2 body = chanW total incl. walls).
	// No l3: arch mortar row 0 is the channel floor — seamless connection.
	// Channel — full-width so it connects flush to both sides of the arch.
	// chanW = n*colW → channel walls align exactly with label row edges.
	// The arch piers sit inside the channel; rowPadL (grows each row) forms
	// solid masonry abutments that widen toward the base — architecturally correct.
	// Waterfall / channel-water styles — three brightness levels.
	// Used for both the falling waterfall and the water flowing inside the channel.
	wfBright := lipgloss.NewStyle().Foreground(lipgloss.Color("#a8eeff"))
	wfMid    := lipgloss.NewStyle().Foreground(lipgloss.Color("#3ec8e8"))
	wfDim    := lipgloss.NewStyle().Foreground(lipgloss.Color("#1a7a96"))

	cStyle := dim
	l1     := prefix + chanPad + cStyle.Render(strings.Repeat("▀", chanW))
	// Wave pattern: 6-char repeating unit. Each char has a style.
	// Animated by offsetting the start position each frame — water flows left→right.
	type waveCell struct { ch string; sty lipgloss.Style }
	waveCells := []waveCell{
		{"░", wfDim}, {"▒", wfMid}, {"▓", wfBright},
		{"≈", wfMid}, {"▒", wfMid}, {"░", wfDim},
	}
	const waveViz = 6

	// renderWave renders `n` chars of the scrolling wave at the current frame offset.
	// offset>0 = pattern scrolls left (water flows right through the channel).
	renderWave := func(n int) string {
		var wb strings.Builder
		for i := 0; i < n; i++ {
			cell := waveCells[(i-frame%waveViz+waveViz*1000)%waveViz]
			wb.WriteString(cell.sty.Render(cell.ch))
		}
		return wb.String()
	}

	// buildChanWater centers `infoStr` with animated wave filling each side.
	buildChanWater := func(infoStr string, infoStyle lipgloss.Style) string {
		info     := infoStyle.Render(infoStr)
		infoViz  := len([]rune(infoStr))
		sideW    := (chanW - 2 - infoViz) / 2
		if sideW < 0 { sideW = 0 }
		// Right side may be 1 wider to fill odd remainder.
		rightW := chanW - 2 - infoViz - sideW
		if rightW < 0 { rightW = 0 }
		return renderWave(sideW) + info + renderWave(rightW)
	}

	var water string
	if ch.DropletID != "" {
		bar     := progressBar(ch.CataractaIndex, ch.TotalCataractae, 8)
		infoStr := fmt.Sprintf("  %s  %s  %s  ", ch.DropletID, formatElapsed(ch.Elapsed), bar)
		water    = buildChanWater(infoStr, wfMid)
	} else {
		water = buildChanWater("  — idle —  ", wfDim)
	}

	// Waterfall: parabolic arc, animated by cycling the brightness pattern downward
	// each frame — creates the illusion of water accelerating as it falls.
	// There are 3 brightness frames; the ▓ core shifts one position down per cycle.
	// wfRow(subRow) returns the rendered waterfall string for that sub-row.
	sp := func(n int) string { return strings.Repeat(" ", n) }

	// Brightness pattern rotates with frame so ▓ appears to fall through the curtain.
	// f0=bright, f1=mid, f2=dim — a 3-position cycle.
	wfA := func(sub int) lipgloss.Style {
		switch (sub + frame) % 3 {
		case 0: return wfBright
		case 1: return wfMid
		default: return wfDim
		}
	}

	// Option C: Spill & curtain — water exits flush against the abutment edge,
	// hugs the structure for the first couple of rows, then falls nearly vertical.
	// Stays close → feels connected to the arch. Wide pool forms at the base.
	wfRows := [8]string{
		// sub 0: exits flush — ▓ right against the abutment edge
		sp(0) + wfMid.Render("▒") + wfA(0).Render("▓") + wfMid.Render("▒") + wfDim.Render("░"),
		// sub 1: still hugging the structure
		sp(0) + wfDim.Render("░") + wfA(1).Render("▓") + wfMid.Render("▒"),
		// sub 2: just starts to peel away
		sp(1) + wfMid.Render("▒") + wfA(2).Render("▓") + wfMid.Render("▒"),
		// sub 3: drifting slightly, near-vertical
		sp(1) + wfDim.Render("░") + wfA(0).Render("▓") + wfMid.Render("▒"),
		// sub 4: thin falling curtain
		sp(2) + wfA(1).Render("▓") + wfMid.Render("▒"),
		// sub 5: thin falling curtain
		sp(2) + wfA(2).Render("▓") + wfMid.Render("▒"),
		// sub 6: widening as it approaches the base
		sp(2) + wfDim.Render("░") + wfMid.Render("▒") + wfA(0).Render("▓") + wfMid.Render("▒") + wfDim.Render("░"),
		// sub 7: wide pool — spray at edges, bright core, spreads both ways
		sp(0) + wfDim.Render("░≈") + wfMid.Render("▒▒") + wfA(1).Render("▓▓") + wfMid.Render("▒▒") + wfDim.Render("≈░"),
	}

	// Channel exit: compact spill — trim trailing two blocks (▒░) off the top row.
	wfExit := wfDim.Render("░") + wfMid.Render("▒") + wfA(0).Render("▓▓")
	l2 := indent + chanPad + cStyle.Render("█") + water + cStyle.Render("█") + wfExit

	// Arch + pier rows: each logical row → 2 rendered sub-rows.
	// Solid masonry ABUTMENTS (rowPadL wide) fill each side so the arch spans
	// exactly chanW at every row — no blank gaps beside the channel walls.
	// Mortar sub-row: t = lr/taperRows  (start of logical row)
	// Brick sub-row:  t = (lr+0.5)/taperRows  (mid-point — extra curve step)
	var archLines []string
	for lr := 0; lr < taperRows+pierRows; lr++ {
		bodyW := archTopW - lr*2
		if bodyW < pierW {
			bodyW = pierW
		}
		rowPadL := (colW - bodyW) / 2
		gapW    := colW - bodyW

		// Mortar sub-row arch crown: t at start of this logical row.
		tMort  := math.Min(float64(lr)/float64(taperRows), 1.0)
		lfM, ogM, rfM := 0, gapW, 0
		if lr < taperRows {
			lfM, ogM, rfM = archCrownAtT(tMort, gapW)
		}

		// Brick sub-row arch crown: t at midpoint — gives extra curve resolution.
		tBrick := math.Min(float64(lr)+0.5, float64(taperRows)) / float64(taperRows)
		lfB, ogB, rfB := 0, gapW, 0
		if lr < taperRows {
			lfB, ogB, rfB = archCrownAtT(tBrick, gapW)
		}

		var mortSB, brickSB strings.Builder
		mortSB.WriteString(indent)
		brickSB.WriteString(indent)

		// Left abutment: solid masonry filling from channel wall to first pier edge.
		// Width = rowPadL, grows each row so base is wider than keystone — correct.
		offset := (brickW / 2) * (lr % 2)
		{
			abutMort := strings.Repeat("▀", rowPadL)
			abutBrick := make([]rune, rowPadL)
			for c := 0; c < rowPadL; c++ {
				if (c+offset)%(brickW+1) == brickW {
					abutBrick[c] = '▌'
				} else {
					abutBrick[c] = '█'
				}
			}
			mortSB.WriteString(dim.Render(abutMort))
			brickSB.WriteString(dim.Render(string(abutBrick)))
		}

		for i, step := range steps {
			pStyle := dim
			if isActive(step) {
				pStyle = g
			}

			// Pier mortar sub-row.
			mortSB.WriteString(pStyle.Render(strings.Repeat("▀", bodyW)))

			// Pier brick sub-row: staggered joints.
			body   := make([]rune, bodyW)
			for c := 0; c < bodyW; c++ {
				if (c+offset)%(brickW+1) == brickW {
					body[c] = '▌'
				} else {
					body[c] = '█'
				}
			}
			brickSB.WriteString(pStyle.Render(string(body)))

			// Inter-pier span: arch crown colored per side.
			// Left fill (lf) belongs to the LEFT pier; right fill (rf) to the RIGHT pier.
			// This prevents color bleeding onto adjacent idle piers.
			if i < n-1 {
				lStyle := dim // left arch crown = left pier's color
				if isActive(step) {
					lStyle = g
				}
				rStyle := dim // right arch crown = right pier's color
				if isActive(steps[i+1]) {
					rStyle = g
				}

				// ── Mortar sub-row ────────────────────────────────────────────────────
				if lfM > 0 {
					mortSB.WriteString(lStyle.Render(strings.Repeat("▀", lfM)))
				}
				if ogM > 0 {
					mortSB.WriteString(strings.Repeat(" ", ogM))
				}
				if rfM > 0 {
					mortSB.WriteString(rStyle.Render(strings.Repeat("▀", rfM)))
				}

				// ── Brick sub-row (▌▐ haunch at intrados edge) ───────────────────────
				if lfB > 0 {
					if lfB > 1 {
						brickSB.WriteString(lStyle.Render(strings.Repeat("█", lfB-1)))
					}
					brickSB.WriteString(lStyle.Render("▌"))
				}
				if ogB > 0 {
					brickSB.WriteString(strings.Repeat(" ", ogB))
				}
				if rfB > 0 {
					brickSB.WriteString(rStyle.Render("▐"))
					if rfB > 1 {
						brickSB.WriteString(rStyle.Render(strings.Repeat("█", rfB-1)))
					}
				}
			}
		}
		// Right abutment: mirrors the left, fills channel wall to last pier edge.
		{
			abutMort := strings.Repeat("▀", rowPadL)
			abutBrick := make([]rune, rowPadL)
			for c := 0; c < rowPadL; c++ {
				if (c+offset)%(brickW+1) == brickW {
					abutBrick[c] = '▌'
				} else {
					abutBrick[c] = '█'
				}
			}
			mortSB.WriteString(dim.Render(abutMort))
			brickSB.WriteString(dim.Render(string(abutBrick)))
		}

		// Waterfall: append pre-built parabolic arc row for this sub-row pair.
		subRow := lr * 2
		mortSB.WriteString(wfRows[subRow])
		brickSB.WriteString(wfRows[subRow+1])

		archLines = append(archLines, mortSB.String(), brickSB.String())
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

	result := []string{l1, l2}
	result  = append(result, archLines...)
	result  = append(result, lblLine.String())
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


// RunDashboardTUI runs the Bubble Tea TUI dashboard using the alternate screen.
func RunDashboardTUI(cfgPath, dbPath string) error {
	m := newDashboardTUIModel(cfgPath, dbPath)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
