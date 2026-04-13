package castellarius

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// newTestLogger creates a slog.Logger backed by buf for test inspection.
func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// --- mocks ---

type filedDroplet struct {
	Repo, Title, Description string
	Priority, Complexity     int
}

type mockClient struct {
	mu                  sync.Mutex
	readyItems          []*cistern.Droplet
	readyCalls          int
	steps               map[string]string           // id → current step (for assertions)
	items               map[string]*cistern.Droplet // id → item (for List/SetOutcome)
	notes               map[string][]cistern.CataractaeNote
	issues              map[string][]cistern.DropletIssue // id → issues
	pooled              map[string]string
	attached            []attachedNote
	closed              map[string]bool
	lastReviewedCommits map[string]string
	addNoteErr          error             // if set, AddNote returns this error
	getNotesErr         error             // if set, GetNotes returns this error
	getReadyErr         error             // if set, GetReady returns this error once then clears
	listErr             error             // if set, List returns this error
	listIssuesErr       error             // if set, ListIssues returns this error
	poolErr             error             // if set, Pool returns this error
	assignErr           error             // if set, Assign returns this error
	cancelled           map[string]string // id → cancel reason
	filed               []filedDroplet    // FileDroplet calls
	assignCalls         int               // total Assign call count
}

type attachedNote struct {
	id, fromStep, notes string
}

func newMockClient() *mockClient {
	return &mockClient{
		steps:               make(map[string]string),
		items:               make(map[string]*cistern.Droplet),
		notes:               make(map[string][]cistern.CataractaeNote),
		issues:              make(map[string][]cistern.DropletIssue),
		pooled:              make(map[string]string),
		closed:              make(map[string]bool),
		lastReviewedCommits: make(map[string]string),
		cancelled:           make(map[string]string),
	}
}

func (m *mockClient) GetReady(repo string) (*cistern.Droplet, error) {
	m.mu.Lock()
	if m.getReadyErr != nil {
		err := m.getReadyErr
		m.getReadyErr = nil
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	return m.GetReadyForAqueduct(repo, "")
}

func (m *mockClient) GetReadyForAqueduct(repo, aqueductName string) (*cistern.Droplet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readyCalls++
	for i, b := range m.readyItems {
		// Sticky: skip droplets assigned to a different aqueduct.
		if aqueductName != "" && b.AssignedAqueduct != "" && b.AssignedAqueduct != aqueductName {
			continue
		}
		m.readyItems = append(m.readyItems[:i], m.readyItems[i+1:]...)
		b.Status = "in_progress"
		cp := *b
		m.items[b.ID] = &cp
		return b, nil
	}
	return nil, nil
}

func (m *mockClient) SetAssignedAqueduct(id, aqueductName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item, ok := m.items[id]; ok && item.AssignedAqueduct == "" {
		item.AssignedAqueduct = aqueductName
	}
	return nil
}

func (m *mockClient) Assign(id, worker, step string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assignCalls++
	if m.assignErr != nil {
		return m.assignErr
	}
	m.steps[id] = step
	if item, ok := m.items[id]; ok {
		item.CurrentCataractae = step
		item.Assignee = worker
		item.Outcome = "" // clear outcome on advance
		if worker == "" {
			item.Status = "open"
			item.AssignedAqueduct = ""
		} else {
			item.Status = "in_progress"
			item.StageDispatchedAt = time.Now()
		}
	}
	return nil
}

func (m *mockClient) Get(id string) (*cistern.Droplet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item, ok := m.items[id]; ok {
		return item, nil
	}
	return nil, fmt.Errorf("not found: %s", id)
}

func (m *mockClient) SetOutcome(id, outcome string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item, ok := m.items[id]; ok {
		item.Outcome = outcome
	}
	return nil
}

func (m *mockClient) AddNote(id, fromStep, notes string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attached = append(m.attached, attachedNote{id, fromStep, notes})
	if m.addNoteErr == nil {
		// Mirror production: successful writes appear in GetNotes on the next tick.
		m.notes[id] = append(m.notes[id], cistern.CataractaeNote{
			DropletID:      id,
			CataractaeName: fromStep,
			Content:        notes,
			CreatedAt:      time.Now(),
		})
	}
	return m.addNoteErr
}

func (m *mockClient) GetNotes(id string) ([]cistern.CataractaeNote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getNotesErr != nil {
		return nil, m.getNotesErr
	}
	return m.notes[id], nil
}

func (m *mockClient) Pool(id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poolErr != nil {
		return m.poolErr
	}
	m.pooled[id] = reason
	if item, ok := m.items[id]; ok {
		item.Status = "pooled"
		item.Assignee = ""
		item.AssignedAqueduct = ""
	}
	return nil
}

func (m *mockClient) CloseItem(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed[id] = true
	m.steps[id] = "done"
	return nil
}

func (m *mockClient) Cancel(id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelled[id] = reason
	if item, ok := m.items[id]; ok {
		item.Status = "cancelled"
	}
	return nil
}

func (m *mockClient) FileDroplet(repo, title, description string, priority, complexity int) (*cistern.Droplet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := &cistern.Droplet{
		ID:    fmt.Sprintf("mock-filed-%d", len(m.filed)),
		Repo:  repo,
		Title: title,
	}
	m.filed = append(m.filed, filedDroplet{repo, title, description, priority, complexity})
	return d, nil
}

func (m *mockClient) Heartbeat(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[id]
	if !ok {
		return fmt.Errorf("droplet not found: %s", id)
	}
	item.LastHeartbeatAt = time.Now()
	return nil
}

func (m *mockClient) List(repo, status string) ([]*cistern.Droplet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	var result []*cistern.Droplet
	for _, item := range m.items {
		if status == "" || item.Status == status {
			cp := *item
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (m *mockClient) Purge(olderThan time.Duration, dryRun bool) (int, error) {
	return 0, nil
}

func (m *mockClient) SetCataractae(id, cataractae string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps[id] = cataractae
	if item, ok := m.items[id]; ok {
		item.CurrentCataractae = cataractae
	}
	return nil
}

func (m *mockClient) GetLastReviewedCommit(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastReviewedCommits[id], nil
}

func (m *mockClient) ListIssues(dropletID string, openOnly bool, flaggedBy string) ([]cistern.DropletIssue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listIssuesErr != nil {
		return nil, m.listIssuesErr
	}
	var result []cistern.DropletIssue
	for _, iss := range m.issues[dropletID] {
		if openOnly && iss.Status != "open" {
			continue
		}
		if flaggedBy != "" && iss.FlaggedBy != flaggedBy {
			continue
		}
		result = append(result, iss)
	}
	return result, nil
}

// mockRunner records Spawn calls and writes outcomes to the mockClient.
// Set client to enable routing assertions; nil disables outcome writing.
type mockRunner struct {
	mu       sync.Mutex
	outcomes map[string]string // step name → outcome string ("pass", "recirculate", etc.)
	calls    []CataractaeRequest
	err      error
	done     chan struct{} // receives after each Spawn call
	client   *mockClient
}

func newMockRunner(client *mockClient) *mockRunner {
	return &mockRunner{
		outcomes: make(map[string]string),
		done:     make(chan struct{}, 16),
		client:   client,
	}
}

func (r *mockRunner) Spawn(_ context.Context, req CataractaeRequest) error {
	r.mu.Lock()
	defer func() {
		r.mu.Unlock()
		r.done <- struct{}{}
	}()
	r.calls = append(r.calls, req)
	if r.err != nil {
		return r.err
	}
	outcome := "pass"
	if o, ok := r.outcomes[req.Step.Name]; ok {
		outcome = o
	}
	if r.client != nil {
		r.client.SetOutcome(req.Item.ID, outcome)
	}
	return nil
}

func (r *mockRunner) waitCalls(n int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for range n {
		select {
		case <-r.done:
		case <-deadline:
			return false
		}
	}
	return true
}

// blockingRunner blocks in Spawn until ch is closed (simulates long-running agent).
// It does not write outcomes, so workers stay busy indefinitely.
type blockingRunner struct {
	ch   chan struct{}
	done chan struct{}
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{
		ch:   make(chan struct{}),
		done: make(chan struct{}, 16),
	}
}

func (r *blockingRunner) Spawn(ctx context.Context, _ CataractaeRequest) error {
	r.done <- struct{}{}
	select {
	case <-r.ch:
		return nil
	case <-ctx.Done():
		return nil
	}
}

// --- helpers ---

func testWorkflow() *aqueduct.Workflow {
	return &aqueduct.Workflow{
		Name: "test",
		Cataractae: []aqueduct.WorkflowCataractae{
			{
				Name:   "implement",
				Type:   aqueduct.CataractaeTypeAgent,
				OnPass: "review",
				OnFail: "pooled",
			},
			{
				Name:          "review",
				Type:          aqueduct.CataractaeTypeAgent,
				OnPass:        "done",
				OnFail:        "implement",
				OnRecirculate: "implement",
			},
		},
	}
}

func testConfig() aqueduct.AqueductConfig {
	return aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{
				Name:       "test-repo",
				Cataractae: 2,
				Names:      []string{"alpha", "beta"},
				Prefix:     "test",
			},
		},
		MaxCataractae: 4,
	}
}

func testScheduler(client CisternClient, runner CataractaeRunner) *Castellarius {
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	return NewFromParts(config, workflows, clients, runner)
}

// --- tests ---

func TestRoute(t *testing.T) {
	step := aqueduct.WorkflowCataractae{
		OnPass:        "review",
		OnFail:        "pooled",
		OnRecirculate: "implement",
		OnPool:        "human",
	}

	tests := []struct {
		result Result
		want   string
	}{
		{ResultPass, "review"},
		{ResultFail, "pooled"},
		{ResultRecirculate, "implement"},
		{ResultPool, "human"},
		{Result("unknown"), "pooled"},
	}

	for _, tt := range tests {
		got := route(step, tt.result)
		if got != tt.want {
			t.Errorf("route(%q) = %q, want %q", tt.result, got, tt.want)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	for _, name := range []string{"done", "pooled", "human", "pool", "Done", "POOLED"} {
		if !isTerminal(name) {
			t.Errorf("isTerminal(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"implement", "review", "qa", ""} {
		if isTerminal(name) {
			t.Errorf("isTerminal(%q) = true, want false", name)
		}
	}
}

func TestCurrentStep_FirstStep(t *testing.T) {
	wf := testWorkflow()
	item := &cistern.Droplet{ID: "b1"}

	step := currentCataracta(item, wf)
	if step == nil || step.Name != "implement" {
		t.Fatalf("expected first step 'implement', got %v", step)
	}
}

func TestCurrentStep_FromCurrentStep(t *testing.T) {
	wf := testWorkflow()
	item := &cistern.Droplet{
		ID:                "b1",
		CurrentCataractae: "review",
	}

	step := currentCataracta(item, wf)
	if step == nil || step.Name != "review" {
		t.Fatalf("expected step 'review' from current_step, got %v", step)
	}
}

func TestCurrentStep_UnknownStep(t *testing.T) {
	wf := testWorkflow()
	item := &cistern.Droplet{
		ID:                "b1",
		CurrentCataractae: "nonexistent",
	}

	step := currentCataracta(item, wf)
	if step != nil {
		t.Fatalf("expected nil for unknown step, got %v", step)
	}
}

func TestTick_AssignsWork(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "b1", Title: "test item"}}

	runner := newMockRunner(client)

	sched := testScheduler(client, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for runner call")
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.calls))
	}
	if runner.calls[0].Step.Name != "implement" {
		t.Errorf("expected step 'implement', got %q", runner.calls[0].Step.Name)
	}
	if runner.calls[0].AqueductName != "alpha" {
		t.Errorf("expected worker 'alpha', got %q", runner.calls[0].AqueductName)
	}
}

func TestTick_RoutesToNextStep(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "b1", Title: "test"}}

	runner := newMockRunner(client)
	// default outcome is "pass"

	sched := testScheduler(client, runner)
	// Dispatch tick.
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for spawn")
	}
	// Observe tick: routes based on outcome written to DB.
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.steps["b1"] != "review" {
		t.Errorf("expected step 'review', got %q", client.steps["b1"])
	}
}

func TestTick_TerminalDone(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "b1", CurrentCataractae: "review"},
	}

	runner := newMockRunner(client)
	// default outcome is "pass"; review.OnPass = "done"

	sched := testScheduler(client, runner)
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	if !client.closed["b1"] {
		t.Error("expected item to be closed for terminal 'done'")
	}
}

func TestTick_TerminalPooled(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "b1", CurrentCataractae: "implement"},
	}

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "fail" // fail → ResultFail → OnFail = "pooled"

	sched := testScheduler(client, runner)
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	if _, ok := client.pooled["b1"]; !ok {
		t.Error("expected item pooled for terminal 'pooled'")
	}
}

func TestTick_PerRepoCap(t *testing.T) {
	// Cap is per-repo (pool size), not global. With 3 pool slots and 10 droplets,
	// exactly 3 should flow — no more, no less.
	client := newMockClient()
	for i := range 10 {
		client.readyItems = append(client.readyItems, &cistern.Droplet{
			ID: fmt.Sprintf("b%d", i),
		})
	}

	runner := newBlockingRunner()

	config := aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "r1", Cataractae: 3, Names: []string{"w1", "w2", "w3"}, Prefix: "r1"},
		},
	}
	wf := testWorkflow()
	clients := map[string]CisternClient{"r1": client}
	workflows := map[string]*aqueduct.Workflow{"r1": wf}
	sched := NewFromParts(config, workflows, clients, runner)

	// Tick multiple times.
	for range 5 {
		sched.Tick(context.Background())
	}

	total := sched.totalBusy()
	if total > 3 {
		t.Errorf("per-repo cap violated: %d busy workers (pool size=3)", total)
	}

	close(runner.ch)
}

func TestTick_CrashRequeue(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "b1"}}

	runner := newMockRunner(client)
	runner.err = fmt.Errorf("agent crashed")

	sched := testScheduler(client, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out")
	}
	time.Sleep(50 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	// Spawn failed — droplet is reset to open at step "implement", not left in_progress.
	if client.steps["b1"] != "implement" {
		t.Errorf("expected droplet reset to step 'implement' after spawn failure, got %q", client.steps["b1"])
	}
	if item, ok := client.items["b1"]; ok && item.Status != "open" {
		t.Errorf("expected droplet status 'open' after spawn failure, got %q", item.Status)
	}
}

func TestTick_NotesForwarding(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "b1"}}
	client.notes["b1"] = []cistern.CataractaeNote{
		{ID: 1, DropletID: "b1", CataractaeName: "refine", Content: "specs clarified"},
	}

	runner := newMockRunner(client)
	// default outcome "pass"

	sched := testScheduler(client, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out")
	}

	runner.mu.Lock()
	req := runner.calls[0]
	runner.mu.Unlock()

	if len(req.Notes) != 1 || req.Notes[0].CataractaeName != "refine" {
		t.Errorf("expected prior notes forwarded, got %v", req.Notes)
	}
}

func TestTick_RecirculateAutoPromotesToPass(t *testing.T) {
	// implement has OnPass="review" but no OnRecirculate.
	// Signaling recirculate should auto-promote to pass and route to "review".
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "b1"}}

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate"

	sched := testScheduler(client, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out")
	}
	// Observe tick.
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	// Should route via on_pass → "review", not pool.
	if client.steps["b1"] != "review" {
		t.Errorf("expected auto-promote to route to review, got %q", client.steps["b1"])
	}
	if _, ok := client.pooled["b1"]; ok {
		t.Error("expected no pooling when recirculate auto-promotes via on_pass")
	}
	// Warning note must be attached.
	var hasNote bool
	for _, n := range client.attached {
		if n.id == "b1" && strings.Contains(n.notes, "Auto-promoted") && strings.Contains(n.notes, "recirculate") {
			hasNote = true
			break
		}
	}
	if !hasNote {
		t.Error("expected auto-promote warning note attached to droplet")
	}
}

func TestTick_RecirculateNoPassRoute_StillPools(t *testing.T) {
	// A step with neither on_recirculate nor on_pass: recirculate cannot be promoted,
	// so the droplet must still pool.
	wf := &aqueduct.Workflow{
		Name: "test",
		Cataractae: []aqueduct.WorkflowCataractae{
			{
				Name:   "implement",
				Type:   aqueduct.CataractaeTypeAgent,
				OnFail: "pooled",
				// OnPass and OnRecirculate intentionally omitted.
			},
		},
	}
	cfg := testConfig()
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "b2"}}
	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate"
	sched := NewFromParts(cfg, map[string]*aqueduct.Workflow{"test-repo": wf}, map[string]CisternClient{"test-repo": client}, runner)

	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	if _, ok := client.pooled["b2"]; !ok {
		t.Error("expected pooling when neither on_recirculate nor on_pass exists")
	}
}

func TestTick_RecirculateNoRoute_BlocksWithDiagnosticNote(t *testing.T) {
	// Given: a droplet at "implement" which has no on_recirculate route and no on_pass route
	// (so it can't auto-promote either).
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "b1", CurrentCataractae: "implement"},
	}
	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate"

	// Use a custom workflow where implement has no on_pass or on_recirculate routes.
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{
		"test-repo": {
			Name: "test",
			Cataractae: []aqueduct.WorkflowCataractae{
				{
					Name:   "implement",
					Type:   aqueduct.CataractaeTypeAgent,
					OnFail: "pooled",
					// Intentionally no OnPass and no OnRecirculate
				},
				{
					Name:          "review",
					Type:          aqueduct.CataractaeTypeAgent,
					OnPass:        "done",
					OnFail:        "implement",
					OnRecirculate: "implement",
				},
			},
		},
	}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)

	// When: the cataractae signals recirculate.
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for spawn")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	// Then: droplet is pooled.
	client.mu.Lock()
	defer client.mu.Unlock()
	if _, ok := client.pooled["b1"]; !ok {
		t.Fatal("expected droplet to be pooled when no on_recirculate route exists")
	}

	// And: a diagnostic note naming the step and missing route is attached.
	found := false
	for _, n := range client.attached {
		if n.id == "b1" && strings.Contains(n.notes, "implement") && strings.Contains(n.notes, "on_recirculate") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic note about missing on_recirculate route, got notes: %v", client.attached)
	}
}

// --- Loop recovery tests ---

// loopWorkflow returns a workflow where implement recirculates to itself and
// review can send work back to implement on failure/recirculate.
func loopWorkflow() *aqueduct.Workflow {
	return &aqueduct.Workflow{
		Name: "test",
		Cataractae: []aqueduct.WorkflowCataractae{
			{
				Name:          "implement",
				Type:          aqueduct.CataractaeTypeAgent,
				OnPass:        "review",
				OnFail:        "pooled",
				OnRecirculate: "implement",
			},
			{
				Name:          "review",
				Type:          aqueduct.CataractaeTypeAgent,
				OnPass:        "done",
				OnFail:        "implement",
				OnRecirculate: "implement",
			},
		},
	}
}

func loopTestScheduler(client CisternClient, runner CataractaeRunner) *Castellarius {
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": loopWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	return NewFromParts(config, workflows, clients, runner)
}

func TestTick_ImplementRecirculate_NoReviewerIssues_RoutesNormally(t *testing.T) {
	// Given: implement recirculates with no open reviewer issues.
	// When: the scheduler observes the recirculate outcome.
	// Then: the droplet routes normally back to implement (no loop recovery).
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "d1", CurrentCataractae: "implement"}}
	// No issues registered.

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate"

	sched := loopTestScheduler(client, runner)
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for spawn")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	if client.steps["d1"] != "implement" {
		t.Errorf("expected droplet to remain at implement, got %q", client.steps["d1"])
	}
	if _, ok := client.pooled["d1"]; ok {
		t.Error("expected no pooling")
	}
	// No loop-recovery notes should be added.
	for _, n := range client.attached {
		if n.id == "d1" && strings.Contains(n.notes, "loop-recovery") {
			t.Errorf("unexpected loop-recovery note: %q", n.notes)
		}
	}
}

func TestTick_ImplementRecirculate_ReviewerIssueFirstCycle_AddsPendingNote(t *testing.T) {
	// Given: implement recirculates with one open reviewer issue (first occurrence).
	// When: the scheduler observes the recirculate outcome.
	// Then: droplet still routes back to implement but a pending note is added.
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "d2", CurrentCataractae: "implement"}}
	client.issues["d2"] = []cistern.DropletIssue{
		{ID: "iss-001", DropletID: "d2", FlaggedBy: "review", Status: "open", Description: "fix the bug"},
	}

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate"

	sched := loopTestScheduler(client, runner)
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for spawn")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	// Should still route to implement on the first cycle.
	if client.steps["d2"] != "implement" {
		t.Errorf("expected implement on first cycle, got %q", client.steps["d2"])
	}

	// A loop-recovery-pending note mentioning the issue ID must be attached.
	var hasPending bool
	for _, n := range client.attached {
		if n.id == "d2" && strings.Contains(n.notes, "loop-recovery-pending") && strings.Contains(n.notes, "iss-001") {
			hasPending = true
			break
		}
	}
	if !hasPending {
		t.Errorf("expected [scheduler:loop-recovery-pending] note with issue ID, got: %v", client.attached)
	}
}

func TestTick_ImplementRecirculate_ReviewerIssueSecondCycle_RoutesToReviewer(t *testing.T) {
	// Given: implement recirculates with one open reviewer issue AND a prior
	// loop-recovery-pending note already exists (simulating the second cycle).
	// When: the scheduler observes the recirculate outcome.
	// Then: the droplet is routed directly to the reviewer cataractae.
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "d3", CurrentCataractae: "implement"}}
	client.issues["d3"] = []cistern.DropletIssue{
		{ID: "iss-002", DropletID: "d3", FlaggedBy: "review", Status: "open", Description: "still broken"},
	}
	// Pre-populate one pending note to simulate the first cycle having already fired.
	client.notes["d3"] = []cistern.CataractaeNote{
		{
			DropletID:      "d3",
			CataractaeName: "scheduler",
			Content:        "[scheduler:loop-recovery-pending] issue=iss-002 — open reviewer issue found at implement, routing back to implement (cycle 1/2)",
			CreatedAt:      time.Now().Add(-time.Minute),
		},
	}

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate"

	sched := loopTestScheduler(client, runner)
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for spawn")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	if client.steps["d3"] != "review" {
		t.Errorf("expected droplet routed to review on second cycle, got %q", client.steps["d3"])
	}
	if _, ok := client.pooled["d3"]; ok {
		t.Error("expected no pooling on loop recovery")
	}
}

func TestTick_ImplementRecirculate_LoopRecovery_WritesStructuredNote(t *testing.T) {
	// Given: second-cycle loop condition is met.
	// When: the scheduler recovers the loop.
	// Then: a structured [scheduler:loop-recovery] note with the issue ID is written.
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "d4", CurrentCataractae: "implement"}}
	client.issues["d4"] = []cistern.DropletIssue{
		{ID: "iss-003", DropletID: "d4", FlaggedBy: "review", Status: "open", Description: "needs fix"},
	}
	client.notes["d4"] = []cistern.CataractaeNote{
		{
			DropletID:      "d4",
			CataractaeName: "scheduler",
			Content:        "[scheduler:loop-recovery-pending] issue=iss-003 — open reviewer issue found at implement, routing back to implement (cycle 1/2)",
			CreatedAt:      time.Now().Add(-time.Minute),
		},
	}

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate"

	sched := loopTestScheduler(client, runner)
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for spawn")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	// The structured recovery note must be present.
	var hasRecoveryNote bool
	for _, n := range client.attached {
		if n.id == "d4" &&
			strings.Contains(n.notes, "[scheduler:loop-recovery]") &&
			strings.Contains(n.notes, "iss-003") &&
			strings.Contains(n.notes, "routing to reviewer") {
			hasRecoveryNote = true
			break
		}
	}
	if !hasRecoveryNote {
		t.Errorf("expected [scheduler:loop-recovery] note with issue ID, got: %v", client.attached)
	}
}

func TestTick_ImplementRecirculate_GetNotesError_RoutesNormally(t *testing.T) {
	// Given: implement recirculates with an open reviewer issue but GetNotes fails.
	// When: the scheduler observes the recirculate outcome.
	// Then: loop recovery does not fire; droplet routes normally back to implement.
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "d6", CurrentCataractae: "implement"}}
	client.issues["d6"] = []cistern.DropletIssue{
		{ID: "iss-005", DropletID: "d6", FlaggedBy: "review", Status: "open", Description: "fix needed"},
	}
	client.getNotesErr = errors.New("storage unavailable")

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate"

	sched := loopTestScheduler(client, runner)
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for spawn")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	// Should still route to implement — no recovery without notes.
	if client.steps["d6"] != "implement" {
		t.Errorf("expected implement on GetNotes error, got %q", client.steps["d6"])
	}
	// No loop-recovery notes should have been added.
	for _, n := range client.attached {
		if n.id == "d6" && strings.Contains(n.notes, "loop-recovery") {
			t.Errorf("unexpected loop-recovery note on GetNotes error: %q", n.notes)
		}
	}
}

func TestTick_ImplementRecirculate_ListIssuesError_RoutesNormally(t *testing.T) {
	// Given: implement recirculates but ListIssues fails.
	// When: the scheduler observes the recirculate outcome.
	// Then: loop recovery does not fire; droplet routes normally back to implement.
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "d7", CurrentCataractae: "implement"}}
	client.listIssuesErr = errors.New("storage unavailable")

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate"

	sched := loopTestScheduler(client, runner)
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for spawn")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	// Should still route to implement — no recovery without issue list.
	if client.steps["d7"] != "implement" {
		t.Errorf("expected implement on ListIssues error, got %q", client.steps["d7"])
	}
	// No loop-recovery notes should have been added.
	for _, n := range client.attached {
		if n.id == "d7" && strings.Contains(n.notes, "loop-recovery") {
			t.Errorf("unexpected loop-recovery note on ListIssues error: %q", n.notes)
		}
	}
}

func TestTick_ImplementRecirculate_ClosedIssue_DoesNotTriggerRecovery(t *testing.T) {
	// Given: implement recirculates but the reviewer issue is already resolved.
	// When: the scheduler observes the recirculate outcome.
	// Then: no loop recovery — routes normally back to implement.
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "d5", CurrentCataractae: "implement"}}
	client.issues["d5"] = []cistern.DropletIssue{
		{ID: "iss-004", DropletID: "d5", FlaggedBy: "review", Status: "resolved", Description: "was broken"},
	}
	// Even with a pending note from before, the resolved issue should not trigger recovery.
	client.notes["d5"] = []cistern.CataractaeNote{
		{
			DropletID:      "d5",
			CataractaeName: "scheduler",
			Content:        "[scheduler:loop-recovery-pending] issue=iss-004 — open reviewer issue found at implement, routing back to implement (cycle 1/2)",
			CreatedAt:      time.Now().Add(-time.Minute),
		},
	}

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate"

	sched := loopTestScheduler(client, runner)
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for spawn")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	if client.steps["d5"] != "implement" {
		t.Errorf("expected implement (no recovery for closed issue), got %q", client.steps["d5"])
	}
}

func TestTick_NoWorkAvailable(t *testing.T) {
	client := newMockClient()
	runner := newMockRunner(client)

	sched := testScheduler(client, runner)
	sched.Tick(context.Background())
	time.Sleep(50 * time.Millisecond)

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.calls) != 0 {
		t.Error("expected no runner calls when no work available")
	}
}

// --- Multi-repo tests matching spec: ScaledTest (cascade/tributary) + cistern (confluence) ---

func multiRepoConfig() aqueduct.AqueductConfig {
	return aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "ScaledTest", Cataractae: 2, Names: []string{"cascade", "tributary"}, Prefix: "st"},
			{Name: "cistern", Cataractae: 1, Names: []string{"confluence"}, Prefix: "ct"},
		},
		MaxCataractae: 3,
	}
}

func multiRepoScheduler(clients map[string]CisternClient, runner CataractaeRunner) *Castellarius {
	config := multiRepoConfig()
	wf := testWorkflow()
	workflows := map[string]*aqueduct.Workflow{
		"ScaledTest": wf,
		"cistern":    wf,
	}
	return NewFromParts(config, workflows, clients, runner)
}

func TestMultiRepo_ItemsGoToCorrectWorkers(t *testing.T) {
	stClient := newMockClient()
	stClient.readyItems = []*cistern.Droplet{
		{ID: "st-1", Title: "scaled test item 1"},
		{ID: "st-2", Title: "scaled test item 2"},
	}
	bfClient := newMockClient()
	bfClient.readyItems = []*cistern.Droplet{
		{ID: "bf-1", Title: "cistern item 1"},
	}

	runner := newMockRunner(nil) // no routing assertions needed
	clients := map[string]CisternClient{
		"ScaledTest": stClient,
		"cistern":    bfClient,
	}
	sched := multiRepoScheduler(clients, runner)

	// First tick: should pick up items from both repos.
	sched.Tick(context.Background())

	// ScaledTest has 2 workers and 2 items; cistern has 1 worker and 1 item.
	// All 3 should be assigned (total 3 = MaxCataractae).
	if !runner.waitCalls(3, 2*time.Second) {
		t.Fatal("timed out waiting for 3 runner calls")
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()

	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 runner calls, got %d", len(runner.calls))
	}

	// Verify ScaledTest items went to cascade/tributary.
	stWorkers := map[string]bool{}
	for _, call := range runner.calls {
		if call.RepoConfig.Name == "ScaledTest" {
			stWorkers[call.AqueductName] = true
			if call.AqueductName != "cascade" && call.AqueductName != "tributary" {
				t.Errorf("ScaledTest item %s assigned to wrong worker: %s", call.Item.ID, call.AqueductName)
			}
		}
		if call.RepoConfig.Name == "cistern" {
			if call.AqueductName != "confluence" {
				t.Errorf("cistern item %s assigned to wrong worker: %s (expected confluence)", call.Item.ID, call.AqueductName)
			}
		}
	}

	if len(stWorkers) != 2 {
		t.Errorf("expected 2 distinct ScaledTest workers, got %d: %v", len(stWorkers), stWorkers)
	}
}

func TestMultiRepo_EachRepoUsesItsOwnCap(t *testing.T) {
	// Each repo is capped by its own pool size independently — no global cap.
	stClient := newMockClient()
	for i := range 5 {
		stClient.readyItems = append(stClient.readyItems, &cistern.Droplet{ID: fmt.Sprintf("st-%d", i)})
	}
	bfClient := newMockClient()
	for i := range 5 {
		bfClient.readyItems = append(bfClient.readyItems, &cistern.Droplet{ID: fmt.Sprintf("bf-%d", i)})
	}

	blocker := newBlockingRunner()
	clients := map[string]CisternClient{
		"ScaledTest": stClient,
		"cistern":    bfClient,
	}

	config := multiRepoConfig() // ScaledTest: 2 slots, cistern: 1 slot → 3 total
	wf := testWorkflow()
	workflows := map[string]*aqueduct.Workflow{
		"ScaledTest": wf,
		"cistern":    wf,
	}
	sched := NewFromParts(config, workflows, clients, blocker)

	for range 5 {
		sched.Tick(context.Background())
	}

	total := sched.totalBusy()
	if total > 3 {
		t.Errorf("per-repo cap violated: %d busy workers (total pool size=3)", total)
	}

	close(blocker.ch)
}

func TestMultiRepo_WorkersNeverCrossRepoBoundaries(t *testing.T) {
	stClient := newMockClient()
	stClient.readyItems = []*cistern.Droplet{{ID: "st-1"}}
	bfClient := newMockClient()
	bfClient.readyItems = []*cistern.Droplet{{ID: "bf-1"}}

	runner := newMockRunner(nil)
	clients := map[string]CisternClient{
		"ScaledTest": stClient,
		"cistern":    bfClient,
	}
	sched := multiRepoScheduler(clients, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(2, time.Second) {
		t.Fatal("timed out waiting for runner calls")
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()

	for _, call := range runner.calls {
		switch call.RepoConfig.Name {
		case "ScaledTest":
			if call.AqueductName != "cascade" && call.AqueductName != "tributary" {
				t.Errorf("ScaledTest item used non-ScaledTest worker: %s", call.AqueductName)
			}
		case "cistern":
			if call.AqueductName != "confluence" {
				t.Errorf("cistern item used non-cistern worker: %s", call.AqueductName)
			}
		default:
			t.Errorf("unexpected repo: %s", call.RepoConfig.Name)
		}
	}
}

func TestMultiRepo_RoundRobinPolling(t *testing.T) {
	stClient := newMockClient()
	bfClient := newMockClient()

	runner := newMockRunner(nil)
	clients := map[string]CisternClient{
		"ScaledTest": stClient,
		"cistern":    bfClient,
	}
	sched := multiRepoScheduler(clients, runner)

	// Tick with no work — GetReady is called once per repo per tick (not once per worker).
	// When GetReady returns nil, dispatch stops immediately for that repo.
	sched.Tick(context.Background())

	stClient.mu.Lock()
	stCalls := stClient.readyCalls
	stClient.mu.Unlock()

	bfClient.mu.Lock()
	bfCalls := bfClient.readyCalls
	bfClient.mu.Unlock()

	if stCalls != 1 {
		t.Errorf("expected ScaledTest polled once (no work), got %d", stCalls)
	}
	if bfCalls != 1 {
		t.Errorf("expected cistern polled once (no work), got %d", bfCalls)
	}
}

func TestMultiRepo_OneRepoEmptyOtherHasWork(t *testing.T) {
	stClient := newMockClient() // No items
	bfClient := newMockClient()
	bfClient.readyItems = []*cistern.Droplet{{ID: "bf-1"}}

	runner := newMockRunner(nil)
	clients := map[string]CisternClient{
		"ScaledTest": stClient,
		"cistern":    bfClient,
	}
	sched := multiRepoScheduler(clients, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out")
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	if runner.calls[0].Item.ID != "bf-1" {
		t.Errorf("expected bf-1, got %s", runner.calls[0].Item.ID)
	}
	if runner.calls[0].AqueductName != "confluence" {
		t.Errorf("expected confluence, got %s", runner.calls[0].AqueductName)
	}
}

func TestMultiRepo_RepoWorkersExhausted(t *testing.T) {
	// ScaledTest has 2 workers. Give it 3 items. Only 2 should be assigned.
	stClient := newMockClient()
	stClient.readyItems = []*cistern.Droplet{
		{ID: "st-1"}, {ID: "st-2"}, {ID: "st-3"},
	}
	bfClient := newMockClient()

	blocker := newBlockingRunner()
	clients := map[string]CisternClient{
		"ScaledTest": stClient,
		"cistern":    bfClient,
	}
	sched := multiRepoScheduler(clients, blocker)

	// Multiple ticks. ScaledTest pool has 2 workers, so max 2 items assigned.
	for range 3 {
		sched.Tick(context.Background())
	}

	pool := sched.pools["ScaledTest"]
	if pool.FlowingCount() > 2 {
		t.Errorf("ScaledTest pool exceeded capacity: %d busy (max 2)", pool.FlowingCount())
	}

	close(blocker.ch)
}

func TestTick_PerRepoIsolation(t *testing.T) {
	client1 := newMockClient()
	client1.readyItems = []*cistern.Droplet{{ID: "r1-b1"}}
	client2 := newMockClient()
	client2.readyItems = []*cistern.Droplet{{ID: "r2-b1"}}

	runner := newMockRunner(nil)

	config := aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "repo1", Cataractae: 1, Names: []string{"w1"}, Prefix: "r1"},
			{Name: "repo2", Cataractae: 1, Names: []string{"w2"}, Prefix: "r2"},
		},
		MaxCataractae: 10,
	}
	wf := testWorkflow()
	clients := map[string]CisternClient{"repo1": client1, "repo2": client2}
	workflows := map[string]*aqueduct.Workflow{"repo1": wf, "repo2": wf}
	sched := NewFromParts(config, workflows, clients, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(2, time.Second) {
		t.Fatal("timed out waiting for 2 runner calls")
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 runner calls (one per repo), got %d", len(runner.calls))
	}

	for _, call := range runner.calls {
		if call.Item.ID == "r1-b1" && call.AqueductName != "w1" {
			t.Errorf("repo1 item assigned to wrong worker: %s", call.AqueductName)
		}
		if call.Item.ID == "r2-b1" && call.AqueductName != "w2" {
			t.Errorf("repo2 item assigned to wrong worker: %s", call.AqueductName)
		}
	}
}

func TestRun_CancelledContext(t *testing.T) {
	client := newMockClient()
	runner := newMockRunner(nil)
	sched := testScheduler(client, runner)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sched.Run(ctx)
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context canceled error, got %v", err)
	}
}

func TestWorkerPool_Basic(t *testing.T) {
	pool := NewAqueductPool("repo", []string{"a", "b"})

	w := pool.AvailableAqueduct()
	if w == nil || w.Name != "a" {
		t.Fatalf("expected first idle worker 'a', got %v", w)
	}

	pool.Assign(w, "item-1", "implement")
	if pool.FlowingCount() != 1 {
		t.Errorf("expected 1 busy, got %d", pool.FlowingCount())
	}

	w2 := pool.AvailableAqueduct()
	if w2 == nil || w2.Name != "b" {
		t.Fatalf("expected second idle worker 'b', got %v", w2)
	}

	pool.Assign(w2, "item-2", "review")
	if pool.FlowingCount() != 2 {
		t.Errorf("expected 2 busy, got %d", pool.FlowingCount())
	}

	if pool.AvailableAqueduct() != nil {
		t.Error("expected nil when all workers busy")
	}

	pool.Release(w)
	if pool.FlowingCount() != 1 {
		t.Errorf("expected 1 busy after release, got %d", pool.FlowingCount())
	}

	w3 := pool.AvailableAqueduct()
	if w3 == nil || w3.Name != "a" {
		t.Fatalf("expected 'a' available after release, got %v", w3)
	}
}

func TestDefaultWorkerNames(t *testing.T) {
	names := defaultAqueductNames(3)
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	// First three names should be Roman aqueducts
	if names[0] != "virgo" {
		t.Errorf("expected 'virgo', got %q", names[0])
	}
	if names[1] != "marcia" {
		t.Errorf("expected 'marcia', got %q", names[1])
	}
	if names[2] != "claudia" {
		t.Errorf("expected 'claudia', got %q", names[2])
	}

	// n=0 should return at least 1
	names = defaultAqueductNames(0)
	if len(names) != 1 {
		t.Errorf("expected 1 name for n=0, got %d", len(names))
	}

	// Beyond pool size falls back to operator-N
	names = defaultAqueductNames(len(romanAqueducts) + 1)
	last := names[len(names)-1]
	if last != fmt.Sprintf("operator-%d", len(romanAqueducts)) {
		t.Errorf("expected fallback name, got %q", last)
	}
}

func TestWriteContext(t *testing.T) {
	dir := t.TempDir()
	notes := []cistern.CataractaeNote{
		{CataractaeName: "implement", Content: "wrote the feature"},
		{CataractaeName: "review", Content: "needs error handling"},
	}

	if err := WriteContext(dir, notes); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "CONTEXT.md"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, "## Step: implement") {
		t.Error("missing implement step header")
	}
	if !strings.Contains(content, "wrote the feature") {
		t.Error("missing implement note text")
	}
	if !strings.Contains(content, "## Step: review") {
		t.Error("missing review step header")
	}
	if !strings.Contains(content, "needs error handling") {
		t.Error("missing review note text")
	}
}

func TestWriteContext_Empty(t *testing.T) {
	dir := t.TempDir()
	if err := WriteContext(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CONTEXT.md")); !os.IsNotExist(err) {
		t.Error("expected no CONTEXT.md for empty notes")
	}
}

func TestReadOutcome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outcome.json")
	o := Outcome{
		Result: ResultPass,
		Notes:  "all good",
		Annotations: []Annotation{
			{File: "main.go", Line: 10, Comment: "nice"},
		},
	}
	data, _ := json.Marshal(o)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadOutcome(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != ResultPass {
		t.Errorf("result = %q, want %q", got.Result, ResultPass)
	}
	if got.Notes != "all good" {
		t.Errorf("notes = %q, want 'all good'", got.Notes)
	}
	if len(got.Annotations) != 1 {
		t.Errorf("expected 1 annotation, got %d", len(got.Annotations))
	}
}

func TestReadOutcome_NotFound(t *testing.T) {
	_, err := ReadOutcome("/nonexistent/outcome.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLookupStep(t *testing.T) {
	wf := testWorkflow()

	if s := lookupCataracta(wf, "implement"); s == nil || s.Name != "implement" {
		t.Error("expected to find 'implement'")
	}
	if s := lookupCataracta(wf, "nonexistent"); s != nil {
		t.Error("expected nil for unknown step")
	}
}

// --- complexity / human-gate tests ---

// criticalWorkflow returns a workflow with RequireHuman for critical (level 3)
// droplets and no skip rules on any step.
func criticalWorkflow() *aqueduct.Workflow {
	return &aqueduct.Workflow{
		Name: "feature",
		Cataractae: []aqueduct.WorkflowCataractae{
			{Name: "implement", Type: aqueduct.CataractaeTypeAgent, OnPass: "adversarial-review", OnFail: "pooled"},
			{Name: "adversarial-review", Type: aqueduct.CataractaeTypeAgent, OnPass: "qa", OnFail: "implement", OnRecirculate: "implement"},
			{Name: "qa", Type: aqueduct.CataractaeTypeAgent, OnPass: "docs", OnFail: "implement"},
			{Name: "docs", Type: aqueduct.CataractaeTypeAgent, OnPass: "delivery", OnFail: "implement", OnRecirculate: "implement", OnPool: "human"},
			{Name: "delivery", Type: aqueduct.CataractaeTypeAgent, OnPass: "done", OnRecirculate: "implement", OnPool: "human"},
		},
		Complexity: aqueduct.ComplexityConfig{
			Standard: aqueduct.ComplexityLevel{Level: 1},
			Full:     aqueduct.ComplexityLevel{Level: 2},
			Critical: aqueduct.ComplexityLevel{Level: 3, RequireHuman: true},
		},
	}
}

func TestComplexity_CriticalHumanGateBeforeMerge(t *testing.T) {
	wf := criticalWorkflow()
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "crit-1", CurrentCataractae: "docs", Complexity: 3},
	}

	runner := newMockRunner(client)
	// default outcome "pass"; docs.OnPass = "delivery" → critical → "human" → pool

	config := aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "test-repo", Cataractae: 1, Names: []string{"alpha"}, Prefix: "test"},
		},
		MaxCataractae: 4,
	}
	workflows := map[string]*aqueduct.Workflow{"test-repo": wf}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	// docs passes → next is delivery → critical requires human gate → should pool.
	if _, ok := client.pooled["crit-1"]; !ok {
		t.Errorf("expected critical droplet pooled to human before delivery, got step %q", client.steps["crit-1"])
	}
}

func TestTick_StandardDrop_AdvancesToAdversarialReview(t *testing.T) {
	wf := criticalWorkflow()
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "std-1", Complexity: 1},
	}

	runner := newMockRunner(client)

	config := aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "test-repo", Cataractae: 1, Names: []string{"alpha"}, Prefix: "test"},
		},
		MaxCataractae: 4,
	}
	workflows := map[string]*aqueduct.Workflow{"test-repo": wf}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out")
	}
	// implement passed → advance to adversarial-review.
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.steps["std-1"] != "adversarial-review" {
		t.Errorf("expected droplet at adversarial-review, got %q", client.steps["std-1"])
	}
}

func TestComplexity_HumanGateSetsCurrentCataractae(t *testing.T) {
	wf := criticalWorkflow()
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "crit-2", CurrentCataractae: "docs", Complexity: 3},
	}

	runner := newMockRunner(client)
	config := aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "test-repo", Cataractae: 1, Names: []string{"alpha"}, Prefix: "test"},
		},
		MaxCataractae: 4,
	}
	workflows := map[string]*aqueduct.Workflow{"test-repo": wf}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for runner")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	// Human gate: pooled and current_cataractae must be set to "human".
	if _, ok := client.pooled["crit-2"]; !ok {
		t.Errorf("expected critical droplet pooled, not found in pooled map")
	}
	if client.steps["crit-2"] != "human" {
		t.Errorf("expected current_cataractae='human', got %q", client.steps["crit-2"])
	}
}

func TestParseOutcome(t *testing.T) {
	tests := []struct {
		outcome      string
		wantResult   Result
		wantRecircTo string
	}{
		{"pass", ResultPass, ""},
		{"recirculate", ResultRecirculate, ""},
		{"recirculate:implement", ResultRecirculate, "implement"},
		{"pool", ResultPool, ""},
		{"block", ResultFail, ""},
		{"unknown", ResultFail, ""},
	}
	for _, tt := range tests {
		r, to := parseOutcome(tt.outcome)
		if r != tt.wantResult {
			t.Errorf("parseOutcome(%q).result = %q, want %q", tt.outcome, r, tt.wantResult)
		}
		if to != tt.wantRecircTo {
			t.Errorf("parseOutcome(%q).recirculateTo = %q, want %q", tt.outcome, to, tt.wantRecircTo)
		}
	}
}

// --- Phantom commit prevention tests ---

// makeGitSandbox initialises a git repo in dir with an initial commit and
// returns the HEAD hash. Used by scheduler tests that need a real sandbox.
func makeGitSandbox(t *testing.T, dir string) string {
	t.Helper()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// Create an initial commit.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestObserve_ExternallyChangedStatus_FreesPoolSlot verifies that when a droplet's
// status is changed to 'cancelled' or 'pooled' externally while in_progress (without
// signaling an outcome), the observe phase detects it and releases the aqueduct pool slot.
func TestObserve_ExternallyChangedStatus_FreesPoolSlot(t *testing.T) {
	for _, extStatus := range []string{"cancelled", "pooled"} {
		t.Run(extStatus, func(t *testing.T) {
			var logBuf bytes.Buffer
			client := newMockClient()
			item := &cistern.Droplet{
				ID:                "ext-" + extStatus + "-1",
				Repo:              "test-repo",
				CurrentCataractae: "implement",
				Assignee:          "alpha",
				Status:            "in_progress",
			}
			client.items[item.ID] = item

			runner := newMockRunner(nil) // no auto-outcomes; agent is still "running"
			config := testConfig()
			workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
			clients := map[string]CisternClient{"test-repo": client}
			sched := NewFromParts(config, workflows, clients, runner,
				WithLogger(newTestLogger(&logBuf)))

			// Claim the pool slot to reflect the in-progress dispatch state.
			pool := sched.pools["test-repo"]
			w := pool.FindByName("alpha")
			pool.Assign(w, item.ID, "implement")

			// Simulate external status change without an outcome signal.
			client.mu.Lock()
			client.items[item.ID].Status = extStatus
			client.mu.Unlock()

			sched.Tick(context.Background())

			if pool.IsFlowing("alpha") {
				t.Errorf("expected alpha aqueduct to be idle after external %s, got flowing", extStatus)
			}

			// Verify that the INFO log line was emitted.
			if !strings.Contains(logBuf.String(), "aqueduct freed") {
				t.Errorf("expected INFO log containing 'aqueduct freed' on external %s, got: %s", extStatus, logBuf.String())
			}
		})
	}
}

// TestObserve_ExternalCancel_NormalFlowUnaffected verifies that the external-cancel
// secondary check does not interfere with normal outcome-driven pool release.
func TestObserve_ExternalCancel_NormalFlowUnaffected(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "normal-1", Title: "normal flow"}}

	runner := newMockRunner(client) // default outcome is "pass"
	sched := testScheduler(client, runner)

	// Dispatch tick.
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for spawn")
	}
	// Observe tick: routes "pass" → "review".
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.steps["normal-1"] != "review" {
		t.Errorf("normal pass routing broken: expected 'review', got %q", client.steps["normal-1"])
	}
}

// TestDispatch_DirtyWorktree verifies that when a worktree has uncommitted files
// from a prior session, the agent spawns normally and the dirty state is preserved.
// The agent is told about uncommitted files via CONTEXT.md and must commit them.
func TestDispatch_DirtyWorktree(t *testing.T) {
	sandboxRoot := t.TempDir()

	const itemID = "dirty-1"
	client := newMockClient()
	client.readyItems = append(client.readyItems, &cistern.Droplet{
		ID:                itemID,
		CurrentCataractae: "implement",
		Status:            "open",
	})

	sandboxDir := filepath.Join(sandboxRoot, "test-repo", itemID)
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeGitSandbox(t, sandboxDir)

	cmd := exec.Command("git", "checkout", "-b", "feat/"+itemID)
	cmd.Dir = sandboxDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b feat/%s: %v\n%s", itemID, err, out)
	}

	// Modify a tracked file without committing — creates the dirty state.
	if err := os.WriteFile(filepath.Join(sandboxDir, "README.md"), []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}

	runner := newMockRunner(client)
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner,
		WithSandboxRoot(sandboxRoot))

	sched.Tick(context.Background())
	time.Sleep(50 * time.Millisecond)

	// Spawn proceeds normally — dirty state is preserved for the agent to commit.
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.calls) != 1 {
		t.Fatalf("expected spawn to proceed with dirty worktree, got %d runner calls", len(runner.calls))
	}

	// The dirty state should still be present — not hard-reset away.
	content, err := os.ReadFile(filepath.Join(sandboxDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "dirty\n" {
		t.Errorf("dirty file was reset; expected preserved content, got %q", string(content))
	}
}

// TestDispatch_RebaseInProgress verifies that when a worktree is left with a
// rebase in progress (e.g. from an interrupted delivery agent), the next
// dispatch aborts the rebase and proceeds normally.
func TestDispatch_RebaseInProgress(t *testing.T) {
	sandboxRoot := t.TempDir()

	const itemID = "rebase-1"
	client := newMockClient()
	client.readyItems = append(client.readyItems, &cistern.Droplet{
		ID:                itemID,
		CurrentCataractae: "implement",
		Status:            "open",
	})

	// Create the worktree directory and initialize a git repo inside it.
	sandboxDir := filepath.Join(sandboxRoot, "test-repo", itemID)
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeGitSandbox(t, sandboxDir)

	// Create the feature branch with a conflicting commit.
	for _, args := range [][]string{
		{"git", "checkout", "-b", "feat/" + itemID},
		{"git", "checkout", "-b", "conflict-branch"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = sandboxDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	// Make a commit on conflict-branch that modifies README.md.
	if err := os.WriteFile(filepath.Join(sandboxDir, "README.md"), []byte("conflict-branch content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "README.md"},
		{"git", "commit", "-m", "conflict on branch"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = sandboxDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	// Switch to the feature branch and make a conflicting commit.
	cmd := exec.Command("git", "checkout", "feat/"+itemID)
	cmd.Dir = sandboxDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout feat branch: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(sandboxDir, "README.md"), []byte("feature-branch content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "README.md"},
		{"git", "commit", "-m", "conflict on feat"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = sandboxDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	// Start a rebase onto conflict-branch — this will hit a conflict and
	// leave the repo in a rebase-in-progress state.
	rebase := exec.Command("git", "rebase", "conflict-branch")
	rebase.Dir = sandboxDir
	rebase.Run() // expected to fail with conflict

	// Verify rebase is actually in progress.
	status := exec.Command("git", "status")
	status.Dir = sandboxDir
	statusOut, _ := status.CombinedOutput()
	if !strings.Contains(string(statusOut), "rebase") {
		t.Fatalf("expected rebase in progress, got:\n%s", statusOut)
	}

	// Verify that "git checkout" would fail without the fix.
	verifyCheckout := exec.Command("git", "checkout", "feat/"+itemID)
	verifyCheckout.Dir = sandboxDir
	if _, err := verifyCheckout.CombinedOutput(); err == nil {
		t.Skip("git checkout succeeded despite rebase in progress — cannot test fix")
	}

	runner := newMockRunner(client)
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner,
		WithSandboxRoot(sandboxRoot))

	sched.Tick(context.Background())
	time.Sleep(50 * time.Millisecond)

	// The dispatch should have succeeded — rebase --abort clears the state
	// before checkout.
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.calls) != 1 {
		t.Errorf("expected 1 runner call (dispatch succeeded after rebase abort), got %d", len(runner.calls))
	}
}

// TestDispatch_DiffOnlyStepGetsSandboxDir verifies that when a diff_only agent
// step is dispatched, the Castellarius prepares the per-droplet worktree and
// passes its path as req.SandboxDir. Without this, generateDiff runs on the
// worker's own sandbox (on main, no changes) and produces an empty diff.patch.
//
// Regression test for ci-s5eg9: adversarial-review blocked 3× with empty diff.
func TestDispatch_DiffOnlyStepGetsSandboxDir(t *testing.T) {
	sandboxRoot := t.TempDir()

	const itemID = "diff-1"
	client := newMockClient()
	client.readyItems = append(client.readyItems, &cistern.Droplet{
		ID:                itemID,
		CurrentCataractae: "adversarial-review",
		Status:            "open",
	})

	// Pre-create the per-droplet worktree so prepareDropletWorktree takes the
	// resume path (no git fetch against a non-existent remote is attempted).
	sandboxDir := filepath.Join(sandboxRoot, "test-repo", itemID)
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeGitSandbox(t, sandboxDir)

	// Create the feature branch so prepareDropletWorktree's checkout succeeds.
	cmd := exec.Command("git", "checkout", "-b", "feat/"+itemID)
	cmd.Dir = sandboxDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b feat/%s: %v\n%s", itemID, err, out)
	}

	// Workflow with a diff_only adversarial-review step.
	wf := &aqueduct.Workflow{
		Name: "test",
		Cataractae: []aqueduct.WorkflowCataractae{
			{Name: "implement", Type: aqueduct.CataractaeTypeAgent, OnPass: "adversarial-review"},
			{
				Name:    "adversarial-review",
				Type:    aqueduct.CataractaeTypeAgent,
				Context: aqueduct.ContextDiffOnly,
				OnPass:  "done",
			},
		},
	}

	runner := newMockRunner(client)
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": wf}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner,
		WithSandboxRoot(sandboxRoot))

	sched.Tick(context.Background())
	if !runner.waitCalls(1, 2*time.Second) {
		t.Fatal("timed out waiting for runner Spawn call")
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.calls))
	}
	if runner.calls[0].SandboxDir != sandboxDir {
		t.Errorf("SandboxDir = %q, want %q (diff_only step must get per-droplet worktree path)",
			runner.calls[0].SandboxDir, sandboxDir)
	}
}

// --- heartbeat progress-monitoring tests ---

// TestHeartbeatRepo_StallDetected_AppendsNoteAndWarnLog verifies that when all
// three progress signals are older than the stall threshold, heartbeatRepo
// appends a note to the droplet and emits a Warn-level log entry.
func TestHeartbeatRepo_StallDetected_AppendsNoteAndWarnLog(t *testing.T) {
	var buf bytes.Buffer
	client := newMockClient()

	// Droplet with no signals — all three signal sources return zero time.
	item := &cistern.Droplet{
		ID:                "stall-basic",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "",
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 1 // 1-minute threshold; zero signals → always stalled.

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner,
		WithLogger(newTestLogger(&buf)))

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Stall note and orphan note must both be appended.
	if len(client.attached) != 2 {
		t.Fatalf("expected 2 notes (stall + orphan), got %d", len(client.attached))
	}
	if !strings.HasPrefix(client.attached[0].notes, stallNotePrefix) {
		t.Errorf("stall note missing structured prefix %q; got: %s", stallNotePrefix, client.attached[0].notes)
	}
	if !strings.Contains(client.attached[1].notes, "[scheduler:recovery]") {
		t.Errorf("orphan note missing '[scheduler:recovery]'; got: %s", client.attached[1].notes)
	}

	// A Warn-level log entry must be present containing the droplet ID.
	out := buf.String()
	if !strings.Contains(out, "stall-basic") {
		t.Errorf("heartbeat log missing droplet ID; got: %s", out)
	}
	if !strings.Contains(out, "stall_duration=") {
		t.Errorf("heartbeat log missing stall_duration field; got: %s", out)
	}
}

// TestHeartbeatRepo_OrphanRecovery_SecondTick_ItemResetToOpenNotReprocessed
// verifies that after orphan handling resets a no-assignee in_progress droplet
// to open on the first tick, the second heartbeat tick writes no additional
// notes because the item is no longer in_progress and is not returned by List.
func TestHeartbeatRepo_OrphanRecovery_SecondTick_ItemResetToOpenNotReprocessed(t *testing.T) {
	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "stall-debounce",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "",
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 1

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	// First call: no signals → stalled → stall note + recovery note written, item reset to open.
	sched.heartbeatRepo(context.Background(), config.Repos[0])
	if len(client.attached) != 2 {
		t.Fatalf("expected 2 notes (stall + recovery) after first tick, got %d", len(client.attached))
	}
	if item.Status != "open" {
		t.Errorf("expected item reset to open after orphan handling, got status %q", item.Status)
	}

	// Second call: item is now open (no longer in_progress) → no additional notes.
	sched.heartbeatRepo(context.Background(), config.Repos[0])
	if len(client.attached) != 2 {
		t.Errorf("expected still 2 notes after second tick (item is open), got %d", len(client.attached))
	}
}

// TestHeartbeatRepo_StallThreshold_ExplicitMinutesRespected verifies that an
// explicitly configured stall_threshold_minutes is used for stall detection.
func TestHeartbeatRepo_StallThreshold_ExplicitMinutesRespected(t *testing.T) {
	client := newMockClient()

	// Droplet with heartbeat 2 minutes old.
	twoMinAgo := time.Now().Add(-2 * time.Minute)
	item := &cistern.Droplet{
		ID:                "stall-thresh-explicit",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "",
		LastHeartbeatAt:   twoMinAgo,
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 1 // 2 min > 1 min → stalled

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	if len(client.attached) != 2 {
		t.Errorf("expected 2 notes (stall + recovery) with 1-min threshold and 2-min-old heartbeat, got %d", len(client.attached))
	}
}

// TestHeartbeatRepo_StallThreshold_DefaultsTo45Minutes verifies that when
// stall_threshold_minutes is absent or zero, the default threshold of 45
// minutes is used and a droplet with a heartbeat only 2 minutes old is not stalled.
func TestHeartbeatRepo_StallThreshold_DefaultsTo45Minutes(t *testing.T) {
	client := newMockClient()

	// Droplet with heartbeat 2 minutes old — well within the 45-min default.
	twoMinAgo := time.Now().Add(-2 * time.Minute)
	item := &cistern.Droplet{
		ID:                "stall-thresh-default",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "",
		LastHeartbeatAt:   twoMinAgo,
	}
	client.items[item.ID] = item

	config := testConfig()
	// StallThresholdMinutes deliberately left at zero → should default to 45 min.

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// 2 min < 45 min → not stalled → no note written.
	if len(client.attached) != 0 {
		t.Errorf("expected 0 stall notes with default 45-min threshold and 2-min-old heartbeat, got %d", len(client.attached))
	}
}

// TestHeartbeatRepo_StallWithAssignee_WritesNoteNoRespawn verifies that when a
// stall is detected and the droplet has an assignee, a stall note is written but
// runner.Spawn is NOT called. Stall detection no longer auto-respawns: agents
// emitting heartbeats are alive, and dead agents are handled by zombie detection.
func TestHeartbeatRepo_StallWithAssignee_WritesNoteNoRespawn(t *testing.T) {
	// Mock tmux as alive so liveness check passes through to stall detector.
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = orig })
	// Mock agent as alive so the agent-dead zombie path is not triggered.
	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	client := newMockClient()
	runner := newMockRunner(client)

	item := &cistern.Droplet{
		ID:                "stall-no-respawn",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 1

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Stall note must be written.
	if len(client.attached) != 1 {
		t.Fatalf("expected 1 stall note, got %d", len(client.attached))
	}
	if !strings.HasPrefix(client.attached[0].notes, stallNotePrefix) {
		t.Errorf("stall note missing prefix %q; got: %s", stallNotePrefix, client.attached[0].notes)
	}

	// Spawn must NOT have been called — stall detection does not respawn.
	runner.mu.Lock()
	calls := runner.calls
	runner.mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("expected runner.Spawn NOT called, got %d calls", len(calls))
	}

	// Status and assignee must be unchanged.
	if item.Status != "in_progress" {
		t.Errorf("item status = %q, want in_progress", item.Status)
	}
	if item.Assignee != "alpha" {
		t.Errorf("item assignee = %q, want alpha", item.Assignee)
	}
}

// TestHeartbeatRepo_StallWithNoAssignee_RecoverAndNoSpawn verifies that when a
// stall is detected on an orphaned droplet (no assignee), both the stall note and
// the recovery note are written, the droplet is reset to open for re-dispatch,
// and runner.Spawn is NOT called (there is no session to resume).
func TestHeartbeatRepo_StallWithNoAssignee_RecoverAndNoSpawn(t *testing.T) {
	client := newMockClient()
	runner := newMockRunner(client)

	item := &cistern.Droplet{
		ID:                "stall-no-assignee",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "",
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 1

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Stall note and orphan note must both be written.
	if len(client.attached) != 2 {
		t.Fatalf("expected 2 notes (stall + orphan) for orphaned droplet, got %d", len(client.attached))
	}
	if !strings.Contains(client.attached[1].notes, "[scheduler:recovery]") {
		t.Errorf("second note should be recovery note; got: %s", client.attached[1].notes)
	}

	// Item must be reset to open for re-dispatch.
	if item.Status != "open" {
		t.Errorf("expected item reset to open, got status %q", item.Status)
	}

	// Spawn must NOT have been called.
	runner.mu.Lock()
	calls := runner.calls
	runner.mu.Unlock()
	if len(calls) != 0 {
		t.Errorf("expected runner.Spawn not called for orphaned droplet, got %d calls", len(calls))
	}
}

// TestHeartbeatRepo_OrphanRecovery_ClearsAssignedAqueduct verifies that the
// orphan handling path clears assigned_aqueduct so the pooled droplet is not
// locked to a specific aqueduct operator that may no longer exist.
func TestHeartbeatRepo_OrphanRecovery_ClearsAssignedAqueduct(t *testing.T) {
	client := newMockClient()
	runner := newMockRunner(client)

	item := &cistern.Droplet{
		ID:               "orphan-aq",
		CurrentCataractae: "implement",
		Status:           "in_progress",
		Assignee:         "",
		AssignedAqueduct: "virgo", // stale aqueduct from a previous dispatch attempt
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 1

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	if item.Status != "open" {
		t.Errorf("expected item status=open after orphan handling, got %q", item.Status)
	}
	if item.AssignedAqueduct != "" {
		t.Errorf("expected assigned_aqueduct cleared after recovery, got %q", item.AssignedAqueduct)
	}
}

// TestHeartbeatRepo_OrphanRecovery_AssignFailure_ClearsDebounce verifies that
// when the Assign call fails, the debounce entry is cleared so the next
// heartbeat retries the orphan handling rather than suppressing it permanently.
func TestHeartbeatRepo_OrphanRecovery_AssignFailure_ClearsDebounce(t *testing.T) {
	client := newMockClient()
	client.assignErr = errors.New("db error")
	runner := newMockRunner(client)

	item := &cistern.Droplet{
		ID:                "orphan-assign-fail",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "",
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 1

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Orphan note must be written (best-effort) even when Pool fails.
	orphanNotes := 0
	for _, n := range client.attached {
		if strings.Contains(n.notes, "[scheduler:recovery]") {
			orphanNotes++
		}
	}
	if orphanNotes < 1 {
		t.Errorf("expected recovery note even on Assign failure, got %d recovery notes", orphanNotes)
	}
}

// --- heartbeat stall detection integration tests ---

// TestHeartbeatRepo_AgentEmittingHeartbeat_NotStalled verifies that an agent
// actively emitting heartbeats (heartbeat < threshold) is not considered stalled,
// even if it has been running for a long time without commits or an outcome.
func TestHeartbeatRepo_AgentEmittingHeartbeat_NotStalled(t *testing.T) {
	// Mock tmux/agent as alive to avoid zombie detection path.
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = orig })
	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	client := newMockClient()
	runner := newMockRunner(client)

	// Heartbeat 30s ago — well within the 1-minute threshold.
	item := &cistern.Droplet{
		ID:                "hb-alive",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		LastHeartbeatAt:   time.Now().Add(-30 * time.Second),
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 1

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Agent is heartbeating → not stalled → no note written, no spawn.
	if len(client.attached) != 0 {
		t.Errorf("expected no stall notes for heartbeating agent, got %d", len(client.attached))
	}
	runner.mu.Lock()
	calls := runner.calls
	runner.mu.Unlock()
	if len(calls) != 0 {
		t.Errorf("expected no Spawn calls for heartbeating agent, got %d", len(calls))
	}
}

// TestHeartbeatRepo_AgentNotEmittingHeartbeat_Stalled verifies that an agent
// that has not emitted a heartbeat for longer than the stall threshold is
// detected as stalled and an escalation note is written. No auto-respawn occurs.
func TestHeartbeatRepo_AgentNotEmittingHeartbeat_Stalled(t *testing.T) {
	// Mock tmux/agent as alive to avoid zombie detection path.
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = orig })
	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	client := newMockClient()
	runner := newMockRunner(nil) // nil client: Spawn must not be called

	// Heartbeat 90s ago — older than the 1-minute threshold.
	item := &cistern.Droplet{
		ID:                "hb-stalled",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		LastHeartbeatAt:   time.Now().Add(-90 * time.Second),
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 1

	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Stall detected → escalation note written.
	if len(client.attached) != 1 {
		t.Fatalf("expected 1 stall note for non-heartbeating agent, got %d", len(client.attached))
	}
	if !strings.HasPrefix(client.attached[0].notes, stallNotePrefix) {
		t.Errorf("stall note missing prefix %q; got: %s", stallNotePrefix, client.attached[0].notes)
	}
	if !strings.Contains(client.attached[0].notes, "heartbeat=") {
		t.Errorf("stall note missing heartbeat field; got: %s", client.attached[0].notes)
	}

	// No auto-respawn — stall detection does not respawn; that is zombie detection's job.
	runner.mu.Lock()
	calls := runner.calls
	runner.mu.Unlock()
	if len(calls) != 0 {
		t.Errorf("expected no Spawn calls (stall detection must not auto-respawn), got %d", len(calls))
	}
}

// --- heartbeat zombie detection tests ---

// TestHeartbeatRepo_TmuxDead_WritesNoteAndResetsDroplet verifies that when the
// tmux session is dead the droplet is reset to open for re-dispatch and a zombie
// note is written.
func TestHeartbeatRepo_TmuxDead_WritesNoteAndResetsDroplet(t *testing.T) {
	// Ensure tmux appears dead for our test session.
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return false }
	t.Cleanup(func() { isTmuxAliveFn = orig })

	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "zombie-tmuxdead",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		StageDispatchedAt: time.Now().Add(-10 * time.Minute), // old enough to pass age guard
	}
	client.items[item.ID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Note must have been written.
	if len(client.attached) != 1 {
		t.Fatalf("expected 1 zombie note, got %d", len(client.attached))
	}
	note := client.attached[0].notes
	if !strings.Contains(note, "[scheduler:zombie]") {
		t.Errorf("zombie note missing [scheduler:zombie] prefix; got: %s", note)
	}

	// Droplet must have been reset to open for re-dispatch.
	if item.Status != "open" {
		t.Errorf("item status = %q, want open", item.Status)
	}
	if item.Assignee != "" {
		t.Errorf("item assignee = %q, want empty after reset", item.Assignee)
	}
}

// TestHeartbeatRepo_TmuxAliveAgentDead_WritesNoteKillsSessionAndResetsDroplet
// verifies the path: tmux session alive but agent process has exited.
// The heartbeat must kill the session, write a diagnostic note, and reset
// the droplet to open for re-dispatch.
func TestHeartbeatRepo_TmuxAliveAgentDead_WritesNoteKillsSessionAndResetsDroplet(t *testing.T) {
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = orig })

	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool { return false } // shell-only pane
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "zombie-agentdead",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
	}
	client.items[item.ID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Note must have been written with the expected diagnostic text.
	if len(client.attached) != 1 {
		t.Fatalf("expected 1 note, got %d", len(client.attached))
	}
	note := client.attached[0].notes
	if !strings.Contains(note, "Session killed") {
		t.Errorf("note missing 'Session killed'; got: %s", note)
	}
	if !strings.Contains(note, "Re-dispatching") {
		t.Errorf("note missing 'Re-dispatching'; got: %s", note)
	}

	// Droplet must have been reset to open for re-dispatch.
	if item.Status != "open" {
		t.Errorf("item status = %q, want open", item.Status)
	}
	if item.Assignee != "" {
		t.Errorf("item assignee = %q, want empty after reset", item.Assignee)
	}
}

// TestHeartbeatRepo_TmuxAliveAgentDead_RecentDispatch_SkipsZombieHandling verifies
// that the minimum age guard prevents false-positive zombie kills during the
// startup window when tmux is alive but the agent process has not yet been forked.
func TestHeartbeatRepo_TmuxAliveAgentDead_RecentDispatch_SkipsZombieHandling(t *testing.T) {
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = orig })

	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool { return false } // agent not yet forked
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "zombie-agentdead-recent",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		StageDispatchedAt: time.Now(), // just dispatched — within zombieGuard
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 60 // high threshold — not stalled
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	if len(client.attached) != 0 {
		t.Errorf("expected no notes written for recently-dispatched session, got %d", len(client.attached))
	}
	if item.Status != "in_progress" {
		t.Errorf("item status = %q, want in_progress", item.Status)
	}
	if item.Assignee != "alpha" {
		t.Errorf("item assignee = %q, want alpha", item.Assignee)
	}
}

// TestHeartbeatRepo_TmuxAliveAgentAlive_SkipsZombieHandling verifies that when
// both tmux and the claude process are alive no zombie action is taken.
func TestHeartbeatRepo_TmuxAliveAgentAlive_SkipsZombieHandling(t *testing.T) {
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return true }
	t.Cleanup(func() { isTmuxAliveFn = orig })

	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool { return true } // live claude process
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "agent-alive",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 60 // high threshold — not stalled
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	// Provide a recent heartbeat so stall detection does not fire.
	item.LastHeartbeatAt = time.Now().Add(-30 * time.Second)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	if len(client.attached) != 0 {
		t.Errorf("expected no notes written for live agent, got %d", len(client.attached))
	}
	if item.Status != "in_progress" {
		t.Errorf("item status = %q, want in_progress", item.Status)
	}
}

// TestHeartbeatRepo_TmuxAliveAgentDead_NoAssignee_SkipsZombieCheck verifies that
// when the droplet has no assignee the agent-dead check is not attempted.
func TestHeartbeatRepo_TmuxAliveAgentDead_NoAssignee_SkipsZombieCheck(t *testing.T) {
	var agentCheckCalled bool
	origAgent := isAgentAliveFn
	isAgentAliveFn = func(_ string) bool {
		agentCheckCalled = true
		return false
	}
	t.Cleanup(func() { isAgentAliveFn = origAgent })

	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "no-assignee",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "", // no assignee — zombie checks should be skipped
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 60
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	// Provide a recent heartbeat so stall detection does not fire.
	item.LastHeartbeatAt = time.Now().Add(-30 * time.Second)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	if agentCheckCalled {
		t.Error("isAgentAliveFn should not be called when droplet has no assignee")
	}
}

// TestHeartbeatRepo_FastCompletingStage_StageDispatchedAtRecent_NotZombie verifies
// acceptance criterion (a): a stage dispatched recently (StageDispatchedAt within
// zombieGuard) is never declared a zombie, even if UpdatedAt is stale and the tmux
// session is dead. This models the ci-y89g2 failure: a stage that completes in <2min
// has its tmux session exit naturally; the dispatch timestamp guards against a false
// positive regardless of when other updates touched the droplet.
func TestHeartbeatRepo_FastCompletingStage_StageDispatchedAtRecent_NotZombie(t *testing.T) {
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return false } // session already exited
	t.Cleanup(func() { isTmuxAliveFn = orig })

	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "fast-complete",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		// StageDispatchedAt is recent — stage just started.
		StageDispatchedAt: time.Now(),
		// UpdatedAt is old (e.g. bumped by a prior note) — would fail the old guard.
		UpdatedAt: time.Now().Add(-10 * time.Minute),
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 60
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	// Add a recent note so stall detection does not fire.
	client.notes[item.ID] = []cistern.CataractaeNote{
		{CreatedAt: time.Now()},
	}

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// Age guard (StageDispatchedAt is recent) must suppress zombie reset.
	if item.Status != "in_progress" {
		t.Errorf("item status = %q, want in_progress — fast stage must not be declared zombie", item.Status)
	}
	if item.Assignee != "alpha" {
		t.Errorf("item assignee = %q, want alpha — fast stage must not be reset", item.Assignee)
	}
	if len(client.attached) != 0 {
		t.Errorf("expected no zombie notes for fast-completing stage, got %d: %v",
			len(client.attached), client.attached)
	}
}

// TestHeartbeatRepo_GenuineZombie_StageDispatchedAtOld_Detected verifies
// acceptance criterion (b): a session with a stale StageDispatchedAt, no outcome,
// and a dead tmux session IS correctly identified as a zombie and reset.
// UpdatedAt is kept recent here to confirm the guard uses StageDispatchedAt, not UpdatedAt.
func TestHeartbeatRepo_GenuineZombie_StageDispatchedAtOld_Detected(t *testing.T) {
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return false } // session dead
	t.Cleanup(func() { isTmuxAliveFn = orig })

	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "genuine-zombie",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		Outcome:           "", // no outcome recorded — not a completed stage
		// StageDispatchedAt is old — dispatch happened long ago.
		StageDispatchedAt: time.Now().Add(-10 * time.Minute),
		// UpdatedAt is recent — would suppress detection with the old UpdatedAt guard.
		UpdatedAt: time.Now(),
	}
	client.items[item.ID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// StageDispatchedAt is old → zombie guard fires → droplet must be reset to open.
	if item.Status != "open" {
		t.Errorf("item status = %q, want open — genuine zombie must be reset", item.Status)
	}
	if item.Assignee != "" {
		t.Errorf("item assignee = %q, want empty — genuine zombie must be reset", item.Assignee)
	}
	if len(client.attached) != 1 {
		t.Fatalf("expected 1 zombie note, got %d", len(client.attached))
	}
	if !strings.Contains(client.attached[0].notes, "zombie") {
		t.Errorf("zombie note missing 'zombie'; got: %s", client.attached[0].notes)
	}
}

// TestHeartbeatRepo_UpdatedAtFallback_StaleUpdatedAt_ZombieFires verifies the
// migration fallback branch: when StageDispatchedAt is
// zero (pre-migration droplet) and UpdatedAt is older than zombieGuard, the
// droplet is treated as a zombie and pooled.
func TestHeartbeatRepo_UpdatedAtFallback_StaleUpdatedAt_ZombieFires(t *testing.T) {
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return false }
	t.Cleanup(func() { isTmuxAliveFn = orig })

	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "fallback-stale",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		Outcome:           "",
		StageDispatchedAt: time.Time{},                  // zero — pre-migration droplet
		UpdatedAt:         time.Now().Add(-10 * time.Minute), // stale — older than zombieGuard
	}
	client.items[item.ID] = item

	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// UpdatedAt fallback: stale UpdatedAt → zombie fires → droplet must be reset to open.
	if item.Status != "open" {
		t.Errorf("item status = %q, want open — stale UpdatedAt fallback must trigger zombie reset", item.Status)
	}
	if item.Assignee != "" {
		t.Errorf("item assignee = %q, want empty — zombie must clear assignee", item.Assignee)
	}
	if len(client.attached) != 1 {
		t.Fatalf("expected 1 zombie note, got %d", len(client.attached))
	}
	if !strings.Contains(client.attached[0].notes, "zombie") {
		t.Errorf("zombie note missing 'zombie'; got: %s", client.attached[0].notes)
	}
}

// TestHeartbeatRepo_UpdatedAtFallback_RecentUpdatedAt_ZombieSuppressed verifies the
// migration fallback branch: when StageDispatchedAt is
// zero (pre-migration droplet) and UpdatedAt is recent (within zombieGuard), the
// droplet is NOT declared a zombie — the age guard suppresses the pool.
func TestHeartbeatRepo_UpdatedAtFallback_RecentUpdatedAt_ZombieSuppressed(t *testing.T) {
	orig := isTmuxAliveFn
	isTmuxAliveFn = func(_ string) bool { return false }
	t.Cleanup(func() { isTmuxAliveFn = orig })

	client := newMockClient()

	item := &cistern.Droplet{
		ID:                "fallback-recent",
		CurrentCataractae: "implement",
		Status:            "in_progress",
		Assignee:          "alpha",
		Outcome:           "",
		StageDispatchedAt: time.Time{},  // zero — pre-migration droplet
		UpdatedAt:         time.Now(),   // recent — within zombieGuard
	}
	client.items[item.ID] = item

	config := testConfig()
	config.StallThresholdMinutes = 60
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	runner := newMockRunner(client)
	sched := NewFromParts(config, workflows, clients, runner)

	// Add a recent note so stall detection does not fire.
	client.notes[item.ID] = []cistern.CataractaeNote{
		{CreatedAt: time.Now()},
	}

	sched.heartbeatRepo(context.Background(), config.Repos[0])

	// UpdatedAt fallback: recent UpdatedAt → age guard suppresses zombie reset.
	if item.Status != "in_progress" {
		t.Errorf("item status = %q, want in_progress — recent UpdatedAt fallback must suppress zombie", item.Status)
	}
	if item.Assignee != "alpha" {
		t.Errorf("item assignee = %q, want alpha — age guard must not reset assignee", item.Assignee)
	}
	if len(client.attached) != 0 {
		t.Errorf("expected no zombie notes for recent UpdatedAt fallback, got %d: %v",
			len(client.attached), client.attached)
	}
}

// --- worktree lifecycle logging tests ---

// TestPrepareDropletWorktree_LogsWorktreeCreated verifies that prepareDropletWorktree
// emits a slog.Info entry containing "worktree created" when a new worktree is made.
func TestPrepareDropletWorktree_LogsWorktreeCreated(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	// makeBareAndClone provides a primary with origin/main available for fetch.
	primary := makeBareAndClone(t)
	sandboxRoot := t.TempDir()
	repoName := "logrepo"

	_, err := prepareDropletWorktreeWithLogger(l, primary, sandboxRoot, repoName, "ci-wt-create")
	if err != nil {
		t.Fatalf("prepareDropletWorktree: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "worktree created") {
		t.Errorf("log missing 'worktree created'; got: %s", out)
	}
	if !strings.Contains(out, "ci-wt-create") {
		t.Errorf("log missing droplet ID; got: %s", out)
	}
	if !strings.Contains(out, "duration=") {
		t.Errorf("log missing duration field; got: %s", out)
	}
}

// TestPrepareDropletWorktree_LogsWorktreeResumed verifies that prepareDropletWorktree
// emits a slog.Info entry containing "worktree resumed" on subsequent calls.
func TestPrepareDropletWorktree_LogsWorktreeResumed(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	primary := makeBareAndClone(t)
	sandboxRoot := t.TempDir()
	repoName := "logrepo"

	// First call: create.
	if _, err := prepareDropletWorktreeWithLogger(l, primary, sandboxRoot, repoName, "ci-wt-resume"); err != nil {
		t.Fatalf("first prepareDropletWorktree: %v", err)
	}
	buf.Reset()

	// Second call: resume.
	if _, err := prepareDropletWorktreeWithLogger(l, primary, sandboxRoot, repoName, "ci-wt-resume"); err != nil {
		t.Fatalf("second prepareDropletWorktree: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "worktree resumed") {
		t.Errorf("log missing 'worktree resumed'; got: %s", out)
	}
}

// TestRemoveDropletWorktree_LogsWorktreeDeleted verifies that removeDropletWorktree
// emits a slog.Info entry containing "worktree deleted" when the removal succeeds.
func TestRemoveDropletWorktree_LogsWorktreeDeleted(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	primary := makeBareAndClone(t)
	sandboxRoot := t.TempDir()
	repoName := "logrepo"

	if _, err := prepareDropletWorktreeWithLogger(l, primary, sandboxRoot, repoName, "ci-wt-del"); err != nil {
		t.Fatalf("prepareDropletWorktree: %v", err)
	}
	buf.Reset()

	removeDropletWorktreeWithLogger(l, primary, sandboxRoot, repoName, "ci-wt-del", false)

	out := buf.String()
	if !strings.Contains(out, "worktree deleted") {
		t.Errorf("log missing 'worktree deleted'; got: %s", out)
	}
	if !strings.Contains(out, "ci-wt-del") {
		t.Errorf("log missing droplet ID; got: %s", out)
	}
}

// TestRemoveDropletWorktree_LogsWarn_WhenWorktreeMissing verifies that when the
// worktree does not exist, removeDropletWorktreeWithLogger emits a Warn-level
// entry rather than a false "worktree deleted" success message.
func TestRemoveDropletWorktree_LogsWarn_WhenWorktreeMissing(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	primary := makeBareAndClone(t)
	sandboxRoot := t.TempDir()
	repoName := "logrepo"
	dropletID := "ci-wt-missing"

	// Do NOT create the worktree — removal should fail.
	removeDropletWorktreeWithLogger(l, primary, sandboxRoot, repoName, dropletID, false)

	out := buf.String()
	if strings.Contains(out, "worktree deleted") {
		t.Errorf("unexpected 'worktree deleted' success log when worktree was missing; got: %s", out)
	}
	if !strings.Contains(out, "worktree deletion failed") {
		t.Errorf("expected 'worktree deletion failed' Warn log; got: %s", out)
	}
	if !strings.Contains(out, dropletID) {
		t.Errorf("log missing droplet ID; got: %s", out)
	}
}

// --- checkHungDrought tests ---

// TestCheckHungDrought_WhenDroughtRunningMoreThan5m_EmitsWarning verifies that a drought
// goroutine running for more than 5 minutes causes a "hung" warning in the log.
func TestCheckHungDrought_WhenDroughtRunningMoreThan5m_EmitsWarning(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Now().UTC().Add(-6 * time.Minute)
	hf := HealthFile{
		LastTickAt:       time.Now().UTC(),
		PollIntervalSec:  10,
		DroughtRunning:   true,
		DroughtStartedAt: &startedAt,
	}
	b, err := json.Marshal(hf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "castellarius.health"), b, 0o644); err != nil {
		t.Fatalf("write health file: %v", err)
	}

	var logBuf bytes.Buffer
	s := &Castellarius{
		dbPath: filepath.Join(dir, "cistern.db"),
		logger: newTestLogger(&logBuf),
	}
	s.checkHungDrought()

	if !strings.Contains(logBuf.String(), "hung") {
		t.Errorf("expected warning about hung drought, got log: %s", logBuf.String())
	}
}

// TestCheckHungDrought_WhenDroughtRunningLessThan5m_NoWarning verifies that a drought
// goroutine running for less than 5 minutes does not emit a warning.
func TestCheckHungDrought_WhenDroughtRunningLessThan5m_NoWarning(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Now().UTC().Add(-2 * time.Minute)
	hf := HealthFile{
		LastTickAt:       time.Now().UTC(),
		PollIntervalSec:  10,
		DroughtRunning:   true,
		DroughtStartedAt: &startedAt,
	}
	b, _ := json.Marshal(hf)
	os.WriteFile(filepath.Join(dir, "castellarius.health"), b, 0o644) //nolint:errcheck

	var logBuf bytes.Buffer
	s := &Castellarius{
		dbPath: filepath.Join(dir, "cistern.db"),
		logger: newTestLogger(&logBuf),
	}
	s.checkHungDrought()

	if strings.Contains(logBuf.String(), "hung") {
		t.Errorf("unexpected warning for drought under 5m: %s", logBuf.String())
	}
}

// TestCheckHungDrought_WhenDroughtNotRunning_NoWarning verifies that when
// droughtRunning is false in the health file, no warning is emitted.
func TestCheckHungDrought_WhenDroughtNotRunning_NoWarning(t *testing.T) {
	dir := t.TempDir()
	hf := HealthFile{
		LastTickAt:      time.Now().UTC(),
		PollIntervalSec: 10,
		DroughtRunning:  false,
	}
	b, _ := json.Marshal(hf)
	os.WriteFile(filepath.Join(dir, "castellarius.health"), b, 0o644) //nolint:errcheck

	var logBuf bytes.Buffer
	s := &Castellarius{
		dbPath: filepath.Join(dir, "cistern.db"),
		logger: newTestLogger(&logBuf),
	}
	s.checkHungDrought()

	if strings.Contains(logBuf.String(), "hung") {
		t.Errorf("unexpected warning when drought not running: %s", logBuf.String())
	}
}

// TestCheckHungDrought_WhenHealthFileMissing_NoWarning verifies that a missing health
// file does not cause a warning or panic.
func TestCheckHungDrought_WhenHealthFileMissing_NoWarning(t *testing.T) {
	dir := t.TempDir()
	var logBuf bytes.Buffer
	s := &Castellarius{
		dbPath: filepath.Join(dir, "cistern.db"),
		logger: newTestLogger(&logBuf),
	}
	s.checkHungDrought()

	if strings.Contains(logBuf.String(), "hung") {
		t.Errorf("unexpected warning for missing health file: %s", logBuf.String())
	}
}

// TestCheckHungDrought_WhenEmptyDBPath_NoWarning verifies that an empty dbPath
// is handled gracefully without panic.
func TestCheckHungDrought_WhenEmptyDBPath_NoWarning(t *testing.T) {
	var logBuf bytes.Buffer
	s := &Castellarius{
		dbPath: "",
		logger: newTestLogger(&logBuf),
	}
	s.checkHungDrought()

	if strings.Contains(logBuf.String(), "hung") {
		t.Errorf("unexpected warning for empty dbPath: %s", logBuf.String())
	}
}

// TestDispatch_SetsAssignedAqueduct verifies that dispatchRepo calls
// SetAssignedAqueduct with the operator name immediately after dispatching
// a droplet, so in_progress droplets always carry a non-empty assigned_aqueduct.
func TestDispatch_SetsAssignedAqueduct(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "bf-01", Title: "test item"}}
	client.items["bf-01"] = &cistern.Droplet{ID: "bf-01", Title: "test item"}

	runner := newMockRunner(client)
	sched := testScheduler(client, runner)
	sched.Tick(context.Background())

	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for runner call")
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	item, ok := client.items["bf-01"]
	if !ok {
		t.Fatal("item not found in mock client")
	}
	if item.AssignedAqueduct == "" {
		t.Error("AssignedAqueduct is empty after dispatch — SetAssignedAqueduct was not called")
	}
}
