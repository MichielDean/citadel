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
	scroll    int
}

func newDashboardTUIModel(cfgPath, dbPath string) dashboardTUIModel {
	return dashboardTUIModel{
		cfgPath:   cfgPath,
		dbPath:    dbPath,
		logoLines: loadLogoLines(),
		width:     80,
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
		case "up", "k":
			if m.scroll > 0 {
				m.scroll--
			}
		case "down", "j":
			m.scroll++
		}
	}
	return m, nil
}

func (m dashboardTUIModel) View() string {
	if m.width < 80 || m.height < 20 {
		return fmt.Sprintf("Terminal too small — need at least 80×20 (current: %d×%d)\n", m.width, m.height)
	}
	if m.data == nil {
		return "  Loading…\n"
	}

	sep := tuiStyleDim.Render(strings.Repeat("─", m.width))
	var parts []string

	// 1. Logo header.
	parts = append(parts, m.viewLogo()...)

	// 2. Status bar.
	parts = append(parts, m.viewStatusBar())
	parts = append(parts, sep)

	// 3. AQUEDUCTS section.
	parts = append(parts, tuiStyleHeader.Render("  AQUEDUCTS"))
	parts = append(parts, m.viewAqueducts()...)
	parts = append(parts, sep)

	// 4. CISTERN section.
	parts = append(parts, tuiStyleHeader.Render("  CISTERN"))
	parts = append(parts, m.viewCistern()...)
	parts = append(parts, sep)

	// 5. RECENT FLOW section.
	parts = append(parts, tuiStyleHeader.Render("  RECENT FLOW"))
	parts = append(parts, m.viewRecentFlow()...)
	parts = append(parts, sep)

	// 6. Footer.
	parts = append(parts, tuiStyleFooter.Render("  q quit  r refresh  ↑↓ scroll  ? help"))

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

func (m dashboardTUIModel) viewAqueducts() []string {
	if len(m.data.Cataractae) == 0 {
		return []string{tuiStyleDim.Render("  No aqueducts configured")}
	}
	lines := make([]string, 0, len(m.data.Cataractae))
	for _, ch := range m.data.Cataractae {
		lines = append(lines, m.viewAqueductRow(ch))
	}
	return lines
}

func (m dashboardTUIModel) viewAqueductRow(ch CataractaInfo) string {
	name := padRight(ch.Name, 14)
	if ch.DropletID == "" {
		return fmt.Sprintf("  %s%s", name, tuiStyleDim.Render("idle"))
	}
	bar := progressBar(ch.CataractaIndex, ch.TotalCataractae, 8)
	elapsed := formatElapsed(ch.Elapsed)
	dropletID := padRight(ch.DropletID, 10)
	step := "[" + padRight(ch.Step, 20) + "]"
	return fmt.Sprintf("  %s  %s  %s  %-8s  %s",
		tuiStyleGreen.Render(name),
		dropletID,
		tuiStyleDim.Render(step),
		elapsed,
		tuiStyleGreen.Render(bar),
	)
}

func (m dashboardTUIModel) viewCistern() []string {
	items := m.data.CisternItems
	if len(items) == 0 {
		return []string{tuiStyleDim.Render("  Cistern dry.")}
	}

	// Clamp scroll.
	maxScroll := len(items) - 1
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}

	// Show up to 1/3 of terminal height, minimum 3 rows.
	maxVisible := m.height / 3
	if maxVisible < 3 {
		maxVisible = 3
	}

	start := m.scroll
	end := start + maxVisible
	if end > len(items) {
		end = len(items)
	}

	lines := make([]string, 0, maxVisible+1)
	for _, item := range items[start:end] {
		lines = append(lines, m.viewCisternRow(item))
	}
	if len(items) > maxVisible {
		extra := len(items) - (end - start)
		if extra > 0 {
			lines = append(lines, tuiStyleDim.Render(fmt.Sprintf("  … %d more  (↑↓ to scroll)", extra)))
		}
	}
	return lines
}

func (m dashboardTUIModel) viewCisternRow(item *cistern.Droplet) string {
	step := item.CurrentCataracta
	if step == "" {
		step = "—"
	}

	var statusStyled string
	switch item.Status {
	case "in_progress":
		statusStyled = tuiStyleGreen.Render(padRight(displayStatus(item.Status), 10))
	case "open":
		statusStyled = tuiStyleYellow.Render(padRight(displayStatus(item.Status), 10))
	case "stagnant":
		statusStyled = tuiStyleRed.Render(padRight(displayStatus(item.Status), 10))
	default:
		statusStyled = tuiStyleDim.Render(padRight(displayStatus(item.Status), 10))
	}

	// Truncate title to fill remaining terminal width.
	fixedWidth := 2 + 10 + 2 + 10 + 2 + 20 + 2 // "  id  status  step  "
	titleWidth := m.width - fixedWidth
	if titleWidth < 8 {
		titleWidth = 8
	}
	title := item.Title
	r := []rune(title)
	if len(r) > titleWidth {
		title = string(r[:titleWidth-3]) + "..."
	}

	return fmt.Sprintf("  %s  %s  %-20s  %s",
		tuiStyleDim.Render(padRight(item.ID, 10)),
		statusStyled,
		step,
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

	// Truncate title to fit.
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
