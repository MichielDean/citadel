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
	if impl.Model == nil || *impl.Model != "sonnet" {
		t.Errorf("step[0].Model = %v, want %q", impl.Model, "sonnet")
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
	if impl.OnFail != "pooled" {
		t.Errorf("step[0].OnFail = %q, want %q", impl.OnFail, "pooled")
	}

	review := w.Cataractae[1]
	if review.OnRecirculate != "implement" {
		t.Errorf("step[1].OnRecirculate = %q, want %q", review.OnRecirculate, "implement")
	}
	if review.OnPool != "human" {
		t.Errorf("step[1].OnPool = %q, want %q", review.OnPool, "human")
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

func TestValidateCisternConfig_Valid(t *testing.T) {
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

func TestValidateCisternConfig_NoRepos(t *testing.T) {
	cfg := &AqueductConfig{MaxCataractae: 1}
	err := ValidateAqueductConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "at least one repo") {
		t.Errorf("expected at least one repo error, got %v", err)
	}
}

func TestValidateCisternConfig_MaxCataractaeIsNoOp(t *testing.T) {
	// max_cataractae is deprecated — setting it to 0 should not cause a validation error.
	// Capping is per-repo via pool size.
	cfg := &AqueductConfig{
		Repos:         []RepoConfig{{Name: "r1", Cataractae: 1, Prefix: "r1"}},
		MaxCataractae: 0,
	}
	if err := ValidateAqueductConfig(cfg); err != nil {
		t.Errorf("expected no error with max_cataractae=0 (deprecated), got %v", err)
	}
}

func TestValidateCisternConfig_DuplicateRepoName(t *testing.T) {
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

func TestValidateCisternConfig_DuplicatePrefix(t *testing.T) {
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

func TestValidateCisternConfig_WorkersNamesMismatch(t *testing.T) {
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

func TestValidateCisternConfig_ZeroWorkers(t *testing.T) {
	cfg := &AqueductConfig{
		Repos:         []RepoConfig{{Name: "r1", Cataractae: 0}},
		MaxCataractae: 1,
	}
	err := ValidateAqueductConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "cataractae must be > 0") {
		t.Errorf("expected workers > 0 error, got %v", err)
	}
}

func TestValidateCisternConfig_NamesOnly(t *testing.T) {
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

func TestValidateCisternConfig_MissingRepoName(t *testing.T) {
	cfg := &AqueductConfig{
		Repos:         []RepoConfig{{Cataractae: 1}},
		MaxCataractae: 1,
	}
	err := ValidateAqueductConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected name required error, got %v", err)
	}
}

func TestValidateModelMustBeNonEmpty(t *testing.T) {
	cases := []struct {
		name  string
		model string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model
			w := &Workflow{
				Name: "test",
				Cataractae: []WorkflowCataractae{
					{Name: "step", Type: CataractaeTypeAgent, Model: &m, OnPass: "done"},
				},
			}
			err := Validate(w)
			if err == nil || !strings.Contains(err.Error(), "non-empty string") {
				t.Errorf("expected non-empty model error, got %v", err)
			}
		})
	}
}

func TestTerminalRefsAreValid(t *testing.T) {
	// "done", "pooled", "human", "pool" should be accepted as targets.
	yaml := `
name: terminals
cataractae:
  - name: s1
    type: agent
    on_pass: done
    on_fail: pooled
    on_recirculate: human
    on_pool: pool
`
	_, err := ParseWorkflowBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("terminal refs should be valid, got: %v", err)
	}
}

// --- GenerateCataractaeFiles tests ---

// workflowWithIdentity creates a minimal workflow with one agent step using the given identity.
func workflowWithIdentity(identity string) *Workflow {
	return &Workflow{
		Name: "test",
		Cataractae: []WorkflowCataractae{
			{Name: "step", Type: CataractaeTypeAgent, Identity: identity, OnPass: "done"},
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

	w := workflowWithIdentity("reviewer")
	written, err := GenerateCataractaeFiles(w, tmpDir, "")
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

func TestGenerateCataractaeFiles_SkipsWhenNeitherFileExists(t *testing.T) {
	// When PERSONA.md and INSTRUCTIONS.md are absent, the identity is skipped.
	tmpDir := t.TempDir()
	w := workflowWithIdentity("implementer")

	written, err := GenerateCataractaeFiles(w, tmpDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("expected 0 files written when source files missing, got %d", len(written))
	}
}

func TestGenerateCataractaeFiles_SkipsWhenInstructionsMissing(t *testing.T) {
	// When only PERSONA.md exists (no INSTRUCTIONS.md), the identity is skipped.
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "qa")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "PERSONA.md"), []byte("# Role: QA\n\nI am QA."), 0o644); err != nil {
		t.Fatal(err)
	}
	// No INSTRUCTIONS.md written.

	w := workflowWithIdentity("qa")
	written, err := GenerateCataractaeFiles(w, tmpDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("expected 0 files written when INSTRUCTIONS.md missing, got %d", len(written))
	}
	if _, statErr := os.Stat(filepath.Join(identityDir, "CLAUDE.md")); statErr == nil {
		t.Error("CLAUDE.md should not be created when INSTRUCTIONS.md is missing")
	}
}

func TestGenerateCataractaeFiles_DeduplicatesIdentities(t *testing.T) {
	// Same identity appearing in multiple steps is generated only once.
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "reviewer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "PERSONA.md"), []byte("# Role: Reviewer\n\nI am the reviewer."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "INSTRUCTIONS.md"), []byte("Review carefully. ct droplet pass <id>"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Workflow{
		Name: "test",
		Cataractae: []WorkflowCataractae{
			{Name: "review1", Type: CataractaeTypeAgent, Identity: "reviewer", OnPass: "review2"},
			{Name: "review2", Type: CataractaeTypeAgent, Identity: "reviewer", OnPass: "done"},
		},
	}
	written, err := GenerateCataractaeFiles(w, tmpDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Errorf("expected 1 file written (deduplication), got %d", len(written))
	}
}

func TestGenerateCataractaeFiles_EmptyWorkflow(t *testing.T) {
	// Workflow with no agent steps returns empty list.
	tmpDir := t.TempDir()
	w := &Workflow{
		Name:       "test",
		Cataractae: []WorkflowCataractae{{Name: "gate", Type: CataractaeTypeGate, OnPass: "done"}},
	}
	written, err := GenerateCataractaeFiles(w, tmpDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("expected 0 files for workflow with no agent identities, got %d", len(written))
	}
}

// TestGenerateCataractaeFiles_ReturnsErrorOnUnreadablePersona verifies that a
// non-ENOENT read error on PERSONA.md is surfaced as an error rather than silently
// skipping the identity.
func TestGenerateCataractaeFiles_ReturnsErrorOnUnreadablePersona(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("chmod 000 has no effect when running as root")
	}
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	personaPath := filepath.Join(identityDir, "PERSONA.md")
	if err := os.WriteFile(personaPath, []byte("persona"), 0o000); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(personaPath, 0o644) })

	w := workflowWithIdentity("implementer")
	_, err := GenerateCataractaeFiles(w, tmpDir, "")
	if err == nil {
		t.Fatal("expected error for unreadable PERSONA.md, got nil")
	}
}

// TestGenerateCataractaeFiles_ReturnsErrorOnUnreadableInstructions verifies that a
// non-ENOENT read error on INSTRUCTIONS.md is surfaced as an error rather than
// silently skipping the identity.
func TestGenerateCataractaeFiles_ReturnsErrorOnUnreadableInstructions(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("chmod 000 has no effect when running as root")
	}
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "PERSONA.md"), []byte("persona"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	instrPath := filepath.Join(identityDir, "INSTRUCTIONS.md")
	if err := os.WriteFile(instrPath, []byte("instructions"), 0o000); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(instrPath, 0o644) })

	w := workflowWithIdentity("implementer")
	_, err := GenerateCataractaeFiles(w, tmpDir, "")
	if err == nil {
		t.Fatal("expected error for unreadable INSTRUCTIONS.md, got nil")
	}
}

// TestGenerateCataractaeFiles_WritesProviderInstructionsFile verifies that a
// non-default instructionsFile is written instead of CLAUDE.md.
func TestGenerateCataractaeFiles_WritesProviderInstructionsFile(t *testing.T) {
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "PERSONA.md"), []byte("# Role: Implementer\n\nI implement."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "INSTRUCTIONS.md"), []byte("Do the work. ct droplet pass <id>"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := workflowWithIdentity("implementer")
	written, err := GenerateCataractaeFiles(w, tmpDir, "AGENTS.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d", len(written))
	}
	if written[0] != filepath.Join(identityDir, "AGENTS.md") {
		t.Errorf("written path = %q, want AGENTS.md path", written[0])
	}

	// AGENTS.md must exist with expected content.
	data, err := os.ReadFile(filepath.Join(identityDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md not created: %v", err)
	}
	if !strings.Contains(string(data), "I implement.") {
		t.Error("AGENTS.md missing persona content")
	}

	// CLAUDE.md must NOT be created.
	if _, statErr := os.Stat(filepath.Join(identityDir, "CLAUDE.md")); statErr == nil {
		t.Error("CLAUDE.md should not be created when instructionsFile is AGENTS.md")
	}
}

// TestGenerateCataractaeFiles_PreservesExistingClaudeMd verifies that a pre-existing
// CLAUDE.md is left untouched when generating a different InstructionsFile — backward
// compatibility for repos that have CLAUDE.md and switch to another provider.
func TestGenerateCataractaeFiles_PreservesExistingClaudeMd(t *testing.T) {
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "reviewer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "PERSONA.md"), []byte("# Role: Reviewer\n\nI review."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "INSTRUCTIONS.md"), []byte("Review carefully. ct droplet pass <id>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-existing CLAUDE.md from a previous claude-preset run.
	if err := os.WriteFile(filepath.Join(identityDir, "CLAUDE.md"), []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := workflowWithIdentity("reviewer")
	written, err := GenerateCataractaeFiles(w, tmpDir, "GEMINI.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d", len(written))
	}

	// GEMINI.md is written.
	if _, statErr := os.Stat(filepath.Join(identityDir, "GEMINI.md")); statErr != nil {
		t.Error("GEMINI.md was not created")
	}

	// CLAUDE.md preserved with original content.
	data, err := os.ReadFile(filepath.Join(identityDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal("CLAUDE.md was unexpectedly deleted")
	}
	if string(data) != "old content" {
		t.Errorf("CLAUDE.md content changed: got %q, want %q", string(data), "old content")
	}
}

// TestGenerateCataractaeFiles_EmptyInstructionsFileDefaultsToClaude verifies that
// passing "" for instructionsFile defaults to writing CLAUDE.md.
func TestGenerateCataractaeFiles_EmptyInstructionsFileDefaultsToClaude(t *testing.T) {
	tmpDir := t.TempDir()
	identityDir := filepath.Join(tmpDir, "qa")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "PERSONA.md"), []byte("# Role: QA\n\nI test."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "INSTRUCTIONS.md"), []byte("Test all things. ct droplet pass <id>"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := workflowWithIdentity("qa")
	written, err := GenerateCataractaeFiles(w, tmpDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d", len(written))
	}
	if _, statErr := os.Stat(filepath.Join(identityDir, "CLAUDE.md")); statErr != nil {
		t.Error("empty instructionsFile should default to writing CLAUDE.md")
	}
}

// --- TitleCaseName tests ---

func TestTitleCaseName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"implementer", "Implementer"},
		{"docs_writer", "Docs Writer"},
		{"my-role", "My Role"},
		{"qa", "Qa"},
		{"security_reviewer", "Security Reviewer"},
	}
	for _, tt := range tests {
		got := TitleCaseName(tt.input)
		if got != tt.want {
			t.Errorf("TitleCaseName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- ScaffoldCataractaeDir tests ---

func TestScaffoldCataractaeDir_CreatesTemplateFiles(t *testing.T) {
	tmpDir := t.TempDir()
	personaPath, instrPath, err := ScaffoldCataractaeDir(tmpDir, "my_role")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPersona := filepath.Join(tmpDir, "my_role", "PERSONA.md")
	if personaPath != wantPersona {
		t.Errorf("personaPath = %q, want %q", personaPath, wantPersona)
	}
	wantInstr := filepath.Join(tmpDir, "my_role", "INSTRUCTIONS.md")
	if instrPath != wantInstr {
		t.Errorf("instrPath = %q, want %q", instrPath, wantInstr)
	}

	personaContent, err := os.ReadFile(personaPath)
	if err != nil {
		t.Fatalf("read PERSONA.md: %v", err)
	}
	if !strings.Contains(string(personaContent), "# Role: My Role") {
		t.Errorf("PERSONA.md missing role header, got:\n%s", personaContent)
	}

	instrContent, err := os.ReadFile(instrPath)
	if err != nil {
		t.Fatalf("read INSTRUCTIONS.md: %v", err)
	}
	if !strings.Contains(string(instrContent), "Read CONTEXT.md") {
		t.Errorf("INSTRUCTIONS.md missing protocol step, got:\n%s", instrContent)
	}
	if !strings.Contains(string(instrContent), "ct droplet pass") {
		t.Errorf("INSTRUCTIONS.md missing signal instructions, got:\n%s", instrContent)
	}
}

func TestScaffoldCataractaeDir_ErrorIfPersonaExists(t *testing.T) {
	tmpDir := t.TempDir()
	roleDir := filepath.Join(tmpDir, "existing")
	if err := os.MkdirAll(roleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roleDir, "PERSONA.md"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := ScaffoldCataractaeDir(tmpDir, "existing")
	if err == nil {
		t.Fatal("expected error for existing PERSONA.md, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want it to contain 'already exists'", err)
	}
}

func TestScaffoldCataractaeDir_ErrorIfInstructionsExists(t *testing.T) {
	tmpDir := t.TempDir()
	roleDir := filepath.Join(tmpDir, "existing")
	if err := os.MkdirAll(roleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roleDir, "INSTRUCTIONS.md"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := ScaffoldCataractaeDir(tmpDir, "existing")
	if err == nil {
		t.Fatal("expected error for existing INSTRUCTIONS.md, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want it to contain 'already exists'", err)
	}
}

// --- TrackerConfig tests ---

// TestTrackerConfig_ResolvedToken_UsesLiteralToken verifies that when TokenEnv is
// empty, the literal Token value is returned.
func TestTrackerConfig_ResolvedToken_UsesLiteralToken(t *testing.T) {
	tc := TrackerConfig{Token: "my-secret-token"}
	if got := tc.ResolvedToken(); got != "my-secret-token" {
		t.Errorf("ResolvedToken() = %q, want %q", got, "my-secret-token")
	}
}

// TestTrackerConfig_ResolvedToken_UsesEnvVarWhenSet verifies that when TokenEnv
// names a populated environment variable, its value is returned.
func TestTrackerConfig_ResolvedToken_UsesEnvVarWhenSet(t *testing.T) {
	t.Setenv("TEST_TRACKER_TOKEN", "env-token-value")
	tc := TrackerConfig{TokenEnv: "TEST_TRACKER_TOKEN"}
	if got := tc.ResolvedToken(); got != "env-token-value" {
		t.Errorf("ResolvedToken() = %q, want %q", got, "env-token-value")
	}
}

// TestTrackerConfig_ResolvedToken_EnvVarTakesPrecedenceOverLiteral verifies that
// when both Token and TokenEnv are set, the env var value wins.
func TestTrackerConfig_ResolvedToken_EnvVarTakesPrecedenceOverLiteral(t *testing.T) {
	t.Setenv("TEST_TRACKER_TOKEN2", "from-env")
	tc := TrackerConfig{Token: "literal", TokenEnv: "TEST_TRACKER_TOKEN2"}
	if got := tc.ResolvedToken(); got != "from-env" {
		t.Errorf("ResolvedToken() = %q, want env value %q", got, "from-env")
	}
}

// TestTrackerConfig_ResolvedToken_FallsBackToTokenWhenEnvUnset verifies that when
// TokenEnv is set but the variable is not in the environment, Token is returned.
func TestTrackerConfig_ResolvedToken_FallsBackToTokenWhenEnvUnset(t *testing.T) {
	// Set the variable to empty string — os.Getenv returns "" for both unset and empty, so this exercises the fallback path.
	t.Setenv("TEST_TRACKER_MISSING", "")
	tc := TrackerConfig{Token: "fallback-token", TokenEnv: "TEST_TRACKER_MISSING"}
	if got := tc.ResolvedToken(); got != "fallback-token" {
		t.Errorf("ResolvedToken() = %q, want %q", got, "fallback-token")
	}
}

// TestTrackerConfig_ResolvedToken_EmptyWhenNeitherSet verifies that an empty string
// is returned when neither Token nor TokenEnv is configured.
func TestTrackerConfig_ResolvedToken_EmptyWhenNeitherSet(t *testing.T) {
	tc := TrackerConfig{}
	if got := tc.ResolvedToken(); got != "" {
		t.Errorf("ResolvedToken() = %q, want empty string", got)
	}
}

// TestAqueductConfig_Trackers_ParsedFromYAML verifies that the trackers: key is
// parsed correctly from a cistern.yaml config.
func TestAqueductConfig_Trackers_ParsedFromYAML(t *testing.T) {
	yaml := `
repos:
  - name: myrepo
    cataractae: 1
    prefix: mr
trackers:
  - name: jira
    url: https://myorg.atlassian.net
    email: me@example.com
    token: secret123
  - name: linear
    url: https://linear.app
    token_env: LINEAR_TOKEN
`
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("ParseAqueductConfig: %v", err)
	}

	if len(cfg.Trackers) != 2 {
		t.Fatalf("Trackers: got %d entries, want 2", len(cfg.Trackers))
	}

	jira := cfg.Trackers[0]
	if jira.Name != "jira" {
		t.Errorf("Trackers[0].Name = %q, want %q", jira.Name, "jira")
	}
	if jira.URL != "https://myorg.atlassian.net" {
		t.Errorf("Trackers[0].URL = %q, want expected URL", jira.URL)
	}
	if jira.Email != "me@example.com" {
		t.Errorf("Trackers[0].Email = %q, want %q", jira.Email, "me@example.com")
	}
	if jira.Token != "secret123" {
		t.Errorf("Trackers[0].Token = %q, want %q", jira.Token, "secret123")
	}

	linear := cfg.Trackers[1]
	if linear.Name != "linear" {
		t.Errorf("Trackers[1].Name = %q, want %q", linear.Name, "linear")
	}
	if linear.TokenEnv != "LINEAR_TOKEN" {
		t.Errorf("Trackers[1].TokenEnv = %q, want %q", linear.TokenEnv, "LINEAR_TOKEN")
	}
}

// TestAqueductConfig_Trackers_OmittedWhenAbsent verifies that the Trackers field
// is nil when the trackers: key is absent from the config.
func TestAqueductConfig_Trackers_OmittedWhenAbsent(t *testing.T) {
	yaml := `
repos:
  - name: myrepo
    cataractae: 1
    prefix: mr
`
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("ParseAqueductConfig: %v", err)
	}

	if cfg.Trackers != nil {
		t.Errorf("Trackers = %v, want nil when trackers: absent", cfg.Trackers)
	}
}
