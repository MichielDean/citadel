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

// Pixel-art arch mipmap — pre-rendered ANSI art at 20×7.
// This is the single arch image used for all aqueduct rows.

//go:embed assets/arch_mipmaps/arch_20x7.ansi
var archMipmap20x7 string

// archMipmapStripper removes chafa's cursor-visibility escape sequences
// (\x1b[?25l hide-cursor and \x1b[?25h show-cursor) from embedded mipmap files.
// These sequences are terminal control signals, not visual content; bubbletea
// manages cursor visibility independently.
var archMipmapStripper = strings.NewReplacer("\x1b[?25l", "", "\x1b[?25h", "")

// archMipmapWidth returns the pixel column width of the arch mipmap.
func archMipmapWidth(_ int) int { return 20 }

// selectArchMipmap returns the ANSI arch mipmap with cursor-control sequences stripped.
func selectArchMipmap(_ int) string {
	return archMipmapStripper.Replace(archMipmap20x7)
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
	tuiStyleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a8a9a"))
	tuiStyleHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9db1db"))
	tuiStyleFooter = lipgloss.NewStyle()
)

// --- Messages ---

type tuiTickMsg time.Time
type tuiAnimMsg time.Time
type tuiDataMsg *DashboardData

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
	frame           int // animation frame counter — increments every animInterval
	scrollY         int // scroll offset in lines (0 = top)
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
			if m.scrollY < 0 {
				m.scrollY = 0
			}
		case "pgdown", "ctrl+d":
			m.scrollY += m.height / 2
		}

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.scrollY > 0 {
				m.scrollY -= 3
			}
			if m.scrollY < 0 {
				m.scrollY = 0
			}
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

	sep := strings.Repeat("─", m.width)
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

	// 5. Pooled droplets — dedicated panel, always visible.
	parts = append(parts, tuiStyleHeader.Render("  POOLED"))
	parts = append(parts, m.viewPooled()...)
	parts = append(parts, sep)

	// 6. Recent flow.
	parts = append(parts, tuiStyleHeader.Render("  RECENT FLOW"))
	parts = append(parts, m.viewRecentFlow()...)
	parts = append(parts, sep)

	// 7. Footer — always pinned at the bottom (not scrolled).
	footer := tuiStyleFooter.Render("  q quit  r refresh  ↑↓/jk scroll  g/G top/bottom  p peek")

	// Apply scroll: render full content, slice visible window.
	full := strings.Join(parts, "\n")
	lines := strings.Split(full, "\n")
	total := len(lines)
	// Reserve 1 line for the pinned footer.
	viewH := m.height - 1
	if viewH < 1 {
		viewH = 1
	}
	maxScroll := total - viewH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scrollY > maxScroll {
		m.scrollY = maxScroll
	}
	end := m.scrollY + viewH
	if end > total {
		end = total
	}
	visible := lines[m.scrollY:end]
	return strings.Join(visible, "\n") + "\n" + footer
}

// cisternLogoLines are the raw block-character logo lines for CISTERN.
// Rendered with a left→right water gradient (deep teal → bright cyan).
var cisternLogoLines = []string{
	` ██████╗ ██╗███████╗████████╗███████╗██████╗ ███╗   ██╗`,
	`██╔════╝ ██║██╔════╝╚══██╔══╝██╔════╝██╔══██╗████╗  ██║`,
	`██║      ██║███████╗   ██║   █████╗  ██████╔╝██╔██╗ ██║`,
	`██║      ██║╚════██║   ██║   ██╔══╝  ██╔══██╗██║╚██╗██║`,
	`╚██████╗ ██║███████║   ██║   ███████╗██║  ██║██║ ╚████║`,
	` ╚═════╝ ╚═╝╚══════╝   ╚═╝   ╚══════╝╚═╝  ╚═╝╚═╝  ╚═══╝`,
}

func (m dashboardTUIModel) viewLogo() []string {
	const colorA = "#0d5a72"
	const colorB = "#c8f4ff"
	var lines []string
	for _, line := range cisternLogoLines {
		runes := []rune(line)
		n := len(runes)
		var sb strings.Builder
		for i, r := range runes {
			t := float64(i) / float64(n)
			color := interpolateHex(colorA, colorB, t)
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(string(r)))
		}
		lines = append(lines, tuiPadCenter(sb.String(), m.width))
	}
	return lines
}

func (m dashboardTUIModel) viewStatusBar() string {
	d := m.data
	flowing := tuiStyleGreen.Render(fmt.Sprintf("● %d flowing", d.FlowingCount))
	queued := tuiStyleYellow.Render(fmt.Sprintf("○ %d queued", d.QueuedCount))
	done := tuiStyleGreen.Render(fmt.Sprintf("✓ %d delivered", d.DoneCount))
	ts := "— last update " + d.FetchedAt.Format("15:04:05")
	return fmt.Sprintf("  %s  %s  %s  %s", flowing, queued, done, ts)
}

// viewAqueductArches renders the dashboard aqueduct section.
// Active aqueducts first (with blank line between each), then all idle ones below.
func (m dashboardTUIModel) viewAqueductArches() []string {
	if len(m.data.Cataractae) == 0 {
		return []string{"  No aqueducts configured"}
	}

	var active, idle []CataractaeInfo
	for _, ch := range m.data.Cataractae {
		if ch.DropletID != "" {
			active = append(active, ch)
		} else {
			idle = append(idle, ch)
		}
	}

	var lines []string

	// Active aqueducts: progress bar followed immediately by inline flow notes,
	// with a blank line between each aqueduct unit for breathing room.
	for i, ch := range active {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, m.viewAqueductProgress(ch))
		lines = append(lines, m.viewInlineFlowNotes(ch)...)
	}

	// Blank separator between active and idle sections.
	if len(active) > 0 && len(idle) > 0 {
		lines = append(lines, "")
	}

	// Idle aqueducts: compact single-line rows at the bottom.
	for _, ch := range idle {
		lines = append(lines, m.viewIdleAqueductRow(ch))
	}

	return lines
}

// viewAqueductRow renders a single unified row for an aqueduct.
// Active: name + elapsed + segmented bar (one segment per cataracta)
// Idle:   name + repo + "· idle"
func (m dashboardTUIModel) viewAqueductRow(ch CataractaeInfo) string {
	if ch.DropletID == "" {
		return m.viewIdleAqueductRow(ch)
	}
	return m.viewAqueductProgress(ch)
}

// viewAqueductProgress renders an active aqueduct as a two-row segmented bar.
//
// The gate character is always ][ (2 chars). Its position conveys state:
//
//	RAISED (cataracta passed): ][ appears on the TOP row; bottom row is seamless █ fill.
//	CLOSED (not yet reached):  ][ appears on the BOTTOM row in-channel; top row is blank.
//
// Example — implement done, review active, deliver pending:
//
//	virgo  ci-abc12  3m
//	implement      review       deliver
//	             ][                       ← top: implement gate raised
//	│██████████████│░░░░░░░░░][░░░░░│    ← bottom: deliver gate closed
func (m dashboardTUIModel) viewAqueductProgress(ch CataractaeInfo) string {
	g := tuiStyleGreen

	steps := ch.Steps
	if len(steps) == 0 {
		steps = []string{"—"}
	}
	n := len(steps)

	indent := "  "
	avail := m.width - len([]rune(indent))
	if avail < 20 {
		avail = 20
	}

	// Gate is always ][ — 2 chars wide.
	const gateW = 2

	// Total width: n*segW + (n-1)*gateW = avail
	segW := (avail - gateW*(n-1)) / n
	if segW < 3 {
		segW = 3
	}

	// Active step index (0-based).
	activeIdx := -1
	for i, s := range steps {
		if s == ch.Step && ch.DropletID != "" {
			activeIdx = i
			break
		}
	}

	// Water colors.
	const (
		waterFull = "#1a8fa8"
		waterDark = "#0a3545"
	)
	waterGradA := "#1a7a96"
	waterGradB := "#a8eeff"

	gateStyle := tuiStyleDim // ][ color

	wallStyle := func(i int) lipgloss.Style {
		if i < activeIdx {
			return lipgloss.NewStyle().Foreground(lipgloss.Color(waterFull))
		} else if i == activeIdx {
			return lipgloss.NewStyle().Foreground(lipgloss.Color(waterGradB))
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color(waterDark))
	}

	// Label row: step names centered over their segments.
	// The active step also shows the elapsed time: "review 3m"
	elapsed := formatElapsed(ch.Elapsed)
	var lblRow strings.Builder
	lblRow.WriteString(indent)
	for i, s := range steps {
		if i > 0 {
			lblRow.WriteString(strings.Repeat(" ", gateW))
		}
		lbl := s
		if i == activeIdx && elapsed != "" {
			lbl = s + " " + elapsed
		}
		if len([]rune(lbl)) > segW {
			lbl = string([]rune(lbl)[:segW-1]) + "…"
		}
		centered := padOrTruncCenter(lbl, segW)
		if i == activeIdx {
			lblRow.WriteString(g.Bold(true).Render(centered))
		} else if i < activeIdx {
			lblRow.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(waterFull)).Render(centered))
		} else {
			lblRow.WriteString(centered)
		}
	}

	// Build top and bottom rows simultaneously.
	//
	// topRow: ][ at raised gate positions, spaces everywhere else.
	// botRow: the channel. Raised gates leave NO trace — bars connect seamlessly.
	//         Closed gates: ][ sits in the channel as the boundary.
	//
	// Segment width on the bottom row is always segW (no wall chars consumed by raised gates).
	// Walls (│) only appear at the outermost edges and around closed gates.
	var topRow, botRow strings.Builder
	topRow.WriteString(indent)
	botRow.WriteString(indent)

	for i := range steps {
		if i > 0 {
			raised := (i - 1) < activeIdx
			if raised {
				// Raised: ][ lifts to top row; bottom still needs gateW chars of
				// fill so the two rows stay column-aligned.
				topRow.WriteString(gateStyle.Render("]["))
				botRow.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(waterFull)).Render(strings.Repeat("█", gateW)))
			} else {
				// Closed: nothing on top, ][ in the channel.
				topRow.WriteString("  ")
				botRow.WriteString(gateStyle.Render("]["))
			}
		}

		// Top row: spaces across the full segment width.
		topRow.WriteString(strings.Repeat(" ", segW))

		// Left │ only at the very start of the bar (i==0).
		// After a raised gate: no wall (seamless fill).
		// After a closed gate: ][ IS the boundary — no additional │.
		rightRaised := i < n-1 && i < activeIdx

		if i == 0 {
			botRow.WriteString(wallStyle(i).Render("│"))
		}

		// Fill width: segW minus left │ (only at i==0) minus right │ (when not right-raised).
		innerW := segW
		if i == 0 {
			innerW-- // left wall at start
		}
		if !rightRaised {
			innerW--
		}
		if innerW < 0 {
			innerW = 0
		}

		for j := 0; j < innerW; j++ {
			if i < activeIdx {
				botRow.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(waterFull)).Render("█"))
			} else if i == activeIdx {
				filled := innerW
				halfFilled := (filled * (m.frame % (filled + 1))) / filled
				if j < halfFilled {
					t := float64(j) / float64(filled)
					color := interpolateHex(waterGradA, waterGradB, t)
					botRow.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("█"))
				} else if j == halfFilled {
					edge := []string{"░", "▒", "▓"}[m.frame%3]
					botRow.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(waterGradA)).Render(edge))
				} else {
					botRow.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(waterDark)).Render("░"))
				}
			} else {
				botRow.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(waterDark)).Render("░"))
			}
		}

		// Right │ only at the very end of the bar.
		// Closed gate ahead: ][ of next iteration is the boundary — no │ here.
		// Raised gate ahead: seamless — no │.
		if i == n-1 {
			botRow.WriteString(wallStyle(i).Render("│"))
		}
	}

	const nameW = 10
	name := g.Render(padRight(ch.Name, nameW))
	header := fmt.Sprintf("%s%s  %s", indent, name, ch.DropletID)

	return header + "\n" + lblRow.String() + "\n" + topRow.String() + "\n" + botRow.String()
}

// pipelineLabel returns the pipeline steps as "step1 → step2 → ..." with the
// active step bold+green, truncated to maxW visual chars.
func pipelineLabel(ch CataractaeInfo, maxW int, g, dim lipgloss.Style) string {
	var sb strings.Builder
	used := 0
	for i, s := range ch.Steps {
		sep := ""
		if i > 0 {
			sep = " → "
		}
		partW := len([]rune(sep)) + len([]rune(s))
		if used+partW > maxW {
			sb.WriteString("…")
			break
		}
		if i > 0 {
			sb.WriteString(sep)
		}
		if s == ch.Step && ch.DropletID != "" {
			sb.WriteString(g.Bold(true).Render(s))
		} else {
			sb.WriteString(s)
		}
		used += partW
	}
	return sb.String()
}

// interpolateHex linearly interpolates between two hex colors (#rrggbb) at t in [0,1].
func interpolateHex(a, b string, t float64) string {
	ar, ag, ab_ := hexToRGB(a)
	br, bg, bb_ := hexToRGB(b)
	r := uint8(float64(ar) + t*float64(int(br)-int(ar)))
	g2 := uint8(float64(ag) + t*float64(int(bg)-int(ag)))
	blu := uint8(float64(ab_) + t*float64(int(bb_)-int(ab_)))
	return fmt.Sprintf("#%02x%02x%02x", r, g2, blu)
}

func hexToRGB(h string) (uint8, uint8, uint8) {
	if len(h) == 7 && h[0] == '#' {
		h = h[1:]
	}
	var r, g, b uint8
	fmt.Sscanf(h, "%02x%02x%02x", &r, &g, &b)
	return r, g, b
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
		name,
		repo,
		status,
	)
}

func (m dashboardTUIModel) viewPeekSelectOverlay() string {
	if m.data == nil {
		return "  Loading…\n"
	}
	active := activeAqueducts(m.data.Cataractae)

	const lineW = 60
	divider := strings.Repeat("─", lineW)

	var rows []string
	rows = append(rows, tuiStyleHeader.Render("  select aqueduct"))
	rows = append(rows, divider)
	for i, ch := range active {
		line := fmt.Sprintf("  %-12s  %-12s  %-12s  %s",
			ch.Name, ch.RepoName, ch.DropletID, ch.Step)
		if i == m.peekSelectIndex {
			rows = append(rows, tuiStyleGreen.Render(line))
		} else {
			rows = append(rows, line)
		}
	}
	rows = append(rows, divider)
	rows = append(rows, "  ↑↓ navigate  enter connect  esc cancel")

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
			return "  " + namePfx + "  (idle)", ""
		}
		return "  " + tuiStyleGreen.Render(namePfx) + "  " + ch.Step, ""
	}

	var g strings.Builder
	g.WriteString("  ")
	g.WriteString(namePfx)
	g.WriteString("  ")
	activeVisualCol := -1
	visualCol := pfxWidth

	for i, step := range ch.Steps {
		if i > 0 {
			// " ──" = 3 visual chars, "●"/"○" = 1, "──▶ " = 4 → total 8
			if step == ch.Step && ch.DropletID != "" {
				g.WriteString(" ──")
				g.WriteString(tuiStyleGreen.Render("●"))
				g.WriteString("──▶ ")
			} else {
				g.WriteString(" ──○──▶ ")
			}
			visualCol += 8
		}
		if step == ch.Step && ch.DropletID != "" {
			g.WriteString(tuiStyleGreen.Bold(true).Render(step))
			activeVisualCol = visualCol // step name starts here (after any incoming edge)
		} else {
			g.WriteString(step)
		}
		visualCol += len([]rune(step))
	}

	graphLine = g.String()
	if activeVisualCol >= 0 {
		bar := progressBar(ch.CataractaeIndex, ch.TotalCataractae, 8)
		elapsed := formatElapsed(ch.Elapsed)
		infoLine = strings.Repeat(" ", activeVisualCol) +
			"↑ " +
			tuiStyleGreen.Render(ch.Name) +
			" · " + ch.DropletID +
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
				infoLine += "  " + title
			}
		}
	}
	return
}

// viewInlineFlowNotes renders the current-flow content for the droplet carried
// by ch, placed inline below the aqueduct's progress bar. Returns nil if there
// is no matching FlowActivity for ch.DropletID.
func (m dashboardTUIModel) viewInlineFlowNotes(ch CataractaeInfo) []string {
	if m.data == nil || ch.DropletID == "" {
		return nil
	}

	var fa *FlowActivity
	for i := range m.data.FlowActivities {
		if m.data.FlowActivities[i].DropletID == ch.DropletID {
			fa = &m.data.FlowActivities[i]
			break
		}
	}
	if fa == nil {
		return nil
	}

	maxW := m.width - 6
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

	firstMeaningfulLine := func(content string) string {
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "---") {
				return line
			}
		}
		return strings.TrimSpace(content)
	}

	// Header: droplet ID + step + title.
	stepStr := tuiStyleGreen.Render(fa.Step)
	idStr := tuiStyleHeader.Render(fa.DropletID)
	title := "  " + truncate(fa.Title, maxW-30)
	var lines []string
	lines = append(lines, fmt.Sprintf("  %s  %s%s", idStr, stepStr, title))

	if len(fa.RecentNotes) == 0 {
		lines = append(lines, "    (no notes yet — first pass)")
	} else {
		for _, note := range fa.RecentNotes {
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

			who := "[" + note.CataractaeName + "]"
			text := firstMeaningfulLine(note.Content)
			text = truncate(text, maxW-30)
			lines = append(lines,
				fmt.Sprintf("    › %s  %s  %s", who, text, ts),
			)
		}
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
		return []string{"  Cistern is empty."}
	}

	lines := make([]string, 0, len(queued))
	for _, item := range queued {
		lines = append(lines, m.viewCisternRow(item))
	}
	return lines
}

func (m dashboardTUIModel) viewCisternRow(item *cistern.Droplet) string {
	age := time.Since(item.CreatedAt).Round(time.Minute)
	id := padRight(item.ID, 10)

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
		prio = "·"
	case 3:
		prio = "↓"
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

	elapsed := formatElapsed(age)
	return fmt.Sprintf("  %s %s  %s  %s  %s",
		prio,
		id,
		elapsed,
		statusStr,
		title,
	)
}

// viewPooled renders the pooled droplets panel.
// When no pooled droplets exist it renders a compact count label ("Pooled: 0")
// so the operator always knows the section is present. When droplets are present
// the section expands to a full list showing ID, time since last state change, and title.
func (m dashboardTUIModel) viewPooled() []string {
	if len(m.data.PooledItems) == 0 {
		return []string{"  " + tuiStyleDim.Render("Pooled: 0")}
	}
	lines := make([]string, 0, len(m.data.PooledItems))
	for _, item := range m.data.PooledItems {
		lines = append(lines, m.viewPooledRow(item))
	}
	return lines
}

func (m dashboardTUIModel) viewPooledRow(item *cistern.Droplet) string {
	icon := tuiStyleRed.Render("✗")
	id := padRight(item.ID, 10)
	elapsed := formatElapsed(time.Since(item.UpdatedAt))

	// Truncate title to fit terminal width.
	fixedW := 2 + 2 + 2 + 10 + 2 + 7 + 2
	titleW := m.width - fixedW
	if titleW < 8 {
		titleW = 8
	}
	title := item.Title
	r := []rune(title)
	if len(r) > titleW {
		title = string(r[:titleW-1]) + "…"
	}

	return fmt.Sprintf("  %s  %s  %s  %s", icon, id, elapsed, title)
}

func (m dashboardTUIModel) viewRecentFlow() []string {
	if len(m.data.RecentItems) == 0 {
		return []string{"  No recent flow."}
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
	case "pooled":
		icon = tuiStyleRed.Render("✗")
	default:
		icon = "·"
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
		t,
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
