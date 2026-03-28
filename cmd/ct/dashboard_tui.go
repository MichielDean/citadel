package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MichielDean/cistern/internal/cistern"
)

// Pixel-art arch mipmaps — pre-rendered ANSI art at four sizes.
// selectArchMipmap picks the level whose width is closest to the available slot.

//go:embed assets/arch_mipmaps/arch_100x38.ansi
var archMipmap100x38 string

//go:embed assets/arch_mipmaps/arch_80x30.ansi
var archMipmap80x30 string

//go:embed assets/arch_mipmaps/arch_60x22.ansi
var archMipmap60x22 string

//go:embed assets/arch_mipmaps/arch_36x12.ansi
var archMipmap36x12 string

// archMipmapStripper removes chafa's cursor-visibility escape sequences
// (\x1b[?25l hide-cursor and \x1b[?25h show-cursor) from embedded mipmap files.
// These sequences are terminal control signals, not visual content; bubbletea
// manages cursor visibility independently.
var archMipmapStripper = strings.NewReplacer("\x1b[?25l", "", "\x1b[?25h", "")

// archMipmapWidth returns the pixel column width of the mipmap level chosen for
// the given slot width. The arch is rendered per-slot (one pillar column), so
// the slot width is archPillarW, not the full terminal width.
func archMipmapWidth(slotWidth int) int {
	if slotWidth >= 90 {
		return 100
	}
	if slotWidth >= 70 {
		return 80
	}
	if slotWidth >= 50 {
		return 60
	}
	return 36
}

// selectArchMipmap returns the ANSI arch mipmap whose width best fits slotWidth,
// with cursor-control sequences stripped. The arch is centered over a single
// pillar slot (archPillarW wide), so slotWidth should be archPillarW, not the
// full terminal width.
//   - slot >= 90  → 100x38 mipmap
//   - slot >= 70  → 80x30 mipmap
//   - slot >= 50  → 60x22 mipmap
//   - slot < 50   → 36x12 mipmap
func selectArchMipmap(slotWidth int) string {
	var raw string
	switch archMipmapWidth(slotWidth) {
	case 100:
		raw = archMipmap100x38
	case 80:
		raw = archMipmap80x30
	case 60:
		raw = archMipmap60x22
	default:
		raw = archMipmap36x12
	}
	return archMipmapStripper.Replace(raw)
}

// insideTmux reports whether the process is running inside a tmux session.
// Replaced in tests to control environment without requiring a real tmux session.
var insideTmux = func() bool {
	return os.Getenv("TMUX") != ""
}

// tmuxNewWindowFunc opens a new tmux window attaching read-only to the named session.
// Replaced in tests to capture the call without running tmux.
var tmuxNewWindowFunc = func(dropletID, session string) error {
	return exec.Command("tmux", "new-window", "-n", "peek:"+dropletID, "--", "tmux", "attach-session", "-t", session, "-r").Run()
}

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

// tuiPeekNewWindowErrMsg is sent when tmuxNewWindowFunc fails, triggering a
// fallback to the inline capture-pane overlay.
type tuiPeekNewWindowErrMsg struct {
	ch  CataractaeInfo
	err error
}

const animInterval = 150 * time.Millisecond // water animation speed

// --- Model ---

type dashboardTUIModel struct {
	cfgPath         string
	dbPath          string
	data            *DashboardData
	frame           int    // animation frame counter — increments every animInterval
	scrollY         int    // scroll offset in lines (0 = top)
	width           int
	height          int
	peekActive      bool
	peek            peekModel
	peekSelectMode  bool   // picker overlay active when multiple aqueducts are flowing
	peekSelectIndex int    // index of highlighted aqueduct in the picker
	stateHash       string // fingerprint of last data; "" = first poll (never triggers idle)
	idleMode        bool   // true = backing off to idleRefreshInterval between polls
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
	// The tick chain is self-sustaining:
	// fetchDataCmd → tuiDataMsg → tuiTickWithInterval → tuiTickMsg → fetchDataCmd → …
	// tuiDataMsg chooses the interval (fast or slow) based on idleMode.
	return tea.Batch(m.fetchDataCmd(), tuiAnimTick())
}

// tuiTickWithInterval schedules a single data-refresh tick after d.
// The interval is chosen by the tuiDataMsg handler based on idle state.
func tuiTickWithInterval(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
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

// applyDataMsg updates model fields for new data and returns the interval-aware
// tick command for the next refresh.
func (m dashboardTUIModel) applyDataMsg(msg tuiDataMsg) (dashboardTUIModel, tea.Cmd) {
	prev := m.stateHash
	m.data = (*DashboardData)(msg)
	newHash := dashboardStateHash(m.data)
	m.idleMode = newHash == prev && prev != "" && m.data.FlowingCount == 0
	m.stateHash = newHash
	interval := refreshInterval
	if m.idleMode {
		interval = idleRefreshInterval
	}
	return m, tuiTickWithInterval(interval)
}

// activeAqueducts returns the subset of cataractae that have a flowing droplet.
func activeAqueducts(cataractae []CataractaeInfo) []CataractaeInfo {
	var active []CataractaeInfo
	for _, ch := range cataractae {
		if ch.DropletID != "" {
			active = append(active, ch)
		}
	}
	return active
}

// openPeekOn transitions to peek mode for the given aqueduct, returning the
// updated model and a tea.Cmd to execute. When running inside a tmux session,
// a new tmux window is opened for live attach and the dashboard continues
// running undisturbed. When not inside tmux, the inline capture-pane overlay
// is used as a fallback.
func (m dashboardTUIModel) openPeekOn(ch CataractaeInfo) (dashboardTUIModel, tea.Cmd) {
	session := ch.RepoName + "-" + ch.Name

	if insideTmux() {
		m.peekSelectMode = false
		// Spawn a new tmux window for live read-only attach; dashboard stays open.
		return m, func() tea.Msg {
			if err := tmuxNewWindowFunc(ch.DropletID, session); err != nil {
				return tuiPeekNewWindowErrMsg{ch: ch, err: err}
			}
			return nil
		}
	}

	// Not inside tmux: fall back to inline capture-pane peek overlay.
	return m.openInlinePeek(ch, nil)
}

// openInlinePeek sets up the inline capture-pane overlay for the given aqueduct.
// If err is non-nil, the header notes the tmux failure instead of the "not inside tmux" hint.
func (m dashboardTUIModel) openInlinePeek(ch CataractaeInfo, err error) (dashboardTUIModel, tea.Cmd) {
	session := ch.RepoName + "-" + ch.Name
	var header string
	if err != nil {
		header = fmt.Sprintf("[%s] %s — flowing %s\ntmux new-window failed (%v) — showing capture-pane snapshot",
			ch.DropletID, ch.Step, formatElapsed(ch.Elapsed), err)
	} else {
		header = fmt.Sprintf("[%s] %s — flowing %s\nnot inside tmux — for live view: tmux attach-session -t %s -r",
			ch.DropletID, ch.Step, formatElapsed(ch.Elapsed), session)
	}
	pk := newPeekModel(defaultCapturer, session, header, 0)
	pk.width = m.width
	pk.height = m.height
	m.peek = pk
	m.peekActive = true
	m.peekSelectMode = false
	return m, m.peek.Init()
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
			case "q", "esc", "ctrl+c":
				// ctrl+c is treated as "close peek" rather than "quit program"
				// because in a web PTY context the browser may send ctrl+c (the
				// copy shortcut, or as part of a terminal capability response)
				// when the peek overlay opens.  Quitting the TUI on ctrl+c
				// while peek is open causes a disconnect/reconnect loop in the
				// web dashboard.  The user can still quit via ctrl+c from the
				// main dashboard view (where peek is not active).
				m.peekActive = false
				return m, nil
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
			return m, m.fetchDataCmd()
		case tuiAnimMsg:
			m.frame++
			return m, tuiAnimTick()
		case tuiDataMsg:
			return m.applyDataMsg(msg)
		}
		return m, nil
	}

	// When the picker overlay is active, handle only picker navigation.
	if m.peekSelectMode {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if m.data == nil {
				m.peekSelectMode = false
				return m, nil
			}
			active := activeAqueducts(m.data.Cataractae)
			switch msg.String() {
			case "ctrl+c", "q", "esc":
				// ctrl+c cancels the picker for the same reason it cancels the
				// peek overlay: to prevent accidental quit via browser ctrl+c.
				m.peekSelectMode = false
				return m, nil
			case "up", "k":
				if m.peekSelectIndex > 0 {
					m.peekSelectIndex--
				}
			case "down", "j":
				if m.peekSelectIndex < len(active)-1 {
					m.peekSelectIndex++
				}
			case "enter":
				if m.peekSelectIndex < len(active) {
					return m.openPeekOn(active[m.peekSelectIndex])
				}
			}
			return m, nil
		case tea.WindowSizeMsg:
			m.width = msg.Width
			m.height = msg.Height
			return m, nil
		case tuiTickMsg:
			return m, m.fetchDataCmd()
		case tuiAnimMsg:
			m.frame++
			return m, tuiAnimTick()
		case tuiDataMsg:
			var cmd tea.Cmd
			m, cmd = m.applyDataMsg(msg)
			active := activeAqueducts(m.data.Cataractae)
			if len(active) == 0 {
				m.peekSelectMode = false
			} else if m.peekSelectIndex >= len(active) {
				m.peekSelectIndex = len(active) - 1
			}
			return m, cmd
		case tuiPeekNewWindowErrMsg:
			return m.openInlinePeek(msg.ch, msg.err)
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tuiTickMsg:
		return m, m.fetchDataCmd()

	case tuiAnimMsg:
		m.frame++
		return m, tuiAnimTick()

	case tuiDataMsg:
		return m.applyDataMsg(msg)

	case tuiPeekNewWindowErrMsg:
		return m.openInlinePeek(msg.ch, msg.err)

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		case "r", "R":
			return m, m.fetchDataCmd()
		case "p", "enter":
			// One active aqueduct: open peek directly.
			// Multiple active aqueducts: show picker so user can choose.
			if m.data != nil {
				active := activeAqueducts(m.data.Cataractae)
				switch {
				case len(active) == 1:
					return m.openPeekOn(active[0])
				case len(active) > 1:
					m.peekSelectMode = true
					m.peekSelectIndex = 0
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
	if m.peekSelectMode {
		return m.viewPeekSelectOverlay()
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

// viewAqueductArches renders the dashboard aqueduct section.
//
// Active aqueducts (flowing droplet): full arch diagram each, stacked vertically.
// Drought (all idle): one centered arch with "drought" label above it.
// In both cases, a compact list of all aqueduct names and their status is shown below.
func (m dashboardTUIModel) viewAqueductArches() []string {
	if len(m.data.Cataractae) == 0 {
		return []string{tuiStyleDim.Render("  No aqueducts configured")}
	}

	active := activeAqueducts(m.data.Cataractae)
	var lines []string

	if len(active) == 0 {
		// Drought: one centered arch, no water animation.
		lines = append(lines, m.viewDroughtArch()...)
	} else {
		// Active: one arch per flowing aqueduct, stacked.
		for i, ch := range active {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, m.tuiAqueductRow(ch, m.frame)...)
		}
	}

	// Compact list of all aqueducts below the arch(es).
	lines = append(lines, "")
	for _, ch := range m.data.Cataractae {
		lines = append(lines, m.viewIdleAqueductRow(ch))
	}

	return lines
}

// viewDroughtArch renders a single centered arch for the drought state.
func (m dashboardTUIModel) viewDroughtArch() []string {
	mipmap := selectArchMipmap(archPillarW)
	mipmapLines := strings.Split(strings.TrimRight(mipmap, "\n"), "\n")

	leftPad := (m.width - archPillarW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	indent := strings.Repeat(" ", leftPad)

	droughtLabel := tuiStyleDim.Render(tuiPadCenter("drought", m.width))
	lines := make([]string, 0, len(mipmapLines)+1)
	lines = append(lines, droughtLabel)
	for _, line := range mipmapLines {
		lines = append(lines, indent+line)
	}
	return lines
}

// viewIdleAqueductRow renders a single aqueduct as a compact status line.
func (m dashboardTUIModel) viewIdleAqueductRow(ch CataractaeInfo) string {
	const nameW = 12
	const repoW = 18
	name := padRight(ch.Name, nameW)
	repo := padRight(ch.RepoName, repoW)
	status := "·  idle"
	if ch.DropletID != "" {
		status = tuiStyleGreen.Render("▶  " + ch.Step)
	}
	return fmt.Sprintf("  %s  %s  %s",
		tuiStyleDim.Render(name),
		tuiStyleDim.Render(repo),
		tuiStyleDim.Render(status),
	)
}

// viewPeekSelectOverlay renders a centered picker overlay listing every active aqueduct.
// The user navigates with Up/Down, confirms with Enter, and cancels with Esc or q.
func (m dashboardTUIModel) viewPeekSelectOverlay() string {
	if m.data == nil {
		return "  Loading…\n"
	}
	active := activeAqueducts(m.data.Cataractae)

	const lineW = 60
	divider := strings.Repeat("─", lineW)

	var rows []string
	rows = append(rows, tuiStyleHeader.Render("  select aqueduct"))
	rows = append(rows, tuiStyleDim.Render(divider))
	for i, ch := range active {
		line := fmt.Sprintf("  %-12s  %-12s  %-12s  %s",
			ch.Name, ch.RepoName, ch.DropletID, ch.Step)
		if i == m.peekSelectIndex {
			rows = append(rows, tuiStyleGreen.Render(line))
		} else {
			rows = append(rows, line)
		}
	}
	rows = append(rows, tuiStyleDim.Render(divider))
	rows = append(rows, tuiStyleDim.Render("  ↑↓ navigate  enter connect  esc cancel"))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#46465a")).
		Padding(0, 1)

	box := boxStyle.Render(strings.Join(rows, "\n"))

	w := m.width
	h := m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

// ansiTruncVisual truncates s to at most width visible rune-columns while
// preserving all ANSI escape sequences (CSI: \x1b[…m) within the retained
// portion. A reset (\x1b[0m) is appended when ANSI codes were present so that
// subsequent styled output (e.g. wfExit) starts from a clean colour state.
func ansiTruncVisual(s string, width int) string {
	var sb strings.Builder
	visual := 0
	hasANSI := false
	runes := []rune(s)
	i := 0
	for i < len(runes) && visual < width {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			// CSI escape sequence: \x1b[ params final-byte (0x40–0x7E).
			hasANSI = true
			sb.WriteRune(runes[i]) // \x1b
			i++
			sb.WriteRune(runes[i]) // [
			i++
			for i < len(runes) {
				sb.WriteRune(runes[i])
				if runes[i] >= 0x40 && runes[i] <= 0x7E {
					i++
					break
				}
				i++
			}
			continue
		}
		sb.WriteRune(runes[i])
		visual++
		i++
	}
	if hasANSI {
		sb.WriteString("\x1b[0m")
	}
	return sb.String()
}

// animateTroughLine post-processes a single ANSI-encoded mipmap row for water
// animation. For each visible (non-space) character in the row it substitutes a
// cycling wave character from the pattern (░▒▓≈▒░) based on column position and
// frame. Space characters are preserved so arch walls or blank sky areas remain
// untouched. width is the expected visual column count of the row.
func animateTroughLine(line string, frame, width int) string {
	stripped := []rune(stripANSI(line))

	type waveCell struct {
		ch  string
		sty lipgloss.Style
	}
	const waveViz = 6
	waveCells := [waveViz]waveCell{
		{"░", archRoleWaterDim}, {"▒", archRoleWaterMid}, {"▓", archRoleWaterBright},
		{"≈", archRoleWaterMid}, {"▒", archRoleWaterMid}, {"░", archRoleWaterDim},
	}

	var sb strings.Builder
	for col, r := range stripped {
		if r == ' ' {
			sb.WriteRune(' ')
		} else {
			cell := waveCells[((col-frame)%waveViz+waveViz)%waveViz]
			sb.WriteString(cell.sty.Render(cell.ch))
		}
	}
	// Pad to width if the stripped content was shorter.
	for col := len(stripped); col < width; col++ {
		sb.WriteRune(' ')
	}
	return sb.String()
}

// tuiAqueductRow renders a single aqueduct as a pixel art arch diagram.
// Layout (top to bottom): name → info → step labels → mipmap arch.
// Total lines: 3 header rows + mipmap height (12/22/30/37 depending on terminal width).
//
// Water is animated inside the mipmap trough (top 2 mipmap rows) for active aqueducts.
// Idle aqueducts (no active droplet) show a static mipmap with no water animation.
func (m dashboardTUIModel) tuiAqueductRow(ch CataractaeInfo, frame int) []string {
	const nameW = 10
	// archBlockW matches the per-slot column budget used by viewAqueductArches.
	const archBlockW = archPillarW + 2

	g   := tuiStyleGreen
	dim := tuiStyleDim

	steps := ch.Steps
	if len(steps) == 0 {
		steps = []string{"—"}
	}

	// indent is the shared left padding for info and label rows.
	// Kept minimal (2 chars) so each arch block fits within archBlockW columns,
	// enabling horizontal side-by-side tiling in an 80-column terminal.
	const indent = "  "

	// Name line: aqueduct name + repo name on the same line.
	repoLabel := ch.RepoName
	if len([]rune(repoLabel)) > nameW {
		repoLabel = string([]rune(repoLabel)[:nameW-1]) + "…"
	}
	nameLine := "  " + g.Render(padRight(ch.Name, nameW)) + "  " + dim.Render(repoLabel)

	// Info line: droplet ID, elapsed time, and title — shown only when active.
	var infoLine string
	if ch.DropletID != "" {
		infoBase := ch.DropletID + "  " + formatElapsed(ch.Elapsed)
		// indent visual width: 2 chars (the compact indent).
		const indentW = 2
		titleW := archBlockW - indentW - len([]rune(infoBase)) - 2
		if titleW > 0 && ch.Title != "" {
			title := ch.Title
			if len([]rune(title)) > titleW {
				title = string([]rune(title)[:titleW-1]) + "…"
			}
			infoLine = indent + g.Render(infoBase) + "  " + dim.Render(title)
		} else {
			infoLine = indent + g.Render(infoBase)
		}
	}
	// Find active step index (0-based); -1 if none.
	activeIdx := -1
	for i, step := range steps {
		if step == ch.Step && ch.DropletID != "" {
			activeIdx = i
			break
		}
	}

	// Waterfall is visible only when the droplet is on the final step.
	isLastStep := activeIdx >= 0 && activeIdx == len(steps)-1

	// Mipmap arch: select the mipmap that fits within one pillar slot (archPillarW).
	// The arch is left-aligned at the indent position so that each arch block is
	// exactly indent+mipmapW columns wide, enabling horizontal tiling in viewAqueductArches.
	mipmapW := archMipmapWidth(archPillarW)
	mipmap := selectArchMipmap(archPillarW)
	mipmapLines := strings.Split(strings.TrimRight(mipmap, "\n"), "\n")

	var archLines []string
	for _, line := range mipmapLines {
		archLines = append(archLines, indent+line)
	}

	// For active aqueducts, animate the trough: post-process the top 2 mipmap rows
	// by replacing each visible (non-space) character with a cycling wave character.
	// Space positions (arch walls, blank sky) are preserved unchanged.
	if activeIdx >= 0 && len(archLines) >= 2 {
		archLines[0] = indent + animateTroughLine(mipmapLines[0], frame, mipmapW)
		archLines[1] = indent + animateTroughLine(mipmapLines[1], frame, mipmapW)
	}

	// Waterfall exit: replace the last wfW visual chars of the last mipmap row with
	// wfExit, keeping the arch line within archBlockW.
	// ansiTruncVisual preserves 24-bit pixel-art colours in the retained prefix.
	if isLastStep && len(archLines) > 0 {
		const wfW = 4 // visual width of wfExit (░▒▓▓)
		const trimTo = archBlockW - wfW
		// Waterfall brightness rotates with frame so ▓ appears to fall.
		wfAccent := []lipgloss.Style{archRoleWaterBright, archRoleWaterMid, archRoleWaterDim}[frame%3]
		wfExit := archRoleWaterDim.Render("░") + archRoleWaterMid.Render("▒") + wfAccent.Render("▓▓")
		archLines[len(archLines)-1] = ansiTruncVisual(archLines[len(archLines)-1], trimTo) + wfExit
	}

	// Label line: full pipeline shown as "step1 → step2 → step3 → …"
	// Active step is bold+green; others are dim. Truncated to terminal width.
	var lblSB strings.Builder
	lblSB.WriteString(indent)
	effectiveWidth := m.width
	if effectiveWidth <= 0 {
		effectiveWidth = 80
	}
	maxLblW := effectiveWidth - len([]rune(indent))
	usedW := 0
	for i, step := range steps {
		sep := ""
		if i > 0 {
			sep = " → "
		}
		part := sep + step
		partW := len([]rune(part))
		if usedW+partW > maxLblW {
			lblSB.WriteString(dim.Render("…"))
			break
		}
		if i == activeIdx {
			if i > 0 {
				lblSB.WriteString(dim.Render(" → "))
			}
			lblSB.WriteString(g.Bold(true).Render(step))
		} else {
			lblSB.WriteString(dim.Render(part))
		}
		usedW += partW
	}
	lblLine := lblSB.String()

	// Return order: name → info → label → mipmap arch rows (with animated water trough for active).
	result := []string{nameLine, infoLine, lblLine}
	result = append(result, archLines...)
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
		elapsed := formatElapsed(ch.Elapsed)
		infoLine = strings.Repeat(" ", activeVisualCol) +
			tuiStyleDim.Render("↑ ") +
			tuiStyleGreen.Render(ch.Name) +
			tuiStyleDim.Render(" · "+ch.DropletID) +
			"  " + elapsed +
			"  " + tuiStyleGreen.Render(bar)
		if ch.Title != "" {
			// Visual width of the non-ANSI content before the title.
			usedW := activeVisualCol + 2 + len([]rune(ch.Name)) + 3 + len([]rune(ch.DropletID)) +
				2 + len([]rune(elapsed)) + 2 + 8
			titleW := m.width - usedW - 2
			if titleW > 0 {
				title := ch.Title
				if len([]rune(title)) > titleW {
					title = string([]rune(title)[:titleW-1]) + "…"
				}
				infoLine += "  " + tuiStyleDim.Render(title)
			}
		}
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
