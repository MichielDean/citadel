package delivery

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeAdder is a test double for DropletAdder.
type fakeAdder struct {
	id  string
	err error
}

func (f *fakeAdder) Add(title, repo, description string, priority, complexity int) (string, error) {
	return f.id, f.err
}

func newTestHandler(t testing.TB, adder DropletAdder, ipLimit, tokenLimit int) *Handler {
	clk := &mockClock{t: time.Now()}
	rl := newTestLimiter(t, ipLimit, tokenLimit, time.Minute, clk)
	return NewHandler(adder, rl)
}

func TestHandler_Success(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 100, 100)

	body := `{"title":"my feature","repo":"github.com/org/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var resp struct{ ID string `json:"id"` }
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != "ct-abc12" {
		t.Errorf("id = %q, want %q", resp.ID, "ct-abc12")
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 100, 100)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/droplets", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, w.Code)
		}
	}
}

func TestHandler_NoAuth(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 100, 100)

	body := `{"title":"my feature","repo":"github.com/org/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
	// No Authorization header.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_MalformedBearerToken(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 100, 100)

	body := `{"title":"my feature","repo":"github.com/org/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz") // not Bearer
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for non-Bearer auth, got %d", w.Code)
	}
}

func TestHandler_RateLimitedByIP(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 2, 100)

	makeReq := func() *http.Request {
		body := `{"title":"t","repo":"github.com/org/repo"}`
		req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok-a")
		req.RemoteAddr = "1.2.3.4:9999"
		return req
	}

	// First two should pass.
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, makeReq())
		if w.Code != http.StatusCreated {
			t.Fatalf("request %d: expected 201, got %d", i+1, w.Code)
		}
	}

	// Third should be rate limited.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, makeReq())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after IP limit, got %d", w.Code)
	}
}

func TestHandler_RateLimitedByToken(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 100, 2)

	makeReq := func(ip string) *http.Request {
		body := `{"title":"t","repo":"github.com/org/repo"}`
		req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer shared-token")
		req.RemoteAddr = ip + ":9999"
		return req
	}

	// Two different IPs, same token — both pass.
	ips := []string{"1.1.1.1", "2.2.2.2"}
	for _, ip := range ips {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, makeReq(ip))
		if w.Code != http.StatusCreated {
			t.Fatalf("ip %s: expected 201, got %d", ip, w.Code)
		}
	}

	// Third request with same token from a fresh IP should be rate limited.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, makeReq("3.3.3.3"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after token limit, got %d", w.Code)
	}
}

func TestHandler_RetryAfterHeader(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 1, 100)

	makeReq := func() *http.Request {
		body := `{"title":"t","repo":"github.com/org/repo"}`
		req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok-a")
		req.RemoteAddr = "1.2.3.4:9999"
		return req
	}

	h.ServeHTTP(httptest.NewRecorder(), makeReq()) // consume the limit

	w := httptest.NewRecorder()
	h.ServeHTTP(w, makeReq())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header in 429 response")
	}
}

func TestHandler_InvalidBody(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 100, 100)

	req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader("not-json"))
	req.Header.Set("Authorization", "Bearer tok-a")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_MissingTitle(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 100, 100)

	body := `{"repo":"github.com/org/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok-a")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing title, got %d", w.Code)
	}
}

func TestHandler_MissingRepo(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 100, 100)

	body := `{"title":"my feature"}`
	req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok-a")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing repo, got %d", w.Code)
	}
}

func TestHandler_AdderError(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{err: errors.New("db error")}, 100, 100)

	body := `{"title":"my feature","repo":"github.com/org/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok-a")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on adder error, got %d", w.Code)
	}
}

func TestHandler_RealIPFromXRealIP(t *testing.T) {
	// IP extracted from X-Real-IP should be used for rate limiting.
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 1, 100)

	makeReq := func() *http.Request {
		body := `{"title":"t","repo":"github.com/org/repo"}`
		req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok-a")
		req.Header.Set("X-Real-IP", "10.20.30.40")
		req.RemoteAddr = "127.0.0.1:9999" // proxy address, different from real IP
		return req
	}

	h.ServeHTTP(httptest.NewRecorder(), makeReq()) // consume limit for 10.20.30.40

	w := httptest.NewRecorder()
	h.ServeHTTP(w, makeReq())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 using X-Real-IP, got %d", w.Code)
	}
}

func TestHandler_RealIPFromXForwardedFor(t *testing.T) {
	// When X-Forwarded-For is set, the first entry is used.
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 1, 100)

	makeReq := func() *http.Request {
		body := `{"title":"t","repo":"github.com/org/repo"}`
		req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok-a")
		req.Header.Set("X-Forwarded-For", "192.168.1.1, 10.0.0.1")
		req.RemoteAddr = "127.0.0.1:9999"
		return req
	}

	h.ServeHTTP(httptest.NewRecorder(), makeReq())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, makeReq())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 using X-Forwarded-For, got %d", w.Code)
	}
}

func TestHandler_UntrustedProxyHeadersIgnored(t *testing.T) {
	// Verify that X-Real-IP / X-Forwarded-For are ignored when RemoteAddr is not
	// a trusted proxy. This is the primary defence against per-IP rate-limit
	// bypass. A regression in isTrustedProxy would let any client spoof its IP.
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 1, 100)

	makeReq := func(remoteAddr, xRealIP string) *http.Request {
		body := `{"title":"t","repo":"github.com/org/repo"}`
		req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok-a")
		req.RemoteAddr = remoteAddr
		req.Header.Set("X-Real-IP", xRealIP)
		return req
	}

	// Consume the rate limit for 5.6.7.8 (an untrusted RemoteAddr).
	h.ServeHTTP(httptest.NewRecorder(), makeReq("5.6.7.8:9999", "1.2.3.4"))

	// A second request from the same RemoteAddr must be blocked.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, makeReq("5.6.7.8:9999", "1.2.3.4"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for exhausted RemoteAddr, got %d", w.Code)
	}

	// A request from a fresh RemoteAddr sharing the same X-Real-IP must succeed,
	// proving the header was not used as the rate-limit key.
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, makeReq("9.9.9.9:9999", "1.2.3.4"))
	if w2.Code != http.StatusCreated {
		t.Fatalf("expected 201 for fresh RemoteAddr with reused X-Real-IP, got %d", w2.Code)
	}
}

func TestHandler_BodyTooLarge(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 100, 100)

	// Build valid JSON whose total size exceeds maxBodyBytes (1 MiB).
	// Using null/zero bytes would cause json.Decode to fail before MaxBytesReader
	// is ever consulted, giving a false pass. A valid JSON string field ensures
	// the decoder reads far enough into the body to trigger the size limit.
	desc := strings.Repeat("x", maxBodyBytes)
	body := `{"title":"t","repo":"r","description":"` + desc + `"}`
	req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok-a")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d", w.Code)
	}
}

func TestHandler_ContentTypeJSON(t *testing.T) {
	h := newTestHandler(t, &fakeAdder{id: "ct-abc12"}, 100, 100)

	body := `{"title":"my feature","repo":"github.com/org/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/droplets", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok-a")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
