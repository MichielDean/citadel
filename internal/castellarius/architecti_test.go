package castellarius

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
)

// pooledDroplet creates a droplet in pooled state whose UpdatedAt is updatedAgo in the past.
func pooledDroplet(id string, updatedAgo time.Duration) *cistern.Droplet {
	return &cistern.Droplet{
		ID:        id,
		Repo:      "test-repo",
		Status:    "pooled",
		UpdatedAt: time.Now().Add(-updatedAgo),
	}
}

// testSchedulerWithArchitecti returns a Castellarius configured for architecti tests.
// It injects a no-op exec function so tests don't need a real claude or system prompt.
func testSchedulerWithArchitecti(client *mockClient) *Castellarius {
	s := testScheduler(client, newMockRunner(client))
	// Default exec: return empty array (no actions).
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("[]"), nil
	}
	s.restartCastellariusFn = func() error { return nil }
	return s
}

// writeTestHealthFile writes a HealthFile JSON to {dir}/castellarius.health for testing.
func writeTestHealthFile(t *testing.T, dir string, hf HealthFile) {
	t.Helper()
	path := dir + "/castellarius.health"
	b, err := json.Marshal(hf)
	if err != nil {
		t.Fatalf("marshal health file: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write health file: %v", err)
	}
}

// --- Action dispatcher tests ---

func TestArchitectiAction_Restart_ResetsDropletToNamedCataractae(t *testing.T) {
	// Given: a restart action for d-001 → implement
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	repoByDroplet := map[string]string{"d-001": "test-repo"}
	action := ArchitectiAction{
		Action:     "restart",
		DropletID:  "d-001",
		Cataractae: "implement",
		Reason:     "transient failure",
	}

	// When: architectiRestart is called directly
	err := s.architectiRestart(action, repoByDroplet)

	// Then: Assign was called with the correct step
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	step := client.steps["d-001"]
	client.mu.Unlock()
	if step != "implement" {
		t.Errorf("step = %q, want %q", step, "implement")
	}
}

func TestArchitectiAction_Restart_MissingCataractae_ReturnsError(t *testing.T) {
	// Given: restart action with no cataractae field
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	repoByDroplet := map[string]string{"d-001": "test-repo"}
	action := ArchitectiAction{
		Action:    "restart",
		DropletID: "d-001",
		Reason:    "missing cataractae",
	}

	// When: architectiRestart is called directly
	err := s.architectiRestart(action, repoByDroplet)

	// Then: error returned; Assign never called
	if err == nil {
		t.Fatal("expected error for missing cataractae, got nil")
	}
	client.mu.Lock()
	assigns := client.assignCalls
	client.mu.Unlock()
	if assigns != 0 {
		t.Errorf("assignCalls = %d, want 0 (missing cataractae must be rejected)", assigns)
	}
}

func TestArchitectiAction_RestartOrEscalate_PriorRestartNote_EscalatesToCancelAndFile(t *testing.T) {
	// Given: droplet that was already restarted by Architecti (prior restart note exists).
	// A second restart request must escalate: cancel + file, not restart.
	client := newMockClient()
	droplet := pooledDroplet("d-001", 60*time.Minute)
	client.items["d-001"] = droplet
	client.notes["d-001"] = []cistern.CataractaeNote{
		{
			DropletID:      "d-001",
			CataractaeName: "architecti",
			Content:        "Architecti restart → implement: transient orphan",
			CreatedAt:      time.Now().Add(-25 * time.Hour),
		},
	}

	s := testSchedulerWithArchitecti(client)
	repoByDroplet := map[string]string{"d-001": "test-repo"}
	action := ArchitectiAction{
		Action:     "restart",
		DropletID:  "d-001",
		Cataractae: "implement",
		Reason:     "pooled again after restart",
	}

	// When: architectiRestartOrEscalate is called
	err := s.architectiRestartOrEscalate(action, repoByDroplet)

	// Then: no error, restart NOT executed, droplet cancelled, bug droplet filed
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	assigns := client.assignCalls
	cancelReason := client.cancelled["d-001"]
	filedCount := len(client.filed)
	client.mu.Unlock()

	if assigns != 0 {
		t.Errorf("assignCalls = %d, want 0 (restart must be escalated, not executed)", assigns)
	}
	if cancelReason == "" {
		t.Error("expected d-001 to be cancelled (escalation path), got empty reason")
	}
	if filedCount != 1 {
		t.Errorf("filed = %d, want 1 (bug droplet must be filed on escalation)", filedCount)
	}
}

func TestArchitectiAction_RestartOrEscalate_GetNotesFails_ProceedsWithRestart(t *testing.T) {
	// Given: GetNotes returns an error — fail-open: proceed with restart.
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	client.getNotesErr = errors.New("db read error")
	s := testSchedulerWithArchitecti(client)

	repoByDroplet := map[string]string{"d-001": "test-repo"}
	action := ArchitectiAction{
		Action:     "restart",
		DropletID:  "d-001",
		Cataractae: "implement",
		Reason:     "test",
	}

	// When: architectiRestartOrEscalate is called (GetNotes fails → skip escalation check)
	err := s.architectiRestartOrEscalate(action, repoByDroplet)

	// Then: restart proceeds (fail-open when notes are unreadable)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// getNotesErr also blocks AddNote — only check assignCalls to confirm restart attempted.
	client.mu.Lock()
	assigns := client.assignCalls
	client.mu.Unlock()
	if assigns != 1 {
		t.Errorf("assignCalls = %d, want 1 (restart should proceed when escalation note lookup fails)", assigns)
	}
}

func TestArchitectiAction_Cancel_CancelsDroplet(t *testing.T) {
	// Given: a cancel action for d-001
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	repoByDroplet := map[string]string{"d-001": "test-repo"}
	action := ArchitectiAction{
		Action:    "cancel",
		DropletID: "d-001",
		Reason:    "irrecoverable",
	}

	// When: architectiCancel is called
	err := s.architectiCancel(action, repoByDroplet)

	// Then: droplet is cancelled
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	cancelReason := client.cancelled["d-001"]
	client.mu.Unlock()
	if cancelReason == "" {
		t.Error("expected d-001 to be cancelled, but cancelled map is empty")
	}
}

func TestArchitectiAction_Cancel_MissingDropletID_ReturnsError(t *testing.T) {
	// Given: a cancel action with no droplet_id
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	action := ArchitectiAction{Action: "cancel", Reason: "no id"}

	// When: architectiCancel is called
	err := s.architectiCancel(action, map[string]string{})

	// Then: error returned
	if err == nil {
		t.Fatal("expected error for missing droplet_id, got nil")
	}
}

func TestArchitectiAction_File_CreatesNewDroplet(t *testing.T) {
	// Given: a file action for test-repo
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	action := ArchitectiAction{
		Action:      "file",
		Repo:        "test-repo",
		Title:       "Fix the thing",
		Description: "details",
		Complexity:  "standard",
		Reason:      "structural bug",
	}

	// When: architectiFile is called
	err := s.architectiFile(action)

	// Then: new droplet is filed with correct title
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	filed := client.filed
	client.mu.Unlock()
	if len(filed) != 1 {
		t.Fatalf("filed count = %d, want 1", len(filed))
	}
	if filed[0].Title != "Fix the thing" {
		t.Errorf("filed title = %q, want %q", filed[0].Title, "Fix the thing")
	}
}

func TestArchitectiAction_File_MissingRepo_ReturnsError(t *testing.T) {
	// Given: a file action with no repo
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	action := ArchitectiAction{Action: "file", Title: "something", Reason: "r"}

	// When: architectiFile is called
	err := s.architectiFile(action)

	// Then: error returned
	if err == nil {
		t.Fatal("expected error for missing repo, got nil")
	}
}

func TestArchitectiAction_Note_AddsNoteToDroplet(t *testing.T) {
	// Given: a note action for d-001
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	repoByDroplet := map[string]string{"d-001": "test-repo"}
	action := ArchitectiAction{
		Action:    "note",
		DropletID: "d-001",
		Body:      "looks like a known transient",
		Reason:    "r",
	}

	// When: architectiNote is called
	err := s.architectiNote(action, repoByDroplet)

	// Then: note added to the droplet
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	notes := client.notes["d-001"]
	client.mu.Unlock()
	var found bool
	for _, n := range notes {
		if n.CataractaeName == "architecti" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected note from 'architecti', found none")
	}
}

func TestArchitectiAction_RestartCastellarius_WhenSchedulerHung(t *testing.T) {
	// Given: health file shows scheduler has not ticked recently (hung)
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	tmpDir := t.TempDir()
	s.dbPath = tmpDir + "/cistern.db"
	s.pollInterval = 10 * time.Second
	// LastTickAt 60s ago > 5×10s = 50s threshold → scheduler is hung.
	hf := HealthFile{
		LastTickAt:      time.Now().Add(-60 * time.Second),
		PollIntervalSec: 10,
	}
	writeTestHealthFile(t, tmpDir, hf)

	var restartCalled int32
	s.restartCastellariusFn = func() error {
		atomic.AddInt32(&restartCalled, 1)
		return nil
	}

	// When: architectiRestartCastellarius is called
	err := s.architectiRestartCastellarius(ArchitectiAction{Reason: "scheduler appears hung"})

	// Then: restart is invoked
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&restartCalled); n != 1 {
		t.Errorf("restartCastellariusFn called %d times, want 1", n)
	}
}

func TestArchitectiAction_RestartCastellarius_SkipsWhenSchedulerHealthy(t *testing.T) {
	// Given: health file shows scheduler ticked recently (healthy)
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	tmpDir := t.TempDir()
	s.dbPath = tmpDir + "/cistern.db"
	s.pollInterval = 10 * time.Second
	hf := HealthFile{
		LastTickAt:      time.Now(), // just ticked
		PollIntervalSec: 10,
	}
	writeTestHealthFile(t, tmpDir, hf)

	var restartCalled int32
	s.restartCastellariusFn = func() error {
		atomic.AddInt32(&restartCalled, 1)
		return nil
	}

	// When: architectiRestartCastellarius is called
	err := s.architectiRestartCastellarius(ArchitectiAction{Reason: "just testing"})

	// Then: restart is NOT invoked (scheduler is healthy)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&restartCalled); n != 0 {
		t.Errorf("restartCastellariusFn called %d times, want 0 (scheduler healthy)", n)
	}
}

func TestArchitectiAction_RestartCastellarius_RefusesWhenDbPathEmpty(t *testing.T) {
	// Given: dbPath is empty — health file cannot be read (fail-closed)
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)
	// s.dbPath is "" by default

	var restartCalled int32
	s.restartCastellariusFn = func() error {
		atomic.AddInt32(&restartCalled, 1)
		return nil
	}

	// When: architectiRestartCastellarius is called
	err := s.architectiRestartCastellarius(ArchitectiAction{Reason: "test"})

	// Then: restart refused — cannot verify hung state without health file
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&restartCalled); n != 0 {
		t.Errorf("restartCastellariusFn called %d times, want 0 (fail-closed: no health file)", n)
	}
}

func TestArchitectiAction_RestartCastellarius_RefusesWhenHealthFileUnreadable(t *testing.T) {
	// Given: dbPath is set but the health file does not exist
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	tmpDir := t.TempDir()
	s.dbPath = tmpDir + "/cistern.db"
	// Deliberately do NOT write a health file — ReadHealthFile will fail.

	var restartCalled int32
	s.restartCastellariusFn = func() error {
		atomic.AddInt32(&restartCalled, 1)
		return nil
	}

	// When: architectiRestartCastellarius is called
	err := s.architectiRestartCastellarius(ArchitectiAction{Reason: "test"})

	// Then: restart refused — health file unreadable (fail-closed)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&restartCalled); n != 0 {
		t.Errorf("restartCastellariusFn called %d times, want 0 (fail-closed: health file unreadable)", n)
	}
}

// --- RunArchitectiAdHoc tests ---

func TestRunArchitectiAdHoc_DryRun_ReturnsSnapshotAndOutput_WithoutDispatching(t *testing.T) {
	// Given: dry-run mode, agent returns a restart action
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	agentOutput := `[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"test"}]`
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(agentOutput), nil
	}

	// When: RunArchitectiAdHoc is called with dryRun=true
	snapshot, rawOutput, actions, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		ArchitectiDefaultMaxFilesPerRun,
		true,
	)

	// Then: no error, snapshot non-empty, raw output matches, actions nil (not parsed in dry-run)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if snapshot == "" {
		t.Error("expected non-empty snapshot")
	}
	if string(rawOutput) != agentOutput {
		t.Errorf("rawOutput = %q, want %q", rawOutput, agentOutput)
	}
	if actions != nil {
		t.Errorf("actions = %v, want nil (dry-run must not parse)", actions)
	}
	// Then: no dispatch — Assign not called
	client.mu.Lock()
	assigns := client.assignCalls
	client.mu.Unlock()
	if assigns != 0 {
		t.Errorf("assignCalls = %d, want 0 (dry-run must not dispatch)", assigns)
	}
}

func TestRunArchitectiAdHoc_Normal_DispatchesActions(t *testing.T) {
	// Given: normal mode, agent returns a restart action for d-001
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"test"}]`), nil
	}

	// When: RunArchitectiAdHoc is called with dryRun=false
	snapshot, rawOutput, actions, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		ArchitectiDefaultMaxFilesPerRun,
		false,
	)

	// Then: no error, dispatch occurred, returned actions match dispatched actions
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if snapshot == "" {
		t.Error("expected non-empty snapshot")
	}
	if len(rawOutput) == 0 {
		t.Error("expected non-empty rawOutput")
	}
	if len(actions) != 1 || actions[0].Action != "restart" || actions[0].DropletID != "d-001" {
		t.Errorf("actions = %v, want [{restart d-001 implement test}]", actions)
	}
	client.mu.Lock()
	step := client.steps["d-001"]
	client.mu.Unlock()
	if step != "implement" {
		t.Errorf("step = %q, want %q (action was dispatched)", step, "implement")
	}
}

func TestRunArchitectiAdHoc_Normal_EmptyActions_NoDispatch(t *testing.T) {
	// Given: agent returns empty action array
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[]`), nil
	}

	_, _, actions, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		ArchitectiDefaultMaxFilesPerRun,
		false,
	)

	// Then: no error, no dispatch, nil actions
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if actions != nil {
		t.Errorf("actions = %v, want nil (empty actions list)", actions)
	}
	client.mu.Lock()
	assigns := client.assignCalls
	client.mu.Unlock()
	if assigns != 0 {
		t.Errorf("assignCalls = %d, want 0 (empty actions list)", assigns)
	}
}

func TestRunArchitectiAdHoc_ExecError_ReturnsError(t *testing.T) {
	// Given: exec fn returns an error
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	execErr := errors.New("session spawn failed")
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return nil, execErr
	}

	// When: RunArchitectiAdHoc is called
	_, _, _, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		ArchitectiDefaultMaxFilesPerRun,
		false,
	)

	// Then: error wraps the exec error
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "exec") {
		t.Errorf("expected error to mention exec, got: %v", err)
	}
	if !errors.Is(err, execErr) {
		t.Errorf("expected error to wrap execErr, got: %v", err)
	}
}

func TestRunArchitectiAdHoc_SnapshotContainsTriggerDropletID(t *testing.T) {
	// Given: synthetic trigger droplet with a known ID
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[]`), nil
	}

	trigger := &cistern.Droplet{
		ID:        "my-trigger-droplet",
		Status:    "pooled",
		UpdatedAt: time.Now().Add(-30 * time.Minute),
	}

	// When: RunArchitectiAdHoc is called
	snapshot, _, _, err := s.RunArchitectiAdHoc(
		context.Background(),
		trigger,
		ArchitectiDefaultMaxFilesPerRun,
		false,
	)

	// Then: snapshot includes the trigger droplet ID
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(snapshot, "my-trigger-droplet") {
		t.Errorf("snapshot does not contain trigger droplet ID; snapshot = %q", snapshot)
	}
}

func TestRunArchitectiAdHoc_Normal_MarkdownWrappedJSON_ReturnsParsedActions(t *testing.T) {
	// Given: LLM output wraps JSON in markdown code block (typical LLM output)
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	agentOutput := "Here are my proposed actions:\n\n```json\n" +
		`[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"pooled"}]` +
		"\n```\n"
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(agentOutput), nil
	}

	// When: RunArchitectiAdHoc dispatches (non-dry-run)
	_, _, actions, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		ArchitectiDefaultMaxFilesPerRun,
		false,
	)

	// Then: actions are parsed correctly despite markdown wrapping
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if actions[0].Action != "restart" || actions[0].DropletID != "d-001" {
		t.Errorf("actions[0] = %+v, want {action:restart droplet_id:d-001}", actions[0])
	}
}

func TestRunArchitectiAdHoc_ParseError_ReturnsError(t *testing.T) {
	// Given: exec fn returns plain text with no JSON array
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("The agent could not determine any actions at this time."), nil
	}

	// When: RunArchitectiAdHoc is called with dryRun=false
	_, _, _, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		ArchitectiDefaultMaxFilesPerRun,
		false,
	)

	// Then: error is non-nil and wraps 'parse'
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected error to mention parse, got: %v", err)
	}
}

func TestResolveArchitectiSystemPrompt_NotFound_ReturnsError(t *testing.T) {
	// Given: HOME points to a temp dir (no SYSTEM_PROMPT.md there),
	// and sandboxRoot also points to a temp dir with no SYSTEM_PROMPT.md.
	t.Setenv("HOME", t.TempDir())

	client := newMockClient()
	s := testScheduler(client, newMockRunner(client))
	s.sandboxRoot = t.TempDir()

	// When: resolveArchitectiSystemPrompt is called
	path, err := s.resolveArchitectiSystemPrompt()

	// Then: error is non-nil and mentions SYSTEM_PROMPT.md not found; path is empty
	if err == nil {
		t.Fatalf("expected error, got nil (path=%q)", path)
	}
	if !strings.Contains(err.Error(), "SYSTEM_PROMPT.md not found") {
		t.Errorf("error %q does not contain \"SYSTEM_PROMPT.md not found\"", err.Error())
	}
}

func TestRunArchitectiAdHoc_Normal_ReturnsFilteredActions_MaxFilesPerRun(t *testing.T) {
	// Given: LLM returns more file actions than maxFilesPerRun allows
	const maxFilesForTest = 3

	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	// Build output with 4 file actions; maxFilesForTest = 3
	agentOutput := `[` +
		`{"action":"file","repo":"test-repo","title":"t1","reason":"r1"},` +
		`{"action":"file","repo":"test-repo","title":"t2","reason":"r2"},` +
		`{"action":"file","repo":"test-repo","title":"t3","reason":"r3"},` +
		`{"action":"file","repo":"test-repo","title":"t4","reason":"r4"}` +
		`]`
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(agentOutput), nil
	}

	// When: RunArchitectiAdHoc dispatches with maxFilesForTest cap
	_, rawOutput, actions, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		maxFilesForTest,
		false,
	)

	// Then: rawOutput is unfiltered (4 actions), returned actions are filtered (≤maxFilesForTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rawOutput) == 0 {
		t.Fatal("expected non-empty rawOutput")
	}
	if len(actions) != maxFilesForTest {
		t.Errorf("len(actions) = %d, want %d (capped by MaxFilesPerRun; rawOutput had 4)", len(actions), maxFilesForTest)
	}
}

// --- buildArchitectiSnapshot tests ---

func TestBuildArchitectiSnapshot_CisternReference_SkillPresent_AppendsVerbatimContent(t *testing.T) {
	// Given: SKILL.md exists at the expected path under sandboxRoot
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	sandboxRoot := t.TempDir()
	s.sandboxRoot = sandboxRoot

	skillDir := filepath.Join(sandboxRoot, "cistern", "_primary", "openclaw", "cistern")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}
	skillContent := "# Cistern Skills\n\nUse ct droplet pass <id> to signal completion.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// When: snapshot is built
	snapshot, _ := s.buildArchitectiSnapshot(context.Background(), pooledDroplet("trigger", 1*time.Minute), ArchitectiDefaultMaxFilesPerRun)

	// Then: ## Cistern Reference heading is present
	if !strings.Contains(snapshot, "## Cistern Reference\n") {
		t.Errorf("snapshot missing '## Cistern Reference' heading; snapshot = %q", snapshot)
	}
	// Then: skill file content appears verbatim
	if !strings.Contains(snapshot, skillContent) {
		t.Errorf("snapshot missing skill file content; snapshot = %q", snapshot)
	}
	// Then: fallback text is NOT present
	if strings.Contains(snapshot, "(skill file unavailable)") {
		t.Errorf("snapshot contains fallback text despite skill file being present; snapshot = %q", snapshot)
	}
}

func TestBuildArchitectiSnapshot_CisternReference_SkillMissing_AppendsFallback(t *testing.T) {
	// Given: SKILL.md does not exist under sandboxRoot
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)
	s.sandboxRoot = t.TempDir() // empty dir — no SKILL.md at expected path

	// When: snapshot is built
	snapshot, _ := s.buildArchitectiSnapshot(context.Background(), pooledDroplet("trigger", 1*time.Minute), ArchitectiDefaultMaxFilesPerRun)

	// Then: ## Cistern Reference heading is present
	if !strings.Contains(snapshot, "## Cistern Reference\n") {
		t.Errorf("snapshot missing '## Cistern Reference' heading; snapshot = %q", snapshot)
	}
	// Then: fallback line is present
	if !strings.Contains(snapshot, "(skill file unavailable)") {
		t.Errorf("snapshot missing fallback text when skill file absent; snapshot = %q", snapshot)
	}
}
