// cmd/arch-designer — interactive Roman arch parameter designer.
//
// Default mode (no flags): runs a Bubble Tea TUI for tuning arch constants.
// Web mode (--web): spawns itself as a PTY subprocess and serves the TUI
// in a browser via xterm.js on a configurable port (default 5738).
//
// Keyboard shortcuts (TUI and web button panel):
//
//	Tab / Shift+Tab   next / previous parameter
//	↑ / ↓            adjust selected parameter by ±1
//	Shift+↑ / Shift+↓ adjust by ±5
//	l / L             load preset (defaults)
//	r / R             reset to defaults
//	s / S             print Go constants
//	q / Q / Ctrl+C    quit
package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/creack/pty"
)

//go:embed assets/static
var staticAssets embed.FS

// ── Arch parameters ──────────────────────────────────────────────────────────

// archParams holds the configurable parameters for the arch visualization.
// These match the constants defined in cmd/ct/dashboard_tui.go.
type archParams struct {
	ColW      int // total column width (pier + arch span)
	ArchTopW  int // pier width at the top (keystone level)
	TaperRows int // number of rows the pier narrows over
	PierRows  int // constant-width pier body rows below the taper
	BrickW    int // brick face width before the ▌ mortar joint
	NumPiers  int // number of piers to render in the preview
}

func defaultArchParams() archParams {
	return archParams{
		ColW: 14, ArchTopW: 9, TaperRows: 4,
		PierRows: 1, BrickW: 4, NumPiers: 4,
	}
}

// clampParams ensures all parameters are within valid bounds.
func clampParams(p archParams) archParams {
	if p.TaperRows < 1 {
		p.TaperRows = 1
	}
	if p.TaperRows > 8 {
		p.TaperRows = 8
	}
	if p.PierRows < 0 {
		p.PierRows = 0
	}
	if p.PierRows > 4 {
		p.PierRows = 4
	}
	if p.BrickW < 1 {
		p.BrickW = 1
	}
	if p.BrickW > 8 {
		p.BrickW = 8
	}
	if p.NumPiers < 1 {
		p.NumPiers = 1
	}
	if p.NumPiers > 8 {
		p.NumPiers = 8
	}
	// pierW = archTopW - taperRows*2 must be >= 1
	minArchTopW := p.TaperRows*2 + 1
	if p.ArchTopW < minArchTopW {
		p.ArchTopW = minArchTopW
	}
	// gap between piers must be >= 0; archTopW must be < colW
	minColW := p.ArchTopW + 2
	if p.ColW < minColW {
		p.ColW = minColW
	}
	if p.ColW > 30 {
		p.ColW = 30
	}
	if p.ArchTopW > p.ColW-1 {
		p.ArchTopW = p.ColW - 1
	}
	return p
}

// paramNames and paramDescs are parallel slices in display order.
var paramNames = []string{"ColW", "ArchTopW", "TaperRows", "PierRows", "BrickW", "NumPiers"}
var paramDescs = []string{
	"column width",
	"pier top width",
	"taper rows",
	"pier body rows",
	"brick face width",
	"number of piers",
}

// getParam returns the value of parameter i from p.
func getParam(p archParams, i int) int {
	switch i {
	case 0:
		return p.ColW
	case 1:
		return p.ArchTopW
	case 2:
		return p.TaperRows
	case 3:
		return p.PierRows
	case 4:
		return p.BrickW
	case 5:
		return p.NumPiers
	}
	return 0
}

// setParam returns a new archParams with parameter i set to val (then clamped).
func setParam(p archParams, i, val int) archParams {
	switch i {
	case 0:
		p.ColW = val
	case 1:
		p.ArchTopW = val
	case 2:
		p.TaperRows = val
	case 3:
		p.PierRows = val
	case 4:
		p.BrickW = val
	case 5:
		p.NumPiers = val
	}
	return clampParams(p)
}

// goConstants returns the Go constants block for the given params.
func goConstants(p archParams) string {
	spanAtKeystone := p.ColW - p.ArchTopW
	return fmt.Sprintf(
		"const (\n"+
			"\tcolW      = %d  // column width\n"+
			"\tarchTopW  = %d  // pier top width — span = %d chars at keystone\n"+
			"\ttaperRows = %d  // pier narrows by 2 per row\n"+
			"\tpierRows  = %d  // constant-width pier body rows\n"+
			"\tbrickW    = %d  // brick face width before ▌ joint\n"+
			"\tnumPiers  = %d  // arch piers per aqueduct\n"+
			")",
		p.ColW, p.ArchTopW, spanAtKeystone,
		p.TaperRows, p.PierRows, p.BrickW, p.NumPiers,
	)
}

// ── Arch rendering ────────────────────────────────────────────────────────────

// archCrownAtT computes arch-crown fill at t in [0,1].
// t=0: keystone (closed), t=1: impost (open).
// Same formula as cmd/ct/dashboard_tui.go archCrownAtT.
func archCrownAtT(t float64, gapWidth int) (lf, og, rf int) {
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

// renderArch renders an arch preview for the given params and returns display lines.
// Replicates the structural rendering from cmd/ct/dashboard_tui.go without the
// per-droplet data overlay or waterfall animation.
func renderArch(p archParams) []string {
	p = clampParams(p)
	pierW := p.ArchTopW - p.TaperRows*2
	if pierW < 1 {
		pierW = 1
	}
	n := p.NumPiers
	colW := p.ColW

	stoneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b8cba"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#3d5a7a"))

	var result []string

	// Top mortar cap — spans full arch width.
	result = append(result, dimStyle.Render(strings.Repeat("▀", n*colW)))

	for lr := 0; lr < p.TaperRows+p.PierRows; lr++ {
		bodyW := p.ArchTopW - lr*2
		if bodyW < pierW {
			bodyW = pierW
		}
		rowPadL := (colW - bodyW) / 2
		gapW := colW - bodyW
		offset := (p.BrickW / 2) * (lr % 2)

		// Arch crown fill values.
		tMort := math.Min(float64(lr)/float64(p.TaperRows), 1.0)
		lfM, ogM, rfM := 0, gapW, 0
		if lr < p.TaperRows {
			lfM, ogM, rfM = archCrownAtT(tMort, gapW)
		}
		tBrick := math.Min(float64(lr)+0.5, float64(p.TaperRows)) / float64(p.TaperRows)
		lfB, ogB, rfB := 0, gapW, 0
		if lr < p.TaperRows {
			lfB, ogB, rfB = archCrownAtT(tBrick, gapW)
		}

		var mortSB, brickSB strings.Builder

		// Left abutment.
		mortSB.WriteString(dimStyle.Render(strings.Repeat("▀", rowPadL)))
		abutBrick := make([]rune, rowPadL)
		for c := 0; c < rowPadL; c++ {
			if (c+offset)%(p.BrickW+1) == p.BrickW {
				abutBrick[c] = '▌'
			} else {
				abutBrick[c] = '█'
			}
		}
		brickSB.WriteString(dimStyle.Render(string(abutBrick)))

		for i := 0; i < n; i++ {
			// Pier mortar sub-row.
			mortSB.WriteString(stoneStyle.Render(strings.Repeat("▀", bodyW)))

			// Pier brick sub-row with staggered joints.
			body := make([]rune, bodyW)
			for c := 0; c < bodyW; c++ {
				if (c+offset)%(p.BrickW+1) == p.BrickW {
					body[c] = '▌'
				} else {
					body[c] = '█'
				}
			}
			brickSB.WriteString(stoneStyle.Render(string(body)))

			// Inter-pier arch span (not after the last pier).
			if i < n-1 {
				// Mortar sub-row crown.
				if lfM > 0 {
					mortSB.WriteString(stoneStyle.Render(strings.Repeat("▀", lfM)))
				}
				if ogM > 0 {
					mortSB.WriteString(strings.Repeat(" ", ogM))
				}
				if rfM > 0 {
					mortSB.WriteString(stoneStyle.Render(strings.Repeat("▀", rfM)))
				}
				// Brick sub-row crown with ▌▐ haunch at intrados edge.
				if lfB > 0 {
					if lfB > 1 {
						brickSB.WriteString(stoneStyle.Render(strings.Repeat("█", lfB-1)))
					}
					brickSB.WriteString(stoneStyle.Render("▌"))
				}
				if ogB > 0 {
					brickSB.WriteString(strings.Repeat(" ", ogB))
				}
				if rfB > 0 {
					brickSB.WriteString(stoneStyle.Render("▐"))
					if rfB > 1 {
						brickSB.WriteString(stoneStyle.Render(strings.Repeat("█", rfB-1)))
					}
				}
			}
		}

		// Right abutment.
		mortSB.WriteString(dimStyle.Render(strings.Repeat("▀", rowPadL)))
		abutBrick2 := make([]rune, rowPadL)
		for c := 0; c < rowPadL; c++ {
			if (c+offset)%(p.BrickW+1) == p.BrickW {
				abutBrick2[c] = '▌'
			} else {
				abutBrick2[c] = '█'
			}
		}
		brickSB.WriteString(dimStyle.Render(string(abutBrick2)))

		result = append(result, mortSB.String(), brickSB.String())
	}

	return result
}

// ── Bubble Tea TUI ────────────────────────────────────────────────────────────

type tuiModel struct {
	params   archParams
	selected int
	message  string
	showConst bool
}

func newTUIModel() tuiModel {
	return tuiModel{params: defaultArchParams()}
}

func (m tuiModel) Init() tea.Cmd { return nil }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.message = ""
		m.showConst = false
		switch msg.String() {
		case "tab":
			m.selected = (m.selected + 1) % len(paramNames)
		case "shift+tab":
			m.selected = (m.selected - 1 + len(paramNames)) % len(paramNames)
		case "up":
			m.params = setParam(m.params, m.selected, getParam(m.params, m.selected)+1)
		case "down":
			m.params = setParam(m.params, m.selected, getParam(m.params, m.selected)-1)
		case "shift+up":
			m.params = setParam(m.params, m.selected, getParam(m.params, m.selected)+5)
		case "shift+down":
			m.params = setParam(m.params, m.selected, getParam(m.params, m.selected)-5)
		case "l", "L":
			m.params = defaultArchParams()
			m.message = "Preset loaded"
		case "r", "R":
			m.params = defaultArchParams()
			m.message = "Reset to defaults"
		case "s", "S":
			m.showConst = true
			m.message = "Constants (select and copy):"
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m tuiModel) View() string {
	var sb strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#58a6ff"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#b1bac4"))
	selStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6e7681"))
	valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#e6edf3"))
	msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950"))
	constStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f6fc"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#484f58"))

	sb.WriteString("\n")
	sb.WriteString("  " + titleStyle.Render("Arch Designer") + "\n\n")

	// Arch preview.
	for _, line := range renderArch(m.params) {
		sb.WriteString("  " + line + "\n")
	}
	sb.WriteString("\n")

	// Parameter list.
	sb.WriteString("  " + labelStyle.Render("Parameters:") + "\n")
	for i, name := range paramNames {
		val := getParam(m.params, i)
		cursor := "  "
		nameStr := dimStyle.Render(fmt.Sprintf("%-12s", name))
		valStr := valStyle.Render(fmt.Sprintf("%3d", val))
		descStr := dimStyle.Render("  " + paramDescs[i])
		if i == m.selected {
			cursor = "► "
			nameStr = selStyle.Render(fmt.Sprintf("%-12s", name))
			valStr = selStyle.Render(fmt.Sprintf("%3d", val))
		}
		sb.WriteString("  " + cursor + nameStr + valStr + descStr + "\n")
	}
	sb.WriteString("\n")

	// Message / constants.
	if m.message != "" {
		sb.WriteString("  " + msgStyle.Render(m.message) + "\n")
	}
	if m.showConst {
		sb.WriteString(constStyle.Render(goConstants(m.params)) + "\n")
	}
	sb.WriteString("\n")

	// Key hints.
	sb.WriteString("  " + hintStyle.Render("Tab/S+Tab:±param  ↑↓:±1  S+↑↓:±5  l:preset  r:reset  s:save  q:quit") + "\n")

	return sb.String()
}

// ── WebSocket utilities (mirrors cmd/ct/dashboard_web.go) ────────────────────

const (
	wsWriteTimeout     = 10 * time.Second
	wsMaxClientPayload = 4096
	ptyReadBufSize     = 4096
	wsOpcodeText       = 0x1
	wsOpcodeBinary     = 0x2
	wsOpcodeClose      = 0x8
)

func wsAcceptKey(clientKey string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(clientKey + magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func wsSendBinary(w *bufio.Writer, data []byte) error {
	return wsSendFrame(w, wsOpcodeBinary, data)
}

func wsSendFrame(w *bufio.Writer, opcode byte, payload []byte) error {
	n := len(payload)
	var header [10]byte
	header[0] = 0x80 | opcode
	var hLen int
	switch {
	case n < 126:
		header[1] = byte(n)
		hLen = 2
	case n < 65536:
		header[1] = 0x7E
		binary.BigEndian.PutUint16(header[2:4], uint16(n))
		hLen = 4
	default:
		header[1] = 0x7F
		binary.BigEndian.PutUint64(header[2:10], uint64(n))
		hLen = 10
	}
	if _, err := w.Write(header[:hLen]); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

func wsReadClientFrame(br *bufio.Reader, buf []byte) (opcode byte, payload []byte, newBuf []byte, err error) {
	var header [2]byte
	if _, err = io.ReadFull(br, header[:]); err != nil {
		return 0, nil, buf, err
	}
	opcode = header[0] & 0x0F
	masked := header[1]&0x80 != 0
	rawLen := int(header[1] & 0x7F)

	if !masked {
		return 0, nil, buf, fmt.Errorf("unmasked client frame (RFC 6455 §5.1)")
	}

	var payloadLen int
	switch rawLen {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, buf, err
		}
		payloadLen = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, buf, err
		}
		extLen := binary.BigEndian.Uint64(ext[:])
		if extLen > uint64(wsMaxClientPayload) {
			return 0, nil, buf, fmt.Errorf("client frame payload %d exceeds max %d", extLen, wsMaxClientPayload)
		}
		payloadLen = int(extLen)
	default:
		payloadLen = rawLen
	}

	if payloadLen > wsMaxClientPayload {
		return 0, nil, buf, fmt.Errorf("client frame payload %d exceeds max %d", payloadLen, wsMaxClientPayload)
	}

	var mask [4]byte
	if _, err = io.ReadFull(br, mask[:]); err != nil {
		return 0, nil, buf, err
	}
	if payloadLen > len(buf) {
		buf = make([]byte, payloadLen)
	}
	if _, err = io.ReadFull(br, buf[:payloadLen]); err != nil {
		return 0, nil, buf, err
	}
	for i := range buf[:payloadLen] {
		buf[i] ^= mask[i%4]
	}
	return opcode, buf[:payloadLen], buf, nil
}

func wsUpgrade(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "websocket upgrade required", http.StatusUpgradeRequired)
		return nil, nil, fmt.Errorf("not a websocket request")
	}
	key := r.Header.Get("Sec-Websocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, nil, fmt.Errorf("missing Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return nil, nil, fmt.Errorf("hijacking not supported")
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAcceptKey(key) + "\r\n" +
		"\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		conn.Close()
		return nil, nil, err
	}
	if err := brw.Flush(); err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, brw, nil
}

// ── Web server ────────────────────────────────────────────────────────────────

// newArchDesignerMux returns the HTTP mux for the arch-designer web UI.
// Exposed for testing.
func newArchDesignerMux() http.Handler {
	mux := http.NewServeMux()

	exe, _ := os.Executable()

	staticSub, err := fs.Sub(staticAssets, "assets/static")
	if err != nil {
		panic("embedded static assets not found: " + err.Error())
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, archDesignerHTML)
	})

	// WS /ws/term — spawns arch-designer (TUI mode) in a PTY and streams
	// raw ANSI to xterm.js. Unlike /ws/tui, text frames are forwarded as
	// keystrokes to the PTY stdin so the browser button panel can drive the TUI.
	//
	// Protocol (client → server):
	//   {"resize":{"cols":N,"rows":N}}  JSON text frame — resize PTY
	//   any other text frame            raw bytes forwarded to PTY stdin (keystrokes)
	//
	// Protocol (server → client): binary frames containing raw PTY output.
	mux.HandleFunc("/ws/term", func(w http.ResponseWriter, r *http.Request) {
		conn, brw, err := wsUpgrade(w, r)
		if err != nil {
			return
		}
		defer conn.Close()

		if exe == "" {
			return
		}
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(),
			"TERM=xterm-256color",
			"COLORTERM=truecolor",
		)

		ptmx, err := pty.Start(cmd)
		if err != nil {
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer func() {
			cancel()
			cmd.Process.Kill() //nolint:errcheck
			cmd.Wait()         //nolint:errcheck
		}()

		// Goroutine C: shutdown watchdog.
		go func() {
			<-ctx.Done()
			ptmx.Close()
		}()

		_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

		// Goroutine B: read incoming WebSocket frames from the client.
		go func() {
			defer cancel()
			buf := make([]byte, wsMaxClientPayload)
			for {
				if err := conn.SetReadDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
					return
				}
				opcode, payload, nb, err := wsReadClientFrame(brw.Reader, buf)
				buf = nb
				if err != nil {
					return
				}
				switch opcode {
				case wsOpcodeText:
					// Try JSON resize first; fall back to forwarding as PTY input.
					var msg struct {
						Resize *struct {
							Cols uint16 `json:"cols"`
							Rows uint16 `json:"rows"`
						} `json:"resize"`
					}
					if json.Unmarshal(payload, &msg) == nil && msg.Resize != nil {
						_ = pty.Setsize(ptmx, &pty.Winsize{
							Rows: msg.Resize.Rows,
							Cols: msg.Resize.Cols,
						})
					} else {
						// Forward keystrokes to PTY stdin.
						ptmx.Write(payload) //nolint:errcheck
					}
				case wsOpcodeClose:
					return
				}
			}
		}()

		// Goroutine A: forward PTY output → WebSocket as binary frames.
		out := make([]byte, ptyReadBufSize)
		for {
			n, err := ptmx.Read(out)
			if n > 0 {
				conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)) //nolint:errcheck
				if wsSendBinary(brw.Writer, out[:n]) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	})

	return mux
}

// runWeb starts the HTTP server on addr, blocking until SIGINT/SIGTERM.
func runWeb(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           newArchDesignerMux(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      0, // WebSocket connections are long-lived
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "Arch Designer web UI listening on http://localhost%s\n", addr)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// ── Entry point ───────────────────────────────────────────────────────────────

func main() {
	webMode := flag.Bool("web", false, "start web server instead of TUI")
	port := flag.Int("port", 5738, "web server port (--web mode only)")
	flag.Parse()

	if *webMode {
		addr := fmt.Sprintf(":%d", *port)
		if err := runWeb(addr); err != nil {
			fmt.Fprintf(os.Stderr, "arch-designer: %v\n", err)
			os.Exit(1)
		}
		return
	}

	p := tea.NewProgram(newTUIModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "arch-designer: %v\n", err)
		os.Exit(1)
	}
}

// ── HTML ──────────────────────────────────────────────────────────────────────

// archDesignerHTML is the single-page web UI. It renders the arch-designer TUI
// in an xterm.js terminal and overlays a touch-friendly button panel that
// injects keystrokes into the PTY via WebSocket.
//
// The button panel mirrors the TUI keyboard shortcuts exactly:
//
//	Prev / Next    → Shift+Tab / Tab
//	Up / Down      → ↑ / ↓
//	+5 / -5        → Shift+↑ / Shift+↓
//	[L] Preset     → 'l'
//	[R] Reset      → 'r'
//	[S] Save & Copy → 's' + navigator.clipboard (from local param state)
//
// The Save & Copy button maintains a mirror of the parameter state in JS so it
// can compute the Go constants independently and write them to the clipboard via
// navigator.clipboard.writeText.
const archDesignerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Arch Designer</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
html,body{width:100%;height:100%;background:#0d1117;color:#e6edf3;font-family:sans-serif;display:flex;flex-direction:column;overflow:hidden}
#terminal-wrap{flex:1;min-height:0;overflow:auto;-webkit-overflow-scrolling:touch}
#terminal{display:inline-block}
#controls{
  flex-shrink:0;
  background:#161b22;
  border-top:1px solid #30363d;
  padding:8px 10px;
  display:flex;
  flex-wrap:wrap;
  gap:6px;
  align-items:center;
}
.btn{
  background:#21262d;
  color:#e6edf3;
  border:1px solid #30363d;
  border-radius:6px;
  padding:8px 14px;
  font-size:14px;
  cursor:pointer;
  user-select:none;
  -webkit-user-select:none;
  touch-action:manipulation;
  min-width:48px;
  text-align:center;
}
.btn:active{background:#388bfd22;border-color:#388bfd}
.btn-save{background:#1a3a1a;border-color:#3fb950;color:#3fb950}
.btn-save:active{background:#3fb95033}
#save-msg{color:#3fb950;font-size:12px;display:none;align-self:center}
.xterm-viewport::-webkit-scrollbar{width:6px}
.xterm-viewport::-webkit-scrollbar-track{background:#0d1117}
.xterm-viewport::-webkit-scrollbar-thumb{background:#30363d;border-radius:3px}
.xterm-viewport{scrollbar-color:#30363d #0d1117;scrollbar-width:thin}
</style>
<link rel="stylesheet" href="/static/xterm.min.css"/>
</head>
<body>
<div id="terminal-wrap"><div id="terminal"></div></div>
<div id="controls">
  <button class="btn" id="btn-prev"  title="Previous param (Shift+Tab)">◄ Prev</button>
  <button class="btn" id="btn-next"  title="Next param (Tab)">Next ►</button>
  <button class="btn" id="btn-up"    title="Up (↑)">▲</button>
  <button class="btn" id="btn-down"  title="Down (↓)">▼</button>
  <button class="btn" id="btn-plus5" title="+5 (Shift+↑)">+5</button>
  <button class="btn" id="btn-min5"  title="-5 (Shift+↓)">−5</button>
  <button class="btn" id="btn-preset" title="Load preset (l)">[L] Preset</button>
  <button class="btn" id="btn-reset"  title="Reset to defaults (r)">[R] Reset</button>
  <button class="btn btn-save" id="btn-save" title="Save &amp; copy constants (s)">[S] Save &amp; Copy</button>
  <span id="save-msg">Copied!</span>
</div>
<script src="/static/xterm.min.js"></script>
<script src="/static/addon-fit.min.js"></script>
<script>
/* ── xterm.js setup ────────────────────────────────────────────────────── */
var term = new Terminal({
  theme: {
    background:'#0d1117', foreground:'#e6edf3', cursor:'#58a6ff',
    black:'#484f58', red:'#ff7b72', green:'#3fb950', yellow:'#d29922',
    blue:'#58a6ff', magenta:'#bc8cff', cyan:'#39c5cf', white:'#b1bac4',
    brightBlack:'#6e7681', brightRed:'#ffa198', brightGreen:'#56d364',
    brightYellow:'#e3b341', brightBlue:'#79c0ff', brightMagenta:'#d2a8ff',
    brightCyan:'#56d4dd', brightWhite:'#f0f6fc'
  },
  fontFamily:"'Cascadia Code','JetBrains Mono','DejaVu Sans Mono','Fira Code','Menlo','Consolas','Liberation Mono',monospace",
  fontSize:13, lineHeight:1.2, letterSpacing:0, cursorBlink:false,
  scrollback:1000, scrollOnUserInput:false, allowProposedApi:true
});
var fitAddon = new FitAddon.FitAddon();
term.loadAddon(fitAddon);
term.open(document.getElementById('terminal'));

var wrap = document.getElementById('terminal-wrap');
function fitTerminal() {
  var termEl = document.getElementById('terminal');
  termEl.style.width  = wrap.clientWidth  + 'px';
  termEl.style.height = wrap.clientHeight + 'px';
  fitAddon.fit();
}
window.addEventListener('resize', fitTerminal);
requestAnimationFrame(fitTerminal);

/* ── WebSocket connection ──────────────────────────────────────────────── */
var ws = null;
term.onResize(function(e) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({resize:{cols:e.cols, rows:e.rows}}));
  }
});

function connect() {
  var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(proto + '//' + location.host + '/ws/term');
  ws.binaryType = 'arraybuffer';
  ws.onopen = function() {
    term.clear();
    ws.send(JSON.stringify({resize:{cols:term.cols, rows:term.rows}}));
  };
  ws.onmessage = function(e) {
    if (e.data instanceof ArrayBuffer) {
      term.write(new Uint8Array(e.data));
    } else {
      term.write(e.data);
    }
  };
  ws.onclose = function() {
    term.write('\r\n\x1b[2m─── disconnected — reconnecting in 3s ───\x1b[0m\r\n');
    setTimeout(connect, 3000);
  };
  ws.onerror = function() { ws.close(); };
}
connect();

/* ── Send keystroke to PTY ─────────────────────────────────────────────── */
function sendKey(s) {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(s);
}

/* ── Local param state (mirrors TUI state for Save & Copy) ────────────── */
var params = {colW:14, archTopW:9, taperRows:4, pierRows:1, brickW:4, numPiers:4};
var selected = 0;
var paramOrder = ['colW','archTopW','taperRows','pierRows','brickW','numPiers'];

function clampParams() {
  if (params.taperRows < 1) params.taperRows = 1;
  if (params.taperRows > 8) params.taperRows = 8;
  if (params.pierRows  < 0) params.pierRows  = 0;
  if (params.pierRows  > 4) params.pierRows  = 4;
  if (params.brickW    < 1) params.brickW    = 1;
  if (params.brickW    > 8) params.brickW    = 8;
  if (params.numPiers  < 1) params.numPiers  = 1;
  if (params.numPiers  > 8) params.numPiers  = 8;
  var minTopW = params.taperRows * 2 + 1;
  if (params.archTopW < minTopW) params.archTopW = minTopW;
  var minColW = params.archTopW + 2;
  if (params.colW < minColW) params.colW = minColW;
  if (params.colW > 30) params.colW = 30;
  if (params.archTopW > params.colW - 1) params.archTopW = params.colW - 1;
}

function adjustSelected(delta) {
  var k = paramOrder[selected];
  params[k] += delta;
  clampParams();
}

function formatConstants(p) {
  var span = p.colW - p.archTopW;
  return 'const (\n' +
    '\tcolW      = ' + p.colW      + '  // column width\n' +
    '\tarchTopW  = ' + p.archTopW  + '  // pier top width — span = ' + span + ' chars at keystone\n' +
    '\ttaperRows = ' + p.taperRows + '  // pier narrows by 2 per row\n' +
    '\tpierRows  = ' + p.pierRows  + '  // constant-width pier body rows\n' +
    '\tbrickW    = ' + p.brickW    + '  // brick face width before ▌ joint\n' +
    '\tnumPiers  = ' + p.numPiers  + '  // arch piers per aqueduct\n' +
    ')';
}

function showSavedMsg() {
  var el = document.getElementById('save-msg');
  el.style.display = 'inline';
  setTimeout(function(){ el.style.display = 'none'; }, 2000);
}

/* ── Button bindings ───────────────────────────────────────────────────── */
document.getElementById('btn-prev').addEventListener('click', function() {
  selected = (selected - 1 + paramOrder.length) % paramOrder.length;
  sendKey('\x1b[Z'); /* Shift+Tab */
});
document.getElementById('btn-next').addEventListener('click', function() {
  selected = (selected + 1) % paramOrder.length;
  sendKey('\t'); /* Tab */
});
document.getElementById('btn-up').addEventListener('click', function() {
  adjustSelected(1);
  sendKey('\x1b[A'); /* Up arrow */
});
document.getElementById('btn-down').addEventListener('click', function() {
  adjustSelected(-1);
  sendKey('\x1b[B'); /* Down arrow */
});
document.getElementById('btn-plus5').addEventListener('click', function() {
  adjustSelected(5);
  sendKey('\x1b[1;2A'); /* Shift+Up */
});
document.getElementById('btn-min5').addEventListener('click', function() {
  adjustSelected(-5);
  sendKey('\x1b[1;2B'); /* Shift+Down */
});
document.getElementById('btn-preset').addEventListener('click', function() {
  params = {colW:14, archTopW:9, taperRows:4, pierRows:1, brickW:4, numPiers:4};
  selected = 0;
  sendKey('l');
});
document.getElementById('btn-reset').addEventListener('click', function() {
  params = {colW:14, archTopW:9, taperRows:4, pierRows:1, brickW:4, numPiers:4};
  selected = 0;
  sendKey('r');
});
document.getElementById('btn-save').addEventListener('click', function() {
  sendKey('s');
  var constants = formatConstants(params);
  if (navigator.clipboard) {
    navigator.clipboard.writeText(constants).then(showSavedMsg, function(){});
  }
});
</script>
</body>
</html>`
