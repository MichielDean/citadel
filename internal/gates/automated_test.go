package gates

import (
	"context"
	"errors"
	"strings"
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
		{out: []byte("feature-branch\n")},                      // git branch --show-current
		{out: []byte("ok\n")},                                  // git fetch origin main
		{out: []byte("No local changes to save\n")},            // git stash (nothing to stash)
		{out: []byte("ok\n")},                                  // git rebase origin/main
		{out: []byte("ok\n")},                                  // git push --force-with-lease
		{out: []byte("https://github.com/org/repo/pull/42\n")}, // gh pr create
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), DropletContext{
		ID:          "bf-123",
		Title:       "Fix widget",
		Description: "Fixes the broken widget",
		BaseBranch:  "main",
		WorkDir:     "/tmp/repo",
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

	// Verify gh pr create was called with correct args (index 5: branch, fetch, stash, rebase, push, gh).
	ghCall := rec.calls[5]
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
		{out: []byte("ok\n")},                                  // git fetch origin main
		{out: []byte("No local changes to save\n")},            // git stash (nothing)
		{out: []byte("ok\n")},                                  // git rebase origin/main
		{out: []byte("ok\n")},                                  // git push --force-with-lease
		{out: []byte("https://github.com/org/repo/pull/7\n")}, // gh pr create
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), DropletContext{
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
	// Should have called fetch + stash + rebase + push + gh (no git branch --show-current when branch provided).
	if len(rec.calls) != 5 {
		t.Fatalf("want 5 calls (fetch+stash+rebase+push+gh), got %d: %v", len(rec.calls), rec.calls)
	}
	if rec.calls[3].name != "git" || rec.calls[3].args[0] != "push" {
		t.Errorf("want git push at index 3, got %q %v", rec.calls[3].name, rec.calls[3].args)
	}
	if rec.calls[4].name != "gh" {
		t.Errorf("want gh at index 4, got %q", rec.calls[4].name)
	}
}

func TestPRCreate_GhFails(t *testing.T) {
	// Generic gh failure (not "already exists") → ResultFail.
	rec := &recorder{responses: []response{
		{out: []byte("feature\n")},                       // git branch --show-current
		{out: []byte("ok\n")},                            // git fetch
		{out: []byte("No local changes to save\n")},      // git stash
		{out: []byte("ok\n")},                            // git rebase
		{out: []byte("ok\n")},                            // git push
		{out: []byte("authentication required"), err: errors.New("exit 1")}, // gh pr create
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), DropletContext{
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

func TestPRCreate_AlreadyExists(t *testing.T) {
	// "Already exists" → extract URL and return pass (idempotent).
	existingMsg := "a pull request for branch \"feature\" into branch \"main\" already exists:\nhttps://github.com/org/repo/pull/42"
	rec := &recorder{responses: []response{
		{out: []byte("feature\n")},            // git branch --show-current
		{out: []byte("ok\n")},                 // git fetch
		{out: []byte("No local changes to save\n")}, // git stash
		{out: []byte("ok\n")},                 // git rebase
		{out: []byte("ok\n")},                 // git push
		{out: []byte(existingMsg), err: errors.New("exit 1")}, // gh pr create
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), DropletContext{
		ID:      "bf-already",
		WorkDir: "/tmp/repo",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass (idempotent), got %s: %s", out.Result, out.Notes)
	}
	if out.Annotations[AnnoPRURL] != "https://github.com/org/repo/pull/42" {
		t.Errorf("want existing pr_url, got %v", out.Annotations)
	}
}

func TestPRCreate_RebaseConflict(t *testing.T) {
	// Rebase conflict → ResultRecirculate with actionable note.
	rec := &recorder{responses: []response{
		{out: []byte("feature\n")},                                     // git branch --show-current
		{out: []byte("ok\n")},                                          // git fetch
		{out: []byte("stash@{0}: On feature: pre-rebase-stash\n")},     // git stash (stashed)
		{out: []byte("CONFLICT (content): Merge conflict in foo.go\n"), err: errors.New("exit 1")}, // git rebase
		{out: []byte("ok\n")},                                          // git rebase --abort
		{out: []byte("ok\n")},                                          // git stash pop (deferred)
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), DropletContext{
		ID:      "bf-conflict",
		WorkDir: "/tmp/repo",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultRecirculate {
		t.Fatalf("want recirculate, got %s: %s", out.Result, out.Notes)
	}
	if !strings.Contains(out.Notes, "rebase conflict") {
		t.Errorf("want notes mentioning rebase conflict, got: %s", out.Notes)
	}
}

func TestPRCreate_NoBranch(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("\n")}, // git branch returns empty
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), DropletContext{
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
		{out: []byte("ok\n")},                                 // git fetch
		{out: []byte("No local changes to save\n")},           // git stash (nothing)
		{out: []byte("ok\n")},                                 // git rebase
		{out: []byte("ok\n")},                                 // git push
		{out: []byte("https://github.com/org/repo/pull/1\n")}, // gh pr create
	}}

	e := newExecutor(rec)
	out, err := e.PRCreate(context.Background(), DropletContext{
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

	ghCall := rec.calls[4] // fetch + stash + rebase + push + gh
	// Title should be "droplet bf-xyz".
	for i, a := range ghCall.args {
		if a == "--title" && i+1 < len(ghCall.args) {
			if ghCall.args[i+1] != "droplet bf-xyz" {
				t.Errorf("want default title 'droplet bf-xyz', got %q", ghCall.args[i+1])
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
	out, err := e.CIGate(context.Background(), DropletContext{
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
		{out: []byte("MERGEABLE\n")}, // gh pr view (poll 1)
		{out: []byte(pending)},       // gh pr checks (poll 1)
		{out: []byte("MERGEABLE\n")}, // gh pr view (poll 2)
		{out: []byte(passed)},        // gh pr checks (poll 2)
	}}

	e := newExecutor(rec)
	out, err := e.CIGate(context.Background(), DropletContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	}, time.Millisecond)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass, got %s: %s", out.Result, out.Notes)
	}
	if len(rec.calls) != 4 {
		t.Errorf("want 4 calls (2 mergeable + 2 checks), got %d", len(rec.calls))
	}
}

func TestCIGate_CheckFails(t *testing.T) {
	checksJSON := `[{"name":"build","bucket":"pass"},{"name":"test","bucket":"fail"}]`
	rec := &recorder{responses: []response{
		{out: []byte("MERGEABLE\n")},                                                    // gh pr view --json mergeable
		{out: []byte(checksJSON)},                                                       // gh pr checks
		{out: []byte("feature-branch\n")},                                               // git rev-parse
		{out: []byte(`[{"databaseId":12345,"status":"completed"}]`)},                    // gh run list
		{out: []byte("FAIL\tgithub.com/foo/bar\t0.01s\n--- FAIL: TestFoo\n    err\n")},  // gh run view --log-failed
	}}

	e := newExecutor(rec)
	out, err := e.CIGate(context.Background(), DropletContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	}, time.Millisecond)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultRecirculate {
		t.Fatalf("want recirculate, got %s: %s", out.Result, out.Notes)
	}
	if !strings.Contains(out.Notes, "CI failed") {
		t.Errorf("want notes to contain 'CI failed', got: %s", out.Notes)
	}
}

func TestCIGate_NoChecks(t *testing.T) {
	// No checks configured: needs 3 polls with empty checks before declaring pass.
	rec := &recorder{responses: []response{
		{out: []byte("MERGEABLE\n")}, {out: []byte("[]")}, // poll 1
		{out: []byte("MERGEABLE\n")}, {out: []byte("[]")}, // poll 2
		{out: []byte("MERGEABLE\n")}, {out: []byte("[]")}, // poll 3 → pass
	}}

	e := newExecutor(rec)
	out, err := e.CIGate(context.Background(), DropletContext{
		WorkDir:  "/tmp/repo",
		Metadata: map[string]any{MetaPRURL: "https://github.com/org/repo/pull/42"},
	}, time.Millisecond)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != ResultPass {
		t.Fatalf("want pass (no checks configured), got %s: %s", out.Result, out.Notes)
	}
}

func TestCIGate_NoPRURL(t *testing.T) {
	rec := &recorder{}

	e := newExecutor(rec)
	out, err := e.CIGate(context.Background(), DropletContext{
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
	out, err := e.CIGate(ctx, DropletContext{
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
	out, err := e.CIGate(context.Background(), DropletContext{
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
		{out: []byte("Merged\n")}, // gh pr merge
	}}

	e := newExecutor(rec)
	out, err := e.Merge(context.Background(), DropletContext{
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

func TestMerge_Fails(t *testing.T) {
	rec := &recorder{responses: []response{
		{out: []byte("merge conflict"), err: errors.New("exit 1")},
	}}

	e := newExecutor(rec)
	out, err := e.Merge(context.Background(), DropletContext{
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
	out, err := e.Merge(context.Background(), DropletContext{
		WorkDir: "/tmp/repo",
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
