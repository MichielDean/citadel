package evaluate

import (
	"encoding/json"
	"testing"
)

func TestAllDimensions(t *testing.T) {
	dims := AllDimensions()
	if len(dims) != 8 {
		t.Errorf("expected 8 dimensions, got %d", len(dims))
	}

	seen := make(map[Dimension]bool)
	for _, d := range dims {
		if seen[d] {
			t.Errorf("duplicate dimension: %s", d)
		}
		seen[d] = true
		desc := DimensionDescription(d)
		if desc == "unknown dimension" {
			t.Errorf("dimension %s has no description", d)
		}
		if desc == "" {
			t.Errorf("dimension %s has empty description", d)
		}
	}
}

func TestDimensionDescriptions(t *testing.T) {
	desc := dimensionDescriptions()
	if desc == "" {
		t.Error("expected non-empty dimension descriptions")
	}
	for _, d := range AllDimensions() {
		if !contains(desc, string(d)) {
			t.Errorf("dimension descriptions missing: %s", d)
		}
	}
}

func TestScoringPrompt(t *testing.T) {
	prompt := ScoringPrompt()
	if prompt == "" {
		t.Error("expected non-empty scoring prompt")
	}
	for _, d := range AllDimensions() {
		if !contains(prompt, string(d)) {
			t.Errorf("scoring prompt missing dimension: %s", d)
		}
	}
}

func TestParseEvaluationResult(t *testing.T) {
	body := `{
		"source": "cistern",
		"ticket": "PROJ-123",
		"branch": "feat/fix",
		"commit": "abc123",
		"model": "claude-sonnet-4-20250514",
		"scores": [
			{"dimension": "contract_correctness", "score": 4, "evidence": "all methods honor contracts", "suggested": "n/a"},
			{"dimension": "integration_coverage", "score": 3, "evidence": "missing test for loadPermissions", "suggested": "add integration test"},
			{"dimension": "coupling", "score": 2, "evidence": "hardcoded OrganizationTable.id", "suggested": "parameterize"},
			{"dimension": "migration_safety", "score": 5, "evidence": "no migrations in diff", "suggested": "n/a"},
			{"dimension": "idiom_fit", "score": 3, "evidence": "uses inSubQuery instead of Exists", "suggested": "use Exposed Exists DSL"},
			{"dimension": "dry", "score": 1, "evidence": "same bool extraction repeated 13 times", "suggested": "extract boolPerm helper"},
			{"dimension": "naming_clarity", "score": 2, "evidence": "PermissionColumnName is misleading", "suggested": "rename to PermissionName"},
			{"dimension": "error_messages", "score": 4, "evidence": "requireCatalogPermissionId is specific", "suggested": "n/a"}
		],
		"notes": "Significant issues with DRY and coupling."
	}`

	result, err := ParseEvaluationResult(body)
	if err != nil {
		t.Fatalf("ParseEvaluationResult failed: %v", err)
	}

	if result.Source != "cistern" {
		t.Errorf("expected source 'cistern', got %q", result.Source)
	}
	if result.Ticket != "PROJ-123" {
		t.Errorf("expected ticket 'PROJ-123', got %q", result.Ticket)
	}
	if len(result.Scores) != 8 {
		t.Errorf("expected 8 scores, got %d", len(result.Scores))
	}
	if result.TotalScore != 24 {
		t.Errorf("expected total score 24, got %d", result.TotalScore)
	}
	if result.MaxScore != 40 {
		t.Errorf("expected max score 40, got %d", result.MaxScore)
	}
	pct := result.Percentage()
	if pct != 60.0 {
		t.Errorf("expected 60%%, got %.0f%%", pct)
	}
}

func TestParseEvaluationResult_UnknownDimension(t *testing.T) {
	body := `{
		"scores": [
			{"dimension": "unknown_thing", "score": 5, "evidence": "test", "suggested": "test"}
		]
	}`

	_, err := ParseEvaluationResult(body)
	if err == nil {
		t.Error("expected error for unknown dimension")
	}
}

func TestParseEvaluationResult_InvalidScore(t *testing.T) {
	body := `{
		"scores": [
			{"dimension": "contract_correctness", "score": 7, "evidence": "test", "suggested": "test"}
		]
	}`

	_, err := ParseEvaluationResult(body)
	if err == nil {
		t.Error("expected error for score > 5")
	}
}

func TestResultJSON(t *testing.T) {
	r := &Result{
		Source:     "cistern",
		Ticket:     "PROJ-123",
		Branch:     "feat/fix",
		Commit:     "abc123",
		Model:      "test",
		Scores:     []Score{},
		TotalScore: 0,
		MaxScore:   40,
		Notes:      "test",
		Timestamp:  "2026-04-17T00:00:00Z",
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var r2 Result
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if r2.Source != "cistern" {
		t.Errorf("expected source 'cistern', got %q", r2.Source)
	}
}

func TestEvaluate_EmptyDiff(t *testing.T) {
	_, err := Evaluate("", "test", "cistern", "", "", "")
	if err == nil {
		t.Error("expected error for empty diff")
	}
}

func TestEvaluate_PlaceholderResult(t *testing.T) {
	result, err := Evaluate("some diff content", "test", "cistern", "PROJ-1", "feat/x", "abc")
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if result.Source != "cistern" {
		t.Errorf("expected source 'cistern', got %q", result.Source)
	}
	if result.MaxScore != 40 {
		t.Errorf("expected max score 40, got %d", result.MaxScore)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && (s[:len(substr)] == substr || contains(s[1:], substr)))
}