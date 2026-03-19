package cistern

import (
	"strings"
	"testing"
)

func TestAddIssue_And_ListIssues(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	iss, err := c.AddIssue(item.ID, "reviewer", "missing error handling in foo()")
	if err != nil {
		t.Fatal(err)
	}
	if iss.ID == "" {
		t.Fatal("expected non-empty issue ID")
	}
	if !strings.HasPrefix(iss.ID, item.ID+"-") {
		t.Errorf("issue ID %q should be prefixed with droplet ID %q", iss.ID, item.ID)
	}
	if iss.Status != "open" {
		t.Errorf("status = %q, want open", iss.Status)
	}
	if iss.FlaggedBy != "reviewer" {
		t.Errorf("flagged_by = %q, want reviewer", iss.FlaggedBy)
	}

	issues, err := c.ListIssues(item.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].Description != "missing error handling in foo()" {
		t.Errorf("description = %q", issues[0].Description)
	}
}

func TestListIssues_OpenOnly(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	iss1, _ := c.AddIssue(item.ID, "reviewer", "issue one")
	iss2, _ := c.AddIssue(item.ID, "reviewer", "issue two")

	// Resolve iss1.
	if err := c.ResolveIssue(iss1.ID, "fixed in abc123"); err != nil {
		t.Fatal(err)
	}

	// ListIssues(openOnly=false) should return both.
	all, err := c.ListIssues(item.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all: got %d issues, want 2", len(all))
	}

	// ListIssues(openOnly=true) should return only iss2.
	open, err := c.ListIssues(item.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("open: got %d issues, want 1", len(open))
	}
	if open[0].ID != iss2.ID {
		t.Errorf("open issue = %q, want %q", open[0].ID, iss2.ID)
	}
}

func TestResolveIssue(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "some finding")

	if err := c.ResolveIssue(iss.ID, "grep output shows it's fixed"); err != nil {
		t.Fatal(err)
	}

	issues, _ := c.ListIssues(item.ID, false)
	if issues[0].Status != "resolved" {
		t.Errorf("status = %q, want resolved", issues[0].Status)
	}
	if issues[0].Evidence != "grep output shows it's fixed" {
		t.Errorf("evidence = %q", issues[0].Evidence)
	}
	if issues[0].ResolvedAt == nil {
		t.Error("resolved_at should be set")
	}
}

func TestRejectIssue(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	iss, _ := c.AddIssue(item.ID, "reviewer", "some finding")

	if err := c.RejectIssue(iss.ID, "still present: grep shows it"); err != nil {
		t.Fatal(err)
	}

	issues, _ := c.ListIssues(item.ID, false)
	if issues[0].Status != "unresolved" {
		t.Errorf("status = %q, want unresolved", issues[0].Status)
	}
	if issues[0].Evidence != "still present: grep shows it" {
		t.Errorf("evidence = %q", issues[0].Evidence)
	}
}

func TestResolveIssue_NotFound(t *testing.T) {
	c := testClient(t)
	err := c.ResolveIssue("nonexistent-issue", "evidence")
	if err == nil {
		t.Error("expected error for unknown issue")
	}
}

func TestRejectIssue_NotFound(t *testing.T) {
	c := testClient(t)
	err := c.RejectIssue("nonexistent-issue", "evidence")
	if err == nil {
		t.Error("expected error for unknown issue")
	}
}

func TestCountOpenIssues(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	n, err := c.CountOpenIssues(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}

	iss1, _ := c.AddIssue(item.ID, "reviewer", "issue one")
	c.AddIssue(item.ID, "reviewer", "issue two")

	n, _ = c.CountOpenIssues(item.ID)
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}

	// Resolve one.
	c.ResolveIssue(iss1.ID, "fixed")
	n, _ = c.CountOpenIssues(item.ID)
	if n != 1 {
		t.Errorf("count after resolve = %d, want 1", n)
	}
}

func TestSetOutcome_BlockedByOpenIssues(t *testing.T) {
	// Verify CountOpenIssues returns correct data so the CLI can enforce the block.
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.AddIssue(item.ID, "reviewer", "unfixed issue")

	n, err := c.CountOpenIssues(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("expected open issue count > 0 to block pass")
	}
}
