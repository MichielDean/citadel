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
body{background:#0d1117;color:#e6edf3;font-family:'Cascadia Code','Courier New',Courier,monospace;font-size:13px;line-height:1.3}
#conn{font-size:11px;padding:3px 8px;color:#e06c75}
#conn.live{color:#4bb96e}
#header{padding:8px 8px 4px;border-bottom:1px solid #30363d;margin-bottom:4px}
#screen{padding:0 8px;white-space:pre;overflow-x:auto;cursor:default}
/* ── CSS Arch Section ─────────────────────────────────────────────────────── */
#arch-section{padding:0 8px}
.aq-block{margin-bottom:4px}
.aq-active{border:1px solid #30363d;border-radius:4px;overflow:hidden}
.aq-hdr{padding:3px 8px;background:#161b22;display:flex;gap:10px;align-items:baseline;border-bottom:1px solid #30363d;flex-wrap:wrap}
.aq-name{color:#e6edf3;font-weight:bold;font-size:0.875rem;min-width:8ch}
.aq-repo{color:#46465a;font-size:0.75rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.aq-channel-row{display:flex;height:44px;border-bottom:1px solid #30363d;overflow:hidden}
.aq-channel{flex:1;position:relative;overflow:hidden;display:flex;align-items:center;justify-content:center;min-width:0}
.aq-channel.clickable{cursor:pointer}
.aq-wave{position:absolute;inset:0;background:#0d2a30;opacity:0.6}
.aq-info{position:relative;z-index:1;display:flex;align-items:center;gap:8px;padding:0 12px;font-size:0.8125rem;white-space:nowrap;overflow:hidden}
.aq-info.idle{color:#46465a}
.aq-info.revised{color:#f0c86b}
.aq-droplet-id{color:#e6edf3;font-weight:bold}
.aq-elapsed{color:#9db1db}
.aq-revised-mark{color:#f0c86b}
.aq-pbar{height:8px;width:80px;background:#1c2128;border-radius:2px;overflow:hidden;flex-shrink:0;border:1px solid #30363d}
.aq-pbar-fill{height:100%;background:#4bb96e}
.aq-waterfall{width:1px;flex-shrink:0;background:#30363d}
.aq-piers{display:flex}
.aq-pier{flex:1;min-height:40px;min-width:0;border-right:1px solid #30363d;border-bottom:1px solid #30363d}
.aq-pier:last-child{border-right:none}
.aq-pier.active{background:rgba(75,185,110,0.08);box-shadow:inset 0 0 0 1px #4bb96e}
.aq-labels{display:flex}
.aq-lbl{flex:1;min-width:0;text-align:center;font-size:0.875rem;color:#46465a;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;padding:3px 2px 4px}
.aq-lbl.active{color:#4bb96e;font-weight:bold}
.aq-idle-section{margin-top:2px}
.aq-idle{display:flex;gap:8px;padding:2px 4px;align-items:center;font-size:0.875rem;color:#46465a}
.aq-idle-name{min-width:10ch;flex-shrink:0}
.aq-idle-repo{min-width:14ch;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.aq-idle-dot{flex-shrink:0}
.aq-empty{color:#46465a;padding:4px 0;font-size:0.875rem}

@media(max-width:480px){
.aq-piers,.aq-labels{flex-wrap:wrap}
.aq-pier,.aq-lbl{flex:0 0 50%}
.aq-pier:nth-child(2n){border-right:none}
.aq-pier{border-bottom:1px solid #30363d}
.aq-info{font-size:0.75rem;gap:6px}
.aq-pbar{width:60px}
.aq-channel-row{height:48px}
}
/* ── Peek overlay ─────────────────────────────────────────────────────────── */
.peek-overlay{position:fixed;inset:0;background:rgba(0,0,0,.65);display:none;z-index:100;align-items:center;justify-content:center}
.peek-overlay.open{display:flex}
.peek-panel{background:#161b22;border:1px solid #30363d;border-radius:4px;width:90vw;max-width:900px;height:70vh;display:flex;flex-direction:column;overflow:hidden}
.peek-hdr{padding:6px 10px;border-bottom:1px solid #30363d;display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.peek-ro-label{color:#4bb96e;font-size:11px;font-weight:bold;white-space:nowrap}
.peek-title{flex:1;font-size:12px;color:#7d8590;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.peek-btn{background:none;border:1px solid #30363d;color:#7d8590;font-size:11px;padding:2px 6px;cursor:pointer;border-radius:3px;font-family:inherit;min-height:44px}
.peek-btn:hover{color:#e6edf3;border-color:#7d8590}
.peek-content{flex:1;overflow-y:auto;padding:8px;font-size:12px;white-space:pre-wrap;word-break:break-all;color:#e6edf3;background:#0d1117}
.peek-footer{padding:3px 10px;border-top:1px solid #30363d;font-size:11px;color:#7d8590}
</style>
</head>
<body>
<div id="conn">&#x25CB; connecting&#x2026;</div>
<div id="header"></div>
<div id="arch-section"></div>
<pre id="screen"></pre>
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
var headerEl=document.getElementById('header');
var archSectionEl=document.getElementById('arch-section');
var screenEl=document.getElementById('screen');
var connEl=document.getElementById('conn');

// TUI palette — mirrors dashboard_tui.go style vars exactly.
var cDim='#46465a',cGreen='#4bb96e',cYellow='#f0c86b',cRed='#e06c75';
var cHeader='#9db1db',cFoot='#36364a';

var SCR_W=120; // screen width (chars)

function esc(s){
  if(s==null)return'';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// Wrap text in a coloured span. text is HTML-escaped inside.
function sp(color,text,bold){
  if(text==null||text==='')return'';
  var st='color:'+color;
  if(bold)st+=';font-weight:bold';
  return'<span style="'+st+'">'+esc(text)+'</span>';
}

// Pad string to width w (codepoint-aware).
function padR(s,w){
  s=String(s||'');
  var r=Array.from(s);
  if(r.length>=w)return r.slice(0,w).join('');
  return s+' '.repeat(w-r.length);
}

// Centre string in width w, truncate with ellipsis if too long.
function padC(s,w){
  var r=Array.from(s);
  if(r.length>w)return r.slice(0,w-1).join('')+'\u2026';
  var tot=w-r.length,l=Math.floor(tot/2),ri=tot-l;
  return' '.repeat(l)+s+' '.repeat(ri);
}

function fmtNs(ns){
  var s=Math.floor(ns/1e9);
  if(s<0)return'0s';
  if(s<60)return s+'s';
  var m=Math.floor(s/60);
  return m+'m '+(s%60)+'s';
}

// Relative timestamp: mirrors viewCurrentFlow note timestamp logic in TUI.
function relAge(iso){
  if(!iso)return'';
  var age=Math.floor((Date.now()-new Date(iso).getTime())/1000);
  if(age<60)return'just now';
  if(age<3600)return Math.floor(age/60)+'m ago';
  return new Date(iso).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'});
}

// Truncate string to n codepoints, appending ellipsis.
function trunc(s,n){
  var r=Array.from(s||'');
  if(r.length<=n)return s||'';
  return r.slice(0,n-1).join('')+'\u2026';
}

// First non-empty, non-comment line from multi-line note content.
// Mirrors firstMeaningfulLine() in dashboard_tui.go.
function firstLine(txt){
  var ls=(txt||'').split('\n');
  for(var i=0;i<ls.length;i++){
    var l=ls[i].trim();
    if(l&&l.charAt(0)!=='#'&&l.indexOf('---')!==0)return l;
  }
  return(txt||'').trim();
}

// ── CSS Arch Section ──────────────────────────────────────────────────────────
// buildActiveArch renders one active aqueduct as a CSS flexbox card.
// Channel row: scrolling gradient animation. Piers: CSS boxes with borders.
// Waterfall: CSS gradient strip with falling animation. Labels below piers.
function buildActiveArch(ch){
  var steps=(ch.steps&&ch.steps.length)?ch.steps:['\u2014'];
  var n=steps.length;
  function isAct(s){return s===ch.step&&!!ch.droplet_id;}

  var infoClass='aq-info',infoHTML='';
  if(ch.droplet_id){
    if(ch.note_count>0)infoClass+=' revised';
    var pct=ch.total_cataractae>0?Math.min(100,Math.floor(ch.cataractae_index*100/ch.total_cataractae)):0;
    var rev=ch.note_count>0?'<span class="aq-revised-mark">\u267b</span> ':'';
    infoHTML=rev
      +'<span class="aq-droplet-id">'+esc(ch.droplet_id)+'</span>'
      +'<span class="aq-elapsed">'+esc(fmtNs(ch.elapsed))+'</span>'
      +'<div class="aq-pbar"><div class="aq-pbar-fill" style="width:'+pct+'%"></div></div>';
  }else{
    infoClass+=' idle';
    infoHTML='\u2014 idle \u2014';
  }

  var chanAttrs=ch.droplet_id
    ?' class="aq-channel clickable" data-aqname="'+esc(ch.name)+'"'
    :' class="aq-channel"';

  var piersHTML='',labelsHTML='';
  for(var i=0;i<n;i++){
    var step=steps[i],act=isAct(step);
    piersHTML+='<div class="aq-pier'+(act?' active':'')+'"></div>';
    labelsHTML+='<div class="aq-lbl'+(act?' active':'')+'">' +esc(trunc(step,18))+'</div>';
  }

  return'<div class="aq-block">'
    +'<div class="aq-active">'
    +'<div class="aq-hdr">'
    +'<span class="aq-name">'+esc(ch.name)+'</span>'
    +'<span class="aq-repo">'+esc(ch.repo_name||'')+'</span>'
    +'</div>'
    +'<div class="aq-channel-row">'
    +'<div'+chanAttrs+'>'
    +'<div class="aq-wave"></div>'
    +'<div class="'+infoClass+'">'+infoHTML+'</div>'
    +'</div>'
    +'<div class="aq-waterfall"></div>'
    +'</div>'
    +'<div class="aq-piers">'+piersHTML+'</div>'
    +'<div class="aq-labels">'+labelsHTML+'</div>'
    +'</div>'
    +'</div>';
}

// buildIdleRow renders one idle aqueduct as a compact single-line CSS row.
function buildIdleRow(ch){
  return'<div class="aq-idle">'
    +'<span class="aq-idle-name">'+esc(ch.name)+'</span>'
    +'<span class="aq-idle-repo">'+esc(ch.repo_name||'')+'</span>'
    +'<span class="aq-idle-dot">\u00b7 idle</span>'
    +'</div>';
}

// renderArchSection rebuilds the #arch-section div from dashboard data.
// Called only when SSE data changes (CSS animations run independently).
function renderArchSection(d){
  var chs=d.cataractae||[];
  if(!chs.length){archSectionEl.innerHTML='<div class="aq-empty">No aqueducts configured</div>';return;}
  var html='',active=[],idle=[];
  for(var i=0;i<chs.length;i++)(chs[i].droplet_id?active:idle).push(chs[i]);
  for(var i=0;i<active.length;i++)html+=buildActiveArch(active[i]);
  if(idle.length){
    html+='<div class="aq-idle-section">';
    for(var i=0;i<idle.length;i++)html+=buildIdleRow(idle[i]);
    html+='</div>';
  }
  archSectionEl.innerHTML=html;
}


// ── CURRENT FLOW with relative timestamps ────────────────────────────────────
// Mirrors viewCurrentFlow() in dashboard_tui.go.
function viewCurrentFlow(d){
  var acts=d.flow_activities||[];
  if(!acts.length)return[sp(cDim,'  No droplets currently flowing.')];
  var lines=[];
  for(var i=0;i<acts.length;i++){
    var fa=acts[i];
    var rtag='',hC=cGreen;
    if(fa.note_count>0){rtag=sp(cYellow,' \u267b '+fa.note_count);hC=cYellow;}
    lines.push('  '+sp(cHeader,fa.droplet_id,true)+'  '+sp(hC,fa.step)+rtag+sp(cDim,'  '+trunc(fa.title||'',60)));
    var notes=fa.recent_notes||[];
    if(!notes.length){
      lines.push(sp(cDim,'    (no notes yet \u2014 first pass)'));
    }else{
      for(var k=0;k<notes.length;k++){
        var nt=notes[k];
        var ts=relAge(nt.created_at);
        var txt=trunc(firstLine(nt.content),80);
        lines.push('    \u203a '+sp(cDim,'['+(nt.cataractae_name||'')+']')+'  '+sp(cFoot,txt)+'  '+sp(cDim,ts));
      }
    }
    lines.push('');
  }
  if(lines.length&&lines[lines.length-1]==='')lines.pop();
  return lines;
}

// ── CISTERN with priority icons ───────────────────────────────────────────────
// Mirrors viewCisternRow() in dashboard_tui.go.
function viewCistern(d){
  var items=(d.cistern_items||[]).filter(function(x){return x.status==='open';});
  if(!items.length)return[sp(cDim,'  Cistern is empty.')];
  var lines=[];
  for(var i=0;i<items.length;i++){
    var it=items[i];
    var age=Math.max(0,Math.floor((Date.now()-new Date(it.created_at||0).getTime())/1000));
    var bl=d.blocked_by_map&&d.blocked_by_map[it.id];
    var st=bl?sp(cRed,'blocked by '+bl):sp(cYellow,'queued');
    var pr;
    switch(it.priority){case 1:pr=sp(cRed,'\u2191');break;case 3:pr=sp(cDim,'\u2193');break;default:pr=sp(cDim,'\u00b7');}
    lines.push('  '+pr+' '+sp(cDim,padR(it.id,10))+'  '+sp(cDim,fmtNs(age*1e9))+'  '+st+'  '+sp(cDim,trunc(it.title||'',50)));
  }
  return lines;
}

// ── RECENT FLOW ───────────────────────────────────────────────────────────────
// Mirrors viewRecentRow() in dashboard_tui.go.
function viewRecent(d){
  var items=d.recent_items||[];
  if(!items.length)return[sp(cDim,'  No recent flow.')];
  var lines=[];
  for(var i=0;i<items.length;i++){
    var it=items[i];
    var t=it.updated_at?new Date(it.updated_at).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'}):'';
    var step=it.current_cataractae||'\u2014';
    var icon;
    switch(it.status){case'delivered':icon=sp(cGreen,'\u2713');break;case'stagnant':icon=sp(cRed,'\u2717');break;default:icon=sp(cDim,'\u00b7');}
    lines.push('  '+sp(cDim,t)+'  '+sp(cDim,padR(it.id,10))+'  '+sp(cDim,padR(step,20))+'  '+icon+'  '+sp(cDim,trunc(it.title||'',40)));
  }
  return lines;
}

// ── Main render ───────────────────────────────────────────────────────────────
var dashData=null;
function sepLine(){return sp(cDim,'\u2500'.repeat(SCR_W));}

// renderHeader populates the #header div once (static content).
function renderHeader(){
  headerEl.innerHTML='<span style="color:#9db1db;font-weight:bold;font-size:1rem;letter-spacing:0.15em">CISTERN</span>';
}

// render updates #screen with the status bar and text sections.
// The arch section (#arch-section) is updated separately by renderArchSection.
function render(){
  if(!dashData){screenEl.innerHTML=sp(cDim,'  Loading\u2026');return;}
  var d=dashData;
  var lines=[];

  // Status bar — mirrors viewStatusBar().
  var ts=d.fetched_at?new Date(d.fetched_at).toLocaleTimeString():'';
  lines.push(sepLine());
  lines.push('  '+sp(cGreen,'\u25cf '+(d.flowing_count||0)+' flowing')+'  '+sp(cYellow,'\u25cb '+(d.queued_count||0)+' queued')+'  '+sp(cGreen,'\u2713 '+(d.done_count||0)+' delivered')+'  '+sp(cDim,'\u2014 last update '+ts));
  lines.push(sepLine());

  // Current flow.
  lines.push(sp(cHeader,'  CURRENT FLOW',true));
  var cfL=viewCurrentFlow(d);
  for(var i=0;i<cfL.length;i++)lines.push(cfL[i]);
  lines.push(sepLine());

  // Cistern queue.
  lines.push(sp(cHeader,'  CISTERN',true));
  var cqL=viewCistern(d);
  for(var i=0;i<cqL.length;i++)lines.push(cqL[i]);
  lines.push(sepLine());

  // Recent flow.
  lines.push(sp(cHeader,'  RECENT FLOW',true));
  var rfL=viewRecent(d);
  for(var i=0;i<rfL.length;i++)lines.push(rfL[i]);
  lines.push(sepLine());

  lines.push(sp(cFoot,'  last update: '+ts));
  screenEl.innerHTML=lines.join('\n');
}

// Render header once (static content), then poll every 150ms for text sections.
// The CSS wave/waterfall animations run independently of this loop.
renderHeader();
setInterval(render,150);

// SSE connection — rebuilds arch section on each data update.
function connect(){
  connEl.className='';connEl.innerHTML='&#x25CB; connecting&#x2026;';
  var es=new EventSource('/api/dashboard/events');
  es.onopen=function(){connEl.className='live';connEl.innerHTML='&#x25CF; live';};
  es.onmessage=function(e){
    try{dashData=JSON.parse(e.data);renderArchSection(dashData);}
    catch(err){console.error('cistern:',err);}
  };
  es.onerror=function(){connEl.className='';connEl.innerHTML='&#x25CB; reconnecting&#x2026;';es.close();setTimeout(connect,3000);};
}
connect();

// ── Peek modal ────────────────────────────────────────────────────────────────
var peekWs=null,peekPinned=false,peekAqName='';

function peekOpen(name){
  peekAqName=name;peekPinned=false;
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
  btn.style.color=peekPinned?'#4bb96e':'';
  document.getElementById('peek-footer').textContent=peekPinned?'scroll pinned \u2014 click to unpin':'auto-scroll active';
}

function peekConnect(name){
  if(peekWs)peekWs.close();
  var proto=location.protocol==='https:'?'wss:':'ws:';
  var url=proto+'//'+location.host+'/ws/aqueducts/'+encodeURIComponent(name)+'/peek';
  var ws=new WebSocket(url);
  peekWs=ws;
  ws.onopen=function(){document.getElementById('peek-footer').textContent='Observing \u2014 read only';};
  ws.onmessage=function(e){
    if(!e.data)return;
    var el=document.getElementById('peek-content');
    el.textContent=e.data;
    if(!peekPinned)el.scrollTop=el.scrollHeight;
  };
  ws.onerror=function(){document.getElementById('peek-footer').textContent='connection error';};
  ws.onclose=function(){
    if(document.getElementById('peek-overlay').classList.contains('open')&&peekAqName){
      document.getElementById('peek-footer').textContent='disconnected \u2014 retrying in 3s\u2026';
      setTimeout(function(){if(peekAqName)peekConnect(peekAqName);},3000);
    }
  };
}

document.getElementById('peek-overlay').addEventListener('click',function(e){if(e.target===this)peekClose();});
// Peek clicks originate from the CSS arch section (data-aqname on channel divs).
archSectionEl.addEventListener('click',function(e){
  var el=e.target.closest&&e.target.closest('[data-aqname]');
  if(el)peekOpen(el.dataset.aqname);
});
</script>
</body>
</html>`
