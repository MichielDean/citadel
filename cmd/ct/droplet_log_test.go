package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
)

func setupLogTestDB(t *testing.T) (*cistern.Client, string) {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	c, err := cistern.New(db, "ct")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, db
}

func runLogCapture(t *testing.T, id string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := runLog(&buf, id)
	return buf.String(), err
}

func TestDropletLog_ShowsCreationAndNotes(t *testing.T) {
	c, _ := setupLogTestDB(t)
	item, err := c.Add("myrepo", "Log task", "do something", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	c.AddNote(item.ID, "implement", "wrote the code")
	c.AddNote(item.ID, "review", "looks good")

	out, err := runLogCapture(t, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "created") {
		t.Errorf("log output missing 'created' event: %s", out)
	}
	if !strings.Contains(out, "implement") {
		t.Errorf("log output missing 'implement' note: %s", out)
	}
	if !strings.Contains(out, "review") {
		t.Errorf("log output missing 'review' note: %s", out)
	}
	if !strings.Contains(out, "wrote the code") {
		t.Errorf("log output missing note content: %s", out)
	}
}

func TestDropletLog_ShowsPoolEvent(t *testing.T) {
	c, _ := setupLogTestDB(t)
	item, err := c.Add("myrepo", "Pool task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	c.Pool(item.ID, "needs human review")

	out, err := runLogCapture(t, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "pooled") {
		t.Errorf("log output missing 'pooled' event: %s", out)
	}
}

func TestDropletLog_ShowsStageAssignment(t *testing.T) {
	c, _ := setupLogTestDB(t)
	item, err := c.Add("myrepo", "Stage task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}

	c.GetReadyForAqueduct("myrepo", "default")
	c.Assign(item.ID, "worker-1", "implement")
	c.AddNote(item.ID, "implement", "started implementation")

	out, err := runLogCapture(t, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "implement") {
		t.Errorf("log output missing 'implement' cataractae: %s", out)
	}
}

func TestDropletLog_ShowsHeader(t *testing.T) {
	c, _ := setupLogTestDB(t)
	item, err := c.Add("myrepo", "Header task", "desc", 1, 2)
	if err != nil {
		t.Fatal(err)
	}

	out, err := runLogCapture(t, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, item.ID) {
		t.Errorf("log output missing droplet ID: %s", out)
	}
	if !strings.Contains(out, "Header task") {
		t.Errorf("log output missing droplet title: %s", out)
	}
}

func TestDropletLog_ChronologicalOrder(t *testing.T) {
	c, _ := setupLogTestDB(t)
	item, err := c.Add("myrepo", "Order task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	c.AddNote(item.ID, "implement", "first note")
	time.Sleep(10 * time.Millisecond)
	c.AddNote(item.ID, "review", "second note")

	out, err := runLogCapture(t, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	firstIdx := strings.Index(out, "first note")
	secondIdx := strings.Index(out, "second note")
	if firstIdx == -1 || secondIdx == -1 {
		t.Fatalf("log output missing expected notes: %s", out)
	}
	if firstIdx > secondIdx {
		t.Errorf("notes not in chronological order: first note at %d, second at %d", firstIdx, secondIdx)
	}
}

func TestDropletLog_NonexistentDroplet(t *testing.T) {
	_, _ = setupLogTestDB(t)

	_, err := runLogCapture(t, "nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent droplet")
	}
}

func TestDropletLog_EmptyDroplet(t *testing.T) {
	c, _ := setupLogTestDB(t)
	item, err := c.Add("myrepo", "Empty task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}

	out, err := runLogCapture(t, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "created") {
		t.Errorf("log output should show creation event even for empty droplet: %s", out)
	}
	if !strings.Contains(out, "Empty task") {
		t.Errorf("log output should show title: %s", out)
	}
}

func TestDropletLog_JsonFormat(t *testing.T) {
	c, _ := setupLogTestDB(t)
	item, err := c.Add("myrepo", "Json log task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	c.AddNote(item.ID, "implement", "wrote code")

	logFmt = "json"

	var buf bytes.Buffer
	err = runLog(&buf, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 JSON lines (header + note), got %d", len(lines))
	}

	type logEntry struct {
		Time       string `json:"time"`
		Cataractae string `json:"cataractae"`
		Event      string `json:"event"`
		Detail     string `json:"detail"`
	}

	var header logEntry
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("first line is not valid JSON: %v\nline: %s", err, lines[0])
	}
	if header.Event != "created" {
		t.Errorf("first event should be 'created', got %q", header.Event)
	}

	var note logEntry
	if err := json.Unmarshal([]byte(lines[1]), &note); err != nil {
		t.Fatalf("second line is not valid JSON: %v\nline: %s", err, lines[1])
	}
	if note.Event != "note" {
		t.Errorf("second event should be 'note', got %q", note.Event)
	}
}

func TestDropletLog_InvalidFormat(t *testing.T) {
	c, _ := setupLogTestDB(t)
	item, err := c.Add("myrepo", "Format task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}

	logFmt = "xml"
	_, err = runLogCapture(t, item.ID)
	if err == nil {
		t.Error("expected error for invalid format")
	}
	if !strings.Contains(err.Error(), "format must be text or json") {
		t.Errorf("unexpected error message: %v", err)
	}
}
