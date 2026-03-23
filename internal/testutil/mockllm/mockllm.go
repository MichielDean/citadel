// Package mockllm provides a fake LLM HTTP server for use in tests.
//
// The server handles two endpoints that together cover all Cistern provider
// configurations:
//
//   - POST /v1/messages        — Anthropic Messages API format
//   - POST /v1/chat/completions — OpenAI-compatible chat completions format
//
// Both endpoints return a hardcoded JSON proposal array wrapped in the
// appropriate envelope. Tests can inspect [Server.Requests] to assert on the
// model name, auth header, and message bodies that were sent.
//
// Typical usage:
//
//	mock := mockllm.New()
//	defer mock.Close()
//
//	t.Setenv("ANTHROPIC_API_KEY", "test-key")
//	t.Setenv("ANTHROPIC_BASE_URL", mock.URL)
//
//	proposals, err := callRefineAPI("Fix login bug", "")
//	// assert proposals == mockllm.HardcodedProposals parsed
//
//	reqs := mock.Requests()
//	// assert auth header, model, messages...
package mockllm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// HardcodedProposalsJSON is the raw JSON array that every mock response embeds.
// Tests can unmarshal this to verify the full round-trip.
const HardcodedProposalsJSON = `[{"title":"mock proposal","description":"test description","complexity":"standard","depends_on":[]}]`

// Request is a snapshot of an HTTP request received by the mock server.
type Request struct {
	Method  string
	Path    string
	Headers http.Header
	Body    []byte
}

// Server is a fake LLM HTTP server backed by [httptest.Server].
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	requests []Request
}

// New creates and starts a new mock LLM server. Call [Server.Close] when done.
func New() *Server {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", s.handleMessages)
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	s.Server = httptest.NewServer(mux)
	return s
}

// Requests returns a snapshot of all HTTP requests received so far.
// Safe to call concurrently.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	copy(out, s.requests)
	return out
}

// record stores a snapshot of the incoming request.
func (s *Server) record(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	s.mu.Lock()
	s.requests = append(s.requests, Request{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: r.Header.Clone(),
		Body:    body,
	})
	s.mu.Unlock()
}

// handleMessages responds to POST /v1/messages with a well-formed Anthropic
// Messages API response containing [HardcodedProposalsJSON] as the text block.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.record(r)
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"id":            "msg_test0000000000000001",
		"type":          "message",
		"role":          "assistant",
		"content":       []map[string]string{{"type": "text", "text": HardcodedProposalsJSON}},
		"model":         "claude-haiku-4-5-20251001",
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage":         map[string]int{"input_tokens": 10, "output_tokens": 20},
	}
	json.NewEncoder(w).Encode(resp)
}

// handleChatCompletions responds to POST /v1/chat/completions with a
// well-formed OpenAI chat completion response containing [HardcodedProposalsJSON]
// as the assistant message content.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.record(r)
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"id":     "chatcmpl-test0001",
		"object": "chat.completion",
		"model":  "claude-haiku-4-5-20251001",
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": HardcodedProposalsJSON,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     10,
			"completion_tokens": 20,
			"total_tokens":      30,
		},
	}
	json.NewEncoder(w).Encode(resp)
}
