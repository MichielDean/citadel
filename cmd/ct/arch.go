package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Semantic color roles for the arch renderer.
// Use these names instead of inline hex values to keep the palette legible and easy to retheme.
var (
	// archRoleBackground is a black-background cell for blank pixels in a tiled pillar.
	// The black background prevents color bleed between adjacent pillars.
	archRoleBackground = lipgloss.NewStyle().Background(lipgloss.Color("0"))

	// archRoleEdge is the shadow/edge color applied to '░' pixels.
	archRoleEdge = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Background(lipgloss.Color("0"))

	// archRoleIdle is the stone color for '▒' fill pixels when the step is not active.
	archRoleIdle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Background(lipgloss.Color("0"))

	// archRoleActive is the highlight color for '▒' fill pixels when the step is active.
	archRoleActive = lipgloss.NewStyle().Foreground(lipgloss.Color("#4bb96e")).Background(lipgloss.Color("0"))

	// archRoleDrought is the dim color used for all pixels in the drought (all-idle) arch.
	// No background is set because the drought arch is a single centred pillar — no bleed risk.
	archRoleDrought = lipgloss.NewStyle().Foreground(lipgloss.Color("#46465a"))

	// archRoleChannelWall is the color for channel wall characters (▀ top, █ sides).
	archRoleChannelWall = lipgloss.NewStyle().Foreground(lipgloss.Color("#46465a")).Background(lipgloss.Color("0"))

	// archRoleWaterBright / archRoleWaterMid / archRoleWaterDim define the three-level
	// brightness palette for the animated water channel and waterfall.
	archRoleWaterBright = lipgloss.NewStyle().Foreground(lipgloss.Color("#a8eeff")).Background(lipgloss.Color("0"))
	archRoleWaterMid    = lipgloss.NewStyle().Foreground(lipgloss.Color("#3ec8e8")).Background(lipgloss.Color("0"))
	archRoleWaterDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#1a7a96")).Background(lipgloss.Color("0"))
)

// Pixel values used in archPixelMap. Each cell holds exactly one of these runes.
const (
	pxBlank rune = ' ' // transparent / background
	pxEdge  rune = '░' // arch shadow / edge shadow
	pxFill  rune = '▒' // arch stone fill
)

// archPillarW and archPillarH are the fixed dimensions of a single pillar tile.
const (
	archPillarW = 28
	archPillarH = 14
)

// archPixelMap is the static archPillarH×archPillarW pixel map for one Roman arch pillar.
// Each cell contains pxBlank (' '), pxEdge ('░'), or pxFill ('▒').
//
// Reading the map top-to-bottom gives a visual impression of the arch shape:
//
//	rows 0–4:  blank space above the arch crown
//	row  5:    full-width road surface  ▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒
//	row  6:    arch opening widens      ______░▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒_____
//	row  7:    arch narrows             _________░▒▒▒▒▒▒▒▒▒_________
//	row  8:    arch narrows             __________░▒▒▒▒▒▒▒__________
//	rows 9–13: pier body               ____________░▒▒▒▒___________
var archPixelMap = [archPillarH][archPillarW]rune{
	// row 0: blank above arch
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 1: blank above arch
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 2: blank above arch
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 3: blank above arch
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 4: blank above arch
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 5: arch crown — full-width fill (road surface)
	{'▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒'},
	// row 6: arch opening — 6 blank, edge, 16 fill, 5 blank
	{' ', ' ', ' ', ' ', ' ', ' ', '░', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', ' ', ' ', ' ', ' ', ' '},
	// row 7: arch narrows — 9 blank, edge, 9 fill, 9 blank
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', '░', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', '▒', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 8: arch narrows — 10 blank, edge, 7 fill, 10 blank
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', '░', '▒', '▒', '▒', '▒', '▒', '▒', '▒', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 9: pier body — 12 blank, edge, 4 fill, 11 blank
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', '░', '▒', '▒', '▒', '▒', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 10: pier body — 12 blank, edge, 4 fill, 11 blank
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', '░', '▒', '▒', '▒', '▒', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 11: pier body — 12 blank, edge, 4 fill, 11 blank
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', '░', '▒', '▒', '▒', '▒', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 12: pier body — 12 blank, edge, 4 fill, 11 blank
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', '░', '▒', '▒', '▒', '▒', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
	// row 13: pier body — 12 blank, edge, 4 fill, 11 blank
	{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', '░', '▒', '▒', '▒', '▒', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '},
}

// renderArchPillarRowWith renders row r of archPixelMap as a styled string.
// fill is applied to pxFill ('▒') pixels; edge to pxEdge ('░'); bg to pxBlank (' ').
// Pass an empty lipgloss.Style{} as bg to render blank cells as plain spaces with no background.
func renderArchPillarRowWith(r int, fill, edge, bg lipgloss.Style) string {
	row := archPixelMap[r]
	var sb strings.Builder
	for _, px := range row {
		switch px {
		case pxFill:
			sb.WriteString(fill.Render(string(px)))
		case pxEdge:
			sb.WriteString(edge.Render(string(px)))
		default:
			sb.WriteString(bg.Render(string(px)))
		}
	}
	return sb.String()
}

// renderArchPillarRow renders row r for a normal (non-drought) pillar.
// When active is true, pxFill pixels use archRoleActive; otherwise archRoleIdle.
// pxEdge always uses archRoleEdge; pxBlank uses archRoleBackground (black bg).
func renderArchPillarRow(r int, active bool) string {
	fill := archRoleIdle
	if active {
		fill = archRoleActive
	}
	return renderArchPillarRowWith(r, fill, archRoleEdge, archRoleBackground)
}

// renderDroughtPillarRow renders row r for the drought arch (all aqueducts idle).
// Both pxFill and pxEdge use archRoleDrought; blank cells are unstyled plain spaces.
func renderDroughtPillarRow(r int) string {
	return renderArchPillarRowWith(r, archRoleDrought, archRoleDrought, lipgloss.NewStyle())
}
