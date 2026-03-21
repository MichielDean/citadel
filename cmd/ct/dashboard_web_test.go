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
	if !strings.Contains(body, "EventSource") {
		t.Error("body should contain EventSource (SSE client code)")
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
	n := int(b[2])<<8 | int(b[3])
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
