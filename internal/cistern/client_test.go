package cistern

import (
	"path/filepath"
	"strings"
	"testing"
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
	if got.CurrentCataracta != "implement" {
		t.Errorf("current_step = %q, want %q", got.CurrentCataracta, "implement")
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
	if got.CurrentCataracta != "review" {
		t.Errorf("current_step = %q, want %q", got.CurrentCataracta, "review")
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
	if notes[0].CataractaName != "implement" || notes[0].Content != "wrote the code" {
		t.Errorf("note[0] = %+v", notes[0])
	}
	if notes[1].CataractaName != "review" || notes[1].Content != "looks good" {
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
