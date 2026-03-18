package main

import (
	"encoding/json"
	"testing"
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
