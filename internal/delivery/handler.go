package delivery

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// DropletAdder is the interface for persisting a new droplet.
type DropletAdder interface {
	Add(title, repo, description string, priority, complexity int) (string, error)
}

// Handler is an http.Handler for the droplet ingestion endpoint (POST /droplets).
// A Bearer token is required in the Authorization header; it is used as the
// per-token rate-limit key. No token whitelist is enforced — the Bearer value
// is an opaque bucket key, not a secret credential.
type Handler struct {
	adder       DropletAdder
	limiter     *RateLimiter
	trustedNets []*net.IPNet
}

// NewHandler returns a Handler that delegates droplet creation to adder and
// enforces limits via limiter. By default, loopback addresses (127.0.0.0/8
// and ::1/128) are trusted to forward proxy headers. Deploy behind a reverse
// proxy that strips X-Real-IP/X-Forwarded-For from untrusted sources.
func NewHandler(adder DropletAdder, limiter *RateLimiter) *Handler {
	return &Handler{
		adder:       adder,
		limiter:     limiter,
		trustedNets: loopbackNets(),
	}
}

// loopbackNets returns parsed CIDRs for IPv4 and IPv6 loopback ranges.
func loopbackNets() []*net.IPNet {
	var nets []*net.IPNet
	for _, cidr := range []string{"127.0.0.0/8", "::1/128"} {
		_, n, _ := net.ParseCIDR(cidr)
		if n != nil {
			nets = append(nets, n)
		}
	}
	return nets
}

// maxBodyBytes is the maximum number of bytes accepted in a request body.
// Prevents unbounded memory consumption from large payloads.
const maxBodyBytes = 1 << 20 // 1 MiB

type addRequest struct {
	Title       string `json:"title"`
	Repo        string `json:"repo"`
	Description string `json:"description,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	Complexity  int    `json:"complexity,omitempty"`
}

type addResponse struct {
	ID string `json:"id"`
}

// ServeHTTP handles POST /droplets. It returns:
//   - 401 Unauthorized  — missing or non-Bearer Authorization header
//   - 429 Too Many Requests — per-IP or per-token limit exceeded
//   - 400 Bad Request   — malformed JSON or missing required fields
//   - 500 Internal Server Error — storage failure
//   - 201 Created       — droplet accepted; body contains {"id":"..."}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := bearerToken(r)
	if token == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ip := realIP(r, h.trustedNets)
	if !h.limiter.Allow(ip, token) {
		w.Header().Set("Retry-After", strconv.Itoa(int(h.limiter.Window().Seconds())))
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req addRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if req.Repo == "" {
		http.Error(w, "repo is required", http.StatusBadRequest)
		return
	}

	id, err := h.adder.Add(req.Title, req.Repo, req.Description, req.Priority, req.Complexity)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(addResponse{ID: id}) //nolint:errcheck
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
// Returns an empty string if the header is absent or uses a different scheme.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimPrefix(auth, prefix)
}

// realIP returns the client's IP address. Proxy headers (X-Real-IP,
// X-Forwarded-For) are only honoured when RemoteAddr belongs to a trusted
// proxy network; otherwise RemoteAddr is used directly. This prevents
// per-IP rate-limit bypass via spoofed proxy headers.
func realIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if isTrustedProxy(net.ParseIP(host), trusted) {
		if ip := r.Header.Get("X-Real-IP"); ip != "" {
			return ip
		}
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			// X-Forwarded-For may be a comma-separated list; the leftmost is the client.
			return strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
		}
	}
	return host
}

// isTrustedProxy reports whether ip is contained in any of the trusted networks.
func isTrustedProxy(ip net.IP, trusted []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
