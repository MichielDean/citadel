package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/cistern"
)

func execCmd(t *testing.T, args ...string) error {
	t.Helper()
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}

func TestDropletIssueAdd_CreatesIssue(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("CT_CATARACTA_NAME", "reviewer")

	c, err := cistern.New(db, "ct")
	if err != nil {
		t.Fatal(err)
	}
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.Close()

	if err := execCmd(t, "droplet", "issue", "add", item.ID, "missing error handling"); err != nil {
		t.Fatalf("issue add failed: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	issues, _ := c2.ListIssues(item.ID, false, "")
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Description != "missing error handling" {
		t.Errorf("description = %q", issues[0].Description)
	}
	if issues[0].Status != "open" {
		t.Errorf("status = %q, want open", issues[0].Status)
	}
	if issues[0].FlaggedBy != "reviewer" {
		t.Errorf("flagged_by = %q, want reviewer", issues[0].FlaggedBy)
	}
}

func TestDropletIssueResolve_UpdatesStatus(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("CT_CATARACTA_NAME", "reviewer")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "some issue")
	c.Close()

	if err := execCmd(t, "droplet", "issue", "resolve", iss.ID, "--evidence", "grep output"); err != nil {
		t.Fatalf("issue resolve failed: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	issues, _ := c2.ListIssues(item.ID, false, "")
	if issues[0].Status != "resolved" {
		t.Errorf("status = %q, want resolved", issues[0].Status)
	}
}

func TestDropletIssueResolve_ImplementerForbidden(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("CT_CATARACTA_NAME", "implementer")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "some issue")
	c.Close()

	err := execCmd(t, "droplet", "issue", "resolve", iss.ID, "--evidence", "trust me")
	if err == nil {
		t.Error("expected error: implementer should be forbidden from resolving issues")
	}
	if !strings.Contains(err.Error(), "only reviewer") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Verify DB state unchanged.
	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	issues, _ := c2.ListIssues(item.ID, false, "")
	if issues[0].Status != "open" {
		t.Errorf("status should remain open, got %q", issues[0].Status)
	}
}

func TestDropletIssueResolve_ImplementShortName(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("CT_CATARACTA_NAME", "implement")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "some issue")
	c.Close()

	err := execCmd(t, "droplet", "issue", "resolve", iss.ID, "--evidence", "proof")
	if err == nil {
		t.Error("expected error for CT_CATARACTA_NAME=implement")
	}
}

func TestDropletIssueReject_UpdatesStatus(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("CT_CATARACTA_NAME", "reviewer")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "unfixed issue")
	c.Close()

	if err := execCmd(t, "droplet", "issue", "reject", iss.ID, "--evidence", "still broken"); err != nil {
		t.Fatalf("issue reject failed: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	issues, _ := c2.ListIssues(item.ID, false, "")
	if issues[0].Status != "unresolved" {
		t.Errorf("status = %q, want unresolved", issues[0].Status)
	}
	if issues[0].Evidence != "still broken" {
		t.Errorf("evidence = %q", issues[0].Evidence)
	}
}

func TestDropletIssueReject_ImplementerForbidden(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("CT_CATARACTA_NAME", "implementer")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "some issue")
	c.Close()

	err := execCmd(t, "droplet", "issue", "reject", iss.ID, "--evidence", "still broken")
	if err == nil {
		t.Error("expected error: implementer should be forbidden from rejecting issues")
	}
	if !strings.Contains(err.Error(), "only reviewer") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Verify DB state unchanged.
	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	issues, _ := c2.ListIssues(item.ID, false, "")
	if issues[0].Status != "open" {
		t.Errorf("status should remain open, got %q", issues[0].Status)
	}
}

func TestDropletIssueReject_ImplementShortName(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("CT_CATARACTA_NAME", "implement")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "some issue")
	c.Close()

	err := execCmd(t, "droplet", "issue", "reject", iss.ID, "--evidence", "proof")
	if err == nil {
		t.Error("expected error for CT_CATARACTA_NAME=implement")
	}
}

func TestDropletPass_SucceedsWithOpenIssues(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.AddIssue(item.ID, "reviewer", "open issue present")
	c.Close()

	if err := execCmd(t, "droplet", "pass", item.ID); err != nil {
		t.Fatalf("pass should succeed even with open issues: %v", err)
	}

	// Verify outcome was set to pass.
	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	d, _ := c2.Get(item.ID)
	if d.Outcome != "pass" {
		t.Errorf("outcome = %q, want pass", d.Outcome)
	}
}

func TestDropletPass_AllowedWhenIssuesResolved(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("CT_CATARACTA_NAME", "reviewer")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "a finding")
	c.ResolveIssue(iss.ID, "fixed")
	c.Close()

	// Temporarily clear CT_CATARACTA_NAME so pass doesn't get confused.
	os.Unsetenv("CT_CATARACTA_NAME")
	defer os.Setenv("CT_CATARACTA_NAME", "reviewer")

	if err := execCmd(t, "droplet", "pass", item.ID); err != nil {
		t.Fatalf("pass should succeed when all issues resolved: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	d, _ := c2.Get(item.ID)
	if d.Outcome != "pass" {
		t.Errorf("outcome = %q, want pass", d.Outcome)
	}
}

func TestDropletPass_NoIssues(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.Close()

	if err := execCmd(t, "droplet", "pass", item.ID); err != nil {
		t.Fatalf("pass with no issues should succeed: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	d, _ := c2.Get(item.ID)
	if d.Outcome != "pass" {
		t.Errorf("outcome = %q, want pass", d.Outcome)
	}
}

// --- pass: pooled / terminal status tests ---

// TestDropletPass_WhenPooled_SetsStatusDelivered verifies that passing a pooled
// droplet immediately sets status=delivered without Castellarius involvement.
// Given a pooled droplet with no open issues,
// When ct droplet pass is called,
// Then status=delivered and outcome=pass.
func TestDropletPass_WhenPooled_SetsStatusDelivered(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.Pool(item.ID, "timed out")
	c.Close()

	if err := execCmd(t, "droplet", "pass", item.ID); err != nil {
		t.Fatalf("pass on pooled droplet should succeed: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	d, _ := c2.Get(item.ID)
	if d.Status != "delivered" {
		t.Errorf("status = %q, want delivered", d.Status)
	}
	if d.Outcome != "pass" {
		t.Errorf("outcome = %q, want pass", d.Outcome)
	}
}

// TestDropletPass_WhenInProgress_BehaviorUnchanged verifies that passing an in_progress
// droplet only sets the outcome field, leaving status=in_progress for Castellarius.
// Given an in_progress droplet,
// When ct droplet pass is called,
// Then outcome=pass and status remains in_progress.
func TestDropletPass_WhenInProgress_BehaviorUnchanged(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.UpdateStatus(item.ID, "in_progress")
	c.Close()

	if err := execCmd(t, "droplet", "pass", item.ID); err != nil {
		t.Fatalf("pass on in_progress droplet should succeed: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	d, _ := c2.Get(item.ID)
	if d.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress (Castellarius handles routing)", d.Status)
	}
	if d.Outcome != "pass" {
		t.Errorf("outcome = %q, want pass", d.Outcome)
	}
}

// TestDropletPass_WhenDelivered_ReturnsError verifies that passing an already-delivered
// droplet returns a clear error.
func TestDropletPass_WhenDelivered_ReturnsError(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.CloseItem(item.ID)
	c.Close()

	err := execCmd(t, "droplet", "pass", item.ID)
	if err == nil {
		t.Fatal("expected error: cannot pass a delivered droplet")
	}
	if !strings.Contains(err.Error(), "delivered") {
		t.Errorf("error %q should mention 'delivered'", err.Error())
	}
}

// TestDropletPass_WhenCancelled_ReturnsError verifies that passing a cancelled droplet
// returns a clear error.
func TestDropletPass_WhenCancelled_ReturnsError(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.Cancel(item.ID, "no longer needed")
	c.Close()

	err := execCmd(t, "droplet", "pass", item.ID)
	if err == nil {
		t.Fatal("expected error: cannot pass a cancelled droplet")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error %q should mention 'cancelled'", err.Error())
	}
}

// --- pool: pooled / terminal status tests ---

// TestDropletPool_WhenPooled_SetsOutcomeAndKeepsPooled verifies that pooling a
// pooled droplet records outcome=pool and status remains pooled.
// Given a pooled droplet,
// When ct droplet pool is called,
// Then outcome=pool and status=pooled.
func TestDropletPool_WhenPooled_SetsOutcomeAndKeepsPooled(t *testing.T) {
	t.Cleanup(func() { poolNotes = "" })
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.Pool(item.ID, "timed out")
	c.Close()

	if err := execCmd(t, "droplet", "pool", item.ID); err != nil {
		t.Fatalf("pool on pooled droplet should succeed: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	d, _ := c2.Get(item.ID)
	if d.Status != "pooled" {
		t.Errorf("status = %q, want pooled", d.Status)
	}
	if d.Outcome != "pool" {
		t.Errorf("outcome = %q, want pool", d.Outcome)
	}
}

// TestDropletPool_WhenPooled_ForwardsPoolNotes verifies that --notes is recorded
// when pooling a pooled droplet, so the reason is traceable.
// Given a pooled droplet,
// When ct droplet pool --notes "reason" is called,
// Then the note appears in the droplet's notes.
func TestDropletPool_WhenPooled_ForwardsPoolNotes(t *testing.T) {
	t.Cleanup(func() { poolNotes = "" })
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.Pool(item.ID, "timed out")
	c.Close()

	if err := execCmd(t, "droplet", "pool", "--notes", "waiting on upstream API", item.ID); err != nil {
		t.Fatalf("pool --notes on pooled droplet should succeed: %v", err)
	}

	// Verify the poolNotes was added as a cataractae note.
	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	notes, err := c2.GetNotes(item.ID)
	if err != nil {
		t.Fatalf("GetNotes failed: %v", err)
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n.Content, "waiting on upstream API") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected note containing poolNotes reason, notes: %+v", notes)
	}
}

// TestDropletPool_WhenDelivered_ReturnsError verifies that pooling a delivered droplet
// returns a clear error.
func TestDropletPool_WhenDelivered_ReturnsError(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.CloseItem(item.ID)
	c.Close()

	err := execCmd(t, "droplet", "pool", item.ID)
	if err == nil {
		t.Fatal("expected error: cannot pool a delivered droplet")
	}
	if !strings.Contains(err.Error(), "delivered") {
		t.Errorf("error %q should mention 'delivered'", err.Error())
	}
}

// TestDropletPool_WhenCancelled_ReturnsError verifies that pooling a cancelled droplet
// returns a clear error.
func TestDropletPool_WhenCancelled_ReturnsError(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.Cancel(item.ID, "no longer needed")
	c.Close()

	err := execCmd(t, "droplet", "pool", item.ID)
	if err == nil {
		t.Fatal("expected error: cannot pool a cancelled droplet")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error %q should mention 'cancelled'", err.Error())
	}
}

// --- recirculate: pooled / terminal status tests ---

// TestDropletRecirculate_WhenPooled_SetsStatusOpen verifies that recirculating a
// pooled droplet immediately sets status=open and clears outcome.
// Given a pooled droplet,
// When ct droplet recirculate is called,
// Then status=open and outcome="" (Assign clears outcome).
func TestDropletRecirculate_WhenPooled_SetsStatusOpen(t *testing.T) {
	t.Cleanup(func() { recirculateTo = "" })
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.SetCataractae(item.ID, "implement")
	c.Pool(item.ID, "timed out")
	c.Close()

	if err := execCmd(t, "droplet", "recirculate", item.ID); err != nil {
		t.Fatalf("recirculate on pooled droplet should succeed: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	d, _ := c2.Get(item.ID)
	if d.Status != "open" {
		t.Errorf("status = %q, want open", d.Status)
	}
	if d.Outcome != "" {
		t.Errorf("outcome = %q, want empty (Assign clears outcome)", d.Outcome)
	}
}

// TestDropletRecirculate_WhenPooled_DefaultsToCurrentCataractae verifies that when
// --to is not provided, recirculate targets the droplet's current_cataractae.
// Given a pooled droplet at cataractae "implement",
// When ct droplet recirculate is called without --to,
// Then current_cataractae remains "implement".
func TestDropletRecirculate_WhenPooled_DefaultsToCurrentCataractae(t *testing.T) {
	t.Cleanup(func() { recirculateTo = "" })
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.SetCataractae(item.ID, "implement")
	c.Pool(item.ID, "timed out")
	c.Close()

	if err := execCmd(t, "droplet", "recirculate", item.ID); err != nil {
		t.Fatalf("recirculate on pooled droplet should succeed: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	d, _ := c2.Get(item.ID)
	if d.CurrentCataractae != "implement" {
		t.Errorf("current_cataractae = %q, want implement", d.CurrentCataractae)
	}
	if d.Outcome != "" {
		t.Errorf("outcome = %q, want empty", d.Outcome)
	}
}

// TestDropletRecirculate_WhenPooled_WithTo_SetsCurrentCataractae verifies that --to
// overrides the target cataractae when recirculating a pooled droplet.
// Given a pooled droplet at cataractae "review",
// When ct droplet recirculate --to implement is called,
// Then current_cataractae=implement and status=open and outcome="".
func TestDropletRecirculate_WhenPooled_WithTo_SetsCurrentCataractae(t *testing.T) {
	t.Cleanup(func() { recirculateTo = "" })
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.SetCataractae(item.ID, "review")
	c.Pool(item.ID, "timed out")
	c.Close()

	if err := execCmd(t, "droplet", "recirculate", "--to", "implement", item.ID); err != nil {
		t.Fatalf("recirculate --to on pooled droplet should succeed: %v", err)
	}

	c2, _ := cistern.New(db, "ct")
	defer c2.Close()
	d, _ := c2.Get(item.ID)
	if d.CurrentCataractae != "implement" {
		t.Errorf("current_cataractae = %q, want implement", d.CurrentCataractae)
	}
	if d.Status != "open" {
		t.Errorf("status = %q, want open", d.Status)
	}
	if d.Outcome != "" {
		t.Errorf("outcome = %q, want empty", d.Outcome)
	}
}

// TestDropletRecirculate_WhenDelivered_ReturnsError verifies that recirculating a
// delivered droplet returns a clear error.
func TestDropletRecirculate_WhenDelivered_ReturnsError(t *testing.T) {
	t.Cleanup(func() { recirculateTo = "" })
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.CloseItem(item.ID)
	c.Close()

	err := execCmd(t, "droplet", "recirculate", item.ID)
	if err == nil {
		t.Fatal("expected error: cannot recirculate a delivered droplet")
	}
	if !strings.Contains(err.Error(), "delivered") {
		t.Errorf("error %q should mention 'delivered'", err.Error())
	}
}

// TestDropletRecirculate_WhenCancelled_ReturnsError verifies that recirculating a
// cancelled droplet returns a clear error.
func TestDropletRecirculate_WhenCancelled_ReturnsError(t *testing.T) {
	t.Cleanup(func() { recirculateTo = "" })
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.Cancel(item.ID, "no longer needed")
	c.Close()

	err := execCmd(t, "droplet", "recirculate", item.ID)
	if err == nil {
		t.Fatal("expected error: cannot recirculate a cancelled droplet")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error %q should mention 'cancelled'", err.Error())
	}
}

func TestDropletIssueList_NoIssues(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.Close()

	out := captureStdout(t, func() {
		if err := execCmd(t, "droplet", "issue", "list", item.ID); err != nil {
			t.Fatalf("issue list failed: %v", err)
		}
	})
	if !strings.Contains(out, "no issues found") {
		t.Errorf("expected 'no issues found', got: %q", out)
	}
}

func TestDropletIssueList_WithIssues(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.AddIssue(item.ID, "reviewer", "first issue description")
	c.AddIssue(item.ID, "reviewer", "second issue description")
	c.Close()

	out := captureStdout(t, func() {
		if err := execCmd(t, "droplet", "issue", "list", item.ID); err != nil {
			t.Fatalf("issue list failed: %v", err)
		}
	})
	if !strings.Contains(out, "first issue description") {
		t.Errorf("expected first issue in output, got: %q", out)
	}
	if !strings.Contains(out, "second issue description") {
		t.Errorf("expected second issue in output, got: %q", out)
	}
}

func TestDropletIssueList_OpenFilter(t *testing.T) {
	t.Cleanup(func() { issueListOpen = false })
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.AddIssue(item.ID, "reviewer", "open issue stays")
	iss2, _ := c.AddIssue(item.ID, "reviewer", "resolved issue hidden")
	c.ResolveIssue(iss2.ID, "fixed it")
	c.Close()

	out := captureStdout(t, func() {
		if err := execCmd(t, "droplet", "issue", "list", "--open", item.ID); err != nil {
			t.Fatalf("issue list --open failed: %v", err)
		}
	})
	if !strings.Contains(out, "open issue stays") {
		t.Errorf("expected open issue in output, got: %q", out)
	}
	if strings.Contains(out, "resolved issue hidden") {
		t.Errorf("resolved issue should be filtered out, got: %q", out)
	}
}

func TestDropletIssueResolve_EmptyEvidence(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("CT_CATARACTA_NAME", "reviewer")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "some issue")
	c.Close()

	err := execCmd(t, "droplet", "issue", "resolve", iss.ID, "--evidence", "")
	if err == nil {
		t.Error("expected error: resolve with empty --evidence should fail")
	}
}

func TestDropletIssueReject_EmptyEvidence(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("CT_CATARACTA_NAME", "reviewer")

	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "some issue")
	c.Close()

	err := execCmd(t, "droplet", "issue", "reject", iss.ID, "--evidence", "")
	if err == nil {
		t.Error("expected error: reject with empty --evidence should fail")
	}
}

func TestDropletIssueList_FlaggedByFilter(t *testing.T) {
	t.Cleanup(func() { issueListFlaggedBy = "" })
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	// Given: issues filed by two different cataractae.
	c, _ := cistern.New(db, "ct")
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.AddIssue(item.ID, "reviewer", "reviewer filed this")
	c.AddIssue(item.ID, "qa", "qa filed this")
	c.Close()

	// When: --flagged-by reviewer
	out := captureStdout(t, func() {
		if err := execCmd(t, "droplet", "issue", "list", "--flagged-by", "reviewer", item.ID); err != nil {
			t.Fatalf("issue list --flagged-by reviewer failed: %v", err)
		}
	})
	// Then: only reviewer issue shown.
	if !strings.Contains(out, "reviewer filed this") {
		t.Errorf("expected reviewer issue in output, got: %q", out)
	}
	if strings.Contains(out, "qa filed this") {
		t.Errorf("qa issue should be filtered out, got: %q", out)
	}
}
