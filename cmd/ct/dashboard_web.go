package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/creack/pty"
)

//go:embed assets/static
var staticAssets embed.FS

// wsWriteTimeout is the per-frame write deadline set on the hijacked net.Conn
// before each wsSendText call. Without this, a client that disappears via a
// network partition (no TCP FIN) causes the goroutine to block indefinitely
// inside bufio.Writer.Flush.
const wsWriteTimeout = 10 * time.Second

// wsTuiReadTimeout is the read deadline applied in the /ws/tui WS handler's
// frame-reader goroutine (B). It is reset after each received frame to keep
// active sessions alive. Without a deadline, a network partition + idle PTY
// leaks goroutines A (ptmx.Read) and B (io.ReadFull) — neither gets an error,
// and cancel() is never called. Five minutes allows long idle-but-connected
// sessions while still reaping silently-partitioned ones.
const wsTuiReadTimeout = 5 * time.Minute

// wsMaxClientPayload is the maximum payload size accepted from a client frame.
// Client→server frames carry only resize JSON (~40 bytes) or close frames,
// so 4 KiB is generous. This prevents a malicious client from triggering
// unbounded memory allocation via a forged frame length header.
const wsMaxClientPayload = 4096

// ptyReadBufSize is the read buffer for forwarding PTY output to WebSocket.
const ptyReadBufSize = 4096

// dashboardDefaultFontFamily is the CSS font-family fallback used when
// dashboard_font_family is not set in cistern.yaml.
const dashboardDefaultFontFamily = "'Cascadia Code','JetBrains Mono','DejaVu Sans Mono','Fira Code','Menlo','Consolas','Liberation Mono',monospace"

// WebSocket opcodes (RFC 6455 §5.2).
const (
	wsOpcodeText   = 0x1
	wsOpcodeBinary = 0x2
	wsOpcodeClose  = 0x8
)

// aqueductSessionInfo holds the tmux session name and droplet context for an
// active aqueduct worker.
type aqueductSessionInfo struct {
	sessionID string
	dropletID string
	title     string
	elapsed   time.Duration
}

// lookupAqueductSession returns session info for the named aqueduct worker, or
// false if the worker is not currently flowing.
func lookupAqueductSession(dbPath, name string) (aqueductSessionInfo, bool) {
	c, err := cistern.New(dbPath, "")
	if err != nil {
		return aqueductSessionInfo{}, false
	}
	defer c.Close()

	items, err := c.List("", "in_progress")
	if err != nil {
		return aqueductSessionInfo{}, false
	}
	for _, item := range items {
		if item.Assignee == name {
			return aqueductSessionInfo{
				sessionID: item.Repo + "-" + name,
				dropletID: item.ID,
				title:     item.Title,
				elapsed:   time.Since(item.UpdatedAt),
			}, true
		}
	}
	return aqueductSessionInfo{}, false
}

// parsePeekLines reads the optional ?lines= query parameter, falling back to
// defaultPeekLines.
func parsePeekLines(r *http.Request) int {
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultPeekLines
}

// wsAcceptKey computes Sec-WebSocket-Accept per RFC 6455 §4.2.2.
func wsAcceptKey(clientKey string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(clientKey + magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// wsSendText writes a WebSocket text frame to the buffered writer and flushes.
// The server never masks frames (RFC 6455 §5.1).
func wsSendText(w *bufio.Writer, data string) error {
	return wsSendFrame(w, wsOpcodeText, []byte(data))
}

// wsSendBinary writes a WebSocket binary frame.
// Use for raw PTY output which may contain non-UTF-8 bytes — text frames
// with invalid UTF-8 cause browsers to close the connection immediately.
func wsSendBinary(w *bufio.Writer, data []byte) error {
	return wsSendFrame(w, wsOpcodeBinary, data)
}

// wsSendFrame writes a single unfragmented WebSocket frame (FIN=1) and flushes.
func wsSendFrame(w *bufio.Writer, opcode byte, payload []byte) error {
	n := len(payload)
	var header [10]byte
	header[0] = 0x80 | opcode // FIN + opcode
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

// handleTuiTextFrame processes a WebSocket text frame received by the /ws/tui
// handler. If payload decodes to a resize JSON message the resize callback is
// invoked with the requested dimensions. For any other payload (non-JSON, JSON
// without a "resize" key, etc.) the raw bytes are forwarded verbatim to ptmx as
// keystroke input — this is how xterm.js onData sequences (e.g. "\x1b[A" for
// up arrow) reach the running TUI subprocess.
func handleTuiTextFrame(payload []byte, ptmx io.Writer, resize func(cols, rows uint16)) {
	var msg struct {
		Resize *struct {
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		} `json:"resize"`
	}
	if json.Unmarshal(payload, &msg) == nil && msg.Resize != nil {
		resize(msg.Resize.Cols, msg.Resize.Rows)
		return
	}
	_, _ = ptmx.Write(payload)
}

// wsReadClientFrame reads one WebSocket frame from a client (potentially masked).
// It returns the opcode, payload, and any read error. buf is reused across calls
// to avoid per-frame allocation; if the payload exceeds len(buf), a new slice is
// allocated and returned as the buf going forward.
func wsReadClientFrame(br *bufio.Reader, buf []byte) (opcode byte, payload []byte, newBuf []byte, err error) {
	var header [2]byte
	if _, err = io.ReadFull(br, header[:]); err != nil {
		return 0, nil, buf, err
	}
	opcode = header[0] & 0x0F
	masked := header[1]&0x80 != 0
	rawLen := int(header[1] & 0x7F)

	// RFC 6455 §5.1: clients MUST mask all frames to the server.
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
		// Guard before int conversion: a value > wsMaxClientPayload but < math.MaxInt
		// would pass the int-typed check below, so reject it here first.
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

// wsUpgrade performs the RFC 6455 handshake. On success it returns the hijacked
// connection and its buffered read-writer. On failure it writes an HTTP error
// and returns a non-nil error.
// isAllowedWSOrigin returns true for localhost and private-network (RFC 1918)
// addresses. The dashboard is a local tool — LAN access is expected.
func isAllowedWSOrigin(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func wsUpgrade(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	// Validate Origin header to prevent cross-origin WebSocket hijacking.
	// Browsers allow JS on any origin to connect to localhost WS endpoints, so
	// the localhost binding alone is not sufficient protection.
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil {
			http.Error(w, "invalid Origin header", http.StatusForbidden)
			return nil, nil, fmt.Errorf("invalid Origin header: %w", err)
		}
		h := u.Hostname()
		if !isAllowedWSOrigin(h) {
			http.Error(w, "cross-origin WebSocket request rejected", http.StatusForbidden)
			return nil, nil, fmt.Errorf("cross-origin WebSocket rejected: %s", origin)
		}
	}
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

// repaintMarker is the escape sequence Bubble Tea emits at the start of every
// full-screen repaint when running with WithAltScreen: erase display (\033[2J)
// followed by cursor home (\033[H). broadcast() uses it to detect frame boundaries.
var repaintMarker = []byte("\033[2J\033[H")

// tuiFrameFlushDelay is how long broadcast() waits after the most recent PTY
// chunk before committing the pending frame as lastFrame. This ensures that an
// idle TUI still exposes a fresh snapshot to new connections even when no second
// repaint marker arrives to trigger the normal commit path.
const tuiFrameFlushDelay = 200 * time.Millisecond

// tuiClientChanSize is the per-client send-channel depth. Excess frames are
// dropped for slow clients so one lagging consumer cannot stall the broadcast loop.
const tuiClientChanSize = 64

// tuiRestartDelay is the pause between child process exit and automatic restart.
const tuiRestartDelay = 500 * time.Millisecond

// tuiMaxBackoff is the maximum delay between retries when spawn fails repeatedly.
// Spawn failures (missing binary, PTY allocation error) use exponential backoff
// starting at tuiRestartDelay and capping here, preventing a busy-wait loop.
const tuiMaxBackoff = 30 * time.Second

// tuiClient is one active WebSocket consumer of DashboardTUI's broadcast.
type tuiClient struct {
	ch chan []byte
}

// DashboardTUI manages a singleton ct-dashboard child process, tracks the last
// complete repaint frame, and fans out to all connected WebSocket clients.
// The child process survives WebSocket disconnects; only explicit Stop shuts it down.
type DashboardTUI struct {
	exe     string
	cfgPath string
	dbPath  string

	// spawnFn creates a new PTY session. If nil, defaultSpawn is used.
	// Override in tests to inject a controllable in-process connection.
	spawnFn func() (rwc io.ReadWriteCloser, resizeFn func(cols, rows uint16), waitFn func(), err error)

	mu         sync.Mutex
	rwc        io.ReadWriteCloser      // current PTY/pipe master (protected by mu)
	resizeFn   func(cols, rows uint16) // current resize callback (protected by mu)
	clients    map[*tuiClient]struct{} // active WS consumers (protected by mu)
	lastFrame  []byte                  // last committed complete repaint frame (protected by mu)
	pending    []byte                  // frame being accumulated since last repaint marker (protected by mu)
	inFrame    bool                    // true once the first repaint marker has been seen (protected by mu)
	flushTimer *time.Timer             // commits pending to lastFrame after idle period (protected by mu)
	flushGen   uint64                  // generation counter; stale timer callbacks compare and abort (protected by mu)

	stopCh chan struct{} // closed by Stop to terminate run loop
	doneCh chan struct{} // closed when run loop has fully exited
}

// newDashboardTUI creates a DashboardTUI. Call Start to begin the child process lifecycle.
func newDashboardTUI(exe, cfgPath, dbPath string) *DashboardTUI {
	return &DashboardTUI{
		exe:     exe,
		cfgPath: cfgPath,
		dbPath:  dbPath,
		clients: make(map[*tuiClient]struct{}),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start begins the child process lifecycle goroutine.
func (d *DashboardTUI) Start() {
	go d.run()
}

// Stop terminates the run loop and the current child process, blocking until done.
func (d *DashboardTUI) Stop() {
	close(d.stopCh)
	<-d.doneCh
}

// run is the main lifecycle loop: spawn → read → restart.
// Successful spawns restart with tuiRestartDelay. Spawn failures use exponential
// backoff (starting at tuiRestartDelay, doubling each failure, capped at tuiMaxBackoff)
// to avoid a busy-wait goroutine when the binary is missing or PTY allocation fails.
func (d *DashboardTUI) run() {
	defer close(d.doneCh)
	backoff := tuiRestartDelay
	for {
		select {
		case <-d.stopCh:
			return
		default:
		}
		var delay time.Duration
		if d.runOnce() {
			// Child spawned and ran (exited naturally or was stopped). Restart quickly.
			delay = tuiRestartDelay
			backoff = tuiRestartDelay // reset exponential backoff
		} else {
			// Spawn failed. Apply exponential backoff.
			delay = backoff
			backoff *= 2
			if backoff > tuiMaxBackoff {
				backoff = tuiMaxBackoff
			}
		}
		// Pause before restart, or exit immediately if stopped.
		select {
		case <-d.stopCh:
			return
		case <-time.After(delay):
		}
	}
}

// runOnce spawns the child process once and reads its PTY output until it exits.
// It returns true if the spawn succeeded (child ran until exit), or false if
// spawn itself failed, so the caller can apply appropriate retry backoff.
func (d *DashboardTUI) runOnce() bool {
	spawn := d.spawnFn
	if spawn == nil {
		spawn = d.defaultSpawn
	}
	rwc, resizeFn, waitFn, err := spawn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ct dashboard: spawn error: %v\n", err)
		return false
	}

	d.mu.Lock()
	d.rwc = rwc
	d.resizeFn = resizeFn
	d.mu.Unlock()

	// onceDone is closed when runOnce returns; the watchdog goroutine uses it to
	// distinguish natural process exit from an explicit Stop call.
	onceDone := make(chan struct{})
	defer close(onceDone)

	defer func() {
		rwc.Close() //nolint:errcheck
		if waitFn != nil {
			waitFn()
		}
		d.mu.Lock()
		if d.rwc == rwc {
			d.rwc = nil
			d.resizeFn = nil
		}
		d.mu.Unlock()
	}()

	// Watchdog: when Stop is called, close rwc to unblock the Read below.
	go func() {
		select {
		case <-d.stopCh:
			rwc.Close() //nolint:errcheck
		case <-onceDone:
		}
	}()

	buf := make([]byte, ptyReadBufSize)
	for {
		n, err := rwc.Read(buf)
		if n > 0 {
			d.broadcast(bytes.Clone(buf[:n]))
		}
		if err != nil {
			return true
		}
	}
}

// defaultSpawn starts a ct-dashboard child process in a PTY and returns the PTY
// master, a resize callback, a cleanup function, and any error.
func (d *DashboardTUI) defaultSpawn() (io.ReadWriteCloser, func(cols, rows uint16), func(), error) {
	if d.exe == "" {
		return nil, nil, nil, fmt.Errorf("no executable")
	}
	cmd := exec.Command(d.exe, "dashboard", "--db", d.dbPath)
	// Force true-color environment so Bubble Tea renders with full ANSI colors.
	// The web server inherits TERM=dumb from systemd; without these overrides
	// lipgloss strips all colors and the TUI renders black and white.
	cmd.Env = append(os.Environ(),
		"CT_CISTERN_CONFIG="+d.cfgPath,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})
	resizeFn := func(cols, rows uint16) {
		_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
	}
	waitFn := func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	}
	return ptmx, resizeFn, waitFn, nil
}

// broadcast updates the lastFrame state and sends chunk to all registered clients.
// Slow clients have frames dropped rather than blocking the broadcast loop.
func (d *DashboardTUI) broadcast(chunk []byte) {
	d.mu.Lock()
	d.frameAccumulate(chunk)
	// Snapshot client list under lock to avoid holding the lock during sends.
	clients := make([]*tuiClient, 0, len(d.clients))
	for c := range d.clients {
		clients = append(clients, c)
	}
	d.mu.Unlock()

	for _, c := range clients {
		select {
		case c.ch <- chunk:
		default:
			// Slow client: drop frame.
		}
	}
}

// frameAccumulate detects repaint boundaries in chunk and updates lastFrame.
// A repaint boundary is marked by repaintMarker (\033[2J\033[H). When a marker
// is found, the accumulated pending frame is committed as lastFrame and a new
// pending frame begins at the marker. An idle-flush timer commits the current
// pending frame if no second marker arrives within tuiFrameFlushDelay.
// Must be called with d.mu held.
func (d *DashboardTUI) frameAccumulate(chunk []byte) {
	rest := chunk
	for len(rest) > 0 {
		idx := bytes.Index(rest, repaintMarker)
		if idx < 0 {
			if d.inFrame {
				d.pending = append(d.pending, rest...)
				d.scheduleFlush()
			}
			return
		}
		if d.inFrame {
			d.lastFrame = append(d.pending, rest[:idx]...)
		}
		d.pending = bytes.Clone(repaintMarker)
		d.inFrame = true
		rest = rest[idx+len(repaintMarker):]
	}
	// Chunk ended at a repaint marker; arm the flush timer so an idle TUI
	// still exposes a snapshot to new connections.
	if d.inFrame {
		d.scheduleFlush()
	}
}

// scheduleFlush arms (or resets) the idle-flush timer. Must be called with d.mu held.
func (d *DashboardTUI) scheduleFlush() {
	if d.flushTimer != nil {
		d.flushTimer.Stop()
	}
	d.flushGen++
	gen := d.flushGen
	d.flushTimer = time.AfterFunc(tuiFrameFlushDelay, func() { d.flushPendingFrame(gen) })
}

// flushPendingFrame commits the current pending frame as lastFrame. It is called
// by the flush timer when no second repaint marker has arrived within tuiFrameFlushDelay.
// gen is the generation at the time the timer was armed; if d.flushGen has advanced
// the callback is stale and must not overwrite a properly committed lastFrame.
func (d *DashboardTUI) flushPendingFrame(gen uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.flushGen != gen {
		return
	}
	if d.inFrame && len(d.pending) > 0 {
		d.lastFrame = d.pending
	}
	d.flushTimer = nil
}

// attach registers a new WebSocket client. It returns the client handle and a
// copy of the last complete repaint frame (nil if none has been seen yet) to
// send as the initial snapshot on connect or reconnect.
// The caller must call detach when the WebSocket closes.
func (d *DashboardTUI) attach() (*tuiClient, []byte) {
	c := &tuiClient{ch: make(chan []byte, tuiClientChanSize)}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clients[c] = struct{}{}
	return c, bytes.Clone(d.lastFrame)
}

// detach unregisters the client. The child process continues running.
func (d *DashboardTUI) detach(c *tuiClient) {
	d.mu.Lock()
	delete(d.clients, c)
	d.mu.Unlock()
}

// resize updates the PTY dimensions if the child process is running.
func (d *DashboardTUI) resize(cols, rows uint16) {
	d.mu.Lock()
	fn := d.resizeFn
	d.mu.Unlock()
	if fn != nil {
		fn(cols, rows)
	}
}

// Write forwards keystroke bytes to the PTY. Implements io.Writer for
// handleTuiTextFrame. Drops silently if no child process is running.
func (d *DashboardTUI) Write(p []byte) (int, error) {
	d.mu.Lock()
	rwc := d.rwc
	d.mu.Unlock()
	if rwc == nil {
		return len(p), nil
	}
	return rwc.Write(p)
}

// newDashboardMux returns an http.Handler for the web dashboard.
// Exposed for testing. The /ws/tui endpoint is disabled (tui=nil).
func newDashboardMux(cfgPath, dbPath string) http.Handler {
	return newDashboardMuxInternal(cfgPath, dbPath, nil)
}

// newDashboardMuxWith returns an http.Handler for the web dashboard with custom
// fetcher and refresh intervals. Exposed for testing.
func newDashboardMuxWith(cfgPath, dbPath string, fetcher func(cfg, db string) *DashboardData, fastInterval, slowInterval time.Duration) http.Handler {
	return newDashboardMuxInternalWith(cfgPath, dbPath, nil, fetcher, fastInterval, slowInterval)
}

// makeDashboardEventsHandler returns an http.HandlerFunc for the SSE dashboard events
// endpoint. Parameterised so tests can inject a custom fetcher and intervals.
func makeDashboardEventsHandler(cfgPath, dbPath string, fetcher func(string, string) *DashboardData, fastInterval, slowInterval time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		sendEvent := func(d *DashboardData) {
			if b, err := json.Marshal(d); err == nil {
				fmt.Fprintf(w, "data: %s\n\n", b)
				flusher.Flush()
			}
		}

		// Initial send — establishes the hash baseline for adaptive rate.
		data := fetcher(cfgPath, dbPath)
		sendEvent(data)
		lastHash := dashboardStateHash(data)

		ticker := time.NewTicker(fastInterval)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				data = fetcher(cfgPath, dbPath)
				newHash := dashboardStateHash(data)
				sendEvent(data)
				// Adaptive backoff: slow down when Castellarius is idle.
				idle := newHash == lastHash && data.FlowingCount == 0
				lastHash = newHash
				next := fastInterval
				if idle {
					next = slowInterval
				}
				ticker.Reset(next)
			}
		}
	}
}

// newDashboardMuxInternal returns an http.Handler for the web dashboard.
// tui may be nil; if so the /ws/tui endpoint closes connections immediately.
func newDashboardMuxInternal(cfgPath, dbPath string, tui *DashboardTUI) http.Handler {
	return newDashboardMuxInternalWith(cfgPath, dbPath, tui, fetchDashboardData, refreshInterval, idleRefreshInterval)
}

// newDashboardMuxInternalWith returns an http.Handler for the web dashboard with custom
// fetcher and refresh intervals. Exposed for testing.
func newDashboardMuxInternalWith(cfgPath, dbPath string, tui *DashboardTUI, fetcher func(cfg, db string) *DashboardData, fastInterval, slowInterval time.Duration) http.Handler {
	// Read dashboard_font_family fresh at server start so a cistern.yaml edit
	// followed by restarting ct dashboard --web takes effect without recompiling.
	// This is the supported update path: edit cistern.yaml, restart the server.
	fontFamily := dashboardDefaultFontFamily
	if cfg, err := aqueduct.ParseAqueductConfig(cfgPath); err == nil && cfg.DashboardFontFamily != "" {
		// Use json.Marshal to produce a fully JS-safe escaped string (handles
		// backslash, double-quote, newlines, </script> sequences, and Unicode
		// line/paragraph separators). Trim the surrounding JSON quotes since the
		// template already wraps the value in double-quotes.
		b, _ := json.Marshal(cfg.DashboardFontFamily)
		fontFamily = string(b[1 : len(b)-1])
	}
	html := strings.Replace(dashboardHTML, "__DASHBOARD_FONT_FAMILY__", fontFamily, 1)

	mux := http.NewServeMux()

	// Serve bundled xterm.js assets so the dashboard works in airgapped environments.
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
		fmt.Fprint(w, html)
	})

	mux.HandleFunc("/api/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data := fetcher(cfgPath, dbPath)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data) //nolint:errcheck
	})

	mux.HandleFunc("/api/dashboard/events", makeDashboardEventsHandler(cfgPath, dbPath, fetcher, fastInterval, slowInterval))

	// GET /api/aqueducts/{name}/peek — snapshot of current tmux pane output.
	mux.HandleFunc("/api/aqueducts/{name}/peek", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := r.PathValue("name")
		lines := parsePeekLines(r)
		sess, ok := lookupAqueductSession(dbPath, name)
		capturer := defaultCapturer
		if !ok || !capturer.HasSession(sess.sessionID) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, "session not active")
			return
		}
		content, err := capturer.Capture(sess.sessionID, lines)
		if err != nil {
			http.Error(w, fmt.Sprintf("capture error: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, stripANSI(content))
	})

	// WS /ws/aqueducts/{name}/peek — live streaming peek (poll every 500ms, send diffs).
	mux.HandleFunc("/ws/aqueducts/{name}/peek", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		lines := parsePeekLines(r)

		conn, brw, err := wsUpgrade(w, r)
		if err != nil {
			return // wsUpgrade already wrote the HTTP error
		}
		defer conn.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Reader goroutine: detects client close frames and network partitions.
		// Sets wsTuiReadTimeout on the connection so a silently-partitioned client
		// (no TCP FIN, no frames) is reaped after 5 minutes. Without this, when
		// tmux output is stable (no diffs) the ticker loop never writes and never
		// sets a write deadline — the goroutine and TCP connection leak indefinitely.
		go func() {
			defer cancel()
			buf := make([]byte, wsMaxClientPayload)
			conn.SetReadDeadline(time.Now().Add(wsTuiReadTimeout)) //nolint:errcheck
			for {
				opcode, _, nb, err := wsReadClientFrame(brw.Reader, buf)
				buf = nb
				if err != nil {
					return
				}
				conn.SetReadDeadline(time.Now().Add(wsTuiReadTimeout)) //nolint:errcheck
				if opcode == wsOpcodeClose {
					return
				}
			}
		}()

		var prev string
		capturer := defaultCapturer
		ticker := time.NewTicker(peekInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				next := "session not active"
				if sess, ok := lookupAqueductSession(dbPath, name); ok && capturer.HasSession(sess.sessionID) {
					content, err := capturer.Capture(sess.sessionID, lines)
					if err != nil {
						continue
					}
					next = stripANSI(content)
				}
				if diff := computeDiff(prev, next); diff != "" {
					conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)) //nolint:errcheck
					if wsSendText(brw.Writer, diff) != nil {
						return
					}
					prev = next
				}
			}
		}
	})

	// WS /ws/tui — attaches to the singleton DashboardTUI and streams raw ANSI
	// to xterm.js. The child process is NOT per-connection; it is owned by tui
	// and survives WebSocket disconnects.
	//
	// Protocol (client → server): JSON text frames for control messages.
	//   {"resize":{"cols":N,"rows":N}}  — resize PTY to match xterm.js viewport
	//
	// Protocol (server → client): binary frames containing raw PTY output bytes.
	// Binary frames are required because PTY output may contain non-UTF-8 byte
	// sequences; text frames with invalid UTF-8 cause browsers to close the WS.
	mux.HandleFunc("/ws/tui", func(w http.ResponseWriter, r *http.Request) {
		conn, brw, err := wsUpgrade(w, r)
		if err != nil {
			return
		}
		defer conn.Close()

		if tui == nil {
			return
		}

		// Attach to the singleton; receive the last complete repaint frame.
		client, lastFrame := tui.attach()
		defer tui.detach(client) // child process continues running on detach

		// Send the last complete frame so a connecting client sees a clean, current
		// TUI state before any new live frames arrive — no replay flicker.
		if len(lastFrame) > 0 {
			conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)) //nolint:errcheck
			if wsSendBinary(brw.Writer, lastFrame) != nil {
				return
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Goroutine B: read incoming WebSocket frames from the client.
		// Exits on read error, read deadline, or close frame; calls cancel().
		go func() {
			defer cancel()
			buf := make([]byte, wsMaxClientPayload)
			conn.SetReadDeadline(time.Now().Add(wsTuiReadTimeout)) //nolint:errcheck
			for {
				opcode, payload, nb, err := wsReadClientFrame(brw.Reader, buf)
				buf = nb
				if err != nil {
					return
				}
				conn.SetReadDeadline(time.Now().Add(wsTuiReadTimeout)) //nolint:errcheck
				switch opcode {
				case wsOpcodeText:
					handleTuiTextFrame(payload, tui, tui.resize)
				case wsOpcodeClose:
					return
				}
			}
		}()

		// Goroutine A (this goroutine): forward broadcast chunks to WebSocket.
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-client.ch:
				if !ok {
					return
				}
				conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)) //nolint:errcheck
				if wsSendBinary(brw.Writer, chunk) != nil {
					return
				}
			}
		}
	})

	return mux
}

// RunDashboardWeb starts the HTTP web dashboard on addr and blocks until
// SIGINT/SIGTERM is received or the server fails.
func RunDashboardWeb(cfgPath, dbPath, addr string) error {
	exe, _ := os.Executable()
	tui := newDashboardTUI(exe, cfgPath, dbPath)
	tui.Start()

	srv := &http.Server{
		Addr:              addr,
		Handler:           newDashboardMuxInternal(cfgPath, dbPath, tui),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      0, // SSE streams are long-lived
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "Cistern web dashboard listening on http://localhost%s\n", addr)

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
		err := srv.Shutdown(shutCtx)
		tui.Stop()
		return err
	case err := <-errCh:
		tui.Stop()
		return err
	}
}

// dashboardHTML is the single-page web dashboard. The aqueduct arch section
// uses CSS-based rendering (flexbox, CSS animations) for responsive mobile
// support. The remaining sections (current flow, cistern, recent flow) use
// pre-formatted HTML identical to the TUI colour palette.
const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Cistern</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
html,body{width:100%;height:100%;background:#0d1117;overflow:hidden}
/* Outer scroll container — pans the scaled terminal */
#scroll{
  width:100%;height:100%;
  overflow:auto;
  -webkit-overflow-scrolling:touch;
}
/* Terminal wrapper — transform-origin top-left so scale grows right/down */
#wrap{
  display:inline-block;
  transform-origin:top left;
  /* width/height set by JS to match scaled canvas size */
}
/* xterm.js scrollbar styling */
.xterm-viewport::-webkit-scrollbar{width:6px}
.xterm-viewport::-webkit-scrollbar-track{background:#0d1117}
.xterm-viewport::-webkit-scrollbar-thumb{background:#30363d;border-radius:3px}
.xterm-viewport{scrollbar-color:#30363d #0d1117;scrollbar-width:thin}
/* ESC = back hint — fixed corner overlay, always visible, subtle */
#esc-hint{position:fixed;bottom:10px;right:14px;z-index:9999;background:rgba(13,17,23,0.82);border:1px solid #30363d;border-radius:4px;padding:3px 8px;font-family:monospace;font-size:11px;color:#8b949e;cursor:pointer;user-select:none;-webkit-user-select:none;outline:none}
#esc-hint:hover{color:#e6edf3;border-color:#58a6ff}
</style>
<link rel="stylesheet" href="/static/xterm.min.css"/>
</head>
<body>
<div id="scroll"><div id="wrap"><div id="terminal"></div></div></div>
<button id="esc-hint" onclick="sendEsc()" title="Send Esc to terminal (back / close overlay)">ESC = back</button>
<script src="/static/xterm.min.js"></script>
<script src="/static/addon-fit.min.js"></script>
<script>
var term = new Terminal({
  theme: {
    background:          '#0d1117',
    foreground:          '#e6edf3',
    cursor:              '#58a6ff',
    cursorAccent:        '#0d1117',
    selectionBackground: '#264f78',
    black:   '#484f58', red:     '#ff7b72', green:   '#3fb950', yellow:  '#d29922',
    blue:    '#58a6ff', magenta: '#bc8cff', cyan:    '#39c5cf', white:   '#b1bac4',
    brightBlack:   '#6e7681', brightRed:     '#ffa198', brightGreen:   '#56d364',
    brightYellow:  '#e3b341', brightBlue:    '#79c0ff', brightMagenta: '#d2a8ff',
    brightCyan:    '#56d4dd', brightWhite:   '#f0f6fc'
  },
  /* Font stack injected from dashboard_font_family in cistern.yaml at server
     start. Falls back to dashboardDefaultFontFamily when the field is unset. */
  fontFamily: "__DASHBOARD_FONT_FAMILY__",
  fontSize: 13,
  lineHeight: 1.2,
  letterSpacing: 0,
  cursorBlink: false,
  scrollback: 1000,
  scrollOnUserInput: false,
  /* Allow Bubble Tea to use the full palette */
  allowProposedApi: true
});

var fitAddon = new FitAddon.FitAddon();
term.loadAddon(fitAddon);
term.open(document.getElementById('terminal'));

/* Forward all keystrokes to the PTY via WebSocket. xterm.js fires onData
   for every keypress with the raw terminal escape sequence (e.g. "\x1b[A"
   for up arrow). The server writes these bytes directly to the PTY stdin. */
term.onData(function(data) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(data);
  }
});

/* sendEsc forwards the Escape byte (\x1b) to the PTY. Called by the ESC hint
   button (onclick) and by the keydown capture listener below. */
function sendEsc() {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send('\x1b');
  }
}

/* Intercept Esc at the capture phase so the PTY receives it reliably even
   when xterm.js does not have keyboard focus or the browser would otherwise
   swallow it (e.g. to close a dialog or auto-complete dropdown).
   stopPropagation() prevents xterm.js from also firing term.onData for the
   same event, which would cause a double-send of \x1b to the PTY. */
document.addEventListener('keydown', function(e) {
  if (e.key === 'Escape') {
    e.preventDefault();
    e.stopPropagation();
    sendEsc();
  }
}, {capture:true});

var ws = null;
var scale = 0.75; /* default: render ~133% more content, scaled down to fit */
var minScale = 0.3;
var maxScale = 3.0;
var wrap = document.getElementById('wrap');
var scroll = document.getElementById('scroll');

/* Send resize to server when PTY dimensions change */
term.onResize(function(e) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({resize: {cols: e.cols, rows: e.rows}}));
  }
});

/* Fit terminal to the virtual (unscaled) area.
   By sizing the terminal element to viewport/scale before fitting, FitAddon
   calculates cols/rows for a larger area than the screen. Bubble Tea renders
   more content at higher detail; CSS scale then shrinks it to fit physically.
   At scale=0.6 (60%): terminal sees 167% of viewport → ~1.7x more content. */
function fitTerminal() {
  var termEl = document.getElementById('terminal');
  termEl.style.width  = Math.round(scroll.clientWidth  / scale) + 'px';
  termEl.style.height = Math.round(scroll.clientHeight / scale) + 'px';
  fitAddon.fit();
}

/* Apply CSS scale transform and update wrap dimensions so scroll container
   knows the actual (scaled) size of the content */
function applyScale() {
  var termEl = document.getElementById('terminal');
  var w = termEl.offsetWidth;
  var h = termEl.offsetHeight;
  wrap.style.transform = 'scale(' + scale + ')';
  /* Wrap must report scaled dimensions to outer scroll container */
  wrap.style.width  = Math.round(w * scale) + 'px';
  wrap.style.height = Math.round(h * scale) + 'px';
}

function initView() {
  fitTerminal();
  applyScale();
}

window.addEventListener('resize', function() {
  fitTerminal();
  applyScale();
});

requestAnimationFrame(initView);

/* ── Zoom controls ─────────────────────────────────────────────────────── */
var pinchStartDist = 0;
var pinchStartScale = 1;

function setScale(s) {
  scale = Math.max(minScale, Math.min(maxScale, s));
  fitTerminal();  /* recalculate cols/rows for new virtual area */
  applyScale();   /* update CSS transform and wrap dimensions */
}

/* Pinch-to-zoom (mobile touch) */
scroll.addEventListener('touchstart', function(e) {
  if (e.touches.length === 2) {
    var dx = e.touches[0].clientX - e.touches[1].clientX;
    var dy = e.touches[0].clientY - e.touches[1].clientY;
    pinchStartDist  = Math.sqrt(dx*dx + dy*dy);
    pinchStartScale = scale;
    e.preventDefault();
  }
}, {passive: false});

scroll.addEventListener('touchmove', function(e) {
  if (e.touches.length === 2) {
    var dx = e.touches[0].clientX - e.touches[1].clientX;
    var dy = e.touches[0].clientY - e.touches[1].clientY;
    var dist = Math.sqrt(dx*dx + dy*dy);
    if (pinchStartDist > 0) {
      setScale(pinchStartScale * (dist / pinchStartDist));
    }
    e.preventDefault();
  }
}, {passive: false});

scroll.addEventListener('touchend', function(e) {
  if (e.touches.length < 2) pinchStartDist = 0;
}, {passive: false});

/* Safari gesture events (trackpad pinch on Mac/iPad) */
scroll.addEventListener('gesturestart', function(e) {
  pinchStartScale = scale;
  e.preventDefault();
}, {passive: false});

scroll.addEventListener('gesturechange', function(e) {
  setScale(pinchStartScale * e.scale);
  e.preventDefault();
}, {passive: false});

/* Ctrl/Cmd + scroll wheel zoom (desktop) */
scroll.addEventListener('wheel', function(e) {
  if (e.ctrlKey || e.metaKey) {
    e.preventDefault();
    var factor = e.deltaY > 0 ? 0.9 : 1.1;
    setScale(scale * factor);
  }
}, {passive: false});

function connect() {
  var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(proto + '//' + location.host + '/ws/tui');
  ws.binaryType = 'arraybuffer';

  ws.onopen = function() {
    term.clear();
    /* Send current size immediately on connect */
    ws.send(JSON.stringify({resize: {cols: term.cols, rows: term.rows}}));
  };

  ws.onmessage = function(e) {
    if (e.data instanceof ArrayBuffer) {
      term.write(new Uint8Array(e.data));
    } else {
      term.write(e.data);
    }
  };

  ws.onclose = function() {
    term.write('\r\n\x1b[2m\u2500\u2500\u2500 disconnected \u2014 reconnecting in 3s \u2500\u2500\u2500\x1b[0m\r\n');
    setTimeout(connect, 3000);
  };

  ws.onerror = function() { ws.close(); };
}

connect();
</script>
</body>
</html>`
