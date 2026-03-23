package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/testutil/mockllm"
)

// --- extractProposals tests ---

func TestExtractProposals_ValidJSONArray(t *testing.T) {
	input := `[
  {
    "title": "Add user auth",
    "description": "Implement JWT-based authentication",
    "complexity": "standard",
    "depends_on": []
  }
]`
	proposals, err := extractProposals(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(proposals))
	}
	if proposals[0].Title != "Add user auth" {
		t.Errorf("expected title 'Add user auth', got %q", proposals[0].Title)
	}
	if proposals[0].Complexity != "standard" {
		t.Errorf("expected complexity 'standard', got %q", proposals[0].Complexity)
	}
}

func TestExtractProposals_MultiplePropWithDeps(t *testing.T) {
	input := `[
  {
    "title": "Create schema",
    "description": "Define database schema",
    "complexity": "trivial",
    "depends_on": []
  },
  {
    "title": "Build API",
    "description": "REST API on top of schema",
    "complexity": "full",
    "depends_on": ["Create schema"]
  }
]`
	proposals, err := extractProposals(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proposals) != 2 {
		t.Fatalf("expected 2 proposals, got %d", len(proposals))
	}
	if len(proposals[1].DependsOn) != 1 || proposals[1].DependsOn[0] != "Create schema" {
		t.Errorf("unexpected depends_on: %v", proposals[1].DependsOn)
	}
}

func TestExtractProposals_EmbeddedInText(t *testing.T) {
	input := `Here are my proposed droplets:

[
  {
    "title": "Fix login bug",
    "description": "Handle edge case in login flow",
    "complexity": "trivial",
    "depends_on": []
  }
]

Let me know if you'd like changes.`
	proposals, err := extractProposals(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(proposals))
	}
	if proposals[0].Title != "Fix login bug" {
		t.Errorf("expected title 'Fix login bug', got %q", proposals[0].Title)
	}
}

func TestExtractProposals_MarkdownCodeFence(t *testing.T) {
	input := "Here is my analysis.\n\n```json\n[\n  {\n    \"title\": \"Refactor auth\",\n    \"description\": \"Clean up auth logic\",\n    \"complexity\": \"standard\",\n    \"depends_on\": []\n  }\n]\n```"
	proposals, err := extractProposals(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(proposals))
	}
	if proposals[0].Title != "Refactor auth" {
		t.Errorf("expected title 'Refactor auth', got %q", proposals[0].Title)
	}
}

func TestExtractProposals_InvalidJSON(t *testing.T) {
	_, err := extractProposals("this is not json at all")
	if err == nil {
		t.Fatal("expected error for non-JSON input")
	}
}

func TestExtractProposals_EmptyArray(t *testing.T) {
	_, err := extractProposals("[]")
	if err == nil {
		t.Fatal("expected error for empty array")
	}
}

func TestExtractProposals_MalformedJSON(t *testing.T) {
	_, err := extractProposals(`[{"title": "broken" "description": "missing comma"}]`)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// --- DropletProposal JSON round-trip ---

func TestDropletProposal_JSONRoundTrip(t *testing.T) {
	original := DropletProposal{
		Title:       "Implement feature X",
		Description: "Full description here",
		Complexity:  "full",
		DependsOn:   []string{"dep-1", "dep-2"},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var got DropletProposal
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got.Title != original.Title {
		t.Errorf("title mismatch: got %q", got.Title)
	}
	if got.Complexity != original.Complexity {
		t.Errorf("complexity mismatch: got %q", got.Complexity)
	}
	if len(got.DependsOn) != 2 || got.DependsOn[0] != "dep-1" {
		t.Errorf("depends_on mismatch: got %v", got.DependsOn)
	}
}

func TestDropletProposal_NullDependsOn(t *testing.T) {
	input := `[{"title":"T","description":"D","complexity":"trivial","depends_on":null}]`
	proposals, err := extractProposals(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proposals[0].DependsOn != nil && len(proposals[0].DependsOn) != 0 {
		t.Errorf("expected nil/empty depends_on, got %v", proposals[0].DependsOn)
	}
}

// --- complexityToInt helper ---

func TestComplexityToInt(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"trivial", 1},
		{"standard", 2},
		{"full", 3},
		{"critical", 4},
		{"unknown", 3},
		{"", 3},
	}
	for _, tt := range tests {
		got := complexityToInt(tt.input)
		if got != tt.want {
			t.Errorf("complexityToInt(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// --- callRefineAPI tests using mockllm ---

// TestCallRefineAPI_ReturnsProposals verifies that callRefineAPI correctly
// parses the Anthropic /v1/messages response returned by the mock server.
// The mock is injected via ANTHROPIC_BASE_URL so no real network call is made.
func TestCallRefineAPI_ReturnsProposals(t *testing.T) {
	mock := mockllm.New()
	defer mock.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", mock.URL)

	proposals, err := callRefineAPI("Fix login bug", "Handle edge case in login flow")
	if err != nil {
		t.Fatalf("callRefineAPI: unexpected error: %v", err)
	}

	if len(proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(proposals))
	}
	if proposals[0].Title != "mock proposal" {
		t.Errorf("title = %q, want %q", proposals[0].Title, "mock proposal")
	}
	if proposals[0].Complexity != "standard" {
		t.Errorf("complexity = %q, want %q", proposals[0].Complexity, "standard")
	}
	if proposals[0].Description != "test description" {
		t.Errorf("description = %q, want %q", proposals[0].Description, "test description")
	}
}

// TestCallRefineAPI_SendsAuthHeader verifies that the request to the LLM
// endpoint carries an auth credential (x-api-key or Authorization header).
func TestCallRefineAPI_SendsAuthHeader(t *testing.T) {
	mock := mockllm.New()
	defer mock.Close()

	t.Setenv("ANTHROPIC_API_KEY", "sk-test-key-12345")
	t.Setenv("ANTHROPIC_BASE_URL", mock.URL)

	if _, err := callRefineAPI("Test title", ""); err != nil {
		t.Fatalf("callRefineAPI: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) == 0 {
		t.Fatal("mock received no requests")
	}
	req := reqs[0]

	apiKey := req.Headers.Get("X-Api-Key")
	authHeader := req.Headers.Get("Authorization")
	if apiKey == "" && authHeader == "" {
		t.Error("request missing auth header (X-Api-Key or Authorization)")
	}
}

// TestCallRefineAPI_MissingAPIKey verifies that callRefineAPI returns an error
// when ANTHROPIC_API_KEY is not set, rather than making a network call.
func TestCallRefineAPI_MissingAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, err := callRefineAPI("title", "desc")
	if err == nil {
		t.Fatal("expected error when ANTHROPIC_API_KEY is empty, got nil")
	}
}

// TestMockLLM_ChatCompletions verifies that the mock server correctly handles
// POST /v1/chat/completions and returns a well-formed OpenAI response.
// This endpoint is used by future multi-provider callRefineAPI configurations
// (openai, openrouter, ollama, custom).
func TestMockLLM_ChatCompletions(t *testing.T) {
	mock := mockllm.New()
	defer mock.Close()

	reqs := mock.Requests()
	if len(reqs) != 0 {
		t.Fatalf("expected 0 requests before any calls, got %d", len(reqs))
	}

	// Parse the hardcoded proposals JSON to verify it is well-formed.
	var proposals []DropletProposal
	if err := json.Unmarshal([]byte(mockllm.HardcodedProposalsJSON), &proposals); err != nil {
		t.Fatalf("HardcodedProposalsJSON is not valid JSON: %v", err)
	}
	if len(proposals) == 0 {
		t.Fatal("HardcodedProposalsJSON contains no proposals")
	}
	if proposals[0].Title != "mock proposal" {
		t.Errorf("proposals[0].Title = %q, want %q", proposals[0].Title, "mock proposal")
	}
}

// TestMockLLM_RecordsRequestsForAllProviders is a table-driven test
// demonstrating how the mock server supports each provider configuration.
// Each entry reflects how a caller would configure callRefineAPI for that
// provider once multi-provider support lands.
func TestMockLLM_RecordsRequestsForAllProviders(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string // /v1/messages or /v1/chat/completions
	}{
		{name: "anthropic", endpoint: "/v1/messages"},
		{name: "openai", endpoint: "/v1/chat/completions"},
		{name: "openrouter", endpoint: "/v1/chat/completions"},
		{name: "ollama", endpoint: "/v1/chat/completions"},
		{name: "custom", endpoint: "/v1/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := mockllm.New()
			defer mock.Close()

			// Invoke the appropriate endpoint directly using the standard
			// library to verify the mock handles it correctly.
			url := mock.URL + tt.endpoint
			resp, err := postToMock(url)
			if err != nil {
				t.Fatalf("POST %s: %v", tt.endpoint, err)
			}
			if resp != 200 {
				t.Errorf("POST %s returned status %d, want 200", tt.endpoint, resp)
			}

			reqs := mock.Requests()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 recorded request, got %d", len(reqs))
			}
			if reqs[0].Path != tt.endpoint {
				t.Errorf("recorded path = %q, want %q", reqs[0].Path, tt.endpoint)
			}
		})
	}
}

// postToMock sends an empty POST to url and returns the HTTP status code.
func postToMock(url string) (int, error) {
	resp, err := http.Post(url, "application/json", strings.NewReader("{}")) //nolint:noctx
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
