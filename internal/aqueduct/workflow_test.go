package aqueduct

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testdataPath(name string) string {
	return filepath.Join("testdata", name)
}

func TestParseValidWorkflow(t *testing.T) {
	w, err := ParseWorkflow(testdataPath("valid_workflow.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Name != "feature" {
		t.Errorf("name = %q, want %q", w.Name, "feature")
	}
	if len(w.Cataractae) != 4 {
		t.Fatalf("got %d steps, want 4", len(w.Cataractae))
	}

	impl := w.Cataractae[0]
	if impl.Name != "implement" {
		t.Errorf("step[0].Name = %q, want %q", impl.Name, "implement")
	}
	if impl.Type != CataractaeTypeAgent {
		t.Errorf("step[0].Type = %q, want %q", impl.Type, CataractaeTypeAgent)
	}
	if impl.Identity != "implementer" {
		t.Errorf("step[0].Role = %q, want %q", impl.Identity, "implementer")
	}
	if impl.Model != "sonnet" {
		t.Errorf("step[0].Model = %q, want %q", impl.Model, "sonnet")
	}
	if impl.Context != ContextFullCodebase {
		t.Errorf("step[0].Context = %q, want %q", impl.Context, ContextFullCodebase)
	}
	if impl.TimeoutMinutes != 30 {
		t.Errorf("step[0].TimeoutMinutes = %d, want 30", impl.TimeoutMinutes)
	}
	if impl.OnPass != "review" {
		t.Errorf("step[0].OnPass = %q, want %q", impl.OnPass, "review")
	}
	if impl.OnFail != "blocked" {
		t.Errorf("step[0].OnFail = %q, want %q", impl.OnFail, "blocked")
	}

	review := w.Cataractae[1]
	if review.OnRecirculate != "implement" {
		t.Errorf("step[1].OnRecirculate = %q, want %q", review.OnRecirculate, "implement")
	}
	if review.OnEscalate != "human" {
		t.Errorf("step[1].OnEscalate = %q, want %q", review.OnEscalate, "human")
	}

	merge := w.Cataractae[3]
	if merge.Type != CataractaeTypeAutomated {
		t.Errorf("step[3].Type = %q, want %q", merge.Type, CataractaeTypeAutomated)
	}
}

func TestCircularRouteError(t *testing.T) {
	_, err := ParseWorkflow(testdataPath("circular_route.yaml"))
	if err == nil {
		t.Fatal("expected circular route error, got nil")
	}
	if !strings.Contains(err.Error(), "circular route") {
		t.Errorf("error = %q, want it to contain 'circular route'", err)
	}
}

func TestMissingRefError(t *testing.T) {
	_, err := ParseWorkflow(testdataPath("missing_ref.yaml"))
	if err == nil {
		t.Fatal("expected missing ref error, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent-step") {
		t.Errorf("error = %q, want it to mention 'nonexistent-step'", err)
	}
	if !strings.Contains(err.Error(), "unknown cataractae") {
		t.Errorf("error = %q, want it to contain 'unknown cataractae'", err)
	}
}

func TestUnknownTypeError(t *testing.T) {
	_, err := ParseWorkflow(testdataPath("unknown_type.yaml"))
	if err == nil {
		t.Fatal("expected unknown type error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error = %q, want it to contain 'unknown type'", err)
	}
	if !strings.Contains(err.Error(), "magic") {
		t.Errorf("error = %q, want it to mention 'magic'", err)
	}
}

func TestParseWorkflowBytes(t *testing.T) {
	yaml := `
name: simple
cataractae:
  - name: do-thing
    type: gate
    on_pass: done
`
	w, err := ParseWorkflowBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Name != "simple" {
		t.Errorf("name = %q, want %q", w.Name, "simple")
	}
	if len(w.Cataractae) != 1 {
		t.Fatalf("got %d steps, want 1", len(w.Cataractae))
	}
	if w.Cataractae[0].Type != CataractaeTypeGate {
		t.Errorf("type = %q, want %q", w.Cataractae[0].Type, CataractaeTypeGate)
	}
}

func TestValidateEmptyName(t *testing.T) {
	w := &Workflow{Cataractae: []WorkflowCataractae{{Name: "x", Type: CataractaeTypeAgent}}}
	err := Validate(w)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected name required error, got %v", err)
	}
}

func TestValidateNoSteps(t *testing.T) {
	w := &Workflow{Name: "empty"}
	err := Validate(w)
	if err == nil || !strings.Contains(err.Error(), "no cataractae") {
		t.Errorf("expected no cataractae error, got %v", err)
	}
}

func TestValidateDuplicateStepName(t *testing.T) {
	w := &Workflow{
		Name: "dup",
		Cataractae: []WorkflowCataractae{
			{Name: "a", Type: CataractaeTypeAgent, OnPass: "done"},
			{Name: "a", Type: CataractaeTypeAgent, OnPass: "done"},
		},
	}
	err := Validate(w)
	if err == nil || !strings.Contains(err.Error(), "duplicate cataractae name") {
		t.Errorf("expected duplicate cataractae error, got %v", err)
	}
}

// --- AqueductConfig validation tests ---

func TestValidateFarmConfig_Valid(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{
			{Name: "ScaledTest", Cataractae: 2, Names: []string{"cascade", "tributary"}, Prefix: "st"},
			{Name: "cistern", Cataractae: 1, Names: []string{"confluence"}, Prefix: "ct"},
		},
		MaxCataractae: 3,
	}
	if err := ValidateAqueductConfig(cfg); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestValidateFarmConfig_NoRepos(t *testing.T) {
	cfg := &AqueductConfig{MaxCataractae: 1}
	err := ValidateAqueductConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "at least one repo") {
		t.Errorf("expected at least one repo error, got %v", err)
	}
}

func TestValidateFarmConfig_MaxTotalWorkersZero(t *testing.T) {
	cfg := &AqueductConfig{
		Repos:           []RepoConfig{{Name: "r1", Cataractae: 1}},
		MaxCataractae: 0,
	}
	err := ValidateAqueductConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "max_cataractae") {
		t.Errorf("expected max_cataractae error, got %v", err)
	}
}

func TestValidateFarmConfig_DuplicateRepoName(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{
			{Name: "dup", Cataractae: 1, Prefix: "a"},
			{Name: "dup", Cataractae: 1, Prefix: "b"},
		},
		MaxCataractae: 2,
	}
	err := ValidateAqueductConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "duplicate repo name") {
		t.Errorf("expected duplicate repo name error, got %v", err)
	}
}

func TestValidateFarmConfig_DuplicatePrefix(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{
			{Name: "r1", Cataractae: 1, Prefix: "shared"},
			{Name: "r2", Cataractae: 1, Prefix: "shared"},
		},
		MaxCataractae: 2,
	}
	err := ValidateAqueductConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "share prefix") {
		t.Errorf("expected shared prefix error, got %v", err)
	}
}

func TestValidateFarmConfig_WorkersNamesMismatch(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{
			{Name: "r1", Cataractae: 3, Names: []string{"a", "b"}},
		},
		MaxCataractae: 3,
	}
	err := ValidateAqueductConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "cataractae=3 but names has 2") {
		t.Errorf("expected cataractae/names mismatch error, got %v", err)
	}
}

func TestValidateFarmConfig_ZeroWorkers(t *testing.T) {
	cfg := &AqueductConfig{
		Repos:           []RepoConfig{{Name: "r1", Cataractae: 0}},
		MaxCataractae: 1,
	}
	err := ValidateAqueductConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "cataractae must be > 0") {
		t.Errorf("expected workers > 0 error, got %v", err)
	}
}

func TestValidateFarmConfig_NamesOnly(t *testing.T) {
	// Names specified, workers omitted — should infer worker count from names.
	cfg := &AqueductConfig{
		Repos: []RepoConfig{
			{Name: "r1", Names: []string{"a", "b"}},
		},
		MaxCataractae: 2,
	}
	if err := ValidateAqueductConfig(cfg); err != nil {
		t.Fatalf("names-only config should be valid, got: %v", err)
	}
}

func TestValidateFarmConfig_MissingRepoName(t *testing.T) {
	cfg := &AqueductConfig{
		Repos:           []RepoConfig{{Cataractae: 1}},
		MaxCataractae: 1,
	}
	err := ValidateAqueductConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected name required error, got %v", err)
	}
}

func TestTerminalRefsAreValid(t *testing.T) {
	// "done", "blocked", "human", "escalate" should be accepted as targets.
	yaml := `
name: terminals
cataractae:
  - name: s1
    type: agent
    on_pass: done
    on_fail: blocked
    on_recirculate: human
    on_escalate: escalate
`
	_, err := ParseWorkflowBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("terminal refs should be valid, got: %v", err)
	}
}

// --- GenerateCataractaeFiles tests ---

func minimalWorkflowWithDef(key, name, desc, instr string) *Workflow {
	return &Workflow{
		Name:       "test",
		Cataractae: []WorkflowCataractae{{Name: "step", Type: CataractaeTypeAgent, OnPass: "done"}},
		CataractaeDefinitions: map[string]CataractaeDefinition{
			key: {Name: name, Description: desc, Instructions: instr},
		},
	}
}

func TestGenerateCataractaeFiles_WithPersonaAndInstructions(t *testing.T) {
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "reviewer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "PERSONA.md"), []byte("# Role: Reviewer\n\nI am the reviewer."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "INSTRUCTIONS.md"), []byte("## Protocol\n\n1. Review carefully."), 0o644); err != nil {
		t.Fatal(err)
	}

	w := minimalWorkflowWithDef("reviewer", "Reviewer", "A reviewer.", "")
	written, err := GenerateCataractaeFiles(w, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d", len(written))
	}

	content, err := os.ReadFile(filepath.Join(identityDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	if !strings.Contains(got, "generated by ct cataractae generate") {
		t.Error("CLAUDE.md missing generated header")
	}
	if !strings.Contains(got, "I am the reviewer.") {
		t.Error("CLAUDE.md missing persona content")
	}
	if !strings.Contains(got, "1. Review carefully.") {
		t.Error("CLAUDE.md missing instructions content")
	}
}

func TestGenerateCataractaeFiles_FallbackToInlineInstructions(t *testing.T) {
	tmpDir := t.TempDir()
	w := minimalWorkflowWithDef("implementer", "Implementer", "An implementer.", "Do the work.")

	written, err := GenerateCataractaeFiles(w, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d", len(written))
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "implementer", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	if !strings.Contains(got, "# Role: Implementer") {
		t.Error("fallback CLAUDE.md missing role header")
	}
	if !strings.Contains(got, "Do the work.") {
		t.Error("fallback CLAUDE.md missing inline instructions")
	}
	if strings.Contains(got, "generated by ct cataractae generate") {
		t.Error("fallback CLAUDE.md should not have generated header")
	}
}

func TestGenerateCataractaeFiles_PersonaOnlyFallsBack(t *testing.T) {
	// If only PERSONA.md exists (no INSTRUCTIONS.md), fall back to legacy format.
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "qa")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "PERSONA.md"), []byte("# Role: QA\n\nI am QA."), 0o644); err != nil {
		t.Fatal(err)
	}
	// No INSTRUCTIONS.md written.

	w := minimalWorkflowWithDef("qa", "QA Reviewer", "Quality reviewer.", "Run tests.")
	_, err := GenerateCataractaeFiles(w, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(identityDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	if strings.Contains(got, "generated by ct cataractae generate") {
		t.Error("partial-files CLAUDE.md should fall back to legacy, not use generated header")
	}
	if !strings.Contains(got, "Run tests.") {
		t.Error("fallback CLAUDE.md should contain inline instructions")
	}
}
