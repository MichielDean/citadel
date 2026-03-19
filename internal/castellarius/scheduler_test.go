package castellarius

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/aqueduct"
)

// --- mocks ---

type mockClient struct {
	mu                  sync.Mutex
	readyItems          []*cistern.Droplet
	readyCalls          int
	steps               map[string]string              // id → current step (for assertions)
	items               map[string]*cistern.Droplet    // id → item (for List/SetOutcome)
	notes               map[string][]cistern.CataractaeNote
	escalated           map[string]string
	attached            []attachedNote
	closed              map[string]bool
	lastReviewedCommits map[string]string
}

type attachedNote struct {
	id, fromStep, notes string
}

func newMockClient() *mockClient {
	return &mockClient{
		steps:               make(map[string]string),
		items:               make(map[string]*cistern.Droplet),
		notes:               make(map[string][]cistern.CataractaeNote),
		escalated:           make(map[string]string),
		closed:              make(map[string]bool),
		lastReviewedCommits: make(map[string]string),
	}
}

func (m *mockClient) GetReady(repo string) (*cistern.Droplet, error) {
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
	m.steps[id] = step
	if item, ok := m.items[id]; ok {
		item.CurrentCataractae = step
		item.Assignee = worker
		item.Outcome = "" // clear outcome on advance
		if worker == "" {
			item.Status = "open"
		} else {
			item.Status = "in_progress"
		}
	}
	return nil
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
	return nil
}

func (m *mockClient) GetNotes(id string) ([]cistern.CataractaeNote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notes[id], nil
}

func (m *mockClient) Escalate(id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.escalated[id] = reason
	return nil
}

func (m *mockClient) CloseItem(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed[id] = true
	m.steps[id] = "done"
	return nil
}

func (m *mockClient) List(repo, status string) ([]*cistern.Droplet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
				OnFail: "blocked",
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
		OnFail:        "blocked",
		OnRecirculate: "implement",
		OnEscalate:    "human",
	}

	tests := []struct {
		result Result
		want   string
	}{
		{ResultPass, "review"},
		{ResultFail, "blocked"},
		{ResultRecirculate, "implement"},
		{ResultEscalate, "human"},
		{Result("unknown"), "blocked"},
	}

	for _, tt := range tests {
		got := route(step, tt.result)
		if got != tt.want {
			t.Errorf("route(%q) = %q, want %q", tt.result, got, tt.want)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	for _, name := range []string{"done", "blocked", "human", "escalate", "Done", "BLOCKED"} {
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
		ID:               "b1",
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
		ID:               "b1",
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

func TestTick_TerminalBlocked(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "b1", CurrentCataractae: "implement"},
	}

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "block" // block → ResultFail → OnFail = "blocked"

	sched := testScheduler(client, runner)
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out")
	}
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	if _, ok := client.escalated["b1"]; !ok {
		t.Error("expected item escalated for terminal 'blocked'")
	}
}

func TestTick_GlobalCap(t *testing.T) {
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
		MaxCataractae: 2,
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
	if total > 2 {
		t.Errorf("global cap violated: %d busy workers (cap=2)", total)
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
	// Item stays at "implement" — not advanced, not escalated.
	if client.steps["b1"] != "implement" {
		t.Errorf("expected step to remain 'implement' after crash, got %q", client.steps["b1"])
	}
	if _, ok := client.escalated["b1"]; ok {
		t.Error("should not escalate on crash — just requeue")
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

func TestTick_NoRoute(t *testing.T) {
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "b1"}}

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "recirculate" // implement has no OnRecirculate

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
	// implement step has no OnRecirculate → empty route → escalate.
	if _, ok := client.escalated["b1"]; !ok {
		t.Error("expected escalation when no route exists")
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

func TestMultiRepo_GlobalCapAcrossRepos(t *testing.T) {
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

	config := multiRepoConfig()
	config.MaxCataractae = 2 // Cap below total pool capacity (3)
	wf := testWorkflow()
	workflows := map[string]*aqueduct.Workflow{
		"ScaledTest": wf,
		"cistern":    wf,
	}
	sched := NewFromParts(config, workflows, clients, blocker)

	// Multiple ticks should never exceed global cap.
	for range 5 {
		sched.Tick(context.Background())
	}

	total := sched.totalBusy()
	if total > 2 {
		t.Errorf("global cap violated: %d busy workers across repos (cap=2)", total)
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

	// Tick with no work — both repos should be polled.
	sched.Tick(context.Background())

	stClient.mu.Lock()
	stCalls := stClient.readyCalls
	stClient.mu.Unlock()

	bfClient.mu.Lock()
	bfCalls := bfClient.readyCalls
	bfClient.mu.Unlock()

	if stCalls != 1 {
		t.Errorf("expected ScaledTest polled once, got %d", stCalls)
	}
	if bfCalls != 1 {
		t.Errorf("expected cistern polled once, got %d", bfCalls)
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

// --- complexity skip tests ---

func complexityWorkflow() *aqueduct.Workflow {
	return &aqueduct.Workflow{
		Name: "feature",
		Cataractae: []aqueduct.WorkflowCataractae{
			{Name: "implement", Type: aqueduct.CataractaeTypeAgent, OnPass: "adversarial-review", OnFail: "blocked"},
			{Name: "adversarial-review", Type: aqueduct.CataractaeTypeAgent, SkipFor: []int{1}, OnPass: "qa", OnFail: "implement", OnRecirculate: "implement"},
			{Name: "qa", Type: aqueduct.CataractaeTypeAgent, SkipFor: []int{1, 2}, OnPass: "docs", OnFail: "implement"},
			{Name: "docs", Type: aqueduct.CataractaeTypeAgent, SkipFor: []int{1}, OnPass: "delivery", OnFail: "implement", OnRecirculate: "implement", OnEscalate: "human"},
			{Name: "delivery", Type: aqueduct.CataractaeTypeAgent, OnPass: "done", OnRecirculate: "implement", OnEscalate: "human"},
		},
		Complexity: aqueduct.ComplexityConfig{
			Trivial:  aqueduct.ComplexityLevel{Level: 1, SkipCataractae: []string{"adversarial-review", "qa", "docs"}},
			Standard: aqueduct.ComplexityLevel{Level: 2, SkipCataractae: []string{"qa"}},
			Full:     aqueduct.ComplexityLevel{Level: 3, SkipCataractae: []string{}},
			Critical: aqueduct.ComplexityLevel{Level: 4, SkipCataractae: []string{}, RequireHuman: true},
		},
	}
}

func TestAdvanceSkipped_TrivialSkipsReviewAndQA(t *testing.T) {
	wf := complexityWorkflow()
	skipSteps := wf.Complexity.SkipCataractaeForLevel(1) // ["adversarial-review", "qa"]

	// After implement passes, next is adversarial-review — should skip to delivery.
	got := advanceSkippedCataractae("adversarial-review", wf, skipSteps)
	if got != "delivery" {
		t.Errorf("advanceSkippedCataractae(adversarial-review, trivial) = %q, want %q", got, "delivery")
	}
}

func TestAdvanceSkipped_StandardSkipsQA(t *testing.T) {
	wf := complexityWorkflow()
	skipSteps := wf.Complexity.SkipCataractaeForLevel(2) // ["qa"]

	// adversarial-review passes → qa (skipped) → docs (not skipped for standard).
	got := advanceSkippedCataractae("qa", wf, skipSteps)
	if got != "docs" {
		t.Errorf("advanceSkippedCataractae(qa, standard) = %q, want %q", got, "docs")
	}

	// adversarial-review itself is not skipped.
	got = advanceSkippedCataractae("adversarial-review", wf, skipSteps)
	if got != "adversarial-review" {
		t.Errorf("advanceSkippedCataractae(adversarial-review, standard) = %q, want %q", got, "adversarial-review")
	}
}

func TestAdvanceSkipped_FullSkipsNothing(t *testing.T) {
	wf := complexityWorkflow()
	skipSteps := wf.Complexity.SkipCataractaeForLevel(3)

	got := advanceSkippedCataractae("adversarial-review", wf, skipSteps)
	if got != "adversarial-review" {
		t.Errorf("advanceSkippedCataractae(adversarial-review, full) = %q, want %q", got, "adversarial-review")
	}
}

func TestAdvanceSkipped_NoSkipList(t *testing.T) {
	wf := complexityWorkflow()
	got := advanceSkippedCataractae("adversarial-review", wf, nil)
	if got != "adversarial-review" {
		t.Errorf("advanceSkippedCataractae with nil skip = %q, want %q", got, "adversarial-review")
	}
}

func TestComplexity_CriticalHumanGateBeforeMerge(t *testing.T) {
	wf := complexityWorkflow()
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "crit-1", CurrentCataractae: "docs", Complexity: 4},
	}

	runner := newMockRunner(client)
	// default outcome "pass"; docs.OnPass = "delivery" → critical → "human" → escalate

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
	// docs passes → next is delivery → critical requires human gate → should escalate.
	if _, ok := client.escalated["crit-1"]; !ok {
		t.Errorf("expected critical droplet escalated to human before delivery, got step %q", client.steps["crit-1"])
	}
}

func TestTick_TrivialDropSkipsReviewAndQA(t *testing.T) {
	wf := complexityWorkflow()
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "triv-1", Complexity: 1},
	}

	runner := newMockRunner(client)
	// default outcome "pass"; implement.OnPass = "adversarial-review"
	// trivial skips adversarial-review, qa, and docs → goes to delivery

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
	// implement passes → adversarial-review skipped → qa skipped → docs skipped → should go to delivery.
	if client.steps["triv-1"] != "delivery" {
		t.Errorf("expected trivial droplet at delivery, got %q", client.steps["triv-1"])
	}
}

func TestComplexity_HumanGateSetsCurrentCataractae(t *testing.T) {
	wf := complexityWorkflow()
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{
		{ID: "crit-2", CurrentCataractae: "docs", Complexity: 4},
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
	// Human gate: escalated and current_cataractae must be set to "human".
	if _, ok := client.escalated["crit-2"]; !ok {
		t.Errorf("expected critical droplet escalated, not found in escalated map")
	}
	if client.steps["crit-2"] != "human" {
		t.Errorf("expected current_cataractae='human', got %q", client.steps["crit-2"])
	}
}

func TestParseOutcome(t *testing.T) {
	tests := []struct {
		outcome         string
		wantResult      Result
		wantRecircTo    string
	}{
		{"pass", ResultPass, ""},
		{"recirculate", ResultRecirculate, ""},
		{"recirculate:implement", ResultRecirculate, "implement"},
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

// TestObserve_HeadNotAdvanced verifies that when implement passes but HEAD
// has not advanced since the last review, the scheduler auto-recirculates.
func TestObserve_HeadNotAdvanced(t *testing.T) {
	sandboxRoot := t.TempDir()

	client := newMockClient()
	item := &cistern.Droplet{
		ID:               "ph-1",
		CurrentCataractae: "implement",
		Assignee:         "alpha",
		Status:           "in_progress",
		Outcome:          "pass",
	}
	client.items["ph-1"] = item

	// Create a real git sandbox so sandboxHead() works.
	sandboxDir := filepath.Join(sandboxRoot, "test-repo", "alpha")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	headHash := makeGitSandbox(t, sandboxDir)

	// Record the same hash as the last reviewed commit — HEAD has not advanced.
	client.lastReviewedCommits["ph-1"] = headHash

	runner := newMockRunner(client)
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner,
		WithSandboxRoot(sandboxRoot))

	// Observe tick should detect the phantom commit and recirculate.
	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	// Item must stay at implement, not advance to review.
	if client.steps["ph-1"] != "implement" {
		t.Errorf("expected item to stay at 'implement', got %q", client.steps["ph-1"])
	}
	// A note must have been attached.
	hasNote := false
	for _, n := range client.attached {
		if n.id == "ph-1" && strings.Contains(n.notes, "HEAD has not advanced") {
			hasNote = true
		}
	}
	if !hasNote {
		t.Errorf("expected phantom commit note to be attached, got: %v", client.attached)
	}
}

// TestObserve_HeadAdvanced verifies that when implement passes and HEAD has
// advanced since the last review, routing proceeds normally to review.
func TestObserve_HeadAdvanced(t *testing.T) {
	sandboxRoot := t.TempDir()

	client := newMockClient()
	item := &cistern.Droplet{
		ID:               "ph-2",
		CurrentCataractae: "implement",
		Assignee:         "alpha",
		Status:           "in_progress",
		Outcome:          "pass",
	}
	client.items["ph-2"] = item

	// Create a real git sandbox and make an additional commit.
	sandboxDir := filepath.Join(sandboxRoot, "test-repo", "alpha")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldHash := makeGitSandbox(t, sandboxDir)

	// Make a new commit so HEAD advances.
	if err := os.WriteFile(filepath.Join(sandboxDir, "feature.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "feat: add feature"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = sandboxDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	// Record the OLD hash as last reviewed — HEAD has now advanced past it.
	client.lastReviewedCommits["ph-2"] = oldHash

	runner := newMockRunner(client)
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	sched := NewFromParts(config, workflows, clients, runner,
		WithSandboxRoot(sandboxRoot))

	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	// Item must have advanced to review.
	if client.steps["ph-2"] != "review" {
		t.Errorf("expected item at 'review', got %q", client.steps["ph-2"])
	}
}

// TestObserve_FirstPass verifies that when LastReviewedCommit is empty
// (first implement pass), the scheduler routes normally without any HEAD check.
func TestObserve_FirstPass(t *testing.T) {
	client := newMockClient()
	item := &cistern.Droplet{
		ID:               "ph-3",
		CurrentCataractae: "implement",
		Assignee:         "alpha",
		Status:           "in_progress",
		Outcome:          "pass",
	}
	client.items["ph-3"] = item
	// lastReviewedCommits["ph-3"] is empty — first pass.

	runner := newMockRunner(client)
	sched := testScheduler(client, runner)

	sched.Tick(context.Background())
	time.Sleep(10 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	// First pass: route normally to review.
	if client.steps["ph-3"] != "review" {
		t.Errorf("expected first pass to route to 'review', got %q", client.steps["ph-3"])
	}
}
