package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

const (
	statusPanelRefreshInterval     = 5 * time.Second
	statusPanelIdleRefreshInterval = 30 * time.Second
)

// statusDataMsg carries fresh DashboardData for the status panel.
// Distinct from tuiDataMsg so the status panel does not respond to
// the dashboard panel's data fetches and vice versa.
type statusDataMsg *DashboardData

// statusTickMsg fires the status panel's periodic refresh timer.
// Distinct from tuiTickMsg for the same reason.
type statusTickMsg time.Time

// Compile-time interface check.
var _ TUIPanel = statusPanel{}

// statusPanel is the Status cockpit module (key: 3).
// It renders ct status output: cistern counts, aqueduct flow summary,
// and castellarius health. Auto-refreshes on a 5-second ticker with
// idle backoff following the dashboardTUIModel pattern. r force-refreshes.
type statusPanel struct {
	cfgPath   string
	dbPath    string
	data      *DashboardData
	width     int
	height    int
	scrollY   int
	stateHash string // fingerprint of last data; "" = first poll (never triggers idle)
	idleMode  bool   // true = backing off to statusPanelIdleRefreshInterval
}

func newStatusPanel(cfgPath, dbPath string) statusPanel {
	return statusPanel{
		cfgPath: cfgPath,
		dbPath:  dbPath,
		width:   100,
		height:  24,
	}
}

func (p statusPanel) Init() tea.Cmd {
	return p.fetchDataCmd()
}

func (p statusPanel) fetchDataCmd() tea.Cmd {
	cfgPath, dbPath := p.cfgPath, p.dbPath
	return func() tea.Msg {
		return statusDataMsg(fetchDashboardData(cfgPath, dbPath))
	}
}

func statusTickWithInterval(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return statusTickMsg(t)
	})
}

// applyDataMsg updates model fields for new data and returns the interval-aware
// tick command for the next refresh, following the dashboardTUIModel pattern.
func (p statusPanel) applyDataMsg(msg statusDataMsg) (statusPanel, tea.Cmd) {
	prev := p.stateHash
	p.data = (*DashboardData)(msg)
	newHash := dashboardStateHash(p.data)
	p.idleMode = newHash == prev && prev != "" && p.data.FlowingCount == 0
	p.stateHash = newHash
	interval := statusPanelRefreshInterval
	if p.idleMode {
		interval = statusPanelIdleRefreshInterval
	}
	return p, statusTickWithInterval(interval)
}

func (p statusPanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height

	case statusTickMsg:
		return p, p.fetchDataCmd()

	case statusDataMsg:
		return p.applyDataMsg(msg)

	case tea.KeyMsg:
		switch msg.String() {
		case "r", "R":
			return p, p.fetchDataCmd()
		case "up", "k":
			if p.scrollY > 0 {
				p.scrollY--
			}
		case "down", "j":
			p.scrollY++
		case "home", "g":
			p.scrollY = 0
		case "end", "G":
			p.scrollY = 999999
		}
	}
	return p, nil
}

func (p statusPanel) View() string {
	if p.data == nil {
		return "  Loading…\n"
	}

	var lines []string

	// ── Cistern summary ──────────────────────────────────────────────────────
	lines = append(lines, "")
	summary := fmt.Sprintf("  %s flowing  ·  %s queued  ·  %s delivered",
		tuiStyleGreen.Render(fmt.Sprintf("%d", p.data.FlowingCount)),
		tuiStyleYellow.Render(fmt.Sprintf("%d", p.data.QueuedCount)),
		tuiStyleDim.Render(fmt.Sprintf("%d", p.data.DoneCount)))
	lines = append(lines, summary, "")

	// ── Human-gated notice ───────────────────────────────────────────────────
	var humanGated []*cistern.Droplet
	for _, d := range p.data.PooledItems {
		if d.CurrentCataractae == "human" {
			humanGated = append(humanGated, d)
		}
	}
	if len(humanGated) > 0 {
		ids := make([]string, len(humanGated))
		for i, d := range humanGated {
			ids[i] = d.ID
		}
		lines = append(lines,
			tuiStyleYellow.Render(fmt.Sprintf("  ⏸  %d droplet(s) awaiting human approval: %s",
				len(humanGated), strings.Join(ids, ", "))),
			"")
	}

	// ── Castellarius health ──────────────────────────────────────────────────
	farmStatus := tuiStyleRed.Render("stopped")
	if p.data.FarmRunning {
		farmStatus = tuiStyleGreen.Render("watching")
	}
	lines = append(lines, "  Castellarius  "+farmStatus, "")

	// ── Aqueduct flow summary ────────────────────────────────────────────────
	if len(p.data.Cataractae) > 0 {
		// Compute max name width for consistent column alignment.
		maxNameW := 8
		for _, ch := range p.data.Cataractae {
			maxNameW = max(maxNameW, len(ch.Name))
		}

		for _, ch := range p.data.Cataractae {
			name := fmt.Sprintf("%-*s", maxNameW, ch.Name)
			if ch.DropletID != "" {
				elapsed := formatElapsed(ch.Elapsed)
				var progress string
				if ch.CataractaeIndex > 0 && ch.TotalCataractae > 0 {
					progress = fmt.Sprintf("%s [%d/%d]", ch.Step, ch.CataractaeIndex, ch.TotalCataractae)
				} else {
					progress = ch.Step
				}
				row := fmt.Sprintf("  %s  →  %-16s  %-24s  %s",
					name, ch.DropletID, progress, elapsed)
				lines = append(lines, tuiStyleGreen.Render(row))
			} else {
				row := fmt.Sprintf("  %s  →  idle", name)
				lines = append(lines, tuiStyleDim.Render(row))
			}
		}
		lines = append(lines, "")
	}

	// ── Footer ───────────────────────────────────────────────────────────────
	if !p.data.FetchedAt.IsZero() {
		age := time.Since(p.data.FetchedAt).Round(time.Second)
		modeStr := ""
		if p.idleMode {
			modeStr = "  idle"
		}
		lines = append(lines, tuiStyleDim.Render(fmt.Sprintf(
			"  refreshed %s ago%s  ·  r to force-refresh", formatElapsed(age), modeStr)))
	}

	// ── Scroll ───────────────────────────────────────────────────────────────
	total := len(lines)
	viewH := max(1, p.height-1)
	top := min(p.scrollY, max(0, total-viewH))
	end := min(top+viewH, total)
	return strings.Join(lines[top:end], "\n")
}

func (p statusPanel) Title() string { return "Status" }

func (p statusPanel) KeyHelp() string { return "r refresh  ↑↓/jk scroll  g/G top/bottom" }

func (p statusPanel) OverlayActive() bool { return false }

func (p statusPanel) SelectedDroplet() *cistern.Droplet { return nil }

func (p statusPanel) PaletteActions(_ *cistern.Droplet) []PaletteAction { return nil }
