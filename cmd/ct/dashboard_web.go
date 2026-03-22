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
	payload := []byte(data)
	n := len(payload)
	header := make([]byte, 0, 10)
	header = append(header, 0x81) // FIN=1, opcode=0x1 (text)
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
	// Bubble Tea requires a real PTY to render correctly; piping to io.Writer
	// produces plain text only. This gives pixel-perfect TUI output in the browser.
	mux.HandleFunc("/ws/tui", func(w http.ResponseWriter, r *http.Request) {
		conn, brw, err := wsUpgrade(w, r)
		if err != nil {
			return
		}
		defer conn.Close()

		// Spawn ct dashboard as a subprocess attached to a PTY.
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

		// Set initial PTY size.
		_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 200})

		// Forward PTY output → WebSocket.
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)) //nolint:errcheck
				if wsSendText(brw.Writer, string(buf[:n])) != nil {
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
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Cistern</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
html,body{width:100%;height:100%;background:#0d1117;overflow:hidden}
#terminal{width:100%;height:100%}
.xterm-viewport{scrollbar-color:#30363d #0d1117}
</style>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/xterm@5.3.0/css/xterm.min.css"/>
</head>
<body>
<div id="terminal"></div>
<script src="https://cdn.jsdelivr.net/npm/xterm@5.3.0/lib/xterm.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/xterm-addon-fit@0.8.0/lib/xterm-addon-fit.min.js"></script>
<script>
var term=new Terminal({
  theme:{background:'#0d1117',foreground:'#e6edf3',cursor:'#e6edf3',selectionBackground:'#264f78'},
  fontFamily:"'Cascadia Code','Courier New',Courier,monospace",
  fontSize:13,
  convertEol:true,
  scrollback:1000
});
var fitAddon=new FitAddon.FitAddon();
term.loadAddon(fitAddon);
term.open(document.getElementById('terminal'));
fitAddon.fit();

window.addEventListener('resize',function(){fitAddon.fit();});

function connect(){
  var proto=location.protocol==='https:'?'wss:':'ws:';
  var ws=new WebSocket(proto+'//'+location.host+'/ws/tui');
  ws.onopen=function(){term.clear();};
  ws.onmessage=function(e){term.write(e.data);};
  ws.onclose=function(){
    term.write('\r\n\x1b[2m--- disconnected, reconnecting in 3s ---\x1b[0m\r\n');
    setTimeout(connect,3000);
  };
  ws.onerror=function(){ws.close();};
}
connect();
</script>
</body>
</html>`
