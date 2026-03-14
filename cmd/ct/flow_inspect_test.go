package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichielDean/citadel/internal/queue"
)

func TestFlowInspectJSON(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_CONFIG", filepath.Join(dir, "missing.yaml")) // config absent — should not fatal

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	inspectTable = false
	err := flowInspectCmd.RunE(flowInspectCmd, nil)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var out inspectOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	// Top-level arrays must not be null.
	if out.Channels == nil {
		t.Error("channels array must not be null")
	}
	if out.Drops == nil {
		t.Error("drops array must not be null")
	}
	if out.RecentEvents == nil {
		t.Error("recent_events array must not be null")
	}
}

func TestFlowInspectCisternCounts(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_CONFIG", filepath.Join(dir, "missing.yaml"))

	c, err := queue.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	// open item
	_, err = c.Add("repo", "open item", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	// in_progress item
	item2, err := c.Add("repo", "flowing item", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateStatus(item2.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	// escalated item
	item3, err := c.Add("repo", "poisoned item", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Escalate(item3.ID, "test reason"); err != nil {
		t.Fatal(err)
	}
	// closed item
	item4, err := c.Add("repo", "closed item", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CloseItem(item4.ID); err != nil {
		t.Fatal(err)
	}
	c.Close()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	inspectTable = false
	err = flowInspectCmd.RunE(flowInspectCmd, nil)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var out inspectOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if out.Cistern.Flowing != 1 {
		t.Errorf("flowing: got %d, want 1", out.Cistern.Flowing)
	}
	if out.Cistern.Queued != 1 {
		t.Errorf("queued: got %d, want 1", out.Cistern.Queued)
	}
	if out.Cistern.Poisoned != 1 {
		t.Errorf("poisoned: got %d, want 1", out.Cistern.Poisoned)
	}
	if out.Cistern.Closed != 1 {
		t.Errorf("closed: got %d, want 1", out.Cistern.Closed)
	}
	if out.Cistern.Total != 3 { // flowing + queued + poisoned, not closed
		t.Errorf("total: got %d, want 3", out.Cistern.Total)
	}

	// Drops array should exclude closed items.
	if len(out.Drops) != 3 {
		t.Errorf("drops: got %d, want 3 (closed excluded)", len(out.Drops))
	}
}

func TestFlowInspectElapsedSeconds(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_CONFIG", filepath.Join(dir, "missing.yaml"))

	c, err := queue.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("repo", "in_progress item", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateStatus(item.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	if err := c.Assign(item.ID, "furiosa", "implement"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	inspectTable = false
	err = flowInspectCmd.RunE(flowInspectCmd, nil)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var out inspectOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(out.Drops) != 1 {
		t.Fatalf("expected 1 drop, got %d", len(out.Drops))
	}
	d := out.Drops[0]
	if d.Status != "in_progress" {
		t.Errorf("status: got %q, want in_progress", d.Status)
	}
	if d.Assignee != "furiosa" {
		t.Errorf("assignee: got %q, want furiosa", d.Assignee)
	}
}

func TestTmuxSessionAlive(t *testing.T) {
	// A session with a random name should not exist.
	if tmuxSessionAlive("citadel-inspect-test-nonexistent-xyz987") {
		t.Error("expected non-existent tmux session to return false")
	}
}

func TestBuildInspectOutput_UsesProvidedPathsAndIncludesPoisoned(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	cfgPath := tempCfg(t)

	c, err := queue.New(db, "mr")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("myrepo", "poisoned item", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Escalate(item.ID, "test reason"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	out, err := buildInspectOutput(cfgPath, db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Citadel.Config != cfgPath {
		t.Errorf("config: got %q, want %q", out.Citadel.Config, cfgPath)
	}
	if out.Cistern.Poisoned != 1 {
		t.Errorf("poisoned: got %d, want 1", out.Cistern.Poisoned)
	}
	if len(out.Drops) != 1 {
		t.Fatalf("drops: got %d, want 1", len(out.Drops))
	}
	if out.Drops[0].Status != "escalated" {
		t.Errorf("drop status: got %q, want escalated", out.Drops[0].Status)
	}
}
