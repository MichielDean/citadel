package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichielDean/cistern/internal/cistern"
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
	if out.Cataractae == nil {
		t.Error("cataractae array must not be null")
	}
	if out.Droplets == nil {
		t.Error("droplets array must not be null")
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

	c, err := cistern.New(db, "ts")
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
	// pooled item
	item3, err := c.Add("repo", "pooled item", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Pool(item3.ID, "test reason"); err != nil {
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

	if out.Counts.Flowing != 1 {
		t.Errorf("flowing: got %d, want 1", out.Counts.Flowing)
	}
	if out.Counts.Queued != 1 {
		t.Errorf("queued: got %d, want 1", out.Counts.Queued)
	}
	if out.Counts.Pooled != 1 {
		t.Errorf("pooled: got %d, want 1", out.Counts.Pooled)
	}
	if out.Counts.Delivered != 1 {
		t.Errorf("delivered: got %d, want 1", out.Counts.Delivered)
	}
	if out.Counts.Total != 3 { // flowing + queued + pooled, not closed
		t.Errorf("total: got %d, want 3", out.Counts.Total)
	}

	// Droplets array should exclude closed items.
	if len(out.Droplets) != 3 {
		t.Errorf("droplets: got %d, want 3 (closed excluded)", len(out.Droplets))
	}
}

func TestFlowInspectElapsedSeconds(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_CONFIG", filepath.Join(dir, "missing.yaml"))

	c, err := cistern.New(db, "ts")
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
	if err := c.Assign(item.ID, "upstream", "implement"); err != nil {
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

	if len(out.Droplets) != 1 {
		t.Fatalf("expected 1 droplet, got %d", len(out.Droplets))
	}
	d := out.Droplets[0]
	if d.Status != "in_progress" {
		t.Errorf("status: got %q, want in_progress", d.Status)
	}
	if d.Operator != "upstream" {
		t.Errorf("operator: got %q, want upstream", d.Operator)
	}
}

func TestTmuxSessionAlive(t *testing.T) {
	// A session with a random name should not exist.
	if tmuxSessionAlive("cistern-inspect-test-nonexistent-xyz987") {
		t.Error("expected non-existent tmux session to return false")
	}
}

func TestBuildInspectOutput_UsesProvidedPathsAndIncludesPooled(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	cfgPath := tempCfg(t)

	c, err := cistern.New(db, "mr")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("myrepo", "pooled item", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Pool(item.ID, "test reason"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	out, err := buildInspectOutput(cfgPath, db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Cistern.Config != cfgPath {
		t.Errorf("config: got %q, want %q", out.Cistern.Config, cfgPath)
	}
	if out.Counts.Pooled != 1 {
		t.Errorf("pooled: got %d, want 1", out.Counts.Pooled)
	}
	if len(out.Droplets) != 1 {
		t.Fatalf("droplets: got %d, want 1", len(out.Droplets))
	}
	if out.Droplets[0].Status != "pooled" {
		t.Errorf("droplet status: got %q, want pooled", out.Droplets[0].Status)
	}
}
