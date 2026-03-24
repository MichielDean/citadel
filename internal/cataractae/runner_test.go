package cataractae

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/provider"
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
		Cataractae: []aqueduct.WorkflowCataractae{
			{Name: "implement", Type: aqueduct.CataractaeTypeAgent, Identity: "implementer", Context: aqueduct.ContextFullCodebase},
			{Name: "review", Type: aqueduct.CataractaeTypeAgent, Identity: "reviewer", Context: aqueduct.ContextDiffOnly},
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

	step := r.CataractaeByName("implement")
	if step == nil {
		t.Fatal("expected step 'implement'")
	}
	if step.Identity != "implementer" {
		t.Errorf("step role = %q, want %q", step.Identity, "implementer")
	}

	if r.CataractaeByName("nonexistent") != nil {
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

	step := &aqueduct.WorkflowCataractae{
		Name:    "implement",
		Type:    aqueduct.CataractaeTypeAgent,
		Identity: "implementer",
		Context: aqueduct.ContextFullCodebase,
	}

	notes := []cistern.CataractaeNote{
		{CataractaeName: "review", Content: "Looks good but needs tests"},
	}

	err := writeContextFile(path, ContextParams{
		Level:      aqueduct.ContextFullCodebase,
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
		if !strings.Contains(content, want) {
			t.Errorf("CONTEXT.md missing %q", want)
		}
	}
}

func TestPrepareContext_FullCodebase(t *testing.T) {
	dir := t.TempDir()
	item := &cistern.Droplet{ID: "bf-1", Title: "Test", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataractae{Name: "implement", Type: aqueduct.CataractaeTypeAgent, Context: aqueduct.ContextFullCodebase}

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
	step := &aqueduct.WorkflowCataractae{Name: "plan", Type: aqueduct.CataractaeTypeAgent, Context: aqueduct.ContextSpecOnly}

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
	if !strings.Contains(string(specData), "Spec test") {
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
	step := &aqueduct.WorkflowCataractae{Name: "review", Type: aqueduct.CataractaeTypeAgent, Context: aqueduct.ContextDiffOnly}

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
	if !strings.Contains(string(diff), "smoke test comment") {
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
	step := &aqueduct.WorkflowCataractae{
		Name:     "implement",
		Type:     aqueduct.CataractaeTypeAgent,
		Identity: "implementer",
		Context:  aqueduct.ContextFullCodebase,
		Skills: []aqueduct.SkillRef{
			{Name: "my-skill"},
		},
	}

	err := writeContextFile(path, ContextParams{
		Level:      aqueduct.ContextFullCodebase,
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
		"<location>" + filepath.Join(dir, ".cistern", "skills", "my-skill", "SKILL.md") + "</location>",
		"</available_skills>",
	}
	for _, want := range checks {
		if !strings.Contains(content, want) {
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
	step := &aqueduct.WorkflowCataractae{
		Name:    "implement",
		Type:    aqueduct.CataractaeTypeAgent,
		Context: aqueduct.ContextFullCodebase,
		Skills: []aqueduct.SkillRef{
			{Name: "evil-skill"},
		},
	}

	err := writeContextFile(path, ContextParams{
		Level:      aqueduct.ContextFullCodebase,
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
	if strings.Contains(content, "<script>") {
		t.Error("CONTEXT.md contains raw <script> tag — prompt injection not escaped")
	}
	// XML-escaped version must be present.
	if !strings.Contains(content, "&lt;script&gt;") {
		t.Error("CONTEXT.md missing XML-escaped description (&lt;script&gt;)")
	}
}

func TestWriteContextFile_NoSkillsBlock_WhenEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CONTEXT.md")

	item := &cistern.Droplet{ID: "no-skill", Title: "No skills", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataractae{
		Name:    "implement",
		Type:    aqueduct.CataractaeTypeAgent,
		Context: aqueduct.ContextFullCodebase,
		// Skills intentionally empty
	}

	err := writeContextFile(path, ContextParams{
		Level:      aqueduct.ContextFullCodebase,
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

	if strings.Contains(string(data), "<available_skills>") {
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
		t.Fatalf("expected 1 cataractae, got %d", len(w.Cataractae))
	}
	impl := w.Cataractae[0]
	if len(impl.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(impl.Skills))
	}
	if impl.Skills[0].Name != "my-skill" {
		t.Errorf("skill name = %q, want %q", impl.Skills[0].Name, "my-skill")
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

// TestResolveModelVal verifies that resolveModelVal returns the correct model
// string given step.Model and preset.DefaultModel values.
func TestResolveModelVal(t *testing.T) {
	stepModel := func(s string) *string { return &s }

	tests := []struct {
		name         string
		stepModel    *string
		defaultModel string
		want         string
	}{
		{
			name:         "step model set — uses step model",
			stepModel:    stepModel("claude-opus-4-6"),
			defaultModel: "claude-sonnet-4-6",
			want:         "claude-opus-4-6",
		},
		{
			name:         "step model nil, preset has default — uses preset default",
			stepModel:    nil,
			defaultModel: "claude-sonnet-4-6",
			want:         "claude-sonnet-4-6",
		},
		{
			name:         "step model nil, no preset default — returns empty",
			stepModel:    nil,
			defaultModel: "",
			want:         "",
		},
		{
			name:         "step model set to empty string — uses step model (empty)",
			stepModel:    stepModel(""),
			defaultModel: "claude-sonnet-4-6",
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preset := provider.ProviderPreset{DefaultModel: tt.defaultModel}
			got := resolveModelVal(tt.stepModel, preset)
			if got != tt.want {
				t.Errorf("resolveModelVal = %q, want %q", got, tt.want)
			}
		})
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

// --- captureHandler for slog output capture ---

type contextCaptureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *contextCaptureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *contextCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}
func (h *contextCaptureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *contextCaptureHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *contextCaptureHandler) hasWarn() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == slog.LevelWarn {
			return true
		}
	}
	return false
}

// errRecorder is a reviewedCommitRecorder that always returns an error.
type errRecorder struct{}

func (e *errRecorder) SetLastReviewedCommit(_, _ string) error {
	return errors.New("db write failed")
}

// TestPrepareDiffOnly_SetLastReviewedCommitError_LogsWarn verifies that when
// SetLastReviewedCommit returns an error, a WARN-level log is emitted and
// PrepareContext still succeeds (non-blocking).
func TestPrepareDiffOnly_SetLastReviewedCommitError_LogsWarn(t *testing.T) {
	sandbox := t.TempDir()

	// Set up a minimal git repo so currentHead succeeds.
	mustRun(t, gitCmd(sandbox, "init"))
	mustRun(t, gitCmd(sandbox, "config", "user.email", "test@test.com"))
	mustRun(t, gitCmd(sandbox, "config", "user.name", "Test"))
	if err := os.WriteFile(filepath.Join(sandbox, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, gitCmd(sandbox, "add", "."))
	mustRun(t, gitCmd(sandbox, "commit", "-m", "initial"))
	mustRun(t, gitCmd(sandbox, "branch", "-M", "main"))
	mustRun(t, gitCmd(sandbox, "update-ref", "refs/remotes/origin/main", "HEAD"))

	h := &contextCaptureHandler{}
	logger := slog.New(h)

	item := &cistern.Droplet{ID: "bf-warn-1", Title: "Test", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataractae{Name: "review", Type: "agent", Context: "diff_only"}

	ctxDir, cleanup, err := PrepareContext(ContextParams{
		Level:       aqueduct.ContextDiffOnly,
		SandboxDir:  sandbox,
		Item:        item,
		Step:        step,
		QueueClient: &errRecorder{},
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	defer cleanup()

	// PrepareContext must succeed — SetLastReviewedCommit failure is non-blocking.
	if ctxDir == "" {
		t.Error("expected non-empty ctxDir")
	}

	// A WARN must have been logged for the SetLastReviewedCommit failure.
	if !h.hasWarn() {
		t.Error("expected WARN log for SetLastReviewedCommit failure, got none")
	}
}

// TestWriteContextFile_ReviewerWithOpenIssues verifies the two-phase review
// protocol is written when there are DB-tracked open issues.
func TestWriteContextFile_ReviewerWithOpenIssues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CONTEXT.md")

	item := &cistern.Droplet{
		ID:     "rr-1",
		Title:  "Review item",
		Status: "in_progress",
		Priority: 1,
	}
	step := &aqueduct.WorkflowCataractae{
		Name:     "review",
		Type:     aqueduct.CataractaeTypeAgent,
		Identity: "reviewer",
		Context:  aqueduct.ContextDiffOnly,
	}
	issues := []cistern.DropletIssue{
		{
			ID:          "rr-1-abc12",
			DropletID:   "rr-1",
			FlaggedBy:   "reviewer",
			Description: "Missing input validation on the endpoint",
		},
	}

	err := writeContextFile(path, ContextParams{
		Level:      aqueduct.ContextDiffOnly,
		SandboxDir: dir,
		Item:       item,
		Step:       step,
		OpenIssues: issues,
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
		"TWO-PHASE REVIEW",
		"Phase 1",
		"Phase 2",
		"rr-1-abc12",
		"Missing input validation",
		"ct droplet issue resolve",
		"ct droplet issue reject",
	}
	for _, want := range checks {
		if !strings.Contains(content, want) {
			t.Errorf("CONTEXT.md missing %q", want)
		}
	}
}

// TestWriteContextFile_ReviewerWithRevisionNotes verifies the legacy two-phase
// review protocol is written when a reviewer step has free-text notes but no
// DB-tracked open issues.
func TestWriteContextFile_ReviewerWithRevisionNotes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CONTEXT.md")

	item := &cistern.Droplet{
		ID:     "rr-2",
		Title:  "Review with notes",
		Status: "in_progress",
		Priority: 1,
	}
	step := &aqueduct.WorkflowCataractae{
		Name:     "review",
		Type:     aqueduct.CataractaeTypeAgent,
		Identity: "reviewer",
		Context:  aqueduct.ContextDiffOnly,
	}
	notes := []cistern.CataractaeNote{
		{CataractaeName: "reviewer", Content: "Error handling missing in auth module"},
	}

	err := writeContextFile(path, ContextParams{
		Level:      aqueduct.ContextDiffOnly,
		SandboxDir: dir,
		Item:       item,
		Step:       step,
		Notes:      notes,
		// OpenIssues intentionally empty — triggers legacy fallback path
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
		"TWO-PHASE REVIEW",
		"Phase 1",
		"Phase 2",
		"Prior Issue 1",
		"Error handling missing in auth module",
		"RESOLVED:",
		"UNRESOLVED:",
	}
	for _, want := range checks {
		if !strings.Contains(content, want) {
			t.Errorf("CONTEXT.md missing %q", want)
		}
	}
}

// TestWriteContextFile_NotesTruncated verifies that Recent Step Notes are
// capped at 4 to prevent anchoring hallucination.
func TestWriteContextFile_NotesTruncated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CONTEXT.md")

	item := &cistern.Droplet{ID: "trunc-1", Title: "Truncation test", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataractae{Name: "implement", Type: aqueduct.CataractaeTypeAgent}

	// 6 notes from "implementer" — they appear in Recent Step Notes only,
	// not in revision cycle notes (which filters for review/qa/security names).
	var notes []cistern.CataractaeNote
	for i := 1; i <= 6; i++ {
		notes = append(notes, cistern.CataractaeNote{
			CataractaeName: "implementer",
			Content:        fmt.Sprintf("Note number %d", i),
		})
	}

	err := writeContextFile(path, ContextParams{
		Level:      aqueduct.ContextFullCodebase,
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
	for i := 1; i <= 4; i++ {
		want := fmt.Sprintf("Note number %d", i)
		if !strings.Contains(content, want) {
			t.Errorf("CONTEXT.md missing note %d (should be within cap)", i)
		}
	}
	for i := 5; i <= 6; i++ {
		notWant := fmt.Sprintf("Note number %d", i)
		if strings.Contains(content, notWant) {
			t.Errorf("CONTEXT.md contains note %d — should be truncated (cap is 4)", i)
		}
	}
}

// TestWriteContextFile_AssigneeField verifies the Assignee line is written
// when the droplet has an assignee.
func TestWriteContextFile_AssigneeField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CONTEXT.md")

	item := &cistern.Droplet{
		ID:       "asn-1",
		Title:    "Assigned item",
		Status:   "in_progress",
		Priority: 2,
		Assignee: "alice",
	}
	step := &aqueduct.WorkflowCataractae{Name: "implement", Type: aqueduct.CataractaeTypeAgent}

	err := writeContextFile(path, ContextParams{
		Level:      aqueduct.ContextFullCodebase,
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

	if !strings.Contains(string(data), "**Assignee:** alice") {
		t.Error("CONTEXT.md missing Assignee field")
	}
}

// TestPrepareContext_UnknownLevel verifies that an unrecognised context level
// returns an error rather than silently using a default.
func TestPrepareContext_UnknownLevel(t *testing.T) {
	item := &cistern.Droplet{ID: "x-1", Title: "Test", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataractae{Name: "plan", Type: aqueduct.CataractaeTypeAgent}

	_, _, err := PrepareContext(ContextParams{
		Level:      "nonexistent_level",
		SandboxDir: t.TempDir(),
		Item:       item,
		Step:       step,
	})
	if err == nil {
		t.Error("expected error for unknown context level")
	}
	if !strings.Contains(err.Error(), "unknown context level") {
		t.Errorf("error message = %q, expected 'unknown context level'", err.Error())
	}
}

// TestIsReviewerCataractae_Nil verifies nil step returns false without panic.
func TestIsReviewerCataractae_Nil(t *testing.T) {
	if isReviewerCataractae(nil) {
		t.Error("isReviewerCataractae(nil) = true, want false")
	}
}

// TestRevisionCycleNotes_StopsAtPassSignal verifies that notes after a pass
// signal are excluded from the current revision cycle.
func TestRevisionCycleNotes_StopsAtPassSignal(t *testing.T) {
	// Notes are newest-first. The pass signal in position [1] should stop
	// the walk, so only notes[0] (the newest) enters the cycle.
	notes := []cistern.CataractaeNote{
		{CataractaeName: "reviewer", Content: "Found a bug in auth"},
		{CataractaeName: "reviewer", Content: "No issues found — all tests pass"},
		{CataractaeName: "reviewer", Content: "Old issue from before the pass signal"},
	}

	got := revisionCycleNotes(notes)
	if len(got) != 1 {
		t.Fatalf("revisionCycleNotes returned %d notes, want 1", len(got))
	}
	if got[0].Content != "Found a bug in auth" {
		t.Errorf("unexpected note: %q", got[0].Content)
	}
}

// TestWriteContextFile_SkillDescriptionFallback verifies that when a SKILL.md
// file is absent the skill name is used as the description.
func TestWriteContextFile_SkillDescriptionFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir) // no SKILL.md installed

	path := filepath.Join(dir, "CONTEXT.md")
	item := &cistern.Droplet{ID: "sk-2", Title: "Skill fallback", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataractae{
		Name:    "implement",
		Type:    aqueduct.CataractaeTypeAgent,
		Context: aqueduct.ContextFullCodebase,
		Skills:  []aqueduct.SkillRef{{Name: "missing-skill"}},
	}

	err := writeContextFile(path, ContextParams{
		Level:      aqueduct.ContextFullCodebase,
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

	if !strings.Contains(string(data), "<description>missing-skill</description>") {
		t.Error("expected skill name as fallback description when SKILL.md is missing")
	}
}

// TestPrepareDiffOnly_InvalidRepo verifies that PrepareContext with diff_only
// returns an error when the sandbox is not a git repository.
func TestPrepareDiffOnly_InvalidRepo(t *testing.T) {
	item := &cistern.Droplet{ID: "x-2", Title: "Test", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataractae{Name: "review", Type: aqueduct.CataractaeTypeAgent, Context: aqueduct.ContextDiffOnly}

	_, _, err := PrepareContext(ContextParams{
		Level:      aqueduct.ContextDiffOnly,
		SandboxDir: t.TempDir(), // not a git repo — generateDiff will fail
		Item:       item,
		Step:       step,
	})
	if err == nil {
		t.Error("expected error for diff_only context on a non-git directory")
	}
}

// TestSkillDescription_AllHeadings verifies that when SKILL.md contains only
// headings and blank lines (no description), the skill name is used as fallback.
func TestSkillDescription_AllHeadings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	skillCacheDir := filepath.Join(dir, ".cistern", "skills", "headings-only")
	if err := os.MkdirAll(skillCacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillCacheDir, "SKILL.md"),
		[]byte("# Heading One\n\n## Heading Two\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := skillDescription("headings-only")
	if got != "headings-only" {
		t.Errorf("skillDescription for all-headings SKILL.md = %q, want %q", got, "headings-only")
	}
}

// TestSpawnStep_SandboxDirOverride verifies that when sandboxDirOverride is
// non-empty, SpawnStep writes CONTEXT.md to the override dir, not w.SandboxDir.
func TestSpawnStep_SandboxDirOverride(t *testing.T) {
	cfg := Config{
		SkipInitialClone: true,
		Repo:             testRepoConfig(),
		Workflow:         testWorkflow(),
		CisternClient:    testQueueClient(t),
		SandboxRoot:      t.TempDir(),
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := r.Claim()
	if w == nil {
		t.Fatal("no worker available")
	}
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", w.SessionID).Run()
		r.Release(w)
	})

	overrideDir := t.TempDir()
	item := &cistern.Droplet{ID: "ov-1", Title: "Override test", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataractae{
		Name:    "implement",
		Type:    aqueduct.CataractaeTypeAgent,
		Context: aqueduct.ContextFullCodebase,
	}

	spawnErr := r.SpawnStep(w, item, step, overrideDir)
	// tmux may or may not be available; a session-spawn error is expected and fine.
	// What must NOT happen: a context-preparation error before the spawn attempt.
	if spawnErr != nil && !strings.HasPrefix(spawnErr.Error(), "session spawn:") {
		t.Errorf("expected nil or session spawn error, got: %v", spawnErr)
	}

	// CONTEXT.md must be written to the override dir.
	if _, err := os.Stat(filepath.Join(overrideDir, "CONTEXT.md")); err != nil {
		t.Error("CONTEXT.md not found in override dir")
	}
	// CONTEXT.md must NOT be written to w.SandboxDir.
	if _, err := os.Stat(filepath.Join(w.SandboxDir, "CONTEXT.md")); err == nil {
		t.Error("CONTEXT.md written to w.SandboxDir when override was set")
	}
}

// TestSpawnStep_UsesWorkerSandboxDir verifies that without a sandboxDirOverride,
// SpawnStep writes CONTEXT.md to w.SandboxDir.
func TestSpawnStep_UsesWorkerSandboxDir(t *testing.T) {
	cfg := Config{
		SkipInitialClone: true,
		Repo:             testRepoConfig(),
		Workflow:         testWorkflow(),
		CisternClient:    testQueueClient(t),
		SandboxRoot:      t.TempDir(),
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := r.Claim()
	if w == nil {
		t.Fatal("no worker available")
	}
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", w.SessionID).Run()
		r.Release(w)
	})

	// Ensure the worker sandbox dir exists so CONTEXT.md can be written there.
	if err := os.MkdirAll(w.SandboxDir, 0o755); err != nil {
		t.Fatal(err)
	}

	item := &cistern.Droplet{ID: "noov-1", Title: "No override test", Status: "open", Priority: 1}
	step := &aqueduct.WorkflowCataractae{
		Name:    "implement",
		Type:    aqueduct.CataractaeTypeAgent,
		Context: aqueduct.ContextFullCodebase,
	}

	spawnErr := r.SpawnStep(w, item, step, "") // no override
	if spawnErr != nil && !strings.HasPrefix(spawnErr.Error(), "session spawn:") {
		t.Errorf("expected nil or session spawn error, got: %v", spawnErr)
	}

	// CONTEXT.md must be written to w.SandboxDir when no override is set.
	if _, err := os.Stat(filepath.Join(w.SandboxDir, "CONTEXT.md")); err != nil {
		t.Error("CONTEXT.md not found in w.SandboxDir when no override was set")
	}
}

// TestRevisionCycleNotes_AllPrefixNotPassSignal verifies that a note beginning
// with "all" but not a recognised pass-signal phrase does NOT stop the cycle.
// Regression test for the bug where strings.HasPrefix(lower, "all") caused any
// note starting with "all" (e.g. "All tests are still failing") to silently
// truncate the revision cycle.
func TestRevisionCycleNotes_AllPrefixNotPassSignal(t *testing.T) {
	// "All tests are still failing" starts with "all" but is NOT a pass signal.
	notes := []cistern.CataractaeNote{
		{CataractaeName: "reviewer", Content: "Found a new bug"},
		{CataractaeName: "reviewer", Content: "All tests are still failing"},
		{CataractaeName: "reviewer", Content: "Older issue"},
	}

	got := revisionCycleNotes(notes)
	// All three notes are from a reviewer and none is a pass signal, so all
	// three should be in the cycle (oldest-first order).
	if len(got) != 3 {
		t.Fatalf("revisionCycleNotes returned %d notes, want 3; got: %v", len(got), got)
	}
}

// TestSpawnStep_DiffOnly_NoSandboxDirFails verifies that SpawnStep returns a
// hard error when a diff_only step is called without a sandboxDirOverride.
// This guards against silent re-regression of the ci-s5eg9 bug where an
// unset SandboxDir caused generateDiff to produce an empty diff.patch.
func TestSpawnStep_DiffOnly_NoSandboxDirFails(t *testing.T) {
	r := &Runner{
		repo:     testRepoConfig(),
		workflow: testWorkflow(),
		queue:    testQueueClient(t),
	}

	w := &Worker{
		Name:       "alice",
		Repo:       "testrepo",
		SandboxDir: t.TempDir(),
		SessionID:  "testrepo-alice",
	}

	item := &cistern.Droplet{
		ID:       "ci-s5eg9-guard",
		Title:    "Guard test",
		Status:   "open",
		Priority: 1,
	}

	step := &aqueduct.WorkflowCataractae{
		Name:    "adversarial-review",
		Type:    aqueduct.CataractaeTypeAgent,
		Context: aqueduct.ContextDiffOnly,
	}

	err := r.SpawnStep(w, item, step, "") // no sandboxDirOverride
	if err == nil {
		t.Fatal("SpawnStep: expected error for diff_only step with no SandboxDir, got nil")
	}
	if !strings.Contains(err.Error(), "per-droplet SandboxDir not set") {
		t.Errorf("error %q should contain %q", err.Error(), "per-droplet SandboxDir not set")
	}
}

// TestSpawnStep_MissingSkillReturnsError verifies that SpawnStep returns a
// hard error (not a silent warning) when a required skill is not installed.
// A missing skill means the agent would run without critical instructions —
// blocking dispatch is the correct behavior.
func TestSpawnStep_MissingSkillReturnsError(t *testing.T) {
	sandbox := t.TempDir()

	r := &Runner{
		repo:     testRepoConfig(),
		workflow: testWorkflow(),
		queue:    testQueueClient(t),
	}

	w := &Worker{
		Name:       "alice",
		Repo:       "testrepo",
		SandboxDir: sandbox,
		SessionID:  "testrepo-alice",
	}

	item := &cistern.Droplet{
		ID:       "ci-skill-missing",
		Title:    "Skill test",
		Status:   "open",
		Priority: 1,
	}

	step := &aqueduct.WorkflowCataractae{
		Name:    "implement",
		Type:    aqueduct.CataractaeTypeAgent,
		Context: aqueduct.ContextFullCodebase,
		Skills:  []aqueduct.SkillRef{{Name: "nonexistent-skill-xyzabc123"}},
	}

	err := r.SpawnStep(w, item, step, "")
	if err == nil {
		t.Fatal("SpawnStep: expected error for missing skill, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent-skill-xyzabc123") {
		t.Errorf("error %q should mention the missing skill name", err.Error())
	}
}
