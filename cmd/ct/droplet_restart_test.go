package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

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
