package cistern

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T) *Client {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	c, err := New(dbPath, "bf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestNew_CreatesDB(t *testing.T) {
	c := testClient(t)
	if c.db == nil {
		t.Fatal("expected non-nil db")
	}
	if c.prefix != "bf" {
		t.Errorf("prefix = %q, want %q", c.prefix, "bf")
	}
}

func TestGenerateID(t *testing.T) {
	c := testClient(t)
	id, err := c.generateID()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "bf-") {
		t.Errorf("id = %q, want prefix %q", id, "bf-")
	}
	if len(id) != 8 { // "bf-" (3) + 5 chars
		t.Errorf("id length = %d, want 8", len(id))
	}

	// IDs should be unique.
	ids := map[string]bool{}
	for range 100 {
		id, err := c.generateID()
		if err != nil {
			t.Fatal(err)
		}
		if ids[id] {
			t.Fatalf("duplicate id: %s", id)
		}
		ids[id] = true
	}
}

func TestAdd_And_Get(t *testing.T) {
	c := testClient(t)
	item, err := c.Add("github.com/org/repo", "Fix bug", "Details here", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if item.Status != "open" {
		t.Errorf("status = %q, want %q", item.Status, "open")
	}
	if item.Priority != 1 {
		t.Errorf("priority = %d, want 1", item.Priority)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Fix bug" {
		t.Errorf("title = %q, want %q", got.Title, "Fix bug")
	}
	if got.Description != "Details here" {
		t.Errorf("description = %q, want %q", got.Description, "Details here")
	}
}

func TestGetReady_PriorityOrdering(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Low priority", "", 3, 3)
	c.Add("myrepo", "High priority", "", 1, 3)
	c.Add("myrepo", "Medium priority", "", 2, 3)

	item, err := c.GetReady("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if item == nil {
		t.Fatal("expected item")
	}
	if item.Title != "High priority" {
		t.Errorf("title = %q, want %q", item.Title, "High priority")
	}
}

func TestGetReady_RepoFilter(t *testing.T) {
	c := testClient(t)
	c.Add("repo-a", "A task", "", 1, 3)
	c.Add("repo-b", "B task", "", 1, 3)

	item, err := c.GetReady("repo-a")
	if err != nil {
		t.Fatal(err)
	}
	if item.Title != "A task" {
		t.Errorf("got %q from repo-a, want %q", item.Title, "A task")
	}

	item, err = c.GetReady("repo-b")
	if err != nil {
		t.Fatal(err)
	}
	if item.Title != "B task" {
		t.Errorf("got %q from repo-b, want %q", item.Title, "B task")
	}
}

func TestGetReady_OnlyOpen(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Task", "", 1, 3)

	// First GetReady atomically claims the item.
	got, err := c.GetReady("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected item from first GetReady")
	}

	// Second GetReady returns nil — item is already in-progress.
	got, err = c.GetReady("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil (no open items)")
	}
}

func TestGetReady_NoWork(t *testing.T) {
	c := testClient(t)
	item, err := c.GetReady("empty-repo")
	if err != nil {
		t.Fatal(err)
	}
	if item != nil {
		t.Error("expected nil for empty repo")
	}
}

func TestAssign(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	// Claim the item via GetReady (atomically sets in_progress).
	c.GetReady("myrepo")

	if err := c.Assign(item.ID, "alice", "implement"); err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Assignee != "alice" {
		t.Errorf("assignee = %q, want %q", got.Assignee, "alice")
	}
	if got.CurrentCataractae != "implement" {
		t.Errorf("current_step = %q, want %q", got.CurrentCataractae, "implement")
	}
	if got.Status != "in_progress" {
		t.Errorf("status = %q, want %q", got.Status, "in_progress")
	}
}

func TestAssign_EmptyWorker_SetsOpen(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.GetReady("myrepo") // claim item (sets in_progress)
	c.Assign(item.ID, "alice", "implement")

	// Advance to next step with empty worker.
	if err := c.Assign(item.ID, "", "review"); err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Status != "open" {
		t.Errorf("status = %q, want %q", got.Status, "open")
	}
	if got.Assignee != "" {
		t.Errorf("assignee = %q, want empty", got.Assignee)
	}
	if got.CurrentCataractae != "review" {
		t.Errorf("current_step = %q, want %q", got.CurrentCataractae, "review")
	}
}

func TestUpdateStatus(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	if err := c.UpdateStatus(item.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Status != "in_progress" {
		t.Errorf("status = %q, want %q", got.Status, "in_progress")
	}
}

func TestAddNote_And_GetNotes(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	if err := c.AddNote(item.ID, "implement", "wrote the code"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddNote(item.ID, "review", "looks good"); err != nil {
		t.Fatal(err)
	}

	notes, err := c.GetNotes(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(notes))
	}
	if notes[0].CataractaeName != "implement" || notes[0].Content != "wrote the code" {
		t.Errorf("note[0] = %+v", notes[0])
	}
	if notes[1].CataractaeName != "review" || notes[1].Content != "looks good" {
		t.Errorf("note[1] = %+v", notes[1])
	}
}

func TestGetNotes_Empty(t *testing.T) {
	c := testClient(t)
	notes, err := c.GetNotes("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("got %d notes, want 0", len(notes))
	}
}

func TestEscalate(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	if err := c.Escalate(item.ID, "stuck on flaky test"); err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Status != "stagnant" {
		t.Errorf("status = %q, want %q", got.Status, "stagnant")
	}
}

func TestCloseItem(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	if err := c.CloseItem(item.ID); err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Status != "delivered" {
		t.Errorf("status = %q, want %q", got.Status, "delivered")
	}
}

func TestList_All(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Task 1", "", 1, 3)
	c.Add("myrepo", "Task 2", "", 2, 3)
	c.Add("other", "Task 3", "", 1, 3)

	items, err := c.List("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
}

func TestList_ByRepo(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Task 1", "", 1, 3)
	c.Add("other", "Task 2", "", 1, 3)

	items, err := c.List("myrepo", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Title != "Task 1" {
		t.Errorf("title = %q, want %q", items[0].Title, "Task 1")
	}
}

func TestList_ByStatus(t *testing.T) {
	c := testClient(t)
	item1, _ := c.Add("myrepo", "Open task", "", 1, 3)
	item2, _ := c.Add("myrepo", "Closed task", "", 1, 3)
	_ = item1
	c.CloseItem(item2.ID)

	items, err := c.List("", "delivered")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Title != "Closed task" {
		t.Errorf("title = %q, want %q", items[0].Title, "Closed task")
	}
}

func TestGet_NotFound(t *testing.T) {
	c := testClient(t)
	item, err := c.Get("nonexistent")
	if item != nil {
		t.Error("expected nil item")
	}
	if err == nil {
		t.Error("expected error for missing item")
	}
}

func TestAssign_NotFound(t *testing.T) {
	c := testClient(t)
	err := c.Assign("nonexistent", "worker", "step")
	if err == nil {
		t.Error("expected error for missing item")
	}
}

func TestAdd_WithComplexity(t *testing.T) {
	c := testClient(t)
	item, err := c.Add("myrepo", "Trivial fix", "", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if item.Complexity != 1 {
		t.Errorf("complexity = %d, want 1", item.Complexity)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Complexity != 1 {
		t.Errorf("stored complexity = %d, want 1", got.Complexity)
	}
}

func TestAdd_ComplexityDefault(t *testing.T) {
	c := testClient(t)
	// Out-of-range complexity should default to 3.
	item, err := c.Add("myrepo", "Bad cx", "", 2, 99)
	if err != nil {
		t.Fatal(err)
	}
	if item.Complexity != 3 {
		t.Errorf("complexity = %d, want 3 (default)", item.Complexity)
	}
}

func TestGetReady_ReturnsComplexity(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Critical task", "", 1, 4)

	item, err := c.GetReady("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if item.Complexity != 4 {
		t.Errorf("complexity = %d, want 4", item.Complexity)
	}
}

func TestList_ReturnsComplexity(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Trivial", "", 1, 1)
	c.Add("myrepo", "Critical", "", 1, 4)

	items, err := c.List("myrepo", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Complexity != 1 {
		t.Errorf("items[0].Complexity = %d, want 1", items[0].Complexity)
	}
	if items[1].Complexity != 4 {
		t.Errorf("items[1].Complexity = %d, want 4", items[1].Complexity)
	}
}

func TestStats_EmptyDB(t *testing.T) {
	c := testClient(t)
	s, err := c.Stats()
	if err != nil {
		t.Fatalf("Stats on empty DB: %v", err)
	}
	if s.Flowing != 0 || s.Queued != 0 || s.Delivered != 0 || s.Stagnant != 0 {
		t.Errorf("expected all zeros on empty DB, got %+v", s)
	}
}

func TestAdd_WithDeps(t *testing.T) {
	c := testClient(t)
	parent, _ := c.Add("myrepo", "Parent", "", 1, 3)
	child, err := c.Add("myrepo", "Child", "", 1, 3, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	deps, err := c.GetDependencies(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0] != parent.ID {
		t.Errorf("GetDependencies = %v, want [%s]", deps, parent.ID)
	}
}

func TestAdd_UnknownDep(t *testing.T) {
	c := testClient(t)
	_, err := c.Add("myrepo", "Child", "", 1, 3, "nonexistent")
	if err == nil {
		t.Error("expected error for unknown dep ID")
	}
}

func TestAddDependency_And_RemoveDependency(t *testing.T) {
	c := testClient(t)
	a, _ := c.Add("myrepo", "A", "", 1, 3)
	b, _ := c.Add("myrepo", "B", "", 1, 3)

	if err := c.AddDependency(b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	deps, _ := c.GetDependencies(b.ID)
	if len(deps) != 1 || deps[0] != a.ID {
		t.Errorf("after add: GetDependencies = %v, want [%s]", deps, a.ID)
	}

	if err := c.RemoveDependency(b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	deps, _ = c.GetDependencies(b.ID)
	if len(deps) != 0 {
		t.Errorf("after remove: GetDependencies = %v, want []", deps)
	}
}

func TestAddDependency_UnknownDroplet(t *testing.T) {
	c := testClient(t)
	a, _ := c.Add("myrepo", "A", "", 1, 3)
	if err := c.AddDependency("nonexistent", a.ID); err == nil {
		t.Error("expected error for unknown droplet")
	}
	if err := c.AddDependency(a.ID, "nonexistent"); err == nil {
		t.Error("expected error for unknown depends_on")
	}
}

func TestGetBlockedBy(t *testing.T) {
	c := testClient(t)
	parent, _ := c.Add("myrepo", "Parent", "", 1, 3)
	child, _ := c.Add("myrepo", "Child", "", 1, 3, parent.ID)

	// Parent not delivered — child is blocked.
	blocked, err := c.GetBlockedBy(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0] != parent.ID {
		t.Errorf("GetBlockedBy = %v, want [%s]", blocked, parent.ID)
	}

	// Deliver parent — child should no longer be blocked.
	c.CloseItem(parent.ID)
	blocked, err = c.GetBlockedBy(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 0 {
		t.Errorf("GetBlockedBy after deliver = %v, want []", blocked)
	}
}

func TestGetReady_SkipsBlocked(t *testing.T) {
	c := testClient(t)
	parent, _ := c.Add("myrepo", "Parent", "", 1, 3)
	_, _ = c.Add("myrepo", "Child", "", 1, 3, parent.ID)

	// GetReady should return parent (child is blocked).
	got, err := c.GetReady("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected parent, got nil")
	}
	if got.ID != parent.ID {
		t.Errorf("got %s, want parent %s", got.ID, parent.ID)
	}

	// Deliver parent.
	c.CloseItem(parent.ID)
	// Reopen child to 'open' (it was still open, just couldn't be dispatched).
	// Child should now be ready.
	got, err = c.GetReady("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected child to be ready after parent delivered, got nil")
	}
}

func TestGetReady_SkipsBlocked_NothingAvailable(t *testing.T) {
	c := testClient(t)
	parent, _ := c.Add("myrepo", "Parent", "", 1, 3)
	_, _ = c.Add("myrepo", "Child", "", 1, 3, parent.ID)

	// Claim parent.
	c.GetReady("myrepo") // claims parent (in_progress)
	// Now only child is open but blocked — GetReady should return nil.
	got, err := c.GetReady("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil (child blocked), got %s", got.ID)
	}
}

func TestSetAndGetLastReviewedCommit(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	// Initially empty.
	commit, err := c.GetLastReviewedCommit(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if commit != "" {
		t.Errorf("expected empty last_reviewed_commit, got %q", commit)
	}

	// Set a commit hash.
	hash := "abc1234def5678"
	if err := c.SetLastReviewedCommit(item.ID, hash); err != nil {
		t.Fatalf("SetLastReviewedCommit: %v", err)
	}

	// Read it back.
	got, err := c.GetLastReviewedCommit(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != hash {
		t.Errorf("GetLastReviewedCommit = %q, want %q", got, hash)
	}
}

func TestSetLastReviewedCommit_Overwrite(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	if err := c.SetLastReviewedCommit(item.ID, "hash-old"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLastReviewedCommit(item.ID, "hash-new"); err != nil {
		t.Fatal(err)
	}

	got, err := c.GetLastReviewedCommit(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hash-new" {
		t.Errorf("expected overwritten hash 'hash-new', got %q", got)
	}
}

func TestGetLastReviewedCommit_PersistedInGet(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	hash := "deadbeef00"
	if err := c.SetLastReviewedCommit(item.ID, hash); err != nil {
		t.Fatal(err)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastReviewedCommit != hash {
		t.Errorf("Droplet.LastReviewedCommit = %q, want %q", got.LastReviewedCommit, hash)
	}
}

func TestStats_WithData(t *testing.T) {
	c := testClient(t)

	// Add 2 open (queued), 1 in_progress (flowing), 3 delivered, 1 stagnant.
	c.Add("repo", "q1", "", 1, 3)
	c.Add("repo", "q2", "", 1, 3)
	item3, _ := c.Add("repo", "ip1", "", 1, 3)
	item4, _ := c.Add("repo", "d1", "", 1, 3)
	item5, _ := c.Add("repo", "d2", "", 1, 3)
	item6, _ := c.Add("repo", "d3", "", 1, 3)
	item7, _ := c.Add("repo", "s1", "", 1, 3)

	c.UpdateStatus(item3.ID, "in_progress")
	c.CloseItem(item4.ID)
	c.CloseItem(item5.ID)
	c.CloseItem(item6.ID)
	c.Escalate(item7.ID, "stuck")

	s, err := c.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.Queued != 2 {
		t.Errorf("Queued = %d, want 2", s.Queued)
	}
	if s.Flowing != 1 {
		t.Errorf("Flowing = %d, want 1", s.Flowing)
	}
	if s.Delivered != 3 {
		t.Errorf("Delivered = %d, want 3", s.Delivered)
	}
	if s.Stagnant != 1 {
		t.Errorf("Stagnant = %d, want 1", s.Stagnant)
	}
}

func TestSearch(t *testing.T) {
	c := testClient(t)
	c.Add("repo", "Fix login bug", "", 1, 3)
	c.Add("repo", "Add dashboard feature", "", 2, 3)
	c.Add("repo", "Fix signup flow", "", 1, 2)
	ip, _ := c.Add("repo", "Refactor auth module", "", 3, 3)
	c.UpdateStatus(ip.ID, "in_progress")

	t.Run("empty query returns all", func(t *testing.T) {
		results, err := c.Search("", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 4 {
			t.Fatalf("expected 4 results, got %d", len(results))
		}
	})

	t.Run("query matches title substring case-insensitive", func(t *testing.T) {
		results, err := c.Search("fix", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		titles := map[string]bool{}
		for _, r := range results {
			titles[r.Title] = true
		}
		if !titles["Fix login bug"] {
			t.Error("expected 'Fix login bug' in results")
		}
		if !titles["Fix signup flow"] {
			t.Error("expected 'Fix signup flow' in results")
		}
	})

	t.Run("status filter", func(t *testing.T) {
		results, err := c.Search("", "in_progress", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Title != "Refactor auth module" {
			t.Errorf("expected 'Refactor auth module', got %q", results[0].Title)
		}
	})

	t.Run("priority filter", func(t *testing.T) {
		results, err := c.Search("", "", 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("combined query and status", func(t *testing.T) {
		results, err := c.Search("fix", "open", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("no matches returns empty slice", func(t *testing.T) {
		results, err := c.Search("xyz-no-match", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 0 {
			t.Fatalf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("results ordered by priority then created_at", func(t *testing.T) {
		results, err := c.Search("", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		// Priority 1 items should come before priority 2 and 3.
		if results[0].Priority > results[len(results)-1].Priority {
			t.Errorf("results not ordered by priority: first=%d last=%d",
				results[0].Priority, results[len(results)-1].Priority)
		}
	})
}

func TestUpdateTitle(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Old title", "", 1, 3)

	if err := c.UpdateTitle(item.ID, "New title"); err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Title != "New title" {
		t.Errorf("title = %q, want %q", got.Title, "New title")
	}
}

func TestUpdateTitle_NotFound(t *testing.T) {
	c := testClient(t)
	if err := c.UpdateTitle("nonexistent", "New title"); err == nil {
		t.Error("expected error for missing item")
	}
}

func TestSetOutcome(t *testing.T) {
	for _, outcome := range []string{"pass", "recirculate", "block"} {
		t.Run(outcome, func(t *testing.T) {
			c := testClient(t)
			item, _ := c.Add("myrepo", "Task", "", 1, 3)
			if err := c.SetOutcome(item.ID, outcome); err != nil {
				t.Fatal(err)
			}
			got, _ := c.Get(item.ID)
			if got.Outcome != outcome {
				t.Errorf("outcome = %q, want %q", got.Outcome, outcome)
			}
		})
	}
}

func TestSetOutcome_Clear(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.SetOutcome(item.ID, "pass")

	if err := c.SetOutcome(item.ID, ""); err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Outcome != "" {
		t.Errorf("outcome = %q, want empty after clear", got.Outcome)
	}
}

func TestSetOutcome_NotFound(t *testing.T) {
	c := testClient(t)
	if err := c.SetOutcome("nonexistent", "pass"); err == nil {
		t.Error("expected error for missing item")
	}
}

func TestSetCataractae(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	if err := c.SetCataractae(item.ID, "review"); err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.CurrentCataractae != "review" {
		t.Errorf("current_cataractae = %q, want %q", got.CurrentCataractae, "review")
	}
}

func TestSetCataractae_NotFound(t *testing.T) {
	c := testClient(t)
	if err := c.SetCataractae("nonexistent", "review"); err == nil {
		t.Error("expected error for missing item")
	}
}

func TestPurge(t *testing.T) {
	c := testClient(t)
	delivered, _ := c.Add("myrepo", "Delivered", "", 1, 3)
	stagnant, _ := c.Add("myrepo", "Stagnant", "", 1, 3)
	inProgress, _ := c.Add("myrepo", "In progress", "", 1, 3)

	c.CloseItem(delivered.ID)        // status = delivered
	c.Escalate(stagnant.ID, "stuck") // status = stagnant
	c.UpdateStatus(inProgress.ID, "in_progress")

	// Negative duration sets cutoff in the future, making all items eligible by age.
	n, err := c.Purge(-time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("purged %d, want 2 (delivered + stagnant)", n)
	}

	// in_progress item must survive.
	if _, err := c.Get(inProgress.ID); err != nil {
		t.Errorf("in-progress item should not be purged: %v", err)
	}

	// delivered and stagnant must be gone.
	if item, _ := c.Get(delivered.ID); item != nil {
		t.Error("delivered item should have been purged")
	}
	if item, _ := c.Get(stagnant.ID); item != nil {
		t.Error("stagnant item should have been purged")
	}
}

func TestPurge_DryRun(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.CloseItem(item.ID)

	n, err := c.Purge(-time.Hour, true) // dry run
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("dry-run count = %d, want 1", n)
	}
	// Item must still exist after a dry run.
	if _, err := c.Get(item.ID); err != nil {
		t.Errorf("dry run should not delete item: %v", err)
	}
}

func TestPurge_LeavesInProgress(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)
	c.UpdateStatus(item.ID, "in_progress")

	n, err := c.Purge(-time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("purged %d, want 0 (in-progress must not be purged)", n)
	}
	if _, err := c.Get(item.ID); err != nil {
		t.Error("in-progress item should not be purged")
	}
}

func TestListRecentEvents_Empty(t *testing.T) {
	c := testClient(t)
	events, err := c.ListRecentEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

func TestListRecentEvents_WithEvents(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	// AddNote writes to cataractae_notes; Escalate writes to events.
	c.AddNote(item.ID, "implement", "wrote the code")
	c.Escalate(item.ID, "needs human review")

	events, err := c.ListRecentEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	for _, e := range events {
		if e.Droplet != item.ID {
			t.Errorf("event droplet = %q, want %q", e.Droplet, item.ID)
		}
	}
}

func TestListRecentEvents_Limit(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Task", "", 1, 3)

	for range 5 {
		c.AddNote(item.ID, "step", "note")
	}

	events, err := c.ListRecentEvents(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Errorf("got %d events, want 3 (limit enforced)", len(events))
	}
}
