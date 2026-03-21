package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
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

	return mux
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

// dashboardHTML is the single-page web dashboard.
const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Cistern</title>
<style>
:root {
  --bg:#0d1117; --surface:#161b22; --border:#30363d;
  --text:#e6edf3; --dim:#7d8590; --green:#3fb950;
  --yellow:#d29922; --red:#f85149; --blue:#58a6ff;
  --font:'Courier New',Courier,monospace;
}
@media (prefers-color-scheme:light) {
  :root {
    --bg:#ffffff; --surface:#f6f8fa; --border:#d0d7de;
    --text:#1f2328; --dim:#636c76; --green:#1a7f37;
    --yellow:#9a6700; --red:#cf222e; --blue:#0969da;
  }
}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font-family:var(--font);font-size:13px;line-height:1.5;padding:12px;min-height:100vh}
h1{font-size:15px;color:var(--dim);letter-spacing:2px;text-transform:uppercase;margin-bottom:10px}
.sep{border:none;border-top:1px solid var(--border);margin:10px 0}
.section-title{font-size:10px;color:var(--dim);letter-spacing:1px;text-transform:uppercase;margin-bottom:6px}
#conn{font-size:11px;margin-bottom:8px}
.live{color:var(--green)}
.offline{color:var(--red)}
/* Aqueduct cards */
.aqueduct{border:1px solid var(--border);border-radius:4px;margin-bottom:8px;overflow:hidden}
.aq-name{font-size:10px;color:var(--dim);padding:3px 8px;background:var(--surface);border-bottom:1px solid var(--border)}
.aq-channel{padding:5px 8px;text-align:center;font-size:12px;border-bottom:1px solid var(--border)}
.aq-channel.active{color:var(--green);background:rgba(63,185,80,.08)}
.aq-channel.idle{color:var(--dim)}
.aq-piers{display:flex;align-items:flex-start;padding:8px 4px;overflow-x:auto}
.aq-pier{display:flex;flex-direction:column;align-items:center;min-width:64px;flex:1}
.pier-connector{flex:1;height:2px;background:var(--border);min-width:4px;margin-top:12px;align-self:flex-start}
.pier-dot{width:26px;height:26px;border-radius:50%;border:2px solid var(--dim);display:flex;align-items:center;justify-content:center;font-size:11px;color:var(--dim)}
.pier-dot.active{border-color:var(--green);color:var(--green);background:rgba(63,185,80,.1)}
.pier-name{font-size:10px;color:var(--dim);text-align:center;margin-top:3px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:64px;padding:0 2px}
.pier-name.active{color:var(--green)}
/* Stats */
.stats{display:flex;gap:20px;flex-wrap:wrap;margin:8px 0}
.stat-num{font-size:22px;font-weight:bold}
.stat-lbl{font-size:10px;color:var(--dim);text-transform:uppercase}
.flowing .stat-num{color:var(--green)}
.queued .stat-num{color:var(--yellow)}
.done .stat-num{color:var(--dim)}
/* Flow activities */
.activity{border:1px solid var(--border);border-radius:3px;padding:8px;margin-bottom:6px}
.act-header{display:flex;gap:8px;align-items:baseline;flex-wrap:wrap;margin-bottom:3px}
.act-id{color:var(--blue);font-size:12px;white-space:nowrap}
.act-title{font-size:12px;flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.act-step{font-size:11px;color:var(--green)}
.note{border-top:1px solid var(--border);margin-top:4px;padding-top:3px;font-size:11px}
.note-who{color:var(--blue)}
.note-body{color:var(--dim)}
/* Tables */
.items{width:100%;border-collapse:collapse}
.items td{padding:4px 4px;font-size:12px;border-bottom:1px solid var(--border);vertical-align:top}
.items tr:last-child td{border-bottom:none}
.t-id{color:var(--blue);white-space:nowrap}
.t-title{word-break:break-word}
.t-blocked{color:var(--red);font-size:11px;white-space:nowrap}
.s-delivered{color:var(--green)}
.s-stagnant{color:var(--red)}
.s-in_progress{color:var(--yellow)}
.empty{color:var(--dim);font-size:12px;padding:2px 0}
footer{color:var(--dim);font-size:11px;margin-top:12px;padding-top:8px;border-top:1px solid var(--border)}
@media(max-width:480px){body{padding:8px}}
/* Peek modal */
.peek-overlay{position:fixed;inset:0;background:rgba(0,0,0,.65);display:none;z-index:100;align-items:center;justify-content:center}
.peek-overlay.open{display:flex}
.peek-panel{background:var(--surface);border:1px solid var(--border);border-radius:4px;width:90vw;max-width:900px;height:70vh;display:flex;flex-direction:column;overflow:hidden}
.peek-hdr{padding:6px 10px;border-bottom:1px solid var(--border);display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.peek-ro-label{color:var(--green);font-size:11px;font-weight:bold;white-space:nowrap}
.peek-title{flex:1;font-size:12px;color:var(--dim);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.peek-btn{background:none;border:1px solid var(--border);color:var(--dim);font-size:11px;padding:2px 6px;cursor:pointer;border-radius:3px;font-family:var(--font)}
.peek-btn:hover{color:var(--text);border-color:var(--dim)}
.peek-content{flex:1;overflow-y:auto;padding:8px;font-size:12px;white-space:pre-wrap;word-break:break-all;color:var(--text);background:var(--bg)}
.peek-footer{padding:3px 10px;border-top:1px solid var(--border);font-size:11px;color:var(--dim)}
.aq-channel.active{cursor:pointer}
.aq-channel.active:hover{background:rgba(63,185,80,.16)}
</style>
</head>
<body>
<h1>&#x2697; Cistern</h1>
<div id="conn" class="offline">&#x25CB; connecting&#x2026;</div>
<div id="app"></div>
<div id="peek-overlay" class="peek-overlay">
  <div class="peek-panel">
    <div class="peek-hdr">
      <span class="peek-ro-label">Observing &#x2014; read only</span>
      <span id="peek-title" class="peek-title"></span>
      <button class="peek-btn" id="peek-pin-btn" onclick="peekTogglePin()">pin scroll</button>
      <button class="peek-btn" onclick="peekClose()">&#x2715; close</button>
    </div>
    <div id="peek-content" class="peek-content">(connecting&#x2026;)</div>
    <div id="peek-footer" class="peek-footer">connecting&#x2026;</div>
  </div>
</div>
<script>
var app=document.getElementById('app');
var connEl=document.getElementById('conn');

function esc(s){
  if(s==null)return'';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function bar(idx,total,w){
  if(total<=0||idx<=0)return'\u2591'.repeat(w);
  var f=Math.min(Math.floor(idx*w/total),w);
  return'\u2588'.repeat(f)+'\u2591'.repeat(w-f);
}

function fmtNs(ns){
  var s=Math.floor(ns/1e9);
  if(s<0)return'0s';
  if(s<60)return s+'s';
  var m=Math.floor(s/60);
  return m+'m '+(s%60)+'s';
}

function render(d){
  var h='';

  // Aqueducts
  h+='<div class="section"><div class="section-title">Aqueducts</div>';
  var chs=d.cataractae||[];
  if(!chs.length){
    h+='<div class="empty">No aqueducts configured.</div>';
  } else {
    for(var i=0;i<chs.length;i++){
      var ch=chs[i];
      h+='<div class="aqueduct">';
      h+='<div class="aq-name">'+esc(ch.name)+'</div>';
      if(ch.droplet_id){
        h+='<div class="aq-channel active" data-aqname="'+esc(ch.name)+'" title="Click to observe live session">\u2248\u2248  '+esc(ch.droplet_id)+'  '+bar(ch.cataractae_index,ch.total_cataractae,8)+'  '+fmtNs(ch.elapsed)+'  \u2248\u2248</div>';
      } else {
        h+='<div class="aq-channel idle">\u2014 idle \u2014</div>';
      }
      var steps=ch.steps||[];
      if(steps.length){
        h+='<div class="aq-piers">';
        for(var j=0;j<steps.length;j++){
          var step=steps[j];
          var act=(step===ch.step&&!!ch.droplet_id);
          if(j>0)h+='<div class="pier-connector"></div>';
          h+='<div class="aq-pier">';
          h+='<div class="pier-dot'+(act?' active':'')+'">'+( act?'\u25cf':'\u25cb')+'</div>';
          h+='<div class="pier-name'+(act?' active':'')+'">'+esc(step)+'</div>';
          h+='</div>';
        }
        h+='</div>';
      }
      h+='</div>';
    }
  }
  h+='</div><hr class="sep">';

  // Stats
  h+='<div class="stats"><div class="stat flowing"><div class="stat-num">'+(d.flowing_count||0)+'</div><div class="stat-lbl">flowing</div></div>';
  h+='<div class="stat queued"><div class="stat-num">'+(d.queued_count||0)+'</div><div class="stat-lbl">queued</div></div>';
  h+='<div class="stat done"><div class="stat-num">'+(d.done_count||0)+'</div><div class="stat-lbl">delivered</div></div></div><hr class="sep">';

  // Current flow
  h+='<div class="section"><div class="section-title">Current Flow</div>';
  var acts=d.flow_activities||[];
  if(!acts.length){
    h+='<div class="empty">No active flow.</div>';
  } else {
    for(var i=0;i<acts.length;i++){
      var a=acts[i];
      h+='<div class="activity"><div class="act-header"><span class="act-id">'+esc(a.droplet_id)+'</span><span class="act-title">'+esc(a.title)+'</span></div>';
      h+='<div class="act-step">\u2192 '+esc(a.step)+'</div>';
      var notes=a.recent_notes||[];
      for(var k=0;k<notes.length;k++){
        var n=notes[k];
        h+='<div class="note"><span class="note-who">'+esc(n.cataractae_name)+'</span> <span class="note-body">'+esc(n.content)+'</span></div>';
      }
      h+='</div>';
    }
  }
  h+='</div><hr class="sep">';

  // Cistern
  h+='<div class="section"><div class="section-title">Cistern</div>';
  var queued=(d.cistern_items||[]).filter(function(x){return x.status==='open';});
  if(!queued.length){
    h+='<div class="empty">Cistern is empty.</div>';
  } else {
    h+='<table class="items">';
    for(var i=0;i<queued.length;i++){
      var item=queued[i];
      var blocked=d.blocked_by_map&&d.blocked_by_map[item.id];
      h+='<tr><td class="t-id">'+esc(item.id)+'</td><td class="t-title">'+esc(item.title)+'</td>';
      h+=blocked?'<td class="t-blocked">[blocked by '+esc(blocked)+']</td>':'<td></td>';
      h+='</tr>';
    }
    h+='</table>';
  }
  h+='</div><hr class="sep">';

  // Recent flow
  h+='<div class="section"><div class="section-title">Recent Flow</div>';
  var recent=d.recent_items||[];
  if(!recent.length){
    h+='<div class="empty">No recent flow.</div>';
  } else {
    h+='<table class="items">';
    for(var i=0;i<recent.length;i++){
      var item=recent[i];
      var t=item.updated_at?new Date(item.updated_at).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'}):'';
      h+='<tr><td class="t-id">'+esc(item.id)+'</td><td class="t-title">'+esc(item.title)+'</td>';
      h+='<td class="s-'+esc(item.status)+'">'+esc(item.status)+'</td><td style="color:var(--dim)">'+t+'</td></tr>';
    }
    h+='</table>';
  }
  h+='</div>';

  // Footer
  var ts=d.fetched_at?new Date(d.fetched_at).toLocaleTimeString():'';
  h+='<footer>last update: '+ts+'</footer>';

  app.innerHTML=h;
}

function connect(){
  connEl.className='offline';
  connEl.textContent='\u25cb connecting\u2026';
  var es=new EventSource('/api/dashboard/events');
  es.onopen=function(){
    connEl.className='live';
    connEl.textContent='\u25cf live';
  };
  es.onmessage=function(e){
    try{render(JSON.parse(e.data));}catch(err){console.error('cistern parse:',err);}
  };
  es.onerror=function(){
    connEl.className='offline';
    connEl.textContent='\u25cb reconnecting\u2026';
    es.close();
    setTimeout(connect,3000);
  };
}
connect();

// --- Peek modal ---
var peekWs=null;
var peekPinned=false;
var peekAqName='';

function peekOpen(name){
  peekAqName=name;
  peekPinned=false;
  document.getElementById('peek-title').textContent=name;
  document.getElementById('peek-content').textContent='(connecting\u2026)';
  document.getElementById('peek-footer').textContent='connecting\u2026';
  document.getElementById('peek-pin-btn').textContent='pin scroll';
  document.getElementById('peek-pin-btn').style.color='';
  document.getElementById('peek-overlay').classList.add('open');
  peekConnect(name);
}

function peekClose(){
  document.getElementById('peek-overlay').classList.remove('open');
  if(peekWs){peekWs.close();peekWs=null;}
  peekAqName='';
}

function peekTogglePin(){
  peekPinned=!peekPinned;
  var btn=document.getElementById('peek-pin-btn');
  btn.textContent=peekPinned?'unpin scroll':'pin scroll';
  btn.style.color=peekPinned?'var(--green)':'';
  document.getElementById('peek-footer').textContent=peekPinned?'scroll pinned \u2014 click to unpin':'auto-scroll active';
}

function peekConnect(name){
  if(peekWs){peekWs.close();}
  var proto=location.protocol==='https:'?'wss:':'ws:';
  var url=proto+'//'+location.host+'/ws/aqueducts/'+encodeURIComponent(name)+'/peek';
  var ws=new WebSocket(url);
  peekWs=ws;
  ws.onopen=function(){
    document.getElementById('peek-footer').textContent='Observing \u2014 read only';
  };
  ws.onmessage=function(e){
    if(!e.data)return;
    var el=document.getElementById('peek-content');
    el.textContent=e.data;
    if(!peekPinned){el.scrollTop=el.scrollHeight;}
  };
  ws.onerror=function(){
    document.getElementById('peek-footer').textContent='connection error';
  };
  ws.onclose=function(){
    if(document.getElementById('peek-overlay').classList.contains('open')&&peekAqName){
      document.getElementById('peek-footer').textContent='disconnected \u2014 retrying in 3s\u2026';
      setTimeout(function(){if(peekAqName)peekConnect(peekAqName);},3000);
    }
  };
}

// Close peek when clicking the backdrop.
document.getElementById('peek-overlay').addEventListener('click',function(e){
  if(e.target===this)peekClose();
});
// Open peek when clicking an active aqueduct channel.
app.addEventListener('click',function(e){
  var el=e.target.closest&&e.target.closest('[data-aqname]');
  if(el)peekOpen(el.dataset.aqname);
});
</script>
</body>
</html>`
