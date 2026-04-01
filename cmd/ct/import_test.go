package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/tracker"
)

func init() {
	// Register a deterministic fake provider for use in import tests.
	tracker.Register("fake-tracker", func(cfg tracker.TrackerConfig) (tracker.TrackerProvider, error) {
		return &fakeImportProvider{}, nil
	})
}

// fakeImportProvider returns a fixed ExternalIssue for any key.
type fakeImportProvider struct{}

func (f *fakeImportProvider) Name() string {
	return "fake-tracker"
}

func (f *fakeImportProvider) FetchIssue(key string) (*tracker.ExternalIssue, error) {
	if key == "FAIL-1" {
		return nil, fmt.Errorf("fake: issue not found: %s", key)
	}
	return &tracker.ExternalIssue{
		Title:       "Fake issue " + key,
		Description: "Fake description for " + key,
		Priority:    2,
	}, nil
}

func TestImportCmd_AddsDropletDirectly(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	// Seed the DB with a known repo so resolveCanonicalRepo accepts it.
	c, err := cistern.New(db, "ci")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	out := captureStdout(t, func() {
		importRepo = "cistern"
		importFilter = false
		importPriority = 2
		importComplexity = "1"
		err = importCmd.RunE(importCmd, []string{"fake-tracker", "FAKE-42"})
	})
	if err != nil {
		t.Fatalf("RunE error: %v", err)
	}

	id := strings.TrimSpace(out)
	if id == "" {
		t.Fatal("expected droplet ID on stdout, got empty string")
	}

	// Verify the droplet exists with the correct fields.
	c2, err := cistern.New(db, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	droplet, err := c2.Get(id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	if droplet.Title != "Fake issue FAKE-42" {
		t.Errorf("Title = %q, want %q", droplet.Title, "Fake issue FAKE-42")
	}
	if droplet.Description != "Fake description for FAKE-42" {
		t.Errorf("Description = %q", droplet.Description)
	}
	if droplet.ExternalRef != "fake-tracker:FAKE-42" {
		t.Errorf("ExternalRef = %q, want %q", droplet.ExternalRef, "fake-tracker:FAKE-42")
	}
	if droplet.Complexity != 1 {
		t.Errorf("Complexity = %d, want 1 (standard)", droplet.Complexity)
	}
}

func TestImportCmd_OverridesPriorityWhenFlagSet(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, err := cistern.New(db, "ci")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	var id string
	out := captureStdout(t, func() {
		importRepo = "cistern"
		importFilter = false
		importPriority = 1
		importComplexity = "1"
		// Mark the priority flag as changed.
		_ = importCmd.Flags().Set("priority", "1")
		err = importCmd.RunE(importCmd, []string{"fake-tracker", "FAKE-99"})
		id = strings.TrimSpace(captureStdout(t, func() {}))
	})
	if err != nil {
		t.Fatalf("RunE error: %v", err)
	}
	id = strings.TrimSpace(out)

	c2, err := cistern.New(db, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	droplet, err := c2.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if droplet.Priority != 1 {
		t.Errorf("Priority = %d, want 1 (overridden)", droplet.Priority)
	}
}

func TestImportCmd_ErrorsOnUnknownProvider(t *testing.T) {
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	importRepo = "cistern"
	importFilter = false
	importComplexity = "1"
	err := importCmd.RunE(importCmd, []string{"no-such-provider-zzz", "ISSUE-1"})
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "no-such-provider-zzz") {
		t.Errorf("error %q does not mention the provider name", err.Error())
	}
}

func TestImportCmd_ErrorsOnFetchFailure(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, err := cistern.New(db, "ci")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	importRepo = "cistern"
	importFilter = false
	importComplexity = "1"
	err = importCmd.RunE(importCmd, []string{"fake-tracker", "FAIL-1"})
	if err == nil {
		t.Fatal("expected error when FetchIssue fails, got nil")
	}
}

func TestImportCmd_RequiresRepo(t *testing.T) {
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	importRepo = ""
	err := importCmd.RunE(importCmd, []string{"fake-tracker", "ISSUE-1"})
	if err == nil {
		t.Fatal("expected error when --repo is empty, got nil")
	}
	if !strings.Contains(err.Error(), "--repo") {
		t.Errorf("error %q does not mention --repo", err.Error())
	}
}

func TestLoadTrackerConfig_ReturnsMatchingEntry(t *testing.T) {
	// Write a minimal cistern.yaml with a trackers section.
	dir := t.TempDir()
	yamlContent := `
repos:
  - name: myrepo
    url: https://github.com/example/myrepo
    workflow_path: aqueduct/aqueduct.yaml
    cataractae: 1
    prefix: mr

trackers:
  - name: jira
    base_url: https://example.atlassian.net
    token_env: JIRA_TOKEN
    user_env: JIRA_USER
    priority_map:
      High: 1
      Medium: 2
`
	cfgPath := filepath.Join(dir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CT_CONFIG", cfgPath)

	cfg, err := loadTrackerConfig("jira")
	if err != nil {
		t.Fatalf("loadTrackerConfig: %v", err)
	}
	if cfg.BaseURL != "https://example.atlassian.net" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://example.atlassian.net")
	}
	if cfg.TokenEnv != "JIRA_TOKEN" {
		t.Errorf("TokenEnv = %q, want %q", cfg.TokenEnv, "JIRA_TOKEN")
	}
	if cfg.PriorityMap["High"] != 1 {
		t.Errorf("PriorityMap[High] = %d, want 1", cfg.PriorityMap["High"])
	}
}

func TestLoadTrackerConfig_ReturnsDefaultWhenNotFound(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cistern.yaml")
	// Write a config with no trackers section.
	yamlContent := `
repos:
  - name: repo1
    url: https://github.com/example/repo1
    workflow_path: aqueduct/aqueduct.yaml
    cataractae: 1
    prefix: r1
`
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CT_CONFIG", cfgPath)

	cfg, err := loadTrackerConfig("jira")
	if err != nil {
		t.Fatalf("loadTrackerConfig: %v", err)
	}
	if cfg.Name != "jira" {
		t.Errorf("Name = %q, want %q", cfg.Name, "jira")
	}
	if cfg.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty (fallback config)", cfg.BaseURL)
	}
}

// TestImportCmd_WithFilter_PreservesExternalRef verifies that when --filter is
// used, the created droplet retains the external_ref so the source issue
// remains traceable.
// Given a fake LLM agent that returns a single proposal,
// When importCmd is run with importFilter=true,
// Then the created droplet has ExternalRef set to "fake-tracker:FAKE-42".
func TestImportCmd_WithFilter_PreservesExternalRef(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")

	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	cfgPath := writeTestConfigWithAgent(t, "cistern", fakeagentBin)
	t.Setenv("CT_CONFIG", cfgPath)

	c, err := cistern.New(db, "ci")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	t.Cleanup(func() { importFilter = false })

	out := captureStdout(t, func() {
		importRepo = "cistern"
		importFilter = true
		importPriority = 2
		importComplexity = "1"
		err = importCmd.RunE(importCmd, []string{"fake-tracker", "FAKE-42"})
	})
	if err != nil {
		t.Fatalf("RunE error: %v", err)
	}

	id := strings.TrimSpace(out)
	if id == "" {
		t.Fatal("expected droplet ID on stdout, got empty string")
	}

	c2, err := cistern.New(db, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	droplet, err := c2.Get(id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	if droplet.ExternalRef != "fake-tracker:FAKE-42" {
		t.Errorf("ExternalRef = %q, want %q", droplet.ExternalRef, "fake-tracker:FAKE-42")
	}
}

// TestImportCmd_WithFilter_AgentError_ReturnsError verifies that when the LLM
// agent exits non-zero during the filter path, importCmd returns an error.
// Given a failing agent binary,
// When importCmd is run with importFilter=true,
// Then an error is returned.
func TestImportCmd_WithFilter_AgentError_ReturnsError(t *testing.T) {
	failagentBin := buildTestBin(t, "failagent", "github.com/MichielDean/cistern/internal/testutil/failagent")

	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	cfgPath := writeTestConfigWithAgent(t, "cistern", failagentBin)
	t.Setenv("CT_CONFIG", cfgPath)

	c, err := cistern.New(db, "ci")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	t.Cleanup(func() { importFilter = false })

	importRepo = "cistern"
	importFilter = true
	importPriority = 2
	importComplexity = "1"
	err = importCmd.RunE(importCmd, []string{"fake-tracker", "FAKE-42"})
	if err == nil {
		t.Fatal("expected error when agent fails, got nil")
	}
}

func TestImportCmd_JiraProvider_E2E(t *testing.T) {
	// End-to-end test using a real Jira provider against an httptest server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"key": "PROJ-1",
			"fields": map[string]any{
				"summary":     "Implement login page",
				"description": "Add OAuth2 login flow",
				"priority":    map[string]string{"name": "High"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("MY_JIRA_TOKEN", "test-token")

	// Write a cistern.yaml with a jira tracker entry.
	cfgPath := filepath.Join(dir, "cistern.yaml")
	yamlContent := fmt.Sprintf(`
repos:
  - name: myproject
    url: https://github.com/example/myproject
    workflow_path: aqueduct/aqueduct.yaml
    cataractae: 1
    prefix: mp

trackers:
  - name: jira
    base_url: %s
    token_env: MY_JIRA_TOKEN
`, srv.URL)
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CT_CONFIG", cfgPath)

	c, err := cistern.New(db, "mp")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	var out bytes.Buffer
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	importRepo = "myproject"
	importFilter = false
	importPriority = 2
	importComplexity = "1"
	err = importCmd.RunE(importCmd, []string{"jira", "PROJ-1"})

	w.Close()
	os.Stdout = old
	out.ReadFrom(r)

	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	id := strings.TrimSpace(out.String())
	if id == "" {
		t.Fatal("expected droplet ID, got empty string")
	}

	c2, err := cistern.New(db, "mp")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	droplet, err := c2.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if droplet.Title != "Implement login page" {
		t.Errorf("Title = %q", droplet.Title)
	}
	if droplet.ExternalRef != "jira:PROJ-1" {
		t.Errorf("ExternalRef = %q, want %q", droplet.ExternalRef, "jira:PROJ-1")
	}
}
