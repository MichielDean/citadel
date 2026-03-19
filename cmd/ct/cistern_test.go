package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/cistern"
)

func TestCisternListOutputFlag(t *testing.T) {
	// Set up a temp DB.
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	// Verify default flag value is "table".
	f := dropletListCmd.Flags().Lookup("output")
	if f == nil {
		t.Fatal("--output flag not registered")
	}
	if f.DefValue != "table" {
		t.Fatalf("expected default 'table', got %q", f.DefValue)
	}

	// Test json output with empty cistern.
	t.Run("json empty", func(t *testing.T) {
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		listOutput = "json"
		listRepo = ""
		listStatus = ""
		err := dropletListCmd.RunE(dropletListCmd, nil)

		w.Close()
		os.Stdout = old

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var buf bytes.Buffer
		buf.ReadFrom(r)
		out := buf.String()

		var items []*cistern.Droplet
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
		}
		if len(items) != 0 {
			t.Fatalf("expected empty array, got %d items", len(items))
		}
	})

	// Test json output with one item.
	t.Run("json with items", func(t *testing.T) {
		c, err := cistern.New(db, "ts")
		if err != nil {
			t.Fatal(err)
		}
		item, err := c.Add("github.com/test/repo", "Test item", "", 1, 3)
		c.Close()
		if err != nil {
			t.Fatal(err)
		}

		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		listOutput = "json"
		listRepo = ""
		listStatus = ""
		err = dropletListCmd.RunE(dropletListCmd, nil)

		w.Close()
		os.Stdout = old

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var buf bytes.Buffer
		buf.ReadFrom(r)
		out := buf.String()

		var items []*cistern.Droplet
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].ID != item.ID {
			t.Fatalf("expected ID %q, got %q", item.ID, items[0].ID)
		}
	})

	// Test invalid output flag.
	t.Run("invalid output flag", func(t *testing.T) {
		listOutput = "csv"
		err := dropletListCmd.RunE(dropletListCmd, nil)
		if err == nil {
			t.Fatal("expected error for invalid --output value")
		}
	})

	// Reset flag.
	listOutput = "table"
}

func TestCisternListTableOutput(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	captureStdout := func(t *testing.T, fn func()) string {
		t.Helper()
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w
		fn()
		w.Close()
		os.Stdout = old
		var buf bytes.Buffer
		buf.ReadFrom(r)
		return buf.String()
	}

	t.Run("empty cistern", func(t *testing.T) {
		listOutput = "table"
		listRepo = ""
		listStatus = ""
		out := captureStdout(t, func() {
			if err := dropletListCmd.RunE(dropletListCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if strings.TrimSpace(out) != "Cistern dry." {
			t.Fatalf("expected 'Cistern dry.', got %q", out)
		}
	})

	// Add an item with empty CurrentCataracta for remaining subtests.
	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Add("github.com/test/repo", "Test droplet", "", 1, 3)
	c.Close()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("table header CATARACTA", func(t *testing.T) {
		listOutput = "table"
		listRepo = ""
		listStatus = ""
		out := captureStdout(t, func() {
			if err := dropletListCmd.RunE(dropletListCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "CATARACTA") {
			t.Errorf("expected header to contain 'CATARACTA', got:\n%s", out)
		}
		if strings.Contains(out, "SLUICE") {
			t.Errorf("header must not contain 'SLUICE', got:\n%s", out)
		}
	})

	t.Run("em-dash for empty cataracta", func(t *testing.T) {
		listOutput = "table"
		listRepo = ""
		listStatus = ""
		out := captureStdout(t, func() {
			if err := dropletListCmd.RunE(dropletListCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "\u2014") {
			t.Errorf("expected em-dash for empty cataracta column, got:\n%s", out)
		}
	})

	listOutput = "table"
}

func TestParseComplexity(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"1", 1, false},
		{"2", 2, false},
		{"3", 3, false},
		{"4", 4, false},
		{"trivial", 1, false},
		{"standard", 2, false},
		{"full", 3, false},
		{"critical", 4, false},
		{"", 3, false},
		{"5", 0, true},
		{"foo", 0, true},
	}

	for _, tt := range tests {
		got, err := parseComplexity(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseComplexity(%q) = %d, want error", tt.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseComplexity(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseComplexity(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestComplexityName(t *testing.T) {
	tests := []struct {
		level int
		want  string
	}{
		{1, "trivial"},
		{2, "standard"},
		{3, "full"},
		{4, "critical"},
		{0, "full"},
		{99, "full"},
	}
	for _, tt := range tests {
		got := complexityName(tt.level)
		if got != tt.want {
			t.Errorf("complexityName(%d) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestDropletStats_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := dropletStatsCmd.RunE(dropletStatsCmd, nil)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error on empty DB: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	for _, want := range []string{"flowing", "queued", "delivered", "stagnant", "total"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDropletStats_WithData(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	// Seed: 2 queued, 1 flowing, 3 delivered, 1 stagnant.
	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	c.Add("repo", "q1", "", 1, 3)
	c.Add("repo", "q2", "", 1, 3)
	ip, _ := c.Add("repo", "ip1", "", 1, 3)
	d1, _ := c.Add("repo", "d1", "", 1, 3)
	d2, _ := c.Add("repo", "d2", "", 1, 3)
	d3, _ := c.Add("repo", "d3", "", 1, 3)
	s1, _ := c.Add("repo", "s1", "", 1, 3)
	c.UpdateStatus(ip.ID, "in_progress")
	c.CloseItem(d1.ID)
	c.CloseItem(d2.ID)
	c.CloseItem(d3.ID)
	c.Escalate(s1.ID, "stuck")
	c.Close()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = dropletStatsCmd.RunE(dropletStatsCmd, nil)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	out := buf.String()

	checks := []struct{ label, value string }{
		{"flowing", "1"},
		{"queued", "2"},
		{"delivered", "3"},
		{"stagnant", "1"},
		{"total", "7"},
	}
	for _, ch := range checks {
		if !strings.Contains(out, ch.label) {
			t.Errorf("output missing label %q:\n%s", ch.label, out)
		}
		if !strings.Contains(out, ch.value) {
			t.Errorf("output missing value %q for %q:\n%s", ch.value, ch.label, out)
		}
	}
	// Verify separator and total row present.
	if !strings.Contains(out, "──") {
		t.Errorf("output missing separator line:\n%s", out)
	}
}

func TestDropletApprove(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("repo", "Critical feature", "", 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate scheduler routing to human gate.
	c.UpdateStatus(item.ID, "stagnant")
	c.SetCataracta(item.ID, "human")
	c.Close()

	t.Run("approve releases to delivery", func(t *testing.T) {
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		err := dropletApproveCmd.RunE(dropletApproveCmd, []string{item.ID})

		w.Close()
		os.Stdout = old

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var buf bytes.Buffer
		buf.ReadFrom(r)
		out := buf.String()
		if !strings.Contains(out, "approved for delivery") {
			t.Errorf("expected 'approved for delivery' in output, got: %q", out)
		}

		// Verify DB state: status=open, current_cataracta=delivery.
		c2, _ := cistern.New(db, "")
		defer c2.Close()
		got, err := c2.Get(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "open" {
			t.Errorf("expected status 'open', got %q", got.Status)
		}
		if got.CurrentCataracta != "delivery" {
			t.Errorf("expected current_cataracta 'delivery', got %q", got.CurrentCataracta)
		}
	})
}

func TestDropletApprove_NotHumanGated(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("repo", "Normal feature", "", 1, 3)
	c.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = dropletApproveCmd.RunE(dropletApproveCmd, []string{item.ID})
	if err == nil {
		t.Fatal("expected error for non-human-gated droplet")
	}
	if !strings.Contains(err.Error(), "not awaiting human approval") {
		t.Errorf("expected 'not awaiting human approval' in error, got: %v", err)
	}
}

func TestDisplayStatusForDroplet_AwaitingApproval(t *testing.T) {
	// Human-gated droplet should display as "awaiting".
	d := &cistern.Droplet{Status: "stagnant", CurrentCataracta: "human"}
	got := displayStatusForDroplet(d)
	if got != "awaiting" {
		t.Errorf("expected 'awaiting', got %q", got)
	}

	// Non-human stagnant droplet should display as "stagnant".
	d2 := &cistern.Droplet{Status: "stagnant", CurrentCataracta: "implement"}
	got2 := displayStatusForDroplet(d2)
	if got2 != "stagnant" {
		t.Errorf("expected 'stagnant', got %q", got2)
	}

	// Icon for awaiting should be present in statusIcon.
	icon := statusIcon("awaiting")
	if !strings.Contains(icon, "⏸") {
		t.Errorf("expected ⏸ icon for 'awaiting', got %q", icon)
	}
}

func TestDropletSearch(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	captureStdout := func(t *testing.T, fn func()) string {
		t.Helper()
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w
		fn()
		w.Close()
		os.Stdout = old
		var buf bytes.Buffer
		buf.ReadFrom(r)
		return buf.String()
	}

	// Seed data.
	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	c.Add("repo", "Fix login bug", "", 1, 3)
	c.Add("repo", "Add dashboard", "", 2, 3)
	ip, _ := c.Add("repo", "Fix payments", "", 1, 3)
	c.UpdateStatus(ip.ID, "in_progress")
	c.Close()

	t.Run("query filter matches title substring", func(t *testing.T) {
		searchQuery = "fix"
		searchStatus = ""
		searchPriority = 0
		searchOutput = "table"
		out := captureStdout(t, func() {
			if err := dropletSearchCmd.RunE(dropletSearchCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "Fix login bug") {
			t.Errorf("expected 'Fix login bug' in output:\n%s", out)
		}
		if !strings.Contains(out, "Fix payments") {
			t.Errorf("expected 'Fix payments' in output:\n%s", out)
		}
		if strings.Contains(out, "Add dashboard") {
			t.Errorf("'Add dashboard' should be filtered out:\n%s", out)
		}
	})

	t.Run("empty results shows Cistern dry.", func(t *testing.T) {
		searchQuery = "xyz-no-match"
		searchStatus = ""
		searchPriority = 0
		searchOutput = "table"
		out := captureStdout(t, func() {
			if err := dropletSearchCmd.RunE(dropletSearchCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if strings.TrimSpace(out) != "Cistern dry." {
			t.Fatalf("expected 'Cistern dry.', got %q", out)
		}
	})

	t.Run("json output", func(t *testing.T) {
		searchQuery = ""
		searchStatus = ""
		searchPriority = 0
		searchOutput = "json"
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w
		err := dropletSearchCmd.RunE(dropletSearchCmd, nil)
		w.Close()
		os.Stdout = old
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var buf bytes.Buffer
		buf.ReadFrom(r)
		out := buf.String()
		var items []*cistern.Droplet
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
		}
		if len(items) != 3 {
			t.Fatalf("expected 3 items, got %d", len(items))
		}
	})

	t.Run("status filter", func(t *testing.T) {
		searchQuery = ""
		searchStatus = "in_progress"
		searchPriority = 0
		searchOutput = "table"
		out := captureStdout(t, func() {
			if err := dropletSearchCmd.RunE(dropletSearchCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "Fix payments") {
			t.Errorf("expected 'Fix payments' in output:\n%s", out)
		}
		if strings.Contains(out, "Fix login bug") {
			t.Errorf("'Fix login bug' should be filtered out:\n%s", out)
		}
	})

	t.Run("priority filter", func(t *testing.T) {
		searchQuery = ""
		searchStatus = ""
		searchPriority = 2
		searchOutput = "table"
		out := captureStdout(t, func() {
			if err := dropletSearchCmd.RunE(dropletSearchCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "Add dashboard") {
			t.Errorf("expected 'Add dashboard' in output:\n%s", out)
		}
		if strings.Contains(out, "Fix login bug") {
			t.Errorf("'Fix login bug' should be filtered out:\n%s", out)
		}
	})

	t.Run("invalid output flag", func(t *testing.T) {
		searchQuery = ""
		searchStatus = ""
		searchPriority = 0
		searchOutput = "csv"
		err := dropletSearchCmd.RunE(dropletSearchCmd, nil)
		if err == nil {
			t.Fatal("expected error for invalid --output value")
		}
	})

	// Reset flags.
	searchQuery = ""
	searchStatus = ""
	searchPriority = 0
	searchOutput = "table"
}

func TestDropletExport(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	captureStdout := func(t *testing.T, fn func()) string {
		t.Helper()
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w
		fn()
		w.Close()
		os.Stdout = old
		var buf bytes.Buffer
		buf.ReadFrom(r)
		return buf.String()
	}

	// Seed data.
	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("repo", "Export test droplet", "", 1, 3)
	c.Close()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("json empty", func(t *testing.T) {
		dir2 := t.TempDir()
		db2 := filepath.Join(dir2, "empty.db")
		t.Setenv("CT_DB", db2)
		defer t.Setenv("CT_DB", db)

		exportFormat = "json"
		exportQuery = ""
		exportStatus = ""
		exportPriority = 0
		out := captureStdout(t, func() {
			if err := dropletExportCmd.RunE(dropletExportCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		var items []*cistern.Droplet
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
		}
		if len(items) != 0 {
			t.Fatalf("expected empty array, got %d items", len(items))
		}
	})

	t.Run("json with items", func(t *testing.T) {
		exportFormat = "json"
		exportQuery = ""
		exportStatus = ""
		exportPriority = 0
		out := captureStdout(t, func() {
			if err := dropletExportCmd.RunE(dropletExportCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		var items []*cistern.Droplet
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].ID != item.ID {
			t.Fatalf("expected ID %q, got %q", item.ID, items[0].ID)
		}
	})

	t.Run("csv header and row", func(t *testing.T) {
		exportFormat = "csv"
		exportQuery = ""
		exportStatus = ""
		exportPriority = 0
		out := captureStdout(t, func() {
			if err := dropletExportCmd.RunE(dropletExportCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "id,repo,title") {
			t.Errorf("expected CSV header, got:\n%s", out)
		}
		if !strings.Contains(out, item.ID) {
			t.Errorf("expected item ID %q in CSV output:\n%s", item.ID, out)
		}
		if !strings.Contains(out, "Export test droplet") {
			t.Errorf("expected item title in CSV output:\n%s", out)
		}
	})

	t.Run("csv query filter", func(t *testing.T) {
		exportFormat = "csv"
		exportQuery = "export"
		exportStatus = ""
		exportPriority = 0
		out := captureStdout(t, func() {
			if err := dropletExportCmd.RunE(dropletExportCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "Export test droplet") {
			t.Errorf("expected matching item in output:\n%s", out)
		}
	})

	t.Run("invalid format flag", func(t *testing.T) {
		exportFormat = "table"
		err := dropletExportCmd.RunE(dropletExportCmd, nil)
		if err == nil {
			t.Fatal("expected error for invalid --format value")
		}
	})

	// Reset flags.
	exportFormat = "json"
	exportQuery = ""
	exportStatus = ""
	exportPriority = 0
}

func TestDropletRename(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("repo", "Original Title", "", 1, 3)
	c.Close()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("success", func(t *testing.T) {
		err := dropletRenameCmd.RunE(dropletRenameCmd, []string{item.ID, "New Title"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		c2, _ := cistern.New(db, "ts")
		defer c2.Close()
		got, _ := c2.Get(item.ID)
		if got.Title != "New Title" {
			t.Errorf("expected title %q, got %q", "New Title", got.Title)
		}
	})

	t.Run("not found", func(t *testing.T) {
		err := dropletRenameCmd.RunE(dropletRenameCmd, []string{"ts-xxxxx", "Whatever"})
		if err == nil {
			t.Fatal("expected error for unknown droplet ID")
		}
	})
}
