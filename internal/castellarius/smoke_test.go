package castellarius

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/aqueduct"
)

// featureWorkflow returns the full 4-step feature pipeline matching
// the canonical workflow in testdata/valid_workflow.yaml.
func featureWorkflow() *aqueduct.Workflow {
	return &aqueduct.Workflow{
		Name: "feature",
		Cataractae: []aqueduct.WorkflowCataracta{
			{
				Name:           "implement",
				Type:           aqueduct.CataractaTypeAgent,
				Identity: "implementer",
				Context:        aqueduct.ContextFullCodebase,
				TimeoutMinutes: 30,
				OnPass:         "review",
				OnFail:         "blocked",
			},
			{
				Name:       "review",
				Type:       aqueduct.CataractaTypeAgent,
				Identity: "reviewer",
				Context:    aqueduct.ContextDiffOnly,
				OnPass:     "qa",
				OnRecirculate: "implement",
				OnEscalate: "human",
			},
			{
				Name:     "qa",
				Type:     aqueduct.CataractaTypeAgent,
				Identity: "qa",
				Context:  aqueduct.ContextFullCodebase,
				OnPass:   "delivery",
				OnFail:   "implement",
			},
			{
				Name:          "delivery",
				Type:          aqueduct.CataractaTypeAgent,
				Identity:      "delivery",
				OnPass:        "done",
				OnRecirculate: "implement",
				OnEscalate:    "human",
			},
		},
	}
}

// --- pipeline-aware mocks ---

// pipelineClient tracks a single item through the entire workflow,
// re-presenting it to GetReady with updated state until it reaches
// a terminal state. Unlike the queue-based mockClient, this simulates
// an item that persists in the work queue until completion.
type pipelineClient struct {
	mu        sync.Mutex
	item      cistern.Droplet
	stepLog   []string       // every Assign call in order
	attached  []attachedNote // notes attached by steps
	notes     []cistern.CataractaNote
	escalated string
	attempts  map[string]int
	terminal  bool
}

func newPipelineClient(item cistern.Droplet) *pipelineClient {
	if item.Status == "" {
		item.Status = "open"
	}
	return &pipelineClient{
		item:     item,
		attempts: make(map[string]int),
	}
}

// GetReady returns the item only when it is open and awaiting dispatch.
func (c *pipelineClient) GetReady(repo string) (*cistern.Droplet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal || c.item.Status != "open" {
		return nil, nil
	}
	item := c.item
	return &item, nil
}

func (c *pipelineClient) Assign(id, worker, step string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stepLog = append(c.stepLog, step)
	c.item.CurrentCataracta = step
	c.item.Outcome = "" // always clear outcome on (re)assign
	if worker != "" {
		c.item.Status = "in_progress"
		c.item.Assignee = worker
	} else {
		c.item.Status = "open"
		c.item.Assignee = ""
	}
	return nil
}

// SetOutcome is called by the mock runner to signal step completion.
// The observe phase reads this on the next Tick to route the item.
func (c *pipelineClient) SetOutcome(id, outcome string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.item.Outcome = outcome
	return nil
}

func (c *pipelineClient) IncrementAttempts(id string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts[id]++
	return c.attempts[id], nil
}

func (c *pipelineClient) AddNote(id, fromStep, notes string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attached = append(c.attached, attachedNote{id, fromStep, notes})
	c.notes = append(c.notes, cistern.CataractaNote{
		DropletID:     id,
		CataractaName: fromStep,
		Content:       notes,
	})
	return nil
}

func (c *pipelineClient) GetNotes(id string) ([]cistern.CataractaNote, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]cistern.CataractaNote, len(c.notes))
	copy(result, c.notes)
	return result, nil
}

func (c *pipelineClient) Escalate(id, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.escalated = reason
	c.terminal = true
	return nil
}

func (c *pipelineClient) CloseItem(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stepLog = append(c.stepLog, "done")
	c.terminal = true
	return nil
}

// List returns the item when its status matches — used by observeRepo to find
// in_progress items with outcomes ready for routing.
func (c *pipelineClient) List(repo, status string) ([]*cistern.Droplet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal || c.item.Status != status {
		return nil, nil
	}
	item := c.item
	return []*cistern.Droplet{&item}, nil
}

func (c *pipelineClient) Purge(olderThan time.Duration, dryRun bool) (int, error) {
	return 0, nil
}

func (c *pipelineClient) SetCataracta(id, cataracta string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.item.CurrentCataracta = cataracta
	return nil
}

// resultToOutcome converts an Outcome Result to the DB outcome string
// written by `ct droplet` commands.
func resultToOutcome(r Result) string {
	switch r {
	case ResultPass:
		return "pass"
	case ResultRecirculate:
		return "recirculate"
	case ResultEscalate:
		return "escalate"
	default: // ResultFail and anything unknown
		return "block"
	}
}

// stepSequenceRunner returns outcomes from a per-step queue, supporting
// multiple calls to the same step (e.g., revision loops). Spawn is non-blocking:
// it writes notes and outcome to the client then signals done.
type stepSequenceRunner struct {
	mu       sync.Mutex
	outcomes map[string][]*Outcome
	calls    []CataractaRequest
	done     chan struct{}
	client   *pipelineClient
}

func newStepSequenceRunner(client *pipelineClient, outcomes map[string][]*Outcome) *stepSequenceRunner {
	return &stepSequenceRunner{
		outcomes: outcomes,
		done:     make(chan struct{}, 32),
		client:   client,
	}
}

func (r *stepSequenceRunner) Spawn(_ context.Context, req CataractaRequest) error {
	r.mu.Lock()
	seq := r.outcomes[req.Step.Name]
	var o *Outcome
	if len(seq) == 0 {
		o = &Outcome{Result: ResultPass}
	} else {
		o = seq[0]
		r.outcomes[req.Step.Name] = seq[1:]
	}
	r.calls = append(r.calls, req)
	r.mu.Unlock()

	// Simulate agent writing notes, then signaling outcome via `ct droplet`.
	if o.Notes != "" {
		r.client.AddNote(req.Item.ID, req.Step.Name, o.Notes)
	}
	r.client.SetOutcome(req.Item.ID, resultToOutcome(o.Result))
	r.done <- struct{}{}
	return nil
}

func (r *stepSequenceRunner) waitStep(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for step execution")
	}
}

// --- smoke test helpers ---

func smokeConfig() aqueduct.AqueductConfig {
	return aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{
				Name:       "cistern",
				Cataractae: 1,
				Names:      []string{"smoker"},
				Prefix:     "ct",
			},
		},
		MaxCataractae: 1,
	}
}

func smokeScheduler(client CisternClient, runner CataractaRunner) *Castellarius {
	config := smokeConfig()
	workflows := map[string]*aqueduct.Workflow{"cistern": featureWorkflow()}
	clients := map[string]CisternClient{"cistern": client}
	return NewFromParts(config, workflows, clients, runner)
}

// advanceStep runs one scheduler tick (observe previous outcome + dispatch current
// step), waits for the step's Spawn to complete (which writes the outcome to the
// DB), then sleeps to let goroutines settle.
//
// After the last advanceStep call, callers must run one additional Tick to let
// the observe phase route the final step's outcome to its terminal state.
func advanceStep(t *testing.T, sched *Castellarius, runner *stepSequenceRunner) {
	t.Helper()
	sched.Tick(context.Background())
	runner.waitStep(t)
	time.Sleep(50 * time.Millisecond)
}

// --- smoke tests ---

// TestSmoke_FeatureWorkflow_HappyPath drives an item through the complete
// feature pipeline: implement → review → qa → delivery → done.
// Verifies step routing, context levels, notes attachment, and terminal state.
func TestSmoke_FeatureWorkflow_HappyPath(t *testing.T) {
	client := newPipelineClient(cistern.Droplet{
		ID:          "bf-smoke-1",
		Title:       "Smoke test: add trivial comment",
		Description: "Add a test comment to verify the pipeline end-to-end",
	})

	runner := newStepSequenceRunner(client, map[string][]*Outcome{
		"implement": {{Result: ResultPass, Notes: "added comment in main.go"}},
		"review":    {{Result: ResultPass, Notes: "diff clean, no issues found"}},
		"qa":        {{Result: ResultPass, Notes: "all tests pass (go test ./...)"}},
		"delivery": {{Result: ResultPass, Notes: "PR delivered to main"}},
	})

	sched := smokeScheduler(client, runner)

	// 4 dispatch ticks (one per step), then one final observe tick.
	for i := 0; i < 4; i++ {
		advanceStep(t, sched, runner)
	}
	sched.Tick(context.Background()) // observe delivery → done
	time.Sleep(50 * time.Millisecond)

	// --- verify final state ---

	client.mu.Lock()
	defer client.mu.Unlock()

	if !client.terminal {
		t.Fatal("item should have reached terminal state")
	}

	// Each step: Assign(id, worker, step) + Assign(id, "", next).
	// CloseItem appends "done". Pattern: dispatch, route-to-next, dispatch-next, ...
	wantLog := []string{
		"implement", "review",
		"review", "qa",
		"qa", "delivery",
		"delivery", "done",
	}
	if len(client.stepLog) != len(wantLog) {
		t.Fatalf("step log = %v (len %d), want %v (len %d)",
			client.stepLog, len(client.stepLog), wantLog, len(wantLog))
	}
	for i, want := range wantLog {
		if client.stepLog[i] != want {
			t.Errorf("step log[%d] = %q, want %q", i, client.stepLog[i], want)
		}
	}

	// All 4 steps should have been executed with correct context levels.
	runner.mu.Lock()
	defer runner.mu.Unlock()

	if len(runner.calls) != 4 {
		t.Fatalf("expected 4 runner calls, got %d", len(runner.calls))
	}

	wantSteps := []struct {
		name    string
		context aqueduct.ContextLevel
		role    string
	}{
		{"implement", aqueduct.ContextFullCodebase, "implementer"},
		{"review", aqueduct.ContextDiffOnly, "reviewer"},
		{"qa", aqueduct.ContextFullCodebase, "qa"},
		{"delivery", "", "delivery"},
	}
	for i, want := range wantSteps {
		call := runner.calls[i]
		if call.Step.Name != want.name {
			t.Errorf("call[%d].Step.Name = %q, want %q", i, call.Step.Name, want.name)
		}
		if call.Step.Context != want.context {
			t.Errorf("call[%d].Step.Context = %q, want %q", i, call.Step.Context, want.context)
		}
		if call.Step.Identity != want.role {
			t.Errorf("call[%d].Step.Identity = %q, want %q", i, call.Step.Identity, want.role)
		}
	}

	// Notes from each step should be attached (written by Spawn).
	if len(client.attached) != 4 {
		t.Fatalf("expected 4 attached notes, got %d", len(client.attached))
	}
	noteSteps := []string{"implement", "review", "qa", "delivery"}
	for i, step := range noteSteps {
		if client.attached[i].fromStep != step {
			t.Errorf("attached[%d].fromStep = %q, want %q", i, client.attached[i].fromStep, step)
		}
	}

	// No escalation.
	if client.escalated != "" {
		t.Errorf("unexpected escalation: %s", client.escalated)
	}
}

// TestSmoke_FeatureWorkflow_RecirculateLoop tests the review→implement
// recirculate loop: review sends "recirculate" → item returns to implement →
// second attempt passes review → continues to qa → delivery → done.
func TestSmoke_FeatureWorkflow_RecirculateLoop(t *testing.T) {
	client := newPipelineClient(cistern.Droplet{
		ID:    "bf-smoke-2",
		Title: "Smoke test: revision loop",
	})

	runner := newStepSequenceRunner(client, map[string][]*Outcome{
		"implement": {
			{Result: ResultPass, Notes: "first implementation"},
			{Result: ResultPass, Notes: "addressed review feedback"},
		},
		"review": {
			{Result: ResultRecirculate, Notes: "missing error handling on line 42"},
			{Result: ResultPass, Notes: "recirculate looks good"},
		},
		"qa":    {{Result: ResultPass, Notes: "tests pass"}},
		"delivery": {{Result: ResultPass, Notes: "delivered"}},
	})

	sched := smokeScheduler(client, runner)

	// 6 dispatches: implement, review(revision), implement, review(pass), qa, delivery
	for i := 0; i < 6; i++ {
		advanceStep(t, sched, runner)
	}
	sched.Tick(context.Background()) // observe delivery → done
	time.Sleep(50 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	if !client.terminal {
		t.Fatal("item should have reached terminal state")
	}

	// Step log for the revision loop.
	wantLog := []string{
		"implement", "review",    // 1st implement → review
		"review", "implement",    // review(recirculate) → implement
		"implement", "review",    // 2nd implement → review
		"review", "qa",           // review(pass) → qa
		"qa", "delivery",            // qa → delivery
		"delivery", "done",          // delivery → done
	}
	if len(client.stepLog) != len(wantLog) {
		t.Fatalf("step log = %v (len %d), want len %d",
			client.stepLog, len(client.stepLog), len(wantLog))
	}
	for i, want := range wantLog {
		if client.stepLog[i] != want {
			t.Errorf("step log[%d] = %q, want %q", i, client.stepLog[i], want)
		}
	}

	// Runner should have been called 6 times.
	runner.mu.Lock()
	defer runner.mu.Unlock()

	if len(runner.calls) != 6 {
		t.Fatalf("expected 6 runner calls, got %d", len(runner.calls))
	}

	// Verify the second review call received prior notes from all earlier steps.
	// At that point: implement(1st), review(1st), implement(2nd) = 3 notes.
	if len(runner.calls[3].Notes) < 3 {
		t.Errorf("second review call should have >= 3 prior notes, got %d",
			len(runner.calls[3].Notes))
	}
}

// TestSmoke_NotesForwarding verifies that each step receives accumulated
// notes from all prior steps via context forwarding.
func TestSmoke_NotesForwarding(t *testing.T) {
	client := newPipelineClient(cistern.Droplet{
		ID:    "bf-smoke-3",
		Title: "Smoke test: notes forwarding",
	})

	runner := newStepSequenceRunner(client, map[string][]*Outcome{
		"implement": {{Result: ResultPass, Notes: "impl: wrote the feature"}},
		"review":    {{Result: ResultPass, Notes: "review: code is clean"}},
		"qa":        {{Result: ResultPass, Notes: "qa: 42 tests pass"}},
		"delivery": {{Result: ResultPass, Notes: "delivery: PR merged"}},
	})

	sched := smokeScheduler(client, runner)
	for i := 0; i < 4; i++ {
		advanceStep(t, sched, runner)
	}
	sched.Tick(context.Background()) // observe delivery → done
	time.Sleep(50 * time.Millisecond)

	runner.mu.Lock()
	defer runner.mu.Unlock()

	// implement (step 0): no prior notes.
	if len(runner.calls[0].Notes) != 0 {
		t.Errorf("implement should have 0 prior notes, got %d", len(runner.calls[0].Notes))
	}

	// review (step 1): 1 note from implement.
	if len(runner.calls[1].Notes) != 1 {
		t.Errorf("review should have 1 prior note, got %d", len(runner.calls[1].Notes))
	} else if runner.calls[1].Notes[0].CataractaName != "implement" {
		t.Errorf("review note[0].CataractaName = %q, want %q",
			runner.calls[1].Notes[0].CataractaName, "implement")
	}

	// qa (step 2): 2 notes (implement + review).
	if len(runner.calls[2].Notes) != 2 {
		t.Errorf("qa should have 2 prior notes, got %d", len(runner.calls[2].Notes))
	}

	// delivery (step 3): 3 notes (implement + review + qa).
	if len(runner.calls[3].Notes) != 3 {
		t.Errorf("delivery should have 3 prior notes, got %d", len(runner.calls[3].Notes))
	}
}

// TestSmoke_QAFailReturnsToImplement verifies that a QA failure routes
// back to the implement step (not to blocked).
func TestSmoke_QAFailReturnsToImplement(t *testing.T) {
	client := newPipelineClient(cistern.Droplet{
		ID:    "bf-smoke-4",
		Title: "Smoke test: QA failure loop",
	})

	runner := newStepSequenceRunner(client, map[string][]*Outcome{
		"implement": {
			{Result: ResultPass, Notes: "first impl"},
			{Result: ResultPass, Notes: "fixed failing tests"},
		},
		"review": {
			{Result: ResultPass, Notes: "looks good"},
			{Result: ResultPass, Notes: "still good"},
		},
		"qa": {
			{Result: ResultFail, Notes: "TestFoo failed: expected 42, got 0"},
			{Result: ResultPass, Notes: "all tests pass now"},
		},
		"delivery": {{Result: ResultPass, Notes: "delivered"}},
	})

	sched := smokeScheduler(client, runner)

	// implement → review → qa(fail) → implement → review → qa(pass) → delivery
	// That's 7 dispatches.
	for i := 0; i < 7; i++ {
		advanceStep(t, sched, runner)
	}
	sched.Tick(context.Background()) // observe delivery → done
	time.Sleep(50 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()

	if !client.terminal {
		t.Fatal("item should have reached terminal state")
	}

	// Verify qa failure routed back to implement (not blocked).
	wantLog := []string{
		"implement", "review",    // 1st implement → review
		"review", "qa",           // 1st review → qa
		"qa", "implement",        // qa(fail) → implement
		"implement", "review",    // 2nd implement → review
		"review", "qa",           // 2nd review → qa
		"qa", "delivery",            // qa(pass) → delivery
		"delivery", "done",          // delivery → done
	}
	if len(client.stepLog) != len(wantLog) {
		t.Fatalf("step log = %v (len %d), want len %d",
			client.stepLog, len(client.stepLog), len(wantLog))
	}
	for i, want := range wantLog {
		if client.stepLog[i] != want {
			t.Errorf("step log[%d] = %q, want %q", i, client.stepLog[i], want)
		}
	}
}
