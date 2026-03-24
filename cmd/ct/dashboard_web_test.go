package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
)

func TestDashboardWebMux_RootServesHTML(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))

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
	if !strings.Contains(body, "xterm") {
		t.Error("body should contain xterm.js reference")
	}
}

func TestDashboardWebMux_NotFoundForUnknownPaths(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /nonexistent status = %d, want 404", w.Code)
	}
}

func TestDashboardWebMux_APIReturnsJSON(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /api/dashboard status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var data DashboardData
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if data.FetchedAt.IsZero() {
		t.Error("FetchedAt should be set in JSON response")
	}
}

func TestDashboardWebMux_APIMethodNotAllowed(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/dashboard status = %d, want 405", w.Code)
	}
}

func TestDashboardWebMux_EventsSSEHeaders(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))

	// Pre-cancel the context so the SSE handler exits after sending the first event.
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "data: ") {
		t.Errorf("SSE body should start with 'data: ', got %q", truncateStr(body, 60))
	}
	// The first SSE line's payload must be valid JSON.
	firstLine := strings.SplitN(body, "\n", 2)[0]
	payload := strings.TrimPrefix(firstLine, "data: ")
	var d DashboardData
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		t.Errorf("SSE payload is not valid DashboardData JSON: %v — payload: %q", err, truncateStr(payload, 80))
	}
}

func TestDashboardWebMux_APIReturnsCorrectCounts(t *testing.T) {
	cfgPath := tempCfg(t)
	dbPath := tempDB(t)

	// Seed: 1 flowing (virgo/implement), 1 queued.
	c, err := cistern.New(dbPath, "mr")
	if err != nil {
		t.Fatal(err)
	}
	flowing, _ := c.Add("myrepo", "Feature A", "", 1, 2)
	c.GetReady("myrepo")
	c.Assign(flowing.ID, "virgo", "implement")
	c.Add("myrepo", "Feature B", "", 2, 2)
	c.Close()

	mux := newDashboardMux(cfgPath, dbPath)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var data DashboardData
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data.FlowingCount != 1 {
		t.Errorf("FlowingCount = %d, want 1", data.FlowingCount)
	}
	if data.QueuedCount != 1 {
		t.Errorf("QueuedCount = %d, want 1", data.QueuedCount)
	}
}

func TestDashboardWebMux_NoteFieldsRoundTrip(t *testing.T) {
	cfgPath := tempCfg(t)
	dbPath := tempDB(t)

	c, err := cistern.New(dbPath, "mr")
	if err != nil {
		t.Fatal(err)
	}
	droplet, _ := c.Add("myrepo", "Note Test", "", 1, 2)
	c.GetReady("myrepo")
	c.Assign(droplet.ID, "virgo", "implement")
	if err := c.AddNote(droplet.ID, "implementer", "hello world"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	mux := newDashboardMux(cfgPath, dbPath)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var data DashboardData
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(data.FlowActivities) == 0 {
		t.Fatal("expected at least one flow activity")
	}
	notes := data.FlowActivities[0].RecentNotes
	if len(notes) == 0 {
		t.Fatal("expected at least one recent note")
	}
	if notes[0].CataractaeName != "implementer" {
		t.Errorf("CataractaeName = %q, want %q", notes[0].CataractaeName, "implementer")
	}
	if notes[0].Content != "hello world" {
		t.Errorf("Content = %q, want %q", notes[0].Content, "hello world")
	}
}

func TestDashboardWebMux_NoteFieldsSnakeCaseJSON(t *testing.T) {
	cfgPath := tempCfg(t)
	dbPath := tempDB(t)

	c, err := cistern.New(dbPath, "mr")
	if err != nil {
		t.Fatal(err)
	}
	droplet, _ := c.Add("myrepo", "Snake Case Test", "", 1, 2)
	c.GetReady("myrepo")
	c.Assign(droplet.ID, "virgo", "implement")
	if err := c.AddNote(droplet.ID, "implementer", "snake test"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	mux := newDashboardMux(cfgPath, dbPath)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Unmarshal into a generic map to verify raw JSON key names (not Go field names).
	var raw map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw JSON: %v", err)
	}
	activities, _ := raw["flow_activities"].([]interface{})
	if len(activities) == 0 {
		t.Fatal("expected at least one flow_activity in raw JSON")
	}
	act, _ := activities[0].(map[string]interface{})
	notes, _ := act["recent_notes"].([]interface{})
	if len(notes) == 0 {
		t.Fatal("expected at least one recent_note in raw JSON")
	}
	note, _ := notes[0].(map[string]interface{})
	if _, ok := note["cataractae_name"]; !ok {
		t.Errorf("raw JSON note missing key %q (got %v)", "cataractae_name", note)
	}
	if _, ok := note["content"]; !ok {
		t.Errorf("raw JSON note missing key %q (got %v)", "content", note)
	}
}

// truncateStr returns at most n runes of s for safe display in test messages.
func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// --- peek HTTP/WS tests ---

// TestWsAcceptKey checks the RFC 6455 §4.2.2 accept-key derivation using the
// standard test vector from the spec.
func TestWsAcceptKey(t *testing.T) {
	// RFC 6455 example: client key "dGhlIHNhbXBsZSBub25jZQ==" → accept "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	got := wsAcceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Errorf("wsAcceptKey() = %q, want %q", got, want)
	}
}

// TestWsSendText_SmallPayload verifies frame header for a payload under 126 bytes.
func TestWsSendText_SmallPayload(t *testing.T) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	if err := wsSendText(bw, "hello"); err != nil {
		t.Fatalf("wsSendText: %v", err)
	}
	b := buf.Bytes()
	if b[0] != 0x81 {
		t.Errorf("byte[0] = 0x%02x, want 0x81 (FIN+text)", b[0])
	}
	if b[1] != 5 {
		t.Errorf("byte[1] (len) = %d, want 5", b[1])
	}
	if string(b[2:]) != "hello" {
		t.Errorf("payload = %q, want %q", string(b[2:]), "hello")
	}
}

// TestWsSendText_MediumPayload verifies the 2-byte extended length frame (126–65535 bytes).
func TestWsSendText_MediumPayload(t *testing.T) {
	payload := strings.Repeat("x", 200)
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	if err := wsSendText(bw, payload); err != nil {
		t.Fatalf("wsSendText: %v", err)
	}
	b := buf.Bytes()
	if b[0] != 0x81 {
		t.Errorf("byte[0] = 0x%02x, want 0x81", b[0])
	}
	if b[1] != 0x7E {
		t.Errorf("byte[1] = 0x%02x, want 0x7E (medium extended len)", b[1])
	}
	n := int(binary.BigEndian.Uint16(b[2:4]))
	if n != 200 {
		t.Errorf("encoded length = %d, want 200", n)
	}
	if string(b[4:]) != payload {
		t.Error("payload content mismatch")
	}
}

// TestWsSendText_LargePayload verifies the 8-byte extended length frame (>= 65536 bytes).
// This is a regression test for the bug fixed in bdab760 where the high 32 bits
// of the 8-byte payload length were silently discarded (RFC 6455 §5.2 non-compliance).
func TestWsSendText_LargePayload(t *testing.T) {
	payload := strings.Repeat("x", 65536)
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	if err := wsSendText(bw, payload); err != nil {
		t.Fatalf("wsSendText: %v", err)
	}
	b := buf.Bytes()
	if b[0] != 0x81 {
		t.Errorf("byte[0] = 0x%02x, want 0x81 (FIN+text)", b[0])
	}
	if b[1] != 0x7F {
		t.Errorf("byte[1] = 0x%02x, want 0x7F (8-byte extended len)", b[1])
	}
	n := int(binary.BigEndian.Uint64(b[2:10]))
	if n != 65536 {
		t.Errorf("encoded length = %d, want 65536", n)
	}
	if string(b[10:]) != payload {
		t.Error("payload content mismatch for large frame")
	}
}

// TestWsFrameRoundtrip_LargePayload verifies that wsSendText and readWSTextFrame
// correctly encode and decode payloads >= 65536 bytes (RFC 6455 §5.2 case 127).
func TestWsFrameRoundtrip_LargePayload(t *testing.T) {
	payload := strings.Repeat("z", 65536)
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	if err := wsSendText(bw, payload); err != nil {
		t.Fatalf("wsSendText: %v", err)
	}
	br := bufio.NewReader(&buf)
	got, err := readWSTextFrame(br)
	if err != nil {
		t.Fatalf("readWSTextFrame: %v", err)
	}
	if got != payload {
		t.Errorf("roundtrip length mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

// TestLookupAqueductSession_Empty returns false when the DB has no in_progress items.
func TestLookupAqueductSession_Empty(t *testing.T) {
	_, ok := lookupAqueductSession(tempDB(t), "virgo")
	if ok {
		t.Error("expected false for empty DB")
	}
}

// TestLookupAqueductSession_NoMatch returns false when no item is assigned to the named aqueduct.
func TestLookupAqueductSession_NoMatch(t *testing.T) {
	db := tempDB(t)
	c, err := cistern.New(db, "mr")
	if err != nil {
		t.Fatal(err)
	}
	item, _ := c.Add("myrepo", "Some work", "", 1, 2)
	c.GetReady("myrepo")
	c.Assign(item.ID, "other-aqueduct", "implement")
	c.Close()

	_, ok := lookupAqueductSession(db, "virgo")
	if ok {
		t.Error("expected false when no item assigned to 'virgo'")
	}
}

// TestLookupAqueductSession_Found returns true and correct session info when
// an in_progress item is assigned to the named aqueduct.
func TestLookupAqueductSession_Found(t *testing.T) {
	db := tempDB(t)
	c, err := cistern.New(db, "mr")
	if err != nil {
		t.Fatal(err)
	}
	item, _ := c.Add("myrepo", "Peek target", "", 1, 2)
	c.GetReady("myrepo")
	c.Assign(item.ID, "virgo", "implement")
	c.Close()

	info, ok := lookupAqueductSession(db, "virgo")
	if !ok {
		t.Fatal("expected session to be found")
	}
	if info.dropletID != item.ID {
		t.Errorf("dropletID = %q, want %q", info.dropletID, item.ID)
	}
	if !strings.Contains(info.sessionID, "virgo") {
		t.Errorf("sessionID %q should contain 'virgo'", info.sessionID)
	}
	if info.title != "Peek target" {
		t.Errorf("title = %q, want %q", info.title, "Peek target")
	}
}

// TestPeekHTTP_MethodNotAllowed ensures POST /api/aqueducts/{name}/peek returns 405.
func TestPeekHTTP_MethodNotAllowed(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))
	req := httptest.NewRequest(http.MethodPost, "/api/aqueducts/virgo/peek", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

// TestPeekHTTP_IdleAqueduct returns "session not active" when aqueduct is idle.
func TestPeekHTTP_IdleAqueduct(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))
	req := httptest.NewRequest(http.MethodGet, "/api/aqueducts/virgo/peek", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "session not active") {
		t.Errorf("body = %q, want 'session not active'", w.Body.String())
	}
}

// TestPeekHTTP_ActiveWithMockCapturer seeds an in_progress droplet, overrides
// defaultCapturer with a mock, and verifies the pane content is returned.
func TestPeekHTTP_ActiveWithMockCapturer(t *testing.T) {
	db := tempDB(t)
	c, err := cistern.New(db, "mr")
	if err != nil {
		t.Fatal(err)
	}
	item, _ := c.Add("myrepo", "Peek work", "", 1, 2)
	c.GetReady("myrepo")
	c.Assign(item.ID, "virgo", "implement")
	c.Close()

	orig := defaultCapturer
	t.Cleanup(func() { defaultCapturer = orig })
	defaultCapturer = mockCapturer{hasSession: true, content: "pane output line"}

	mux := newDashboardMux(tempCfg(t), db)
	req := httptest.NewRequest(http.MethodGet, "/api/aqueducts/virgo/peek", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "pane output line") {
		t.Errorf("body = %q, want 'pane output line'", w.Body.String())
	}
}

// TestPeekHTTP_ActiveButSessionGone returns "session not active" when the
// aqueduct has an in_progress droplet but tmux session no longer exists.
func TestPeekHTTP_ActiveButSessionGone(t *testing.T) {
	db := tempDB(t)
	c, err := cistern.New(db, "mr")
	if err != nil {
		t.Fatal(err)
	}
	item, _ := c.Add("myrepo", "Gone session", "", 1, 2)
	c.GetReady("myrepo")
	c.Assign(item.ID, "virgo", "implement")
	c.Close()

	orig := defaultCapturer
	t.Cleanup(func() { defaultCapturer = orig })
	defaultCapturer = mockCapturer{hasSession: false}

	mux := newDashboardMux(tempCfg(t), db)
	req := httptest.NewRequest(http.MethodGet, "/api/aqueducts/virgo/peek", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "session not active") {
		t.Errorf("body = %q, want 'session not active'", w.Body.String())
	}
}

// TestPeekHTTP_LinesQueryParam verifies ?lines= is accepted without error.
func TestPeekHTTP_LinesQueryParam(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))
	req := httptest.NewRequest(http.MethodGet, "/api/aqueducts/virgo/peek?lines=50", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	// Idle aqueduct — just verify no server error.
	if w.Code >= 500 {
		t.Errorf("status = %d, want < 500", w.Code)
	}
}

// TestWsUpgrade_CrossOriginRejected verifies that wsUpgrade returns 403 Forbidden
// for WebSocket requests with a non-localhost Origin header.
func TestWsUpgrade_CrossOriginRejected(t *testing.T) {
	cases := []struct {
		name   string
		origin string
	}{
		{"evil_http", "http://evil.com"},
		{"evil_https", "https://evil.com"},
		{"remote_ip", "http://192.168.1.1:8080"},
		{"localhost_subdomain", "http://localhost.evil.com"},
		{"127_lookalike", "http://127.0.0.1.evil.com"},
	}
	mux := newDashboardMux(tempCfg(t), tempDB(t))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ws/aqueducts/virgo/peek", nil)
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-Websocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("Origin %q: status = %d, want 403 Forbidden", tc.origin, w.Code)
			}
		})
	}
}

// TestWsUpgrade_LocalhostOriginAllowed verifies that wsUpgrade permits WebSocket
// requests from localhost, 127.0.0.1, and ::1 origins (the request proceeds past
// Origin validation; httptest.ResponseRecorder does not support hijacking so it
// terminates with 500, but the key assertion is that 403 is NOT returned).
func TestWsUpgrade_LocalhostOriginAllowed(t *testing.T) {
	cases := []struct {
		name   string
		origin string
	}{
		{"localhost", "http://localhost"},
		{"localhost_with_port", "http://localhost:5737"},
		{"loopback_ipv4", "http://127.0.0.1"},
		{"loopback_ipv4_with_port", "http://127.0.0.1:5737"},
		{"loopback_ipv6", "http://[::1]"},
		{"loopback_ipv6_with_port", "http://[::1]:5737"},
	}
	mux := newDashboardMux(tempCfg(t), tempDB(t))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ws/aqueducts/virgo/peek", nil)
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-Websocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code == http.StatusForbidden {
				t.Errorf("Origin %q: got 403, want non-403 (localhost origin must be allowed)", tc.origin)
			}
		})
	}
}

// TestWsUpgrade_MissingOriginAllowed verifies that wsUpgrade allows requests
// with no Origin header (non-browser clients such as native tools and tests).
func TestWsUpgrade_MissingOriginAllowed(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))
	req := httptest.NewRequest(http.MethodGet, "/ws/aqueducts/virgo/peek", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-Websocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	// No Origin header set.
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("missing Origin: got 403, want non-403 (no Origin header should be allowed)")
	}
}

// TestWsPeek_NonWebSocketRejected verifies that a plain GET to the WS endpoint
// returns 426 Upgrade Required.
func TestWsPeek_NonWebSocketRejected(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))
	req := httptest.NewRequest(http.MethodGet, "/ws/aqueducts/virgo/peek", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want 426", w.Code)
	}
}

// TestWsPeek_MissingKeyRejected verifies that a WS upgrade without
// Sec-WebSocket-Key returns 400.
func TestWsPeek_MissingKeyRejected(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))
	req := httptest.NewRequest(http.MethodGet, "/ws/aqueducts/virgo/peek", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// wsDialPeek performs a WebSocket handshake to /ws/aqueducts/{name}/peek and
// returns the buffered reader (for reading frames) and the connection (caller
// must defer conn.Close). A 2s read deadline is set.
func wsDialPeek(t *testing.T, srv *httptest.Server, aqName string) (*bufio.Reader, net.Conn) {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	fmt.Fprintf(conn, "GET /ws/aqueducts/%s/peek HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", aqName, key)
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

// TestWsPeek_SuccessfulStreamIdle connects a real WebSocket to the WS peek
// endpoint for an idle aqueduct and verifies "session not active" is streamed.
func TestWsPeek_SuccessfulStreamIdle(t *testing.T) {
	mux := newDashboardMux(tempCfg(t), tempDB(t))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	br, conn := wsDialPeek(t, srv, "virgo")
	defer conn.Close()

	payload, err := readWSTextFrame(br)
	if err != nil {
		t.Fatalf("read WS frame: %v", err)
	}
	if payload != "session not active" {
		t.Errorf("payload = %q, want %q", payload, "session not active")
	}
}

// TestWsPeek_SuccessfulStreamActive seeds an in_progress droplet with a mock
// capturer and verifies the pane content is streamed over WebSocket.
func TestWsPeek_SuccessfulStreamActive(t *testing.T) {
	db := tempDB(t)
	c, err := cistern.New(db, "mr")
	if err != nil {
		t.Fatal(err)
	}
	item, _ := c.Add("myrepo", "Peek work", "", 1, 2)
	c.GetReady("myrepo")
	c.Assign(item.ID, "virgo", "implement")
	c.Close()

	orig := defaultCapturer
	t.Cleanup(func() { defaultCapturer = orig })
	defaultCapturer = mockCapturer{hasSession: true, content: "live pane output"}

	mux := newDashboardMux(tempCfg(t), db)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	br, conn := wsDialPeek(t, srv, "virgo")
	defer conn.Close()

	payload, err := readWSTextFrame(br)
	if err != nil {
		t.Fatalf("read WS frame: %v", err)
	}
	if !strings.Contains(payload, "live pane output") {
		t.Errorf("payload = %q, want it to contain %q", payload, "live pane output")
	}
}

// TestDashboardHTML_UsesXterm verifies the web dashboard embeds xterm.js
// and the /ws/tui WebSocket endpoint.
func TestDashboardHTML_UsesXterm(t *testing.T) {
	html := dashboardHTML
	checks := []struct {
		want string
		desc string
	}{
		{"xterm", "xterm.js reference"},
		{"FitAddon", "xterm FitAddon"},
		{"/ws/tui", "/ws/tui WebSocket path"},
		{`name="viewport"`, "viewport meta tag"},
		{"id=\"terminal\"", "terminal div"},
	}
	for _, c := range checks {
		if !strings.Contains(html, c.want) {
			t.Errorf("HTML must contain %s (%q)", c.desc, c.want)
		}
	}
}

// TestWsTui_WSReaderExitsOnConnClose verifies the shutdown propagation in the
// /ws/tui handler: goroutine B (WS frame reader) must exit and call cancel()
// when the underlying connection is closed without a WebSocket close frame.
// This is the trigger for shutdown watchdog goroutine C, which closes ptmx
// and unblocks the PTY reader goroutine A from ptmx.Read.
func TestWsTui_WSReaderExitsOnConnClose(t *testing.T) {
	// in-process pipe connection — simulates the hijacked net.Conn
	server, client := net.Pipe()
	defer server.Close()

	br := bufio.NewReader(server)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		buf := make([]byte, 4096)
		for {
			_, _, nb, err := wsReadClientFrame(br, buf)
			buf = nb
			if err != nil {
				return
			}
		}
	}()

	// Simulate abrupt client disconnect — no WebSocket close frame.
	client.Close()

	select {
	case <-done:
		// goroutine exited as expected
	case <-time.After(1 * time.Second):
		t.Fatal("WS reader goroutine did not exit after connection close")
	}
	select {
	case <-ctx.Done():
		// cancel() was called by goroutine's defer — watchdog would fire
	default:
		t.Error("cancel() was not called by WS reader goroutine on connection close")
	}
}

// TestWsTui_WSReaderReadDeadlineExitsOnPartition verifies goroutine B exits
// and calls cancel() when the read deadline fires with no client frames,
// simulating a network partition with an idle PTY.
func TestWsTui_WSReaderReadDeadlineExitsOnPartition(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	// client is intentionally not closed — simulates a network partition
	// where no TCP FIN arrives, so server cannot distinguish idle from dead.
	// runtime.KeepAlive(client) at the end prevents the GC from finalizing the
	// connection before the deadline fires, which would cause an early exit via
	// a connection-close error instead of the read-deadline path under test.

	br := bufio.NewReader(server)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		buf := make([]byte, wsMaxClientPayload)
		for {
			// Mirrors production read-deadline fix; shorter timeout for test speed.
			server.SetReadDeadline(time.Now().Add(50 * time.Millisecond)) //nolint:errcheck
			_, _, nb, err := wsReadClientFrame(br, buf)
			buf = nb
			if err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WS reader goroutine did not exit after read deadline (network partition case)")
	}
	select {
	case <-ctx.Done():
	default:
		t.Error("cancel() was not called by WS reader goroutine on read deadline")
	}
	runtime.KeepAlive(client)
}

// TestWsPeek_ReaderGoroutine_ExitsOnConnClose verifies that the peek handler's
// reader goroutine exits and calls cancel() when the underlying connection is
// closed without a WebSocket close frame, mirroring the /ws/tui behaviour in
// TestWsTui_WSReaderExitsOnConnClose.
func TestWsPeek_ReaderGoroutine_ExitsOnConnClose(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	br := bufio.NewReader(server)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		buf := make([]byte, wsMaxClientPayload)
		for {
			opcode, _, nb, err := wsReadClientFrame(br, buf)
			buf = nb
			if err != nil {
				return
			}
			if opcode == wsOpcodeClose {
				return
			}
		}
	}()

	// Simulate abrupt client disconnect — no WebSocket close frame.
	client.Close()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("peek reader goroutine did not exit after connection close")
	}
	select {
	case <-ctx.Done():
	default:
		t.Error("cancel() was not called by peek reader goroutine on connection close")
	}
}

// TestWsPeek_ReaderGoroutine_ExitsOnReadDeadline verifies that the peek handler's
// reader goroutine exits and calls cancel() when the read deadline fires with no
// client frames, simulating a network partition with stable tmux output (no diffs).
// This is the leak scenario fixed by the reader goroutine: without it the ticker
// loop never writes, never fires a write deadline, and loops forever.
func TestWsPeek_ReaderGoroutine_ExitsOnReadDeadline(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	// client is intentionally not closed — simulates a network partition where
	// no TCP FIN arrives; server cannot distinguish idle from silently dead.

	br := bufio.NewReader(server)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		buf := make([]byte, wsMaxClientPayload)
		for {
			// Mirrors production read-deadline fix; shorter timeout for test speed.
			server.SetReadDeadline(time.Now().Add(50 * time.Millisecond)) //nolint:errcheck
			opcode, _, nb, err := wsReadClientFrame(br, buf)
			buf = nb
			if err != nil {
				return
			}
			server.SetReadDeadline(time.Now().Add(50 * time.Millisecond)) //nolint:errcheck
			if opcode == wsOpcodeClose {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("peek reader goroutine did not exit after read deadline (network partition case)")
	}
	select {
	case <-ctx.Done():
	default:
		t.Error("cancel() was not called by peek reader goroutine on read deadline")
	}
	runtime.KeepAlive(client)
}

// TestWsReadClientFrame_PayloadSizeLimit verifies wsMaxClientPayload enforcement:
// frames with payload > 4096 must be rejected, payload == 4096 must be accepted.
func TestWsReadClientFrame_PayloadSizeLimit(t *testing.T) {
	// buildFrame constructs a masked client text frame with rawLen=126 and
	// the given extended payload length. The payload itself is zero-filled.
	buildFrame := func(extLen uint16) []byte {
		var frame []byte
		frame = append(frame, 0x81)       // FIN + text opcode
		frame = append(frame, 0x80|0x7E)  // masked + rawLen=126
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], extLen)
		frame = append(frame, ext[:]...)
		frame = append(frame, 0, 0, 0, 0) // mask key (all zeros — no-op XOR)
		frame = append(frame, make([]byte, extLen)...)
		return frame
	}

	t.Run("rejects_payload_exceeding_max", func(t *testing.T) {
		frame := buildFrame(5000) // > wsMaxClientPayload (4096)
		br := bufio.NewReader(bytes.NewReader(frame))
		_, _, _, err := wsReadClientFrame(br, make([]byte, 128))
		if err == nil {
			t.Fatal("expected error for payload > wsMaxClientPayload, got nil")
		}
		if !strings.Contains(err.Error(), "exceeds max") {
			t.Errorf("error = %q, want it to mention 'exceeds max'", err)
		}
	})

	t.Run("accepts_payload_at_max", func(t *testing.T) {
		frame := buildFrame(4096) // == wsMaxClientPayload
		br := bufio.NewReader(bytes.NewReader(frame))
		_, payload, _, err := wsReadClientFrame(br, make([]byte, 128))
		if err != nil {
			t.Fatalf("unexpected error for payload == wsMaxClientPayload: %v", err)
		}
		if len(payload) != 4096 {
			t.Errorf("payload length = %d, want 4096", len(payload))
		}
	})
}

// TestWsReadClientFrame_RejectsUnmaskedFrame verifies that wsReadClientFrame
// returns an error for unmasked client frames, as required by RFC 6455 §5.1.
// Browsers always mask frames; a forged unmasked frame indicates a non-browser
// client or a protocol violation.
func TestWsReadClientFrame_RejectsUnmaskedFrame(t *testing.T) {
	// Unmasked text frame: FIN+text (0x81), no mask bit, payload "hello".
	frame := []byte{0x81, 0x05, 'h', 'e', 'l', 'l', 'o'}
	br := bufio.NewReader(bytes.NewReader(frame))
	_, _, _, err := wsReadClientFrame(br, make([]byte, 128))
	if err == nil {
		t.Fatal("expected error for unmasked client frame, got nil")
	}
	if !strings.Contains(err.Error(), "unmasked") {
		t.Errorf("error = %q, want it to mention 'unmasked'", err)
	}
}

// TestHandleTuiTextFrame_ResizeMessage_CallsResize verifies that a well-formed
// resize JSON frame calls the resize function with the correct dimensions and
// does not write any bytes to the PTY.
func TestHandleTuiTextFrame_ResizeMessage_CallsResize(t *testing.T) {
	payload := []byte(`{"resize":{"cols":120,"rows":40}}`)
	var buf bytes.Buffer
	var gotCols, gotRows uint16

	handleTuiTextFrame(payload, &buf, func(cols, rows uint16) {
		gotCols = cols
		gotRows = rows
	})

	if gotCols != 120 {
		t.Errorf("resize cols = %d, want 120", gotCols)
	}
	if gotRows != 40 {
		t.Errorf("resize rows = %d, want 40", gotRows)
	}
	if buf.Len() != 0 {
		t.Errorf("PTY should not receive data on resize, got %d bytes", buf.Len())
	}
}

// TestHandleTuiTextFrame_NonJSONForwardedToPTY verifies that a non-JSON payload
// (e.g. a raw escape sequence from xterm.js onData) is written verbatim to the
// PTY and does not trigger the resize callback.
func TestHandleTuiTextFrame_NonJSONForwardedToPTY(t *testing.T) {
	payload := []byte("\x1b[A") // up arrow
	var buf bytes.Buffer
	resizeCalled := false

	handleTuiTextFrame(payload, &buf, func(cols, rows uint16) {
		resizeCalled = true
	})

	if resizeCalled {
		t.Error("resize should not be called for non-JSON payload")
	}
	if got := buf.Bytes(); !bytes.Equal(got, payload) {
		t.Errorf("PTY received %q, want %q", got, payload)
	}
}

// TestHandleTuiTextFrame_JSONWithoutResizeField_ForwardedToPTY verifies that a
// valid JSON frame whose top-level object has no "resize" key is treated as raw
// keystroke data and forwarded to the PTY unchanged.
func TestHandleTuiTextFrame_JSONWithoutResizeField_ForwardedToPTY(t *testing.T) {
	payload := []byte(`{"other":"value"}`)
	var buf bytes.Buffer
	resizeCalled := false

	handleTuiTextFrame(payload, &buf, func(cols, rows uint16) {
		resizeCalled = true
	})

	if resizeCalled {
		t.Error("resize should not be called for JSON without resize field")
	}
	if got := buf.Bytes(); !bytes.Equal(got, payload) {
		t.Errorf("PTY received %q, want %q", got, payload)
	}
}

// TestHandleTuiTextFrame_TableDriven exercises handleTuiTextFrame across
// multiple keystroke sequences to confirm each is forwarded to the PTY.
func TestHandleTuiTextFrame_TableDriven(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"up_arrow", []byte("\x1b[A")},
		{"down_arrow", []byte("\x1b[B")},
		{"enter", []byte("\r")},
		{"q_key", []byte("q")},
		{"ctrl_c", []byte("\x03")},
		{"r_key", []byte("r")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			handleTuiTextFrame(tc.payload, &buf, func(cols, rows uint16) {
				t.Error("resize should not be called for keystroke payload")
			})
			if got := buf.Bytes(); !bytes.Equal(got, tc.payload) {
				t.Errorf("PTY received %q, want %q", got, tc.payload)
			}
		})
	}
}

// TestDashboardHTML_OnDataForwardsKeystrokes verifies that the web dashboard
// HTML includes a term.onData handler that sends keystroke data to the server
// via WebSocket, enabling interactive TUI control in the browser.
func TestDashboardHTML_OnDataForwardsKeystrokes(t *testing.T) {
	if !strings.Contains(dashboardHTML, "term.onData") {
		t.Error("dashboardHTML must contain term.onData handler for keystroke forwarding")
	}
	if !strings.Contains(dashboardHTML, "ws.send(data)") {
		t.Error("dashboardHTML term.onData handler must forward keystrokes via ws.send(data)")
	}
}

// readWSTextFrame reads one unmasked WebSocket text frame from br and returns the payload.
func readWSTextFrame(br *bufio.Reader) (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		return "", err
	}
	if header[0] != 0x81 {
		return "", fmt.Errorf("unexpected frame byte[0]: 0x%02x, want 0x81", header[0])
	}
	rawLen := int(header[1] & 0x7F)
	var length int
	switch rawLen {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(br, ext); err != nil {
			return "", err
		}
		length = int(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(br, ext); err != nil {
			return "", err
		}
		length = int(binary.BigEndian.Uint64(ext))
	default:
		length = rawLen
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(br, payload); err != nil {
		return "", err
	}
	return string(payload), nil
}
