package cataracta

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/aqueduct"
)

func testQueueClient(t *testing.T) *cistern.Client {
	t.Helper()
	c, err := cistern.New(filepath.Join(t.TempDir(), "test.db"), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func testRepoConfig() aqueduct.RepoConfig {
	return aqueduct.RepoConfig{
		Name:    "testrepo",
		URL:     "https://github.com/example/testrepo",
		Cataractae: 3,
		Names:   []string{"alice", "bob", "charlie"},
		Prefix:  "tr",
	}
}

func testWorkflow() *aqueduct.Workflow {
	return &aqueduct.Workflow{
		Name: "feature",
		Cataractae: []aqueduct.WorkflowCataracta{
			{Name: "implement", Type: "agent", Identity: "implementer", Context: "full_codebase"},
			{Name: "review", Type: "agent", Identity: "reviewer", Context: "diff_only"},
		},
	}
}

func TestNewRunner_NamedWorkers(t *testing.T) {
	cfg := Config{
		SkipInitialClone: true,
		Repo:             testRepoConfig(),
		Workflow:         testWorkflow(),
		CisternClient:      testQueueClient(t),
		SandboxRoot:      t.TempDir(),
	}

	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	workers := r.Workers()
	if len(workers) != 3 {
		t.Fatalf("expected 3 workers, got %d", len(workers))
	}

	want := []struct {
		name, session string
	}{
		{"alice", "testrepo-alice"},
		{"bob", "testrepo-bob"},
		{"charlie", "testrepo-charlie"},
	}

	for i, w := range want {
		if workers[i].Name != w.name {
			t.Errorf("worker %d: name = %q, want %q", i, workers[i].Name, w.name)
		}
		if workers[i].SessionID != w.session {
			t.Errorf("worker %d: session = %q, want %q", i, workers[i].SessionID, w.session)
		}
		if workers[i].Repo != "testrepo" {
			t.Errorf("worker %d: repo = %q, want %q", i, workers[i].Repo, "testrepo")
		}
	}
}

func TestNewRunner_NumberedWorkers(t *testing.T) {
	repo := testRepoConfig()
	repo.Names = nil // No namepool — use numbered names.

	cfg := Config{
		SkipInitialClone: true,
		Repo:             repo,
		Workflow:         testWorkflow(),
		CisternClient:      testQueueClient(t),
		SandboxRoot:      t.TempDir(),
	}

	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	workers := r.Workers()
	for i, w := range workers {
		expected := workerName(repo, i)
		if w.Name != expected {
			t.Errorf("worker %d: name = %q, want %q", i, w.Name, expected)
		}
	}
}

func TestNewRunner_NoWorkflow(t *testing.T) {
	cfg := Config{
		SkipInitialClone: true,
		Repo:             testRepoConfig(),
		CisternClient:      testQueueClient(t),
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for nil workflow")
	}
}

func TestNewRunner_NoQueueClient(t *testing.T) {
	cfg := Config{
		SkipInitialClone: true,
		Repo:             testRepoConfig(),
		Workflow:         testWorkflow(),
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for nil queue client")
	}
}

func TestClaimRelease(t *testing.T) {
	cfg := Config{
		SkipInitialClone: true,
		Repo:             testRepoConfig(),
		Workflow:         testWorkflow(),
		CisternClient:      testQueueClient(t),
		SandboxRoot:      t.TempDir(),
	}

	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if r.IdleCount() != 3 {
		t.Fatalf("idle count = %d, want 3", r.IdleCount())
	}

	// Claim all workers.
	w1 := r.Claim()
	w2 := r.Claim()
	w3 := r.Claim()
	if w1 == nil || w2 == nil || w3 == nil {
		t.Fatal("expected 3 claims to succeed")
	}

	if r.IdleCount() != 0 {
		t.Fatalf("idle count = %d, want 0", r.IdleCount())
	}

	// No more workers available.
	w4 := r.Claim()
	if w4 != nil {
		t.Fatal("expected nil when all workers busy")
	}

	// Release one.
	r.Release(w2)
	if r.IdleCount() != 1 {
		t.Fatalf("idle count = %d, want 1", r.IdleCount())
	}

	// Claim again.
	w5 := r.Claim()
	if w5 == nil {
		t.Fatal("expected claim after release")
	}
	if w5.Name != w2.Name {
		t.Errorf("expected released worker %q, got %q", w2.Name, w5.Name)
	}
}

func TestStepByName(t *testing.T) {
	cfg := Config{
		SkipInitialClone: true,
		Repo:             testRepoConfig(),
		Workflow:         testWorkflow(),
		CisternClient:      testQueueClient(t),
		SandboxRoot:      t.TempDir(),
	}

	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	step := r.CataractaByName("implement")
	if step == nil {
		t.Fatal("expected step 'implement'")
	}
	if step.Identity != "implementer" {
		t.Errorf("step role = %q, want %q", step.Identity, "implementer")
	}

	if r.CataractaByName("nonexistent") != nil {
		t.Error("expected nil for nonexistent step")
	}
}

func TestOutcomeValidate(t *testing.T) {
	tests := []struct {
		result  string
		wantErr bool
	}{
		{"pass", false},
		{"fail", false},
		{"recirculate", false},
		{"escalate", false},
		{"", true},
		{"unknown", true},
	}

	for _, tt := range tests {
		o := &Outcome{Result: tt.result}
		err := o.Validate()
		if (err != nil) != tt.wantErr {
			t.Errorf("Validate(%q): err=%v, wantErr=%v", tt.result, err, tt.wantErr)
		}
	}
}

func TestOutcomeRouteField(t *testing.T) {
	tests := []struct {
		result string
		want   string
	}{
		{"pass", "on_pass"},
		{"fail", "on_fail"},
		{"recirculate", "on_recirculate"},
		{"escalate", "on_escalate"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		o := &Outcome{Result: tt.result}
		if got := o.RouteField(); got != tt.want {
			t.Errorf("RouteField(%q) = %q, want %q", tt.result, got, tt.want)
		}
	}
}

func TestWriteContextFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CONTEXT.md")

	item := &cistern.Droplet{
		ID:          "bf-123",
		Title:       "Test item",
		Status:      "in_progress",
		Priority:    1,
		Description: "Fix the thing",
	}

	step := &aqueduct.WorkflowCataracta{
		Name:    "implement",
		Type:    "agent",
		Identity: "implementer",
		Context: "full_codebase",
	}

	notes := []cistern.CataractaNote{
		{CataractaName: "review", Content: "Looks good but needs tests"},
	}

	err := writeContextFile(path, ContextParams{
		Level:      "full_codebase",
		SandboxDir: dir,
		Item:       item,
		Step:       step,
		Notes:      notes,
	})
	if err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CONTEXT.md: %v", err)
	}

	content := string(data)
	checks := []string{
		"# Context",
		"bf-123",
		"Test item",
		"implementer",
		"From: review",
		"Looks good but needs tests",
		"ct droplet pass",
	}
	for _, want := range checks {
		if !contains(content, want) {
			t.Errorf("CONTEXT.md missing %q", want)
		}
	}
}

func TestPrepareContext_FullCodebase(t *testing.T) {
	dir := t.TempDir()
	item := &cistern.Droplet{ID: "bf-1", Title: "Test", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataracta{Name: "implement", Type: "agent", Context: "full_codebase"}

	ctxDir, cleanup, err := PrepareContext(ContextParams{
		Level:      aqueduct.ContextFullCodebase,
		SandboxDir: dir,
		Item:       item,
		Step:       step,
	})
	if err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	defer cleanup()

	if ctxDir != dir {
		t.Errorf("ctxDir = %q, want sandbox dir %q", ctxDir, dir)
	}

	if _, err := os.Stat(filepath.Join(ctxDir, "CONTEXT.md")); err != nil {
		t.Error("expected CONTEXT.md in sandbox dir")
	}
}

func TestPrepareContext_SpecOnly(t *testing.T) {
	item := &cistern.Droplet{
		ID:          "bf-2",
		Title:       "Spec test",
		Status:      "open",
		Priority:    2,
		Description: "Build the widget",
	}
	step := &aqueduct.WorkflowCataracta{Name: "plan", Type: "agent", Context: "spec_only"}

	ctxDir, cleanup, err := PrepareContext(ContextParams{
		Level:      aqueduct.ContextSpecOnly,
		SandboxDir: t.TempDir(),
		Item:       item,
		Step:       step,
	})
	if err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	defer cleanup()

	// spec.md should exist.
	specData, err := os.ReadFile(filepath.Join(ctxDir, "spec.md"))
	if err != nil {
		t.Fatal("expected spec.md")
	}
	if !contains(string(specData), "Spec test") {
		t.Error("spec.md missing item title")
	}

	// CONTEXT.md should exist.
	if _, err := os.Stat(filepath.Join(ctxDir, "CONTEXT.md")); err != nil {
		t.Error("expected CONTEXT.md")
	}
}

func TestOutcomeJSON(t *testing.T) {
	dir := t.TempDir()
	outcome := Outcome{
		Result: "pass",
		Notes:  "All tests passing",
		Annotations: map[string]string{
			"tests_added": "3",
		},
	}

	data, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	path := filepath.Join(dir, "outcome.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	readData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var parsed Outcome
	if err := json.Unmarshal(readData, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if parsed.Result != "pass" {
		t.Errorf("result = %q, want %q", parsed.Result, "pass")
	}
	if parsed.Annotations["tests_added"] != "3" {
		t.Error("missing annotation tests_added")
	}
}


func TestWorkerSandboxPaths(t *testing.T) {
	sandboxRoot := "/tmp/test-sandboxes"
	repo := testRepoConfig()

	workers, err := initWorkers(repo, filepath.Join(sandboxRoot, repo.Name))
	if err != nil {
		t.Fatalf("initWorkers: %v", err)
	}

	expected := []string{
		filepath.Join(sandboxRoot, "testrepo", "alice"),
		filepath.Join(sandboxRoot, "testrepo", "bob"),
		filepath.Join(sandboxRoot, "testrepo", "charlie"),
	}

	for i, w := range workers {
		if w.SandboxDir != expected[i] {
			t.Errorf("worker %d: sandbox = %q, want %q", i, w.SandboxDir, expected[i])
		}
	}
}

func TestWorkerName_Fallback(t *testing.T) {
	repo := aqueduct.RepoConfig{
		Name:    "test",
		Cataractae: 2,
		Names:   []string{"alpha"},
	}

	if got := workerName(repo, 0); got != "alpha" {
		t.Errorf("workerName(0) = %q, want %q", got, "alpha")
	}
	if got := workerName(repo, 1); got != "worker-1" {
		t.Errorf("workerName(1) = %q, want %q", got, "worker-1")
	}
}

// --- git helpers for integration tests ---

func gitCmd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd
}

func mustRun(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v failed: %v\n%s", cmd.Args, err, out)
	}
}

// TestPrepareContext_DiffOnly_Isolation verifies that the diff_only context
// level creates an isolated temp directory containing exactly two files:
// diff.patch and CONTEXT.md — no repo access, no git history, no source code.
// This is the enforcement point for reviewer context isolation.
func TestPrepareContext_DiffOnly_Isolation(t *testing.T) {
	sandbox := t.TempDir()

	// Set up a git repo with origin/main and a change.
	mustRun(t, gitCmd(sandbox, "init"))
	mustRun(t, gitCmd(sandbox, "config", "user.email", "test@test.com"))
	mustRun(t, gitCmd(sandbox, "config", "user.name", "Test"))

	// Initial commit.
	if err := os.WriteFile(filepath.Join(sandbox, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, gitCmd(sandbox, "add", "."))
	mustRun(t, gitCmd(sandbox, "commit", "-m", "initial"))
	mustRun(t, gitCmd(sandbox, "branch", "-M", "main"))

	// Create fake origin/main ref pointing to current HEAD.
	mustRun(t, gitCmd(sandbox, "update-ref", "refs/remotes/origin/main", "HEAD"))

	// Make a change and commit (this is the "implementer's work").
	if err := os.WriteFile(filepath.Join(sandbox, "main.go"),
		[]byte("package main\n\n// smoke test comment\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, gitCmd(sandbox, "add", "."))
	mustRun(t, gitCmd(sandbox, "commit", "-m", "add smoke test comment"))

	item := &cistern.Droplet{ID: "bf-smoke", Title: "Smoke", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataracta{Name: "review", Type: "agent", Context: "diff_only"}

	ctxDir, cleanup, err := PrepareContext(ContextParams{
		Level:      aqueduct.ContextDiffOnly,
		SandboxDir: sandbox,
		Item:       item,
		Step:       step,
	})
	if err != nil {
		t.Fatalf("PrepareContext diff_only: %v", err)
	}
	defer cleanup()

	// Context dir must NOT be the sandbox (isolation enforced).
	if ctxDir == sandbox {
		t.Error("diff_only context should use a separate temp dir, not the sandbox")
	}

	// Verify exactly 2 files: diff.patch and CONTEXT.md.
	entries, err := os.ReadDir(ctxDir)
	if err != nil {
		t.Fatalf("read context dir: %v", err)
	}

	files := map[string]bool{}
	for _, e := range entries {
		files[e.Name()] = true
	}

	if len(files) != 2 {
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		t.Fatalf("expected exactly 2 files in diff_only context, got %d: %v", len(files), names)
	}

	if !files["CONTEXT.md"] {
		t.Error("missing CONTEXT.md in diff_only context")
	}
	if !files["diff.patch"] {
		t.Error("missing diff.patch in diff_only context")
	}

	// Verify diff.patch contains the actual change.
	diff, err := os.ReadFile(filepath.Join(ctxDir, "diff.patch"))
	if err != nil {
		t.Fatalf("read diff.patch: %v", err)
	}
	if !contains(string(diff), "smoke test comment") {
		t.Error("diff.patch should contain the change ('smoke test comment')")
	}

	// Verify no .git directory (no repo access).
	if _, err := os.Stat(filepath.Join(ctxDir, ".git")); err == nil {
		t.Error("diff_only context should NOT contain .git directory")
	}

	// Verify no source files leaked.
	if _, err := os.Stat(filepath.Join(ctxDir, "main.go")); err == nil {
		t.Error("diff_only context should NOT contain source files")
	}
}

func TestWriteContextFile_AvailableSkillsBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CONTEXT.md")

	// Write a fake cached SKILL.md so skillDescription can read it.
	skillCacheDir := filepath.Join(dir, ".cistern", "skills", "my-skill")
	if err := os.MkdirAll(skillCacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillCacheDir, "SKILL.md"),
		[]byte("# My Skill\n\nDoes awesome things.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	item := &cistern.Droplet{ID: "sk-1", Title: "Skill test", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataracta{
		Name:     "implement",
		Type:     "agent",
		Identity: "implementer",
		Context:  "full_codebase",
		Skills: []aqueduct.SkillRef{
			{Name: "my-skill"},
		},
	}

	err := writeContextFile(path, ContextParams{
		Level:      "full_codebase",
		SandboxDir: dir,
		Item:       item,
		Step:       step,
	})
	if err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CONTEXT.md: %v", err)
	}

	content := string(data)
	checks := []string{
		"<available_skills>",
		"<name>my-skill</name>",
		"<description>Does awesome things.</description>",
		"<location>.claude/skills/my-skill/SKILL.md</location>",
		"</available_skills>",
	}
	for _, want := range checks {
		if !contains(content, want) {
			t.Errorf("CONTEXT.md missing %q\nfull content:\n%s", want, content)
		}
	}
}

func TestWriteContextFile_XMLEscapedDescription(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CONTEXT.md")

	// Write a fake cached SKILL.md whose first description line contains XML
	// special characters — this should be escaped in the output, not injected.
	skillCacheDir := filepath.Join(dir, ".cistern", "skills", "evil-skill")
	if err := os.MkdirAll(skillCacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillCacheDir, "SKILL.md"),
		[]byte("# Evil Skill\n\n<script>alert('xss')</script>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	item := &cistern.Droplet{ID: "xss-1", Title: "XSS test", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataracta{
		Name:    "implement",
		Type:    "agent",
		Context: "full_codebase",
		Skills: []aqueduct.SkillRef{
			{Name: "evil-skill"},
		},
	}

	err := writeContextFile(path, ContextParams{
		Level:      "full_codebase",
		SandboxDir: dir,
		Item:       item,
		Step:       step,
	})
	if err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CONTEXT.md: %v", err)
	}

	content := string(data)
	// Raw script tag must NOT appear — that would be a prompt injection.
	if contains(content, "<script>") {
		t.Error("CONTEXT.md contains raw <script> tag — prompt injection not escaped")
	}
	// XML-escaped version must be present.
	if !contains(content, "&lt;script&gt;") {
		t.Error("CONTEXT.md missing XML-escaped description (&lt;script&gt;)")
	}
}

func TestWriteContextFile_NoSkillsBlock_WhenEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CONTEXT.md")

	item := &cistern.Droplet{ID: "no-skill", Title: "No skills", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataracta{
		Name:    "implement",
		Type:    "agent",
		Context: "full_codebase",
		// Skills intentionally empty
	}

	err := writeContextFile(path, ContextParams{
		Level:      "full_codebase",
		SandboxDir: dir,
		Item:       item,
		Step:       step,
	})
	if err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CONTEXT.md: %v", err)
	}

	if contains(string(data), "<available_skills>") {
		t.Error("CONTEXT.md should not contain <available_skills> when step has no skills")
	}
}

func TestYAMLRoundTrip_SkillsField(t *testing.T) {
	// Test that Skills field round-trips through YAML unmarshal.
	raw := `
name: feature
cataractae:
  - name: implement
    type: agent
    skills:
      - name: my-skill
        url: https://raw.githubusercontent.com/example/repo/main/SKILL.md
`
	w, err := aqueduct.ParseWorkflowBytes([]byte(raw))
	if err != nil {
		t.Fatalf("ParseWorkflowBytes: %v", err)
	}
	if len(w.Cataractae) != 1 {
		t.Fatalf("expected 1 cataracta, got %d", len(w.Cataractae))
	}
	impl := w.Cataractae[0]
	if len(impl.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(impl.Skills))
	}
	if impl.Skills[0].Name != "my-skill" {
		t.Errorf("skill name = %q, want %q", impl.Skills[0].Name, "my-skill")
	}
	// URL field removed — skills are name-only in YAML, installed separately.
	if impl.Skills[0].Path != "" {
		t.Errorf("expected no path for externally installed skill, got %q", impl.Skills[0].Path)
	}
}

func TestYAMLRoundTrip_SkillsOmittedWhenEmpty(t *testing.T) {
	// A step with no skills field should parse fine (omitempty).
	raw := `
name: feature
cataractae:
  - name: implement
    type: agent
    on_pass: done
`
	w, err := aqueduct.ParseWorkflowBytes([]byte(raw))
	if err != nil {
		t.Fatalf("ParseWorkflowBytes: %v", err)
	}
	if len(w.Cataractae[0].Skills) != 0 {
		t.Errorf("expected no skills, got %v", w.Cataractae[0].Skills)
	}
}

// TestCurrentHead verifies that currentHead() returns the HEAD commit hash
// for a git repo. It sets up a minimal repo with one commit.
func TestCurrentHead(t *testing.T) {
	dir := t.TempDir()

	mustRun(t, gitCmd(dir, "init"))
	mustRun(t, gitCmd(dir, "config", "user.email", "test@test.com"))
	mustRun(t, gitCmd(dir, "config", "user.name", "Test"))

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, gitCmd(dir, "add", "."))
	mustRun(t, gitCmd(dir, "commit", "-m", "initial"))

	// Get expected HEAD via git directly.
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	expected := strings.TrimSpace(string(out))

	got, err := currentHead(dir)
	if err != nil {
		t.Fatalf("currentHead: %v", err)
	}
	if got != expected {
		t.Errorf("currentHead = %q, want %q", got, expected)
	}
	// SHA-1 hashes are 40 hex chars; SHA-256 are 64 hex chars. Either way ≥ 40.
	if len(got) < 40 {
		t.Errorf("currentHead hash too short: %q (len=%d)", got, len(got))
	}
}

// TestCurrentHead_NotARepo verifies that currentHead() returns an error for
// a directory that is not a git repository.
func TestCurrentHead_NotARepo(t *testing.T) {
	_, err := currentHead(t.TempDir())
	if err == nil {
		t.Error("expected error for non-repo directory")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
