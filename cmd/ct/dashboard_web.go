package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/creack/pty"
)

// wsWriteTimeout is the per-frame write deadline set on the hijacked net.Conn
// before each wsSendText call. Without this, a client that disappears via a
// network partition (no TCP FIN) causes the goroutine to block indefinitely
// inside bufio.Writer.Flush.
const wsWriteTimeout = 10 * time.Second

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
	return wsSendFrame(w, 0x81, []byte(data))
}

// wsSendBinary writes a WebSocket binary frame (opcode 0x82).
// Use for raw PTY output which may contain non-UTF-8 bytes — text frames
// with invalid UTF-8 cause browsers to close the connection immediately.
func wsSendBinary(w *bufio.Writer, data []byte) error {
	return wsSendFrame(w, 0x82, data)
}

func wsSendFrame(w *bufio.Writer, opcode byte, payload []byte) error {
	n := len(payload)
	header := make([]byte, 0, 10)
	header = append(header, opcode) // FIN=1
	switch {
	case n < 126:
		header = append(header, byte(n))
	case n < 65536:
		header = append(header, 0x7E)
		header = binary.BigEndian.AppendUint16(header, uint16(n))
	default:
		header = append(header, 0x7F)
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

// wsUpgrade performs the RFC 6455 handshake. On success it returns the hijacked
// connection and its buffered read-writer. On failure it writes an HTTP error
// and returns a non-nil error.
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

// newDashboardMux returns an http.Handler for the web dashboard.
// Exposed for testing.
func newDashboardMux(cfgPath, dbPath string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, dashboardHTML)
	})

	mux.HandleFunc("/api/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data := fetchDashboardData(cfgPath, dbPath)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data) //nolint:errcheck
	})

	mux.HandleFunc("/api/dashboard/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		send := func() {
			data := fetchDashboardData(cfgPath, dbPath)
			b, err := json.Marshal(data)
			if err != nil {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}

		send()

		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				send()
			}
		}
	})

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

		var prev string
		capturer := defaultCapturer
		ticker := time.NewTicker(peekInterval)
		defer ticker.Stop()

		for range ticker.C {
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
	})

	// WS /ws/tui — runs ct dashboard in a PTY and streams raw ANSI to xterm.js.
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

		exe, err := os.Executable()
		if err != nil {
			return
		}
		cmd := exec.Command(exe, "dashboard", "--db", dbPath)
		cmd.Env = append(os.Environ(), "CT_CISTERN_CONFIG="+cfgPath)

		ptmx, err := pty.Start(cmd)
		if err != nil {
			return
		}
		defer func() {
			ptmx.Close()
			cmd.Process.Kill() //nolint:errcheck
		}()

		// Default size — will be overridden by the client's first resize message.
		_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

		// Read incoming WebSocket frames from the client.
		// Frames are read via the raw connection; we handle text (control) and
		// binary (keyboard input) frames.
		go func() {
			buf := make([]byte, 4096)
			for {
				// Read a WebSocket frame header.
				header := make([]byte, 2)
				if _, err := io.ReadFull(brw.Reader, header); err != nil {
					return
				}
				opcode := header[0] & 0x0F
				masked := header[1]&0x80 != 0
				rawLen := int(header[1] & 0x7F)

				var payloadLen int
				switch rawLen {
				case 126:
					ext := make([]byte, 2)
					if _, err := io.ReadFull(brw.Reader, ext); err != nil {
						return
					}
					payloadLen = int(ext[0])<<8 | int(ext[1])
				case 127:
					ext := make([]byte, 8)
					if _, err := io.ReadFull(brw.Reader, ext); err != nil {
						return
					}
					payloadLen = int(ext[4])<<24 | int(ext[5])<<16 | int(ext[6])<<8 | int(ext[7])
				default:
					payloadLen = rawLen
				}

				var mask [4]byte
				if masked {
					if _, err := io.ReadFull(brw.Reader, mask[:]); err != nil {
						return
					}
				}

				if payloadLen > len(buf) {
					buf = make([]byte, payloadLen)
				}
				if _, err := io.ReadFull(brw.Reader, buf[:payloadLen]); err != nil {
					return
				}
				if masked {
					for i := range buf[:payloadLen] {
						buf[i] ^= mask[i%4]
					}
				}

				payload := buf[:payloadLen]

				switch opcode {
				case 0x1: // text frame — control message (resize)
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
					}
				case 0x8: // close frame
					return
				}
			}
		}()

		// Forward PTY output → WebSocket as binary frames.
		out := make([]byte, 4096)
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

// stripClearWriter wraps an io.Writer and strips ANSI clear-screen sequences
// (\033[2J\033[H) from the stream. xterm.js renders incrementally — clear
// codes cause visible flashing and can corrupt multi-frame renders.
type stripClearWriter struct{ w io.Writer }

func (s *stripClearWriter) Write(p []byte) (int, error) {
	clean := strings.ReplaceAll(string(p), "\033[2J\033[H", "")
	n, err := s.w.Write([]byte(clean))
	if err != nil {
		return n, err
	}
	return len(p), nil // report original len to avoid short-write errors
}

// RunDashboardWeb starts the HTTP web dashboard on addr and blocks until
// SIGINT/SIGTERM is received or the server fails.
func RunDashboardWeb(cfgPath, dbPath, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           newDashboardMux(cfgPath, dbPath),
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
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
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
<meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no">
<title>Cistern</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
html,body{width:100%;height:100%;background:#0d1117;overflow:hidden;touch-action:none}
#terminal{width:100%;height:100%;position:absolute;inset:0}
/* Scrollbar styling */
.xterm-viewport::-webkit-scrollbar{width:6px}
.xterm-viewport::-webkit-scrollbar-track{background:#0d1117}
.xterm-viewport::-webkit-scrollbar-thumb{background:#30363d;border-radius:3px}
.xterm-viewport{scrollbar-color:#30363d #0d1117;scrollbar-width:thin}
</style>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/css/xterm.min.css"/>
</head>
<body>
<div id="terminal"></div>
<script src="https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/lib/xterm.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/@xterm/addon-fit@0.10.0/lib/addon-fit.min.js"></script>
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
  /* Font stack: prefer fonts known to have good Unicode box-drawing coverage.
     Cascadia Code (Windows Terminal), DejaVu Sans Mono, and JetBrains Mono all
     render box-drawing chars correctly. Fall back to system monospace. */
  fontFamily: "'Cascadia Code','JetBrains Mono','DejaVu Sans Mono','Fira Code','Menlo','Consolas','Liberation Mono',monospace",
  fontSize: 13,
  lineHeight: 1.2,
  letterSpacing: 0,
  cursorBlink: false,
  scrollback: 500,
  /* Allow Bubble Tea to use the full palette */
  allowProposedApi: true
});

var fitAddon = new FitAddon.FitAddon();
term.loadAddon(fitAddon);
term.open(document.getElementById('terminal'));

var ws = null;

/* Send resize message to server whenever xterm.js changes dimensions */
term.onResize(function(e) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({resize: {cols: e.cols, rows: e.rows}}));
  }
});

function fitAndResize() {
  fitAddon.fit();
  /* onResize fires automatically after fitAddon.fit() changes dimensions */
}

window.addEventListener('resize', fitAndResize);

/* Initial fit after the element is visible */
requestAnimationFrame(function() { fitAndResize(); });

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
