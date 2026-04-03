package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
)

// captureStdout redirects os.Stdout to a pipe for the duration of fn, then
// returns everything written to it.  The write-end of the pipe is closed and
// os.Stdout is restored via defer, so both resources are cleaned up even if fn
// panics.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		w.Close() // no-op if already closed; unblocks reader on panic
		os.Stdout = old
	}()
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	r.Close()
	return buf.String()
}

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

	// Add an item with empty CurrentCataractae for remaining subtests.
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

	t.Run("em-dash for empty cataractae", func(t *testing.T) {
		listOutput = "table"
		listRepo = ""
		listStatus = ""
		out := captureStdout(t, func() {
			if err := dropletListCmd.RunE(dropletListCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "\u2014") {
			t.Errorf("expected em-dash for empty cataractaee column, got:\n%s", out)
		}
	})

	listOutput = "table"
}

func TestCisternList_PooledItems_NoFlowingMessage(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := c.Add("repo", "Pooled one", "", 1, 3)
	s2, _ := c.Add("repo", "Pooled two", "", 1, 3)
	c.Pool(s1.ID, "timed out")
	c.Pool(s2.ID, "timed out")
	c.Close()

	t.Run("status open filter with only pooled items shows No flowing droplets message", func(t *testing.T) {
		listOutput = "table"
		listRepo = ""
		listStatus = "open"
		listAll = false
		listCancelled = false
		defer func() { listStatus = "" }()

		out := captureStdout(t, func() {
			if err := dropletListCmd.RunE(dropletListCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		want := "No flowing droplets. 2 droplet(s) pooled."
		if strings.TrimSpace(out) != want {
			t.Fatalf("expected %q, got %q", want, out)
		}
	})

	t.Run("truly empty cistern still shows Cistern dry", func(t *testing.T) {
		dir2 := t.TempDir()
		db2 := filepath.Join(dir2, "test.db")
		t.Setenv("CT_DB", db2)

		listOutput = "table"
		listRepo = ""
		listStatus = ""
		listAll = false
		listCancelled = false

		out := captureStdout(t, func() {
			if err := dropletListCmd.RunE(dropletListCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if strings.TrimSpace(out) != "Cistern dry." {
			t.Fatalf("expected 'Cistern dry.', got %q", out)
		}
	})
}

func TestCisternList_FlowingAndPooled_ShowsNoMessage(t *testing.T) {
	// When flowing droplets exist, a filtered list returning no results must
	// emit no message at all — neither "No flowing droplets." nor "Cistern dry."
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	// Create one in_progress (flowing) droplet.
	ip, _ := c.Add("repo", "In-progress work", "", 1, 3)
	c.UpdateStatus(ip.ID, "in_progress")
	// Create one pooled droplet.
	stuck, _ := c.Add("repo", "Stuck item", "", 1, 3)
	c.Pool(stuck.ID, "timed out")
	c.Close()

	// Filter by --status open: no open droplets exist, so results are empty.
	// stats.Flowing > 0, so no message must be emitted.
	listOutput = "table"
	listRepo = ""
	listStatus = "open"
	listAll = false
	listCancelled = false
	defer func() { listStatus = "" }()

	out := captureStdout(t, func() {
		if err := dropletListCmd.RunE(dropletListCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	got := strings.TrimSpace(out)
	if got != "" {
		t.Fatalf("expected no output when flowing droplets exist, got: %q", got)
	}
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
		{"standard", 1, false},
		{"full", 2, false},
		{"critical", 3, false},
		{"", 2, false},
		{"trivial", 0, true},
		{"4", 0, true},
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
		{1, "standard"},
		{2, "full"},
		{3, "critical"},
		{0, "full"},
		{4, "full"},
		{99, "full"},
	}
	for _, tt := range tests {
		got := complexityName(tt.level)
		if got != tt.want {
			t.Errorf("complexityName(%d) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

// writeTestConfig writes a minimal valid cistern.yaml with the given repo names
// to a temp directory and returns the config file path.
func writeTestConfig(t *testing.T, repoNames ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cistern.yaml")
	var sb strings.Builder
	sb.WriteString("repos:\n")
	for _, name := range repoNames {
		sb.WriteString("  - name: " + name + "\n")
		sb.WriteString("    cataractae: 1\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("writeTestConfig: %v", err)
	}
	return path
}

// TestResolveCanonicalRepo_ExactMatch verifies that an exact-case input returns the
// configured name unchanged.
// Given config with "PortfolioWebsite",
// When resolveCanonicalRepo("PortfolioWebsite") is called,
// Then "PortfolioWebsite" is returned with no error.
func TestResolveCanonicalRepo_ExactMatch(t *testing.T) {
	cfgPath := writeTestConfig(t, "PortfolioWebsite")
	t.Setenv("CT_CONFIG", cfgPath)

	got, err := resolveCanonicalRepo("PortfolioWebsite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "PortfolioWebsite" {
		t.Errorf("got %q, want %q", got, "PortfolioWebsite")
	}
}

// TestResolveCanonicalRepo_CaseInsensitiveMatch verifies that a lower-case input
// matches and returns the configured (canonical) name.
// Given config with "PortfolioWebsite",
// When resolveCanonicalRepo("portfoliowebsite") is called,
// Then "PortfolioWebsite" is returned.
func TestResolveCanonicalRepo_CaseInsensitiveMatch(t *testing.T) {
	cfgPath := writeTestConfig(t, "PortfolioWebsite")
	t.Setenv("CT_CONFIG", cfgPath)

	got, err := resolveCanonicalRepo("portfoliowebsite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "PortfolioWebsite" {
		t.Errorf("got %q, want %q", got, "PortfolioWebsite")
	}
}

// TestResolveCanonicalRepo_UpperCaseMatch verifies that an all-caps input matches
// and returns the canonical name.
func TestResolveCanonicalRepo_UpperCaseMatch(t *testing.T) {
	cfgPath := writeTestConfig(t, "PortfolioWebsite")
	t.Setenv("CT_CONFIG", cfgPath)

	got, err := resolveCanonicalRepo("PORTFOLIOWEBSITE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "PortfolioWebsite" {
		t.Errorf("got %q, want %q", got, "PortfolioWebsite")
	}
}

// TestResolveCanonicalRepo_UnknownRepo_ReturnsError verifies that an input not
// matching any configured repo returns an error listing configured repos.
// Given config with "PortfolioWebsite" and "OtherRepo",
// When resolveCanonicalRepo("nonexistent") is called,
// Then an error mentioning "unknown repo nonexistent" and both configured names is returned.
func TestResolveCanonicalRepo_UnknownRepo_ReturnsError(t *testing.T) {
	cfgPath := writeTestConfig(t, "PortfolioWebsite", "OtherRepo")
	t.Setenv("CT_CONFIG", cfgPath)

	_, err := resolveCanonicalRepo("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown repo, got nil")
	}
	if !strings.Contains(err.Error(), "unknown repo nonexistent") {
		t.Errorf("error %q does not mention 'unknown repo nonexistent'", err.Error())
	}
	if !strings.Contains(err.Error(), "PortfolioWebsite") {
		t.Errorf("error %q does not list configured repo 'PortfolioWebsite'", err.Error())
	}
	if !strings.Contains(err.Error(), "OtherRepo") {
		t.Errorf("error %q does not list configured repo 'OtherRepo'", err.Error())
	}
}

// TestResolveCanonicalRepo_NoConfig_PassesThrough verifies that when no config
// is available the input is returned unchanged without error.
// Given no config file at CT_CONFIG path,
// When resolveCanonicalRepo("anyrepo") is called,
// Then "anyrepo" is returned unchanged with no error.
func TestResolveCanonicalRepo_NoConfig_PassesThrough(t *testing.T) {
	t.Setenv("CT_CONFIG", filepath.Join(t.TempDir(), "nonexistent.yaml"))

	got, err := resolveCanonicalRepo("anyrepo")
	if err != nil {
		t.Fatalf("expected passthrough with no config, got error: %v", err)
	}
	if got != "anyrepo" {
		t.Errorf("got %q, want %q", got, "anyrepo")
	}
}

// TestDropletAddCmd_NormalizesRepoCase verifies that droplet add with a wrong-case
// repo name stores the droplet using the canonical (configured) name.
// Given config with "PortfolioWebsite",
// When droplet add --repo portfoliowebsite --title "..." is called,
// Then the droplet is stored with repo = "PortfolioWebsite".
func TestDropletAddCmd_NormalizesRepoCase(t *testing.T) {
	cfgPath := writeTestConfig(t, "PortfolioWebsite")
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_DB", db)

	addTitle = "Test droplet"
	addRepo = "portfoliowebsite"
	addComplexity = "1"
	addDescription = ""
	addPriority = 1
	addDependsOn = nil
	defer func() {
		addTitle = ""
		addRepo = ""
		addComplexity = ""
		addPriority = 0
		addDependsOn = nil
	}()

	if err := dropletAddCmd.RunE(dropletAddCmd, nil); err != nil {
		t.Fatalf("droplet add: unexpected error: %v", err)
	}

	c, err := cistern.New(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	items, err := c.List("PortfolioWebsite", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("expected droplet stored with canonical repo name 'PortfolioWebsite', got none")
	}
}

// TestDropletAddCmd_UnknownRepo_ReturnsError verifies that droplet add with a repo
// name not found in the config returns a clear error and exits non-zero.
// Given config with "PortfolioWebsite",
// When droplet add --repo nonexistent is called,
// Then an error mentioning "unknown repo nonexistent" is returned.
func TestDropletAddCmd_UnknownRepo_ReturnsError(t *testing.T) {
	cfgPath := writeTestConfig(t, "PortfolioWebsite")
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_DB", filepath.Join(t.TempDir(), "test.db"))

	addTitle = "Test droplet"
	addRepo = "nonexistent"
	addComplexity = "1"
	defer func() {
		addTitle = ""
		addRepo = ""
		addComplexity = ""
	}()

	err := dropletAddCmd.RunE(dropletAddCmd, nil)
	if err == nil {
		t.Fatal("expected error for unknown repo, got nil")
	}
	if !strings.Contains(err.Error(), "unknown repo nonexistent") {
		t.Errorf("error %q does not mention 'unknown repo nonexistent'", err.Error())
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

	for _, want := range []string{"flowing", "queued", "delivered", "pooled", "total"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDropletStats_WithData(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	// Seed: 2 queued, 1 flowing, 3 delivered, 1 pooled.
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
	c.Pool(s1.ID, "stuck")
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
		{"pooled", "1"},
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

func TestDropletCancel(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("repo", "Old feature", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	cancelNotes = ""
	out := captureStdout(t, func() {
		if err := dropletCancelCmd.RunE(dropletCancelCmd, []string{item.ID}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "cancelled") {
		t.Errorf("expected 'cancelled' in output, got: %q", out)
	}

	// Verify status is cancelled in the DB.
	c2, _ := cistern.New(db, "")
	defer c2.Close()
	got, err := c2.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "cancelled" {
		t.Errorf("status = %q, want %q", got.Status, "cancelled")
	}
}

func TestDropletCancel_WithNotes(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("repo", "Superseded feature", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	cancelNotes = "superseded by newer approach"
	defer func() { cancelNotes = "" }()

	if err := dropletCancelCmd.RunE(dropletCancelCmd, []string{item.ID}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the reason note was recorded.
	c2, _ := cistern.New(db, "")
	defer c2.Close()
	notes, err := c2.GetNotes(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n.Content, "superseded by newer approach") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("cancel reason note not recorded; notes: %v", notes)
	}
}

func TestDropletCancel_NotFound(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	cancelNotes = ""
	err := dropletCancelCmd.RunE(dropletCancelCmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent droplet")
	}
}

func TestDropletList_HidesCancelledByDefault(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	active, _ := c.Add("repo", "Active feature", "", 1, 3)
	cancelled, _ := c.Add("repo", "Old feature", "", 1, 3)
	c.Cancel(cancelled.ID, "not needed")
	c.Close()

	// Default list (no flags) must not include the cancelled droplet.
	listOutput = "json"
	listRepo = ""
	listStatus = ""
	listAll = false
	listCancelled = false
	defer func() { listOutput = "table"; listCancelled = false }()

	out := captureStdout(t, func() {
		if err := dropletListCmd.RunE(dropletListCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	var items []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	for _, item := range items {
		if item["id"] == cancelled.ID {
			t.Errorf("cancelled droplet %s should not appear in default list", cancelled.ID)
		}
	}
	found := false
	for _, item := range items {
		if item["id"] == active.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("active droplet %s missing from list", active.ID)
	}
}

func TestDropletList_CancelledFlag(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	c.Add("repo", "Active feature", "", 1, 3)
	cancelled, _ := c.Add("repo", "Old feature", "", 1, 3)
	c.Cancel(cancelled.ID, "not needed")
	c.Close()

	listOutput = "json"
	listRepo = ""
	listStatus = ""
	listAll = false
	listCancelled = true
	defer func() { listOutput = "table"; listCancelled = false }()

	out := captureStdout(t, func() {
		if err := dropletListCmd.RunE(dropletListCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	var items []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(items) != 1 {
		t.Fatalf("--cancelled list returned %d items, want 1", len(items))
	}
	if items[0]["id"] != cancelled.ID {
		t.Errorf("returned item %s, want %s", items[0]["id"], cancelled.ID)
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
	c.UpdateStatus(item.ID, "pooled")
	c.SetCataractae(item.ID, "human")
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

		// Verify DB state: status=open, current_cataractae=delivery.
		c2, _ := cistern.New(db, "")
		defer c2.Close()
		got, err := c2.Get(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "open" {
			t.Errorf("expected status 'open', got %q", got.Status)
		}
		if got.CurrentCataractae != "delivery" {
			t.Errorf("expected current_cataractae 'delivery', got %q", got.CurrentCataractae)
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
	d := &cistern.Droplet{Status: "pooled", CurrentCataractae: "human"}
	got := displayStatusForDroplet(d)
	if got != "awaiting" {
		t.Errorf("expected 'awaiting', got %q", got)
	}

	// Non-human pooled droplet should display as "pooled".
	d2 := &cistern.Droplet{Status: "pooled", CurrentCataractae: "implement"}
	got2 := displayStatusForDroplet(d2)
	if got2 != "pooled" {
		t.Errorf("expected 'pooled', got %q", got2)
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

	t.Run("empty results when flowing droplets exist shows no message", func(t *testing.T) {
		// The shared DB has an in_progress droplet (stats.Flowing > 0), so a
		// search that returns no results must emit no message at all.
		searchQuery = "xyz-no-match"
		searchStatus = ""
		searchPriority = 0
		searchOutput = "table"
		out := captureStdout(t, func() {
			if err := dropletSearchCmd.RunE(dropletSearchCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if strings.TrimSpace(out) != "" {
			t.Fatalf("expected no output when flowing droplets exist, got %q", out)
		}
	})

	t.Run("empty results on truly empty cistern shows Cistern dry.", func(t *testing.T) {
		emptyDir := t.TempDir()
		emptyDB := filepath.Join(emptyDir, "empty.db")
		t.Setenv("CT_DB", emptyDB)

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

	t.Run("empty results with pooled items shows No flowing droplets message", func(t *testing.T) {
		// Use an isolated DB with only a pooled item and no flowing droplets,
		// so that stats.Flowing == 0 and the pooled message is shown.
		pooledDir := t.TempDir()
		pooledDB := filepath.Join(pooledDir, "pooled.db")
		t.Setenv("CT_DB", pooledDB)

		cs, err := cistern.New(pooledDB, "ts")
		if err != nil {
			t.Fatal(err)
		}
		stuck, _ := cs.Add("repo", "Stuck integration", "", 1, 3)
		cs.Pool(stuck.ID, "timed out")
		cs.Close()

		// Search for a term that matches nothing; pooled item has title "Stuck integration".
		searchQuery = "xyz-no-match"
		searchStatus = ""
		searchPriority = 0
		searchOutput = "table"
		out := captureStdout(t, func() {
			if err := dropletSearchCmd.RunE(dropletSearchCmd, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		want := "No flowing droplets. 1 droplet(s) pooled."
		if strings.TrimSpace(out) != want {
			t.Fatalf("expected %q, got %q", want, out)
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

func TestDropletListWatchValidation(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	t.Run("watch requires table output", func(t *testing.T) {
		listWatch = true
		listOutput = "json"
		listRepo = ""
		listStatus = ""
		defer func() { listWatch = false; listOutput = "table" }()

		err := dropletListCmd.RunE(dropletListCmd, nil)
		if err == nil {
			t.Fatal("expected error when --watch used with non-table output")
		}
		if !strings.Contains(err.Error(), "--watch requires --output table") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("watch requires interactive terminal", func(t *testing.T) {
		// In tests stdout is not a terminal, so isTerminal() returns false.
		listWatch = true
		listOutput = "table"
		listRepo = ""
		listStatus = ""
		defer func() { listWatch = false; listOutput = "table" }()

		err := dropletListCmd.RunE(dropletListCmd, nil)
		if err == nil {
			t.Fatal("expected error when --watch used outside an interactive terminal")
		}
		if !strings.Contains(err.Error(), "--watch requires an interactive terminal") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
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

// resetEditFlags re-registers the edit command's flags so each sub-test
// starts with a clean Changed() state.
func resetEditFlags() {
	dropletEditCmd.ResetFlags()
	dropletEditCmd.Flags().StringVar(&editDescription, "description", "", "")
	dropletEditCmd.Flags().StringVar(&editComplexity, "complexity", "", "")
	dropletEditCmd.Flags().IntVar(&editPriority, "priority", 0, "")
}

func TestDropletEdit(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("repo", "Original title", "old description", 3, 3)
	c.Close()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("update description", func(t *testing.T) {
		resetEditFlags()
		dropletEditCmd.Flags().Set("description", "new description")

		out := captureStdout(t, func() {
			if err := dropletEditCmd.RunE(dropletEditCmd, []string{item.ID}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "updated") {
			t.Errorf("expected 'updated' in output, got: %q", out)
		}

		c2, err := cistern.New(db, "")
		if err != nil {
			t.Fatal(err)
		}
		defer c2.Close()
		got, err := c2.Get(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Description != "new description" {
			t.Errorf("description = %q, want %q", got.Description, "new description")
		}
		// Title unchanged.
		if got.Title != "Original title" {
			t.Errorf("title changed unexpectedly: %q", got.Title)
		}
		// Complexity unchanged.
		if got.Complexity != 3 {
			t.Errorf("complexity changed unexpectedly: %d", got.Complexity)
		}
	})

	t.Run("description from stdin", func(t *testing.T) {
		resetEditFlags()
		dropletEditCmd.Flags().Set("description", "-")

		// Replace os.Stdin with a pipe containing the test input.
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		w.WriteString("stdin description\n")
		w.Close()
		oldStdin := os.Stdin
		os.Stdin = r
		defer func() { os.Stdin = oldStdin }()

		if err := dropletEditCmd.RunE(dropletEditCmd, []string{item.ID}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		c2, err := cistern.New(db, "")
		if err != nil {
			t.Fatal(err)
		}
		defer c2.Close()
		got, err := c2.Get(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Description != "stdin description" {
			t.Errorf("description = %q, want %q", got.Description, "stdin description")
		}
	})

	t.Run("update complexity", func(t *testing.T) {
		resetEditFlags()
		dropletEditCmd.Flags().Set("complexity", "standard")

		if err := dropletEditCmd.RunE(dropletEditCmd, []string{item.ID}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		c2, err := cistern.New(db, "")
		if err != nil {
			t.Fatal(err)
		}
		defer c2.Close()
		got, err := c2.Get(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Complexity != 1 {
			t.Errorf("complexity = %d, want 1", got.Complexity)
		}
	})

	t.Run("update priority", func(t *testing.T) {
		resetEditFlags()
		dropletEditCmd.Flags().Set("priority", "1")

		if err := dropletEditCmd.RunE(dropletEditCmd, []string{item.ID}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		c2, err := cistern.New(db, "")
		if err != nil {
			t.Fatal(err)
		}
		defer c2.Close()
		got, err := c2.Get(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Priority != 1 {
			t.Errorf("priority = %d, want 1", got.Priority)
		}
	})

	t.Run("no flags is an error", func(t *testing.T) {
		resetEditFlags()
		// No flags set via .Set(), so Changed() returns false for all.

		err := dropletEditCmd.RunE(dropletEditCmd, []string{item.ID})
		if err == nil {
			t.Fatal("expected error when no flags provided")
		}
		if !strings.Contains(err.Error(), "at least one") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("reject in_progress", func(t *testing.T) {
		resetEditFlags()

		c2, err := cistern.New(db, "")
		if err != nil {
			t.Fatal(err)
		}
		ip, err := c2.Add("repo", "Flowing droplet", "", 1, 3)
		if err != nil {
			t.Fatal(err)
		}
		c2.UpdateStatus(ip.ID, "in_progress")
		c2.Close()

		dropletEditCmd.Flags().Set("description", "new")

		err = dropletEditCmd.RunE(dropletEditCmd, []string{ip.ID})
		if err == nil {
			t.Fatal("expected error for in_progress droplet")
		}
		if !strings.Contains(err.Error(), "cannot edit a droplet that has been picked up") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestDropletHeartbeat(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	c, err := cistern.New(db, "ts")
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Add("repo", "Feature", "", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	before := time.Now().Add(-time.Second)
	out := captureStdout(t, func() {
		if err := dropletHeartbeatCmd.RunE(dropletHeartbeatCmd, []string{item.ID}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	after := time.Now().Add(time.Second)

	if !strings.Contains(out, "heartbeat recorded") {
		t.Errorf("expected 'heartbeat recorded' in output, got: %q", out)
	}

	// Verify last_heartbeat_at is written to DB.
	c2, err := cistern.New(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	got, err := c2.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastHeartbeatAt.IsZero() {
		t.Fatal("LastHeartbeatAt is zero after heartbeat command")
	}
	if got.LastHeartbeatAt.Before(before) || got.LastHeartbeatAt.After(after) {
		t.Errorf("LastHeartbeatAt = %v, want between %v and %v", got.LastHeartbeatAt, before, after)
	}
}

func TestDropletHeartbeat_NotFound(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)

	err := dropletHeartbeatCmd.RunE(dropletHeartbeatCmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent droplet")
	}
}

func TestRootCmd_CompletionCommand_IsHiddenFromHelp(t *testing.T) {
	if !rootCmd.CompletionOptions.HiddenDefaultCmd {
		t.Error("rootCmd.CompletionOptions.HiddenDefaultCmd must be true to hide 'completion' from help output")
	}
}

func TestRootCmd_CompletionCommand_BashSubcommandExists(t *testing.T) {
	rootCmd.InitDefaultCompletionCmd()
	completionCmd, _, err := rootCmd.Find([]string{"completion"})
	if err != nil {
		t.Fatalf("unexpected error finding completion command: %v", err)
	}
	if completionCmd == nil || completionCmd.Name() != "completion" {
		t.Fatal("completion command must exist for installer (ct completion bash)")
	}
	bashCmd, _, err := completionCmd.Find([]string{"bash"})
	if err != nil {
		t.Fatalf("unexpected error finding completion bash subcommand: %v", err)
	}
	if bashCmd == nil || bashCmd.Name() != "bash" {
		t.Fatal("completion bash subcommand must exist for installer")
	}
}
