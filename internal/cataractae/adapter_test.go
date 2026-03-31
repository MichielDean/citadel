package cataractae

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/castellarius"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/gates"
)

// adapterCaptureHandler is a minimal slog.Handler that records log entries.
type adapterCaptureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *adapterCaptureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *adapterCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}
func (h *adapterCaptureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *adapterCaptureHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *adapterCaptureHandler) hasWarn() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == slog.LevelWarn {
			return true
		}
	}
	return false
}

// TestSpawnAutomated_AddNoteError_LogsWarn verifies that when AddNote fails
// during an automated step, the error is logged at WARN level (not silently
// discarded) and spawnAutomated returns the SetOutcome error non-zero.
func TestSpawnAutomated_AddNoteError_LogsWarn(t *testing.T) {
	db, err := cistern.New(filepath.Join(t.TempDir(), "test.db"), "tr")
	if err != nil {
		t.Fatalf("cistern.New: %v", err)
	}
	// Close the DB immediately so that AddNote and SetOutcome both fail.
	db.Close()

	h := &adapterCaptureHandler{}
	a := &Adapter{
		runners:      nil, // not used for automated steps
		executor:     gates.New(),
		queueClients: map[string]*cistern.Client{"testrepo": db},
		logger:       slog.New(h),
	}

	req := castellarius.CataractaeRequest{
		Item:         &cistern.Droplet{ID: "test-id", Title: "Test"},
		Step:         aqueduct.WorkflowCataractae{Name: "noop", Type: aqueduct.CataractaeTypeAutomated},
		RepoConfig:   aqueduct.RepoConfig{Name: "testrepo"},
		AqueductName: "virgo",
	}

	// spawnAutomated is called because step type is automated.
	spawnErr := a.Spawn(context.Background(), req)

	// SetOutcome also fails (closed DB), so Spawn must return an error.
	if spawnErr == nil {
		t.Error("expected Spawn to return error when SetOutcome fails on closed DB")
	}

	// A WARN must have been logged for the AddNote failure.
	if !h.hasWarn() {
		t.Error("expected WARN log for AddNote failure, got none")
	}
}

// newTestAdapter creates an Adapter directly (bypassing NewAdapter which clones
// repos) so spawnAutomated can be tested without network access.
func newTestAdapter(t *testing.T, repoName string, client *cistern.Client) *Adapter {
	t.Helper()
	return &Adapter{
		runners:      map[string]*Runner{},
		executor:     gates.New(),
		queueClients: map[string]*cistern.Client{repoName: client},
	}
}

// TestSpawnAutomated_SetsOutcome verifies that spawnAutomated writes the step
// outcome to the DB so the Castellarius observe phase can route the item.
func TestSpawnAutomated_SetsOutcome(t *testing.T) {
	client := testQueueClient(t)
	a := newTestAdapter(t, "testrepo", client)

	item, err := client.Add("testrepo", "Automated test", "", 1, 1)
	if err != nil {
		t.Fatalf("Create droplet: %v", err)
	}

	req := castellarius.CataractaeRequest{
		Item: item,
		Step: aqueduct.WorkflowCataractae{
			Name: "noop",
			Type: aqueduct.CataractaeTypeAutomated,
		},
		RepoConfig:   aqueduct.RepoConfig{Name: "testrepo"},
		AqueductName: "alice",
		SandboxDir:   t.TempDir(),
	}

	if err := a.spawnAutomated(context.Background(), req); err != nil {
		t.Fatalf("spawnAutomated: %v", err)
	}

	updated, err := client.Get(item.ID)
	if err != nil {
		t.Fatalf("Get droplet: %v", err)
	}
	if updated.Outcome != "pass" {
		t.Errorf("outcome = %q, want %q", updated.Outcome, "pass")
	}
}

// TestSpawnAutomated_SandboxDirFallback verifies that when SandboxDir is empty,
// spawnAutomated falls back to the home-based path and builds the DropletContext
// with ID = AqueductName+"-"+ItemID. The noop gate emits a note containing that
// ID, which is stored to the DB — proving the fallback path was used.
func TestSpawnAutomated_SandboxDirFallback(t *testing.T) {
	client := testQueueClient(t)
	a := newTestAdapter(t, "testrepo", client)

	item, err := client.Add("testrepo", "Fallback test", "", 1, 1)
	if err != nil {
		t.Fatalf("Create droplet: %v", err)
	}

	req := castellarius.CataractaeRequest{
		Item: item,
		Step: aqueduct.WorkflowCataractae{
			Name: "noop",
			Type: aqueduct.CataractaeTypeAutomated,
		},
		RepoConfig:   aqueduct.RepoConfig{Name: "testrepo"},
		AqueductName: "alice",
		SandboxDir:   "", // empty — triggers home-based fallback
	}

	if err := a.spawnAutomated(context.Background(), req); err != nil {
		t.Fatalf("spawnAutomated with empty SandboxDir: %v", err)
	}

	updated, err := client.Get(item.ID)
	if err != nil {
		t.Fatalf("Get droplet: %v", err)
	}
	if updated.Outcome != "pass" {
		t.Errorf("outcome = %q, want %q", updated.Outcome, "pass")
	}

	// The noop gate emits "noop: item <bc.ID> passed through" where bc.ID is
	// AqueductName+"-"+item.ID. Verify this note was written to the DB, which
	// proves the fallback DropletContext was constructed with the correct ID.
	wantID := "alice-" + item.ID
	notes, err := client.GetNotes(item.ID)
	if err != nil {
		t.Fatalf("GetNotes: %v", err)
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n.Content, wantID) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no note containing %q; notes: %v", wantID, notes)
	}
}

// TestSpawnAutomated_WithMetaNotes verifies that spawnAutomated succeeds and
// writes the outcome even when notes include "meta:key=value" prefixed entries
// (metadata parsing is internal to spawnAutomated and not observable here).
func TestSpawnAutomated_WithMetaNotes(t *testing.T) {
	client := testQueueClient(t)
	a := newTestAdapter(t, "testrepo", client)

	item, err := client.Add("testrepo", "Meta test", "", 1, 1)
	if err != nil {
		t.Fatalf("Create droplet: %v", err)
	}

	req := castellarius.CataractaeRequest{
		Item: item,
		Step: aqueduct.WorkflowCataractae{
			Name: "noop",
			Type: aqueduct.CataractaeTypeAutomated,
		},
		RepoConfig:   aqueduct.RepoConfig{Name: "testrepo"},
		AqueductName: "alice",
		SandboxDir:   t.TempDir(),
		Notes: []cistern.CataractaeNote{
			{CataractaeName: "pr-create", Content: "meta:pr_url=https://github.com/example/pr/42"},
			{CataractaeName: "pr-create", Content: "meta:pr_number=42"},
			{CataractaeName: "implementer", Content: "Implemented the feature"},
		},
	}

	if err := a.spawnAutomated(context.Background(), req); err != nil {
		t.Fatalf("spawnAutomated: %v", err)
	}

	// After successful run, outcome must be set.
	updated, err := client.Get(item.ID)
	if err != nil {
		t.Fatalf("Get droplet: %v", err)
	}
	if updated.Outcome != "pass" {
		t.Errorf("outcome = %q, want %q", updated.Outcome, "pass")
	}
}

// TestSpawnAutomated_UnknownRepo verifies that spawnAutomated returns an error
// when the repo name has no corresponding queue client.
func TestSpawnAutomated_UnknownRepo(t *testing.T) {
	client := testQueueClient(t)
	a := newTestAdapter(t, "testrepo", client)

	item := &cistern.Droplet{ID: "auto-5", Title: "Unknown repo", Status: "open", Priority: 1}
	req := castellarius.CataractaeRequest{
		Item: item,
		Step: aqueduct.WorkflowCataractae{
			Name: "noop",
			Type: aqueduct.CataractaeTypeAutomated,
		},
		RepoConfig:   aqueduct.RepoConfig{Name: "no-such-repo"},
		AqueductName: "alice",
		SandboxDir:   t.TempDir(),
	}

	err := a.spawnAutomated(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for unknown repo")
	}
	if !strings.Contains(err.Error(), "no queue client") {
		t.Errorf("error = %q, want 'no queue client'", err.Error())
	}
}

// TestAdapterSpawn_DispatchesToSpawnAutomated verifies that Adapter.Spawn routes
// automated-type steps through spawnAutomated (not the agent tmux path).
func TestAdapterSpawn_DispatchesToSpawnAutomated(t *testing.T) {
	client := testQueueClient(t)
	a := newTestAdapter(t, "testrepo", client)

	item, err := client.Add("testrepo", "Dispatch test", "", 1, 1)
	if err != nil {
		t.Fatalf("Create droplet: %v", err)
	}

	req := castellarius.CataractaeRequest{
		Item: item,
		Step: aqueduct.WorkflowCataractae{
			Name: "noop",
			Type: aqueduct.CataractaeTypeAutomated, // automated → spawnAutomated path
		},
		RepoConfig:   aqueduct.RepoConfig{Name: "testrepo"},
		AqueductName: "alice",
		SandboxDir:   t.TempDir(),
	}

	if err := a.Spawn(context.Background(), req); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	updated, err := client.Get(item.ID)
	if err != nil {
		t.Fatalf("Get droplet: %v", err)
	}
	if updated.Outcome != "pass" {
		t.Errorf("outcome = %q, want %q", updated.Outcome, "pass")
	}
}

// TestAdapterSpawn_AgentNoRunner verifies that Spawn returns an error for an
// agent-type step when no runner is registered for the repo.
func TestAdapterSpawn_AgentNoRunner(t *testing.T) {
	client := testQueueClient(t)
	a := newTestAdapter(t, "testrepo", client)

	item := &cistern.Droplet{ID: "auto-7", Title: "Agent no runner", Status: "open", Priority: 1}
	req := castellarius.CataractaeRequest{
		Item: item,
		Step: aqueduct.WorkflowCataractae{
			Name: "implement",
			Type: aqueduct.CataractaeTypeAgent, // not automated — goes through runner path
		},
		RepoConfig:   aqueduct.RepoConfig{Name: "no-runner-repo"},
		AqueductName: "alice",
	}

	err := a.Spawn(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when no runner for repo")
	}
	if !strings.Contains(err.Error(), "no runner") {
		t.Errorf("error = %q, want 'no runner'", err.Error())
	}
}

// TestAdapterSpawn_AgentNoWorker verifies that Spawn returns an error for an
// agent-type step when the named worker does not exist in the runner.
func TestAdapterSpawn_AgentNoWorker(t *testing.T) {
	client := testQueueClient(t)

	cfg := Config{
		SkipInitialClone: true,
		Repo:             testRepoConfig(),
		Workflow:         testWorkflow(),
		CisternClient:    client,
		SandboxRoot:      t.TempDir(),
	}
	runner, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	a := &Adapter{
		runners:      map[string]*Runner{"testrepo": runner},
		executor:     gates.New(),
		queueClients: map[string]*cistern.Client{"testrepo": client},
	}

	item := &cistern.Droplet{ID: "auto-8", Title: "Agent no worker", Status: "open", Priority: 1}
	req := castellarius.CataractaeRequest{
		Item: item,
		Step: aqueduct.WorkflowCataractae{
			Name: "implement",
			Type: aqueduct.CataractaeTypeAgent,
		},
		RepoConfig:   aqueduct.RepoConfig{Name: "testrepo"},
		AqueductName: "nonexistent-worker", // no such worker
	}

	spawnErr := a.Spawn(context.Background(), req)
	if spawnErr == nil {
		t.Fatal("expected error for nonexistent worker")
	}
	if !strings.Contains(spawnErr.Error(), "worker") {
		t.Errorf("error = %q, want 'worker' in message", spawnErr.Error())
	}
}

// TestNewAdapter_NoWorkflow verifies that NewAdapter returns an error when a
// repo has no workflow configured.
func TestNewAdapter_NoWorkflow(t *testing.T) {
	client := testQueueClient(t)
	cfg := &aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{{Name: "myrepo", URL: "x", Cataractae: 1}},
	}
	workflows := map[string]*aqueduct.Workflow{} // missing "myrepo"
	clients := map[string]*cistern.Client{"myrepo": client}

	_, err := NewAdapter(cfg, workflows, clients)
	if err == nil {
		t.Fatal("expected error when workflow missing")
	}
	if !strings.Contains(err.Error(), "no workflow") {
		t.Errorf("error = %q, want 'no workflow'", err.Error())
	}
}

// TestBranchForDroplet_WithExternalRef_UsesKeyAsSuffix verifies that
// branchForDroplet returns "feat/<key>" when external_ref is "provider:key".
func TestBranchForDroplet_WithExternalRef_UsesKeyAsSuffix(t *testing.T) {
	cases := []struct {
		externalRef string
		wantBranch  string
	}{
		{"jira:DPF-456", "feat/DPF-456"},
		{"linear:LIN-789", "feat/LIN-789"},
		{"jira:PROJ-1", "feat/PROJ-1"},
	}
	for _, tc := range cases {
		d := &cistern.Droplet{ID: "ci-xxxxx", ExternalRef: tc.externalRef}
		got := branchForDroplet(d)
		if got != tc.wantBranch {
			t.Errorf("branchForDroplet(%q) = %q, want %q", tc.externalRef, got, tc.wantBranch)
		}
	}
}

// TestBranchForDroplet_WithoutExternalRef_UsesDropletID verifies that
// branchForDroplet returns "feat/<id>" when no external_ref is set.
func TestBranchForDroplet_WithoutExternalRef_UsesDropletID(t *testing.T) {
	d := &cistern.Droplet{ID: "ci-abcde", ExternalRef: ""}
	got := branchForDroplet(d)
	if got != "feat/ci-abcde" {
		t.Errorf("branchForDroplet(no external_ref) = %q, want %q", got, "feat/ci-abcde")
	}
}

// TestBranchForDroplet_WithMalformedExternalRef_FallsBackToID verifies that
// branchForDroplet falls back to "feat/<id>" when external_ref has no colon.
func TestBranchForDroplet_WithMalformedExternalRef_FallsBackToID(t *testing.T) {
	d := &cistern.Droplet{ID: "ci-abcde", ExternalRef: "nocolon"}
	got := branchForDroplet(d)
	if got != "feat/ci-abcde" {
		t.Errorf("branchForDroplet('nocolon') = %q, want %q", got, "feat/ci-abcde")
	}
}

// TestNewAdapter_NoQueueClient verifies that NewAdapter returns an error when a
// repo has no queue client configured.
func TestNewAdapter_NoQueueClient(t *testing.T) {
	wf := &aqueduct.Workflow{Name: "feature", Cataractae: []aqueduct.WorkflowCataractae{
		{Name: "implement", Type: aqueduct.CataractaeTypeAgent},
	}}
	cfg := &aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{{Name: "myrepo", URL: "x", Cataractae: 1}},
	}
	workflows := map[string]*aqueduct.Workflow{"myrepo": wf}
	clients := map[string]*cistern.Client{} // missing "myrepo"

	_, err := NewAdapter(cfg, workflows, clients)
	if err == nil {
		t.Fatal("expected error when queue client missing")
	}
	if !strings.Contains(err.Error(), "no queue client") {
		t.Errorf("error = %q, want 'no queue client'", err.Error())
	}
}
