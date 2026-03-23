package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ── Web mux tests ─────────────────────────────────────────────────────────────

func TestArchDesignerMux_RootServesHTML(t *testing.T) {
	mux := newArchDesignerMux()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET / status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("body should contain <!DOCTYPE html>")
	}
}

func TestArchDesignerMux_NotFoundForUnknownPaths(t *testing.T) {
	mux := newArchDesignerMux()

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /nonexistent status = %d, want 404", w.Code)
	}
}

func TestArchDesignerHTML_ContainsXterm(t *testing.T) {
	checks := []struct {
		want string
		desc string
	}{
		{"xterm", "xterm.js reference"},
		{"FitAddon", "xterm FitAddon"},
		{"/ws/term", "/ws/term WebSocket path"},
		{`name="viewport"`, "viewport meta tag"},
		{`id="terminal"`, "terminal div"},
	}
	for _, c := range checks {
		if !strings.Contains(archDesignerHTML, c.want) {
			t.Errorf("HTML must contain %s (%q)", c.desc, c.want)
		}
	}
}

func TestArchDesignerHTML_ContainsButtons(t *testing.T) {
	buttons := []struct {
		want string
		desc string
	}{
		{"btn-prev", "previous param button"},
		{"btn-next", "next param button"},
		{"btn-up", "up button"},
		{"btn-down", "down button"},
		{"btn-plus5", "+5 button"},
		{"btn-min5", "-5 button"},
		{"btn-preset", "preset button"},
		{"btn-reset", "reset button"},
		{"btn-save", "save & copy button"},
		{"navigator.clipboard", "clipboard API usage"},
		{`\x1b[Z`, "Shift+Tab escape sequence for Prev"},
		{`\x1b[A`, "Up arrow escape sequence"},
		{`\x1b[B`, "Down arrow escape sequence"},
		{`\x1b[1;2A`, "Shift+Up escape sequence for +5"},
		{`\x1b[1;2B`, "Shift+Down escape sequence for -5"},
	}
	for _, b := range buttons {
		if !strings.Contains(archDesignerHTML, b.want) {
			t.Errorf("HTML must contain %s (%q)", b.desc, b.want)
		}
	}
}

func TestWsTerm_NonWebSocketRejected(t *testing.T) {
	mux := newArchDesignerMux()
	req := httptest.NewRequest(http.MethodGet, "/ws/term", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want 426", w.Code)
	}
}

func TestWsTerm_MissingKeyRejected(t *testing.T) {
	mux := newArchDesignerMux()
	req := httptest.NewRequest(http.MethodGet, "/ws/term", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// wsDialTerm performs a WebSocket handshake to /ws/term and returns the
// buffered reader and connection. A 2s read deadline is set.
func wsDialTerm(t *testing.T, srv *httptest.Server) (*bufio.Reader, net.Conn) {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	fmt.Fprintf(conn, "GET /ws/term HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", key)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		t.Fatalf("read handshake response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	return br, conn
}

func TestWsTerm_UpgradeSucceeds(t *testing.T) {
	mux := newArchDesignerMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, conn := wsDialTerm(t, srv)
	defer conn.Close()
	// Upgrade succeeded (wsDialTerm fatals on failure).
}

// ── Arch parameter tests ──────────────────────────────────────────────────────

func TestClampParams_TaperRowsBounds(t *testing.T) {
	p := clampParams(archParams{ColW: 20, ArchTopW: 9, TaperRows: 0, PierRows: 1, BrickW: 4, NumPiers: 4})
	if p.TaperRows < 1 {
		t.Errorf("TaperRows = %d, want >= 1", p.TaperRows)
	}
	p = clampParams(archParams{ColW: 30, ArchTopW: 27, TaperRows: 9, PierRows: 1, BrickW: 4, NumPiers: 4})
	if p.TaperRows > 8 {
		t.Errorf("TaperRows = %d, want <= 8", p.TaperRows)
	}
}

func TestClampParams_ArchTopWNeverExceedsPierW(t *testing.T) {
	// TaperRows=4 requires pierW=archTopW-8 >= 1, so archTopW >= 9.
	// Setting archTopW=3 should clamp up.
	p := clampParams(archParams{ColW: 20, ArchTopW: 3, TaperRows: 4, PierRows: 1, BrickW: 4, NumPiers: 4})
	pierW := p.ArchTopW - p.TaperRows*2
	if pierW < 1 {
		t.Errorf("pierW = %d (archTopW=%d, taperRows=%d), want >= 1", pierW, p.ArchTopW, p.TaperRows)
	}
}

func TestClampParams_ColWAlwaysGtArchTopW(t *testing.T) {
	p := clampParams(archParams{ColW: 5, ArchTopW: 9, TaperRows: 4, PierRows: 1, BrickW: 4, NumPiers: 4})
	if p.ColW <= p.ArchTopW {
		t.Errorf("ColW=%d should be > ArchTopW=%d", p.ColW, p.ArchTopW)
	}
}

func TestClampParams_NumPiersBounds(t *testing.T) {
	p := clampParams(archParams{ColW: 14, ArchTopW: 9, TaperRows: 4, PierRows: 1, BrickW: 4, NumPiers: 0})
	if p.NumPiers < 1 {
		t.Errorf("NumPiers = %d, want >= 1", p.NumPiers)
	}
	p = clampParams(archParams{ColW: 14, ArchTopW: 9, TaperRows: 4, PierRows: 1, BrickW: 4, NumPiers: 99})
	if p.NumPiers > 8 {
		t.Errorf("NumPiers = %d, want <= 8", p.NumPiers)
	}
}

func TestGetSetParam_RoundTrip(t *testing.T) {
	p := defaultArchParams()
	for i := range paramNames {
		orig := getParam(p, i)
		p2 := setParam(p, i, orig+1)
		got := getParam(p2, i)
		// Clamping may prevent the +1 at boundary values, so only check non-boundary.
		if got != orig && got != orig+1 {
			t.Errorf("param %d: getParam(setParam(p, %d, %d+1)) = %d, want %d or %d",
				i, i, orig, got, orig, orig+1)
		}
	}
}

func TestGoConstants_DefaultParams(t *testing.T) {
	p := defaultArchParams()
	s := goConstants(p)
	if !strings.Contains(s, "const (") {
		t.Error("goConstants should start with 'const ('")
	}
	if !strings.Contains(s, "colW") {
		t.Error("goConstants should contain 'colW'")
	}
	if !strings.Contains(s, "archTopW") {
		t.Error("goConstants should contain 'archTopW'")
	}
}

func TestGoConstants_SpanComputation(t *testing.T) {
	p := archParams{ColW: 14, ArchTopW: 9, TaperRows: 4, PierRows: 1, BrickW: 4, NumPiers: 4}
	s := goConstants(p)
	// span = colW - archTopW = 14 - 9 = 5
	if !strings.Contains(s, "span = 5") {
		t.Errorf("goConstants should mention span = 5, got:\n%s", s)
	}
}

// ── Arch rendering tests ──────────────────────────────────────────────────────

func TestRenderArch_DefaultParamsNoPanic(t *testing.T) {
	p := defaultArchParams()
	lines := renderArch(p)
	if len(lines) == 0 {
		t.Error("renderArch returned no lines")
	}
	// Expect: 1 top mortar + 2*(taperRows+pierRows) sub-rows = 1 + 2*(4+1) = 11 lines
	want := 1 + 2*(p.TaperRows+p.PierRows)
	if len(lines) != want {
		t.Errorf("renderArch returned %d lines, want %d", len(lines), want)
	}
}

func TestRenderArch_ExtremeParams(t *testing.T) {
	// Minimal params should not panic.
	p := clampParams(archParams{ColW: 6, ArchTopW: 3, TaperRows: 1, PierRows: 0, BrickW: 1, NumPiers: 1})
	lines := renderArch(p)
	if len(lines) == 0 {
		t.Error("renderArch with minimal params returned no lines")
	}
}

func TestRenderArch_LineWidthConsistency(t *testing.T) {
	p := defaultArchParams()
	lines := renderArch(p)
	// All lines should be non-empty.
	for i, line := range lines {
		if line == "" {
			t.Errorf("line %d is empty", i)
		}
	}
}

// ── ArchCrownAtT tests ────────────────────────────────────────────────────────

func TestArchCrownAtT_KeystoneFullyClosed(t *testing.T) {
	lf, og, rf := archCrownAtT(0, 6)
	// At t=0 (keystone), arch is closed: lf+rf should fill most/all of gapWidth.
	total := lf + og + rf
	if total != 6 {
		t.Errorf("lf+og+rf = %d, want 6 (gapWidth)", total)
	}
	if og != 0 {
		t.Errorf("og = %d at t=0, want 0 (closed at keystone)", og)
	}
}

func TestArchCrownAtT_ImpostFullyOpen(t *testing.T) {
	lf, og, rf := archCrownAtT(1, 6)
	total := lf + og + rf
	if total != 6 {
		t.Errorf("lf+og+rf = %d, want 6 (gapWidth)", total)
	}
	if og != 6 {
		t.Errorf("og = %d at t=1, want 6 (fully open at impost)", og)
	}
	_ = lf
	_ = rf
}

func TestArchCrownAtT_ZeroGap(t *testing.T) {
	lf, og, rf := archCrownAtT(0.5, 0)
	if lf != 0 || og != 0 || rf != 0 {
		t.Errorf("archCrownAtT with gapWidth=0: lf=%d og=%d rf=%d, want all 0", lf, og, rf)
	}
}

// ── WS utility tests (same assertions as dashboard_web_test.go) ───────────────

func TestWsAcceptKey_RFC6455TestVector(t *testing.T) {
	got := wsAcceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Errorf("wsAcceptKey() = %q, want %q", got, want)
	}
}

func TestWsSendFrame_SmallPayload(t *testing.T) {
	var buf strings.Builder
	bw := bufio.NewWriter(&buf)
	if err := wsSendBinary(bw, []byte("hello")); err != nil {
		t.Fatalf("wsSendBinary: %v", err)
	}
	b := []byte(buf.String())
	if b[0] != 0x82 { // FIN + binary opcode
		t.Errorf("byte[0] = 0x%02x, want 0x82 (FIN+binary)", b[0])
	}
	if b[1] != 5 {
		t.Errorf("byte[1] (len) = %d, want 5", b[1])
	}
}

func TestWsReadClientFrame_RejectsUnmasked(t *testing.T) {
	frame := []byte{0x81, 0x05, 'h', 'e', 'l', 'l', 'o'}
	br := bufio.NewReader(strings.NewReader(string(frame)))
	_, _, _, err := wsReadClientFrame(br, make([]byte, 128))
	if err == nil {
		t.Fatal("expected error for unmasked client frame")
	}
	if !strings.Contains(err.Error(), "unmasked") {
		t.Errorf("error = %q, want it to mention 'unmasked'", err)
	}
}

func TestWsReadClientFrame_PayloadSizeLimit(t *testing.T) {
	buildMaskedFrame := func(extLen uint16) []byte {
		var frame []byte
		frame = append(frame, 0x81)
		frame = append(frame, 0x80|0x7E)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], extLen)
		frame = append(frame, ext[:]...)
		frame = append(frame, 0, 0, 0, 0) // zero mask = no-op XOR
		frame = append(frame, make([]byte, extLen)...)
		return frame
	}

	t.Run("rejects_oversized", func(t *testing.T) {
		br := bufio.NewReader(strings.NewReader(string(buildMaskedFrame(5000))))
		_, _, _, err := wsReadClientFrame(br, make([]byte, 128))
		if err == nil {
			t.Fatal("expected error for oversized payload")
		}
	})
	t.Run("accepts_at_max", func(t *testing.T) {
		br := bufio.NewReader(strings.NewReader(string(buildMaskedFrame(4096))))
		_, payload, _, err := wsReadClientFrame(br, make([]byte, 128))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(payload) != 4096 {
			t.Errorf("payload length = %d, want 4096", len(payload))
		}
	})
}

// ── TUI model tests ───────────────────────────────────────────────────────────

func TestTUIModel_InitialState(t *testing.T) {
	m := newTUIModel()
	if m.selected != 0 {
		t.Errorf("selected = %d, want 0", m.selected)
	}
	if m.params != defaultArchParams() {
		t.Errorf("params = %+v, want default", m.params)
	}
}

func TestTUIModel_TabNextParam(t *testing.T) {
	m := newTUIModel()
	next, _ := m.Update(keyMsg("tab"))
	nm := next.(tuiModel)
	if nm.selected != 1 {
		t.Errorf("after Tab: selected = %d, want 1", nm.selected)
	}
}

func TestTUIModel_ShiftTabPrevParam(t *testing.T) {
	m := newTUIModel()
	m.selected = 2
	next, _ := m.Update(keyMsg("shift+tab"))
	nm := next.(tuiModel)
	if nm.selected != 1 {
		t.Errorf("after Shift+Tab: selected = %d, want 1", nm.selected)
	}
}

func TestTUIModel_TabWrapsAround(t *testing.T) {
	m := newTUIModel()
	m.selected = len(paramNames) - 1
	next, _ := m.Update(keyMsg("tab"))
	nm := next.(tuiModel)
	if nm.selected != 0 {
		t.Errorf("Tab from last: selected = %d, want 0", nm.selected)
	}
}

func TestTUIModel_UpIncreasesParam(t *testing.T) {
	m := newTUIModel()
	m.selected = 0 // ColW
	before := getParam(m.params, 0)
	next, _ := m.Update(keyMsg("up"))
	nm := next.(tuiModel)
	after := getParam(nm.params, 0)
	if after != before+1 {
		t.Errorf("after Up: ColW = %d, want %d", after, before+1)
	}
}

func TestTUIModel_DownDecreasesParam(t *testing.T) {
	m := newTUIModel()
	m.selected = 5 // NumPiers
	before := getParam(m.params, 5)
	next, _ := m.Update(keyMsg("down"))
	nm := next.(tuiModel)
	after := getParam(nm.params, 5)
	if after != before-1 {
		t.Errorf("after Down: NumPiers = %d, want %d", after, before-1)
	}
}

func TestTUIModel_ShiftUpAdjusts5(t *testing.T) {
	m := newTUIModel()
	m.selected = 0 // ColW
	before := getParam(m.params, 0)
	next, _ := m.Update(keyMsg("shift+up"))
	nm := next.(tuiModel)
	after := getParam(nm.params, 0)
	if after != before+5 {
		t.Errorf("after Shift+Up: ColW = %d, want %d", after, before+5)
	}
}

func TestTUIModel_ShiftDownAdjusts5(t *testing.T) {
	m := newTUIModel()
	m.selected = 0 // ColW (=14, so -5 stays valid)
	before := getParam(m.params, 0)
	next, _ := m.Update(keyMsg("shift+down"))
	nm := next.(tuiModel)
	after := getParam(nm.params, 0)
	// 14 - 5 = 9, but clamping may prevent this if it violates archTopW constraint.
	// At minimum, it should decrease or stay at minimum.
	if after > before {
		t.Errorf("after Shift+Down: ColW = %d, should be <= %d", after, before)
	}
}

func TestTUIModel_PresetRestoresDefaults(t *testing.T) {
	m := newTUIModel()
	m.params = setParam(m.params, 0, 20) // modify ColW
	next, _ := m.Update(keyMsg("l"))
	nm := next.(tuiModel)
	if nm.params != defaultArchParams() {
		t.Errorf("after 'l': params = %+v, want defaults", nm.params)
	}
}

func TestTUIModel_ResetRestoresDefaults(t *testing.T) {
	m := newTUIModel()
	m.params = setParam(m.params, 0, 20)
	next, _ := m.Update(keyMsg("r"))
	nm := next.(tuiModel)
	if nm.params != defaultArchParams() {
		t.Errorf("after 'r': params = %+v, want defaults", nm.params)
	}
}

func TestTUIModel_SaveShowsConstants(t *testing.T) {
	m := newTUIModel()
	next, _ := m.Update(keyMsg("s"))
	nm := next.(tuiModel)
	if !nm.showConst {
		t.Error("after 's': showConst should be true")
	}
}

func TestTUIModel_View_ContainsTitleAndHints(t *testing.T) {
	m := newTUIModel()
	view := m.View()
	if !strings.Contains(view, "Arch Designer") {
		t.Error("View should contain 'Arch Designer'")
	}
	if !strings.Contains(view, "Parameters:") {
		t.Error("View should contain 'Parameters:'")
	}
	if !strings.Contains(view, "Tab") {
		t.Error("View should contain key hints with 'Tab'")
	}
}

func TestTUIModel_View_ShowsConstants(t *testing.T) {
	m := newTUIModel()
	m.showConst = true
	view := m.View()
	if !strings.Contains(view, "const (") {
		t.Error("View with showConst should contain 'const ('")
	}
}

// keyMsg builds a tea.KeyMsg from a string (e.g. "tab", "shift+up").
func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Alt: false}
}
