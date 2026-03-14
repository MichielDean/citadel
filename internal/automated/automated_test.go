package automated

import (
	"context"
	"errors"
	"testing"
	"time"
)

// recorder captures exec calls and replays canned responses.
type recorder struct {
	calls     []call
	responses []response
	idx       int
}

type call struct {
	dir  string
	name string
	args []string
}

type response struct {
	out []byte
	err error
}

func (r *recorder) exec(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, call{dir: dir, name: name, args: args})
	if r.idx >= len(r.responses) {
		return nil, errors.New("unexpected call")
	}
	resp := r.responses[r.idx]
	r.idx++
	return resp.out, resp.err
}

func newExecutor(r *recorder) *Executor {
	return &Executor{ExecFn: r.exec}
}

// --- PRCreate tests ---

func TestPRCreate_Success(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("feature-branch\n")},                              // git branch
		{out: []byte("https://github.com/org/repo/pull/42\n")},         // gh pr create
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), BeadContext{
		ID:         "bf-123",
		Title:      "Fix widget",
		Description: "Fixes the broken widget",
		BaseBranch: "main",
		WorkDir:    "/tmp/repo",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass, got %s: %s", out.Result, out.Notes)
	}
	if out.Annotations[AnnoPRURL] != "https://github.com/org/repo/pull/42" {
		t.Errorf("want pr_url annotation, got %v", out.Annotations)
	}
	if out.Annotations[AnnoPRNumber] != "42" {
		t.Errorf("want pr_number=42, got %q", out.Annotations[AnnoPRNumber])
	}

	// Verify gh pr create was called with correct args.
	ghCall := rec.calls[1]
	if ghCall.name != "gh" {
		t.Errorf("want gh command, got %q", ghCall.name)
	}
	wantArgs := []string{"pr", "create", "--title", "Fix widget", "--body", "Fixes the broken widget", "--base", "main", "--head", "feature-branch"}
	if len(ghCall.args) != len(wantArgs) {
		t.Fatalf("args mismatch: want %v, got %v", wantArgs, ghCall.args)
	}
	for i, a := range wantArgs {
		if ghCall.args[i] != a {
			t.Errorf("arg %d: want %q, got %q", i, a, ghCall.args[i])
		}
	}
}

func TestPRCreate_BranchProvided(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("https://github.com/org/repo/pull/7\n")}, // gh pr create (no git branch call)
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), BeadContext{
		ID:         "bf-456",
		Title:      "Add feature",
		Branch:     "my-branch",
		BaseBranch: "main",
		WorkDir:    "/tmp/repo",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass, got %s: %s", out.Result, out.Notes)
	}
	// Should have called gh directly (no git branch --show-current).
	if len(rec.calls) != 1 {
		t.Fatalf("want 1 call (gh only), got %d", len(rec.calls))
	}
	if rec.calls[0].name != "gh" {
		t.Errorf("want gh, got %q", rec.calls[0].name)
	}
}

func TestPRCreate_GhFails(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("feature\n")},                                    // git branch
		{out: []byte("pull request already exists"), err: errors.New("exit 1")}, // gh pr create
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), BeadContext{
		ID:      "bf-789",
		WorkDir: "/tmp/repo",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultFail {
		t.Fatalf("want fail, got %s", out.Result)
	}
	if out.Notes == "" {
		t.Error("want notes with error details")
	}
}

func TestPRCreate_NoBranch(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("\n")}, // git branch returns empty
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), BeadContext{
		ID:      "bf-aaa",
		WorkDir: "/tmp/repo",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultFail {
		t.Fatalf("want fail, got %s", out.Result)
	}
}

func TestPRCreate_DefaultTitle(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("https://github.com/org/repo/pull/1\n")},
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), BeadContext{
		ID:     "bf-xyz",
		Branch: "my-branch",
		// Title and Description empty — should use defaults.
		WorkDir: "/tmp/repo",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass, got %s: %s", out.Result, out.Notes)
	}

	ghCall := rec.calls[0]
	// Title should be "bead bf-xyz".
	for i, a := range ghCall.args {
		if a == "--title" && i+1 < len(ghCall.args) {
			if ghCall.args[i+1] != "bead bf-xyz" {
				t.Errorf("want default title 'bead bf-xyz', got %q", ghCall.args[i+1])
			}
		}
	}
}

// --- CIGate tests ---

func TestCIGate_AllPassImmediately(t *testing.T) {
	checksJSON := `[{"name":"build","bucket":"pass"},{"name":"lint","bucket":"pass"}]`
	rec := &recorder{responses: []response{
		{out: []byte(checksJSON)},
	}}

	e := newExecutor(rec)
	out, err := e.CIGate(context.Background(), BeadContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	}, time.Millisecond)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass, got %s: %s", out.Result, out.Notes)
	}
}

func TestCIGate_PendingThenPass(t *testing.T) {
	pending := `[{"name":"build","bucket":"pending"}]`
	passed := `[{"name":"build","bucket":"pass"}]`
	rec := &recorder{responses: []response{
		{out: []byte(pending)},
		{out: []byte(passed)},
	}}

	e := newExecutor(rec)
	out, err := e.CIGate(context.Background(), BeadContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	}, time.Millisecond)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass, got %s: %s", out.Result, out.Notes)
	}
	if len(rec.calls) != 2 {
		t.Errorf("want 2 poll calls, got %d", len(rec.calls))
	}
}

func TestCIGate_CheckFails(t *testing.T) {
	checksJSON := `[{"name":"build","bucket":"pass"},{"name":"test","bucket":"fail"}]`
	rec := &recorder{responses: []response{
		{out: []byte(checksJSON)},
	}}

	e := newExecutor(rec)
	out, err := e.CIGate(context.Background(), BeadContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	}, time.Millisecond)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultFail {
		t.Fatalf("want fail, got %s", out.Result)
	}
}

func TestCIGate_NoChecks(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("[]")},
	}}

	e := newExecutor(rec)
	out, err := e.CIGate(context.Background(), BeadContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	}, time.Millisecond)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass (no checks), got %s: %s", out.Result, out.Notes)
	}
}

func TestCIGate_NoPRURL(t *testing.T) {
	rec := &recorder{}

	e := newExecutor(rec)
	out, err := e.CIGate(context.Background(), BeadContext{
		WorkDir: "/tmp/repo",
	}, time.Millisecond)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultFail {
		t.Fatalf("want fail, got %s", out.Result)
	}
}

func TestCIGate_Timeout(t *testing.T) {
	pending := `[{"name":"build","bucket":"pending"}]`
	rec := &recorder{responses: []response{
		{out: []byte(pending)},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Already cancelled.

	e := newExecutor(rec)
	out, err := e.CIGate(ctx, BeadContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	}, time.Millisecond)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultFail {
		t.Fatalf("want fail (timeout), got %s", out.Result)
	}
}

func TestCIGate_SkippingCountsAsPass(t *testing.T) {
	checksJSON := `[{"name":"build","bucket":"pass"},{"name":"optional","bucket":"skipping"}]`
	rec := &recorder{responses: []response{
		{out: []byte(checksJSON)},
	}}

	e := newExecutor(rec)
	out, err := e.CIGate(context.Background(), BeadContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	}, time.Millisecond)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass, got %s: %s", out.Result, out.Notes)
	}
}

// --- Merge tests ---

func TestMerge_Success(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("Merged\n")},                // gh pr merge
		{out: []byte(`{"state":"MERGED"}`)},       // gh pr view
	}}

	e := newExecutor(rec)
	out, err := e.Merge(context.Background(), BeadContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass, got %s: %s", out.Result, out.Notes)
	}
}

func TestMerge_MergeFails(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("merge conflict"), err: errors.New("exit 1")},
	}}

	e := newExecutor(rec)
	out, err := e.Merge(context.Background(), BeadContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultFail {
		t.Fatalf("want fail, got %s", out.Result)
	}
}

func TestMerge_StateNotMerged(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("OK\n")},                   // gh pr merge
		{out: []byte(`{"state":"OPEN"}`)},        // gh pr view — still open
	}}

	e := newExecutor(rec)
	out, err := e.Merge(context.Background(), BeadContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultFail {
		t.Fatalf("want fail, got %s", out.Result)
	}
}

func TestMerge_NoPRURL(t *testing.T) {
	rec := &recorder{}

	e := newExecutor(rec)
	out, err := e.Merge(context.Background(), BeadContext{
		WorkDir: "/tmp/repo",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultFail {
		t.Fatalf("want fail, got %s", out.Result)
	}
}

func TestMerge_VerifyFails(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("OK\n")},                                          // gh pr merge
		{out: []byte("not found"), err: errors.New("exit 1")},          // gh pr view
	}}

	e := newExecutor(rec)
	out, err := e.Merge(context.Background(), BeadContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultFail {
		t.Fatalf("want fail, got %s", out.Result)
	}
}

// --- evaluateChecks tests ---

func TestEvaluateChecks_Empty(t *testing.T) {
	allDone, anyFailed, _ := evaluateChecks(nil)
	if !allDone {
		t.Error("empty checks should be allDone")
	}
	if anyFailed {
		t.Error("empty checks should not be anyFailed")
	}
}

func TestEvaluateChecks_Mixed(t *testing.T) {
	checks := []checkRun{
		{Name: "a", Bucket: "pass"},
		{Name: "b", Bucket: "pending"},
		{Name: "c", Bucket: "fail"},
	}
	allDone, anyFailed, summary := evaluateChecks(checks)
	if allDone {
		t.Error("should not be allDone with pending")
	}
	if !anyFailed {
		t.Error("should be anyFailed with fail bucket")
	}
	if summary != "1 passed, 1 failed, 1 pending" {
		t.Errorf("unexpected summary: %s", summary)
	}
}

// --- metaString tests ---

func TestMetaString(t *testing.T) {
	if v := metaString(nil, "key"); v != "" {
		t.Errorf("nil map: want empty, got %q", v)
	}
	if v := metaString(map[string]any{}, "key"); v != "" {
		t.Errorf("missing key: want empty, got %q", v)
	}
	if v := metaString(map[string]any{"key": "val"}, "key"); v != "val" {
		t.Errorf("want 'val', got %q", v)
	}
	if v := metaString(map[string]any{"key": 123}, "key"); v != "" {
		t.Errorf("non-string: want empty, got %q", v)
	}
}
