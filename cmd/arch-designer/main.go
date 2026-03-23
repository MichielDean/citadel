package main

import (
	"fmt"
	"math"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Parameter definitions ───────────────────────────────────────────────────

type param struct {
	name string
	val  int
	min  int
	max  int
}

const (
	pColW = iota
	pArchTopW
	pTaperRows
	pPierRows
	pBrickW
	pStoneColor
	pShadowColor
	pMortarColor
	pLitColor
	pVoidColor
	pShadingMode
	paramCount
)

type shadingMode int

const (
	shadingFlat shadingMode = iota
	shadingLit
)

func (s shadingMode) String() string {
	if s == shadingLit {
		return "lit"
	}
	return "flat"
}

// ── Color presets ───────────────────────────────────────────────────────────

type colorPreset struct {
	name                                       string
	stone, shadow, mortar, lit, void int
}

var presets = []colorPreset{
	{"stone", 243, 238, 240, 252, 233},
	{"sandstone", 180, 137, 144, 223, 233},
	{"terracotta", 131, 88, 95, 174, 233},
	{"slate", 66, 59, 102, 110, 233},
}

// ── Model ───────────────────────────────────────────────────────────────────

type model struct {
	params    [paramCount]param
	selected  int
	presetIdx int
}

func defaultParams() [paramCount]param {
	return [paramCount]param{
		pColW:        {"colW", 14, 8, 30},
		pArchTopW:    {"archTopW", 9, 4, 20},
		pTaperRows:   {"taperRows", 4, 1, 6},
		pPierRows:    {"pierRows", 1, 1, 4},
		pBrickW:      {"brickW", 4, 2, 8},
		pStoneColor:  {"stoneColor", 243, 0, 255},
		pShadowColor: {"shadowColor", 238, 0, 255},
		pMortarColor: {"mortarColor", 240, 0, 255},
		pLitColor:    {"litColor", 252, 0, 255},
		pVoidColor:   {"voidColor", 233, 0, 255},
		pShadingMode: {"shadingMode", 0, 0, 1},
	}
}

func initialModel() model {
	return model{params: defaultParams()}
}

func (m model) Init() tea.Cmd { return nil }

// ── Update ──────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "s", "enter":
			m.printConstants()
			return m, tea.Quit
		case "tab":
			m.selected = (m.selected + 1) % paramCount
		case "shift+tab":
			m.selected = (m.selected - 1 + paramCount) % paramCount
		case "up":
			m.adjust(1)
		case "down":
			m.adjust(-1)
		case "shift+up":
			m.adjust(5)
		case "shift+down":
			m.adjust(-5)
		case "l", "L":
			m.presetIdx = (m.presetIdx + 1) % len(presets)
			p := presets[m.presetIdx]
			m.params[pStoneColor].val = p.stone
			m.params[pShadowColor].val = p.shadow
			m.params[pMortarColor].val = p.mortar
			m.params[pLitColor].val = p.lit
			m.params[pVoidColor].val = p.void
		case "r", "R":
			sel := m.selected
			m = initialModel()
			m.selected = sel
		}
	}
	return m, nil
}

func (m *model) adjust(delta int) {
	p := &m.params[m.selected]
	p.val += delta
	if p.val < p.min {
		p.val = p.min
	}
	if p.val > p.max {
		p.val = p.max
	}
}

// ── View ────────────────────────────────────────────────────────────────────

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))
	selStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	normalStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	statusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

func (m model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("  Cistern Arch Designer") + "\n\n")

	// Parameter panel — two columns.
	leftParams := []int{pColW, pArchTopW, pTaperRows, pPierRows, pBrickW, pShadingMode}
	rightParams := []int{pStoneColor, pShadowColor, pMortarColor, pLitColor, pVoidColor}

	maxRows := len(leftParams)
	if len(rightParams) > maxRows {
		maxRows = len(rightParams)
	}

	for row := 0; row < maxRows; row++ {
		left := ""
		if row < len(leftParams) {
			left = m.renderParam(leftParams[row])
		}
		right := ""
		if row < len(rightParams) {
			right = m.renderParam(rightParams[row])
		}
		// Pad left column to 32 chars (visual width).
		leftPad := 32 - lipgloss.Width(left)
		if leftPad < 0 {
			leftPad = 0
		}
		b.WriteString("  " + left + strings.Repeat(" ", leftPad) + right + "\n")
	}

	// Preset indicator.
	b.WriteString("\n  " + dimStyle.Render(fmt.Sprintf("preset: %s (L to cycle)", presets[m.presetIdx].name)) + "\n")

	b.WriteString("\n")

	// Arch preview.
	preview := m.renderArch()
	for _, line := range preview {
		b.WriteString("  " + line + "\n")
	}

	b.WriteString("\n")
	b.WriteString("  " + statusStyle.Render("↑↓ adjust  Tab next param  S save  Q quit") + "\n")

	return b.String()
}

func (m model) renderParam(idx int) string {
	p := m.params[idx]
	var display string
	if idx == pShadingMode {
		display = shadingMode(p.val).String()
	} else if idx >= pStoneColor && idx <= pVoidColor {
		// Show color swatch.
		swatch := lipgloss.NewStyle().Background(lipgloss.Color(fmt.Sprintf("%d", p.val))).Render("  ")
		display = fmt.Sprintf("%3d %s", p.val, swatch)
	} else {
		display = fmt.Sprintf("%d", p.val)
	}

	label := fmt.Sprintf("%-14s", p.name)
	if idx == m.selected {
		return selStyle.Render("▸ "+label) + " " + selStyle.Render(display)
	}
	return normalStyle.Render("  "+label) + " " + normalStyle.Render(display)
}

// ── Arch rendering (adapted from dashboard_tui.go) ─────────────────────────

func (m model) renderArch() []string {
	colW := m.params[pColW].val
	archTopW := m.params[pArchTopW].val
	taperRows := m.params[pTaperRows].val
	pierRows := m.params[pPierRows].val
	brickW := m.params[pBrickW].val
	shading := shadingMode(m.params[pShadingMode].val)

	stoneColor := fmt.Sprintf("%d", m.params[pStoneColor].val)
	shadowColor := fmt.Sprintf("%d", m.params[pShadowColor].val)
	mortarColor := fmt.Sprintf("%d", m.params[pMortarColor].val)
	litColor := fmt.Sprintf("%d", m.params[pLitColor].val)
	voidColor := fmt.Sprintf("%d", m.params[pVoidColor].val)

	sStone := lipgloss.NewStyle().Foreground(lipgloss.Color(stoneColor))
	sShadow := lipgloss.NewStyle().Foreground(lipgloss.Color(shadowColor))
	sMortar := lipgloss.NewStyle().Foreground(lipgloss.Color(mortarColor))
	sLit := lipgloss.NewStyle().Foreground(lipgloss.Color(litColor))
	sVoid := lipgloss.NewStyle().Foreground(lipgloss.Color(voidColor))

	// Clamp archTopW to colW.
	if archTopW > colW {
		archTopW = colW
	}

	pierW := archTopW - taperRows*2
	if pierW < 1 {
		pierW = 1
	}

	numCols := 4 // 4 columns for preview
	totalW := numCols * colW

	archCrownAtT := func(t float64, gapWidth int) (lf, og, rf int) {
		if gapWidth <= 0 {
			return 0, 0, 0
		}
		r := float64(gapWidth) / 2.0
		oh := r * math.Sin(math.Pi/2.0*t)
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

	// renderPierBrick renders a pier brick row with optional 3-tone shading.
	renderPierBrick := func(width, offset int) string {
		if width <= 0 {
			return ""
		}
		if shading == shadingFlat {
			body := make([]rune, width)
			for c := 0; c < width; c++ {
				if (c+offset)%(brickW+1) == brickW {
					body[c] = '▌'
				} else {
					body[c] = '█'
				}
			}
			return sStone.Render(string(body))
		}
		// Lit shading: left 2px = litColor, middle = stoneColor, right 2px = shadowColor.
		var sb strings.Builder
		litW := 2
		shadW := 2
		if width <= 4 {
			litW = width / 2
			shadW = width - litW
		}
		mainW := width - litW - shadW
		for c := 0; c < litW; c++ {
			if (c+offset)%(brickW+1) == brickW {
				sb.WriteString(sLit.Render("▌"))
			} else {
				sb.WriteString(sLit.Render("█"))
			}
		}
		for c := litW; c < litW+mainW; c++ {
			if (c+offset)%(brickW+1) == brickW {
				sb.WriteString(sStone.Render("▌"))
			} else {
				sb.WriteString(sStone.Render("█"))
			}
		}
		for c := litW + mainW; c < width; c++ {
			if (c+offset)%(brickW+1) == brickW {
				sb.WriteString(sShadow.Render("▌"))
			} else {
				sb.WriteString(sShadow.Render("█"))
			}
		}
		return sb.String()
	}

	// renderPierMortar renders a pier mortar row with optional 3-tone shading.
	renderPierMortar := func(width int) string {
		if width <= 0 {
			return ""
		}
		if shading == shadingFlat {
			return sMortar.Render(strings.Repeat("▀", width))
		}
		litW := 2
		shadW := 2
		if width <= 4 {
			litW = width / 2
			shadW = width - litW
		}
		mainW := width - litW - shadW
		return sLit.Render(strings.Repeat("▀", litW)) +
			sMortar.Render(strings.Repeat("▀", mainW)) +
			sShadow.Render(strings.Repeat("▀", shadW))
	}

	// Channel cap row.
	var lines []string
	chanCap := sMortar.Render(strings.Repeat("▀", totalW))
	lines = append(lines, chanCap)

	// Channel body row.
	chanBody := sStone.Render("█") + sVoid.Render(strings.Repeat("░", totalW-2)) + sStone.Render("█")
	lines = append(lines, chanBody)

	// Arch + pier rows.
	for lr := 0; lr < taperRows+pierRows; lr++ {
		bodyW := archTopW - lr*2
		if bodyW < pierW {
			bodyW = pierW
		}
		rowPadL := (colW - bodyW) / 2
		gapW := colW - bodyW

		tMort := math.Min(float64(lr)/float64(taperRows), 1.0)
		lfM, ogM, rfM := 0, gapW, 0
		if lr < taperRows {
			lfM, ogM, rfM = archCrownAtT(tMort, gapW)
		}

		tBrick := math.Min(float64(lr)+0.5, float64(taperRows)) / float64(taperRows)
		lfB, ogB, rfB := 0, gapW, 0
		if lr < taperRows {
			lfB, ogB, rfB = archCrownAtT(tBrick, gapW)
		}

		offset := (brickW / 2) * (lr % 2)

		var mortSB, brickSB strings.Builder

		// Left abutment.
		mortSB.WriteString(renderPierMortar(rowPadL))
		brickSB.WriteString(renderPierBrick(rowPadL, offset))

		for i := 0; i < numCols; i++ {
			// Pier body.
			mortSB.WriteString(renderPierMortar(bodyW))
			brickSB.WriteString(renderPierBrick(bodyW, offset))

			// Inter-pier span.
			if i < numCols-1 {
				// Mortar crown.
				if lfM > 0 {
					mortSB.WriteString(sMortar.Render(strings.Repeat("▀", lfM)))
				}
				if ogM > 0 {
					mortSB.WriteString(sVoid.Render(strings.Repeat(" ", ogM)))
				}
				if rfM > 0 {
					mortSB.WriteString(sMortar.Render(strings.Repeat("▀", rfM)))
				}

				// Brick crown with haunch markers.
				if lfB > 0 {
					if lfB > 1 {
						brickSB.WriteString(sStone.Render(strings.Repeat("█", lfB-1)))
					}
					brickSB.WriteString(sStone.Render("▌"))
				}
				if ogB > 0 {
					brickSB.WriteString(sVoid.Render(strings.Repeat(" ", ogB)))
				}
				if rfB > 0 {
					brickSB.WriteString(sStone.Render("▐"))
					if rfB > 1 {
						brickSB.WriteString(sStone.Render(strings.Repeat("█", rfB-1)))
					}
				}
			}
		}

		// Right abutment.
		mortSB.WriteString(renderPierMortar(rowPadL))
		brickSB.WriteString(renderPierBrick(rowPadL, offset))

		lines = append(lines, mortSB.String(), brickSB.String())
	}

	// Label row.
	labels := []string{"step-1", "step-2", "step-3", "step-4"}
	var lblSB strings.Builder
	for _, lbl := range labels {
		if len([]rune(lbl)) > colW-1 {
			lbl = string([]rune(lbl)[:colW-2]) + "…"
		}
		padTotal := colW - len([]rune(lbl))
		padL := padTotal / 2
		padR := padTotal - padL
		lblSB.WriteString(dimStyle.Render(strings.Repeat(" ", padL) + lbl + strings.Repeat(" ", padR)))
	}
	lines = append(lines, lblSB.String())

	return lines
}

// ── Print constants ─────────────────────────────────────────────────────────

func (m model) printConstants() {
	fmt.Println()
	fmt.Println("// Arch design constants — paste into tuiAqueductRow() in cmd/ct/dashboard_tui.go")
	fmt.Println("const (")
	fmt.Printf("\tcolW      = %d\n", m.params[pColW].val)
	fmt.Printf("\tarchTopW  = %d\n", m.params[pArchTopW].val)
	fmt.Printf("\ttaperRows = %d\n", m.params[pTaperRows].val)
	fmt.Printf("\tpierRows  = %d\n", m.params[pPierRows].val)
	fmt.Printf("\tbrickW    = %d\n", m.params[pBrickW].val)
	fmt.Println(")")
	fmt.Println("// Colors (256-color palette)")
	fmt.Println("const (")
	fmt.Printf("\tarchStoneColor  = %d\n", m.params[pStoneColor].val)
	fmt.Printf("\tarchShadowColor = %d\n", m.params[pShadowColor].val)
	fmt.Printf("\tarchMortarColor = %d\n", m.params[pMortarColor].val)
	fmt.Printf("\tarchLitColor    = %d\n", m.params[pLitColor].val)
	fmt.Println(")")
}

// ── Main ────────────────────────────────────────────────────────────────────

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
