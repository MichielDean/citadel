package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

func setupRestartTestDB(t *testing.T) (*cistern.Client, string) {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	c, err := cistern.New(db, "bf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, db
}

func TestDropletRestart_WithCataractae(t *testing.T) {
	c, _ := setupRestartTestDB(t)
	item, err := c.Add("myrepo", "Stuck feature", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	c.GetReady("myrepo")
	c.Assign(item.ID, "alice", "implement")

	restartCataractae = "review"
	restartNotes = ""

	out := captureStdout(t, func() {
		if err := dropletRestartCmd.RunE(dropletRestartCmd, []string{item.ID}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "restarting at cataractae") {
		t.Errorf("expected 'restarting at cataractae' in output, got: %q", out)
	}
	if !strings.Contains(out, "review") {
		t.Errorf("expected 'review' in output, got: %q", out)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "open" {
		t.Errorf("status = %q, want %q", got.Status, "open")
	}
	if got.Assignee != "" {
		t.Errorf("assignee = %q, want empty after restart", got.Assignee)
	}
	if got.CurrentCataractae != "review" {
		t.Errorf("current_cataractae = %q, want %q", got.CurrentCataractae, "review")
	}
}

func TestDropletRestart_WithoutCataractae_UsesCurrentCataractae(t *testing.T) {
	c, _ := setupRestartTestDB(t)
	item, err := c.Add("myrepo", "Task", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	c.GetReady("myrepo")
	c.Assign(item.ID, "alice", "implement")

	restartCataractae = ""
	restartNotes = ""

	out := captureStdout(t, func() {
		if err := dropletRestartCmd.RunE(dropletRestartCmd, []string{item.ID}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "restarting at cataractae") {
		t.Errorf("expected 'restarting at cataractae' in output, got: %q", out)
	}
	if !strings.Contains(out, "implement") {
		t.Errorf("expected 'implement' (current cataractae) in output, got: %q", out)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentCataractae != "implement" {
		t.Errorf("current_cataractae = %q, want %q (current stage preserved)", got.CurrentCataractae, "implement")
	}
	if got.Status != "open" {
		t.Errorf("status = %q, want %q", got.Status, "open")
	}
}

func TestDropletRestart_NoCataractaeFlag_NoCurrentStage(t *testing.T) {
	c, _ := setupRestartTestDB(t)
	item, err := c.Add("myrepo", "New task", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}

	restartCataractae = ""
	restartNotes = ""

	err = dropletRestartCmd.RunE(dropletRestartCmd, []string{item.ID})
	if err == nil {
		t.Fatal("expected error when droplet has no current cataractae and --cataractae not provided")
	}
	if !strings.Contains(err.Error(), "no current cataractae") {
		t.Errorf("expected error about no current cataractae, got: %v", err)
	}
}

func TestDropletRestart_WithNotes(t *testing.T) {
	c, _ := setupRestartTestDB(t)
	item, err := c.Add("myrepo", "Task", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}

	restartCataractae = "delivery"
	restartNotes = "PR #157 conflicts resolved manually"

	out := captureStdout(t, func() {
		if err := dropletRestartCmd.RunE(dropletRestartCmd, []string{item.ID}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "note:") {
		t.Errorf("expected 'note:' in output, got: %q", out)
	}
	if !strings.Contains(out, "PR #157") {
		t.Errorf("expected note content in output, got: %q", out)
	}

	notes, err := c.GetNotes(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundUserNote := false
	for _, n := range notes {
		if n.CataractaeName == "restart" && strings.Contains(n.Content, "PR #157") {
			foundUserNote = true
		}
	}
	if !foundUserNote {
		t.Error("expected user note from --notes to be recorded")
	}
}

func TestDropletRestart_WritesSchedulerNote(t *testing.T) {
	c, _ := setupRestartTestDB(t)
	item, err := c.Add("myrepo", "Task", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}

	restartCataractae = "implement"
	restartNotes = ""

	if err := dropletRestartCmd.RunE(dropletRestartCmd, []string{item.ID}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	notes, err := c.GetNotes(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundScheduler := false
	for _, n := range notes {
		if n.CataractaeName == "scheduler" && strings.Contains(n.Content, "restarted at cataractae") {
			foundScheduler = true
		}
	}
	if !foundScheduler {
		t.Error("expected scheduler note with 'restarted at cataractae'")
	}
}

func TestDropletRestart_ClearsOutcomeAndAssignee(t *testing.T) {
	c, _ := setupRestartTestDB(t)
	item, err := c.Add("myrepo", "Task", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	c.GetReady("myrepo")
	c.Assign(item.ID, "worker-1", "review")
	c.SetOutcome(item.ID, "pass")

	restartCataractae = "implement"
	restartNotes = ""

	if err := dropletRestartCmd.RunE(dropletRestartCmd, []string{item.ID}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Assignee != "" {
		t.Errorf("assignee = %q, want empty after restart", got.Assignee)
	}
	if got.Outcome != "" {
		t.Errorf("outcome = %q, want empty after restart", got.Outcome)
	}
}

func TestDropletRestart_NotFound(t *testing.T) {
	_, _ = setupRestartTestDB(t)

	restartCataractae = "implement"
	restartNotes = ""

	err := dropletRestartCmd.RunE(dropletRestartCmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent droplet")
	}
}

func TestDropletRestart_UpdatesTimestamp(t *testing.T) {
	c, _ := setupRestartTestDB(t)
	item, err := c.Add("myrepo", "Task", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	before := item.UpdatedAt
	time.Sleep(10 * time.Millisecond)

	restartCataractae = "review"
	restartNotes = ""

	if err := dropletRestartCmd.RunE(dropletRestartCmd, []string{item.ID}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.UpdatedAt.After(before) {
		t.Errorf("UpdatedAt = %v, want after %v", got.UpdatedAt, before)
	}
}

// --- validateRestartCataractae and findWorkflowForRepo tests ---

// writeWorkflowConfig writes a minimal cistern.yaml with a repo entry that
// references a workflow file, and writes the workflow YAML alongside it.
// Returns the config path (to set CT_CONFIG).
func writeWorkflowConfig(t *testing.T, repoName string, cataractaeNames []string) string {
	t.Helper()
	dir := t.TempDir()

	workflowPath := filepath.Join(dir, "workflow.yaml")
	var wfYAML strings.Builder
	wfYAML.WriteString("name: test-flow\ncataractae:\n")
	for _, name := range cataractaeNames {
		wfYAML.WriteString(fmt.Sprintf("  - name: %s\n    type: agent\n    identity: %s-bot\n", name, name))
	}
	if err := os.WriteFile(workflowPath, []byte(wfYAML.String()), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	var cfgYAML strings.Builder
	cfgYAML.WriteString("repos:\n")
	cfgYAML.WriteString(fmt.Sprintf("  - name: %s\n    url: https://example.com/%s.git\n    workflow_path: %s\n    cataractae: 1\n    prefix: bf\n", repoName, repoName, workflowPath))

	cfgPath := filepath.Join(dir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML.String()), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

func TestValidateRestartCataractae_EmptyName_NoError(t *testing.T) {
	if err := validateRestartCataractae("", "myrepo"); err != nil {
		t.Errorf("expected nil error for empty cataractae name, got: %v", err)
	}
}

func TestValidateRestartCataractae_ConfigLoadFails_PassesThrough(t *testing.T) {
	t.Setenv("CT_CONFIG", filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err := validateRestartCataractae("implement", "myrepo"); err != nil {
		t.Errorf("expected nil error when config cannot be loaded (pass-through), got: %v", err)
	}
}

func TestValidateRestartCataractae_ValidCataractae(t *testing.T) {
	cfgPath := writeWorkflowConfig(t, "myrepo", []string{"implement", "review", "delivery"})
	t.Setenv("CT_CONFIG", cfgPath)

	if err := validateRestartCataractae("review", "myrepo"); err != nil {
		t.Errorf("expected nil for valid cataractae, got: %v", err)
	}
}

func TestValidateRestartCataractae_InvalidCataractae(t *testing.T) {
	cfgPath := writeWorkflowConfig(t, "myrepo", []string{"implement", "review", "delivery"})
	t.Setenv("CT_CONFIG", cfgPath)

	err := validateRestartCataractae("nonexistent", "myrepo")
	if err == nil {
		t.Fatal("expected error for invalid cataractae, got nil")
	}
	if !strings.Contains(err.Error(), "not valid") {
		t.Errorf("expected error to mention 'not valid', got: %v", err)
	}
	if !strings.Contains(err.Error(), "implement") || !strings.Contains(err.Error(), "review") || !strings.Contains(err.Error(), "delivery") {
		t.Errorf("expected error to list valid cataractae, got: %v", err)
	}
}

func TestValidateRestartCataractae_UnknownRepo_PassesThrough(t *testing.T) {
	cfgPath := writeWorkflowConfig(t, "myrepo", []string{"implement", "review", "delivery"})
	t.Setenv("CT_CONFIG", cfgPath)

	if err := validateRestartCataractae("implement", "unknown-repo"); err != nil {
		t.Errorf("expected nil for unknown repo (pass-through), got: %v", err)
	}
}

func TestValidateRestartCataractae_RepoWithNoWorkflowPath_PassesThrough(t *testing.T) {
	dir := t.TempDir()
	cfgYAML := "repos:\n  - name: bare-repo\n    url: https://example.com/bare.git\n    cataractae: 1\n    prefix: bf\n"
	cfgPath := filepath.Join(dir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CT_CONFIG", cfgPath)

	if err := validateRestartCataractae("implement", "bare-repo"); err != nil {
		t.Errorf("expected nil for repo without workflow_path (pass-through), got: %v", err)
	}
}

func TestFindWorkflowForRepo_MatchingRepo(t *testing.T) {
	cfgPath := writeWorkflowConfig(t, "myrepo", []string{"implement", "review", "delivery"})
	t.Setenv("CT_CONFIG", cfgPath)

	cfg, err := aqueduct.ParseAqueductConfig(resolveConfigPath())
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	wf := findWorkflowForRepo(cfg, "myrepo")
	if wf == nil {
		t.Fatal("expected workflow for matching repo, got nil")
	}
	found := false
	for _, step := range wf.Cataractae {
		if step.Name == "implement" {
			found = true
		}
	}
	if !found {
		t.Error("expected workflow to contain 'implement' cataractae")
	}
}

func TestFindWorkflowForRepo_CaseInsensitiveMatch(t *testing.T) {
	cfgPath := writeWorkflowConfig(t, "MyRepo", []string{"implement", "review"})
	t.Setenv("CT_CONFIG", cfgPath)

	cfg, err := aqueduct.ParseAqueductConfig(resolveConfigPath())
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	wf := findWorkflowForRepo(cfg, "myrepo")
	if wf == nil {
		t.Error("expected case-insensitive match, got nil")
	}
}

func TestFindWorkflowForRepo_NoMatchingRepo(t *testing.T) {
	cfgPath := writeWorkflowConfig(t, "myrepo", []string{"implement"})
	t.Setenv("CT_CONFIG", cfgPath)

	cfg, err := aqueduct.ParseAqueductConfig(resolveConfigPath())
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	wf := findWorkflowForRepo(cfg, "other-repo")
	if wf != nil {
		t.Errorf("expected nil for non-matching repo, got %v", wf)
	}
}

func TestFindWorkflowForRepo_RepoWithNoWorkflowPath(t *testing.T) {
	dir := t.TempDir()
	cfgYAML := "repos:\n  - name: bare-repo\n    url: https://example.com/bare.git\n    cataractae: 1\n    prefix: bf\n"
	cfgPath := filepath.Join(dir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CT_CONFIG", cfgPath)

	cfg, err := aqueduct.ParseAqueductConfig(resolveConfigPath())
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	wf := findWorkflowForRepo(cfg, "bare-repo")
	if wf != nil {
		t.Errorf("expected nil for repo without workflow_path, got %v", wf)
	}
}

func TestFindWorkflowForRepo_InvalidWorkflowPath(t *testing.T) {
	dir := t.TempDir()
	cfgYAML := "repos:\n  - name: broken-repo\n    url: https://example.com/broken.git\n    workflow_path: /nonexistent/path/workflow.yaml\n    cataractae: 1\n    prefix: bf\n"
	cfgPath := filepath.Join(dir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CT_CONFIG", cfgPath)

	cfg, err := aqueduct.ParseAqueductConfig(resolveConfigPath())
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	wf := findWorkflowForRepo(cfg, "broken-repo")
	if wf != nil {
		t.Errorf("expected nil for unparseable workflow path, got %v", wf)
	}
}
