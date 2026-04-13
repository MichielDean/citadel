package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/cistern"
)

func setupTailTestDB(t *testing.T) (*cistern.Client, string) {
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

func runTailCapture(t *testing.T, id string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := runTail(&buf, id)
	return buf.String(), err
}

func TestDropletTail_TextFormat_ShowsNotesAndEvents(t *testing.T) {
	c, _ := setupTailTestDB(t)
	item, err := c.Add("myrepo", "Tail task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	c.AddNote(item.ID, "implement", "wrote the code")
	c.AddNote(item.ID, "review", "looks good")

	tailFmt = "text"
	tailCount = 20
	tailFollow = false

	out, err := runTailCapture(t, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "note") {
		t.Errorf("text output missing 'note' kind: %s", out)
	}
	if !strings.Contains(out, "implement") {
		t.Errorf("text output missing 'implement': %s", out)
	}
	if !strings.Contains(out, "review") {
		t.Errorf("text output missing 'review': %s", out)
	}
}

func TestDropletTail_JsonFormat_OutputsNDJson(t *testing.T) {
	c, _ := setupTailTestDB(t)
	item, err := c.Add("myrepo", "Json task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	c.AddNote(item.ID, "implement", "wrote the code")

	tailFmt = "json"
	tailCount = 20
	tailFollow = false

	out, err := runTailCapture(t, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSON line, got %d", len(lines))
	}

	var ch cistern.DropletChange
	if err := json.Unmarshal([]byte(lines[0]), &ch); err != nil {
		t.Fatalf("output is not valid JSON: %v\nline: %s", err, lines[0])
	}
	if ch.Kind != "note" {
		t.Errorf("Kind = %q, want %q", ch.Kind, "note")
	}
	if !strings.Contains(ch.Value, "implement") {
		t.Errorf("Value = %q, want it to contain 'implement'", ch.Value)
	}
}

func TestDropletTail_LinesFlag_LimitsOutput(t *testing.T) {
	c, _ := setupTailTestDB(t)
	item, err := c.Add("myrepo", "Limited task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	notes := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	for _, n := range notes {
		c.AddNote(item.ID, "step", n)
	}

	tailFmt = "text"
	tailCount = 2
	tailFollow = false

	out, err := runTailCapture(t, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lineCount := len(strings.Split(strings.TrimSpace(out), "\n"))
	if lineCount != 2 {
		t.Errorf("expected 2 output lines with --lines 2, got %d", lineCount)
	}
	if !strings.Contains(out, "delta") {
		t.Errorf("expected second-to-last event 'delta' in output, got: %s", out)
	}
	if !strings.Contains(out, "epsilon") {
		t.Errorf("expected last event 'epsilon' in output, got: %s", out)
	}
	if strings.Contains(out, "alpha") || strings.Contains(out, "beta") || strings.Contains(out, "gamma") {
		t.Errorf("older events should not appear with --lines 2, got: %s", out)
	}
}

func TestDropletTail_PoolEvent(t *testing.T) {
	c, _ := setupTailTestDB(t)
	item, err := c.Add("myrepo", "Pooled task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	c.Pool(item.ID, "needs human review")

	tailFmt = "text"
	tailCount = 20
	tailFollow = false

	out, err := runTailCapture(t, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "event") {
		t.Errorf("text output missing 'event' kind: %s", out)
	}
	if !strings.Contains(out, "pool") {
		t.Errorf("text output missing 'pool' in value: %s", out)
	}
}

func TestDropletTail_InvalidFormat(t *testing.T) {
	_, _ = setupTailTestDB(t)
	tailFmt = "xml"
	tailCount = 20
	tailFollow = false

	_, err := runTailCapture(t, "some-id")
	if err == nil {
		t.Error("expected error for invalid format")
	}
	if !strings.Contains(err.Error(), "format must be text or json") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDropletTail_InvalidLines(t *testing.T) {
	_, _ = setupTailTestDB(t)
	tailFmt = "text"
	tailCount = 0
	tailFollow = false

	_, err := runTailCapture(t, "some-id")
	if err == nil {
		t.Error("expected error for invalid lines")
	}
	if !strings.Contains(err.Error(), "lines must be >= 1") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDropletTail_NonexistentDroplet(t *testing.T) {
	_, _ = setupTailTestDB(t)
	tailFmt = "text"
	tailCount = 10
	tailFollow = false

	_, err := runTailCapture(t, "nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent droplet")
	}
}
