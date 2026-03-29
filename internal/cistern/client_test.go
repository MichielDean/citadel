package cistern

import (
	"database/sql"
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

// TestNew_FreshDB_DefaultComplexityIsTwo verifies that a fresh database created
// from schema.sql defaults the complexity column to 2 (full), not 3 (critical).
func TestNew_FreshDB_DefaultComplexityIsTwo(t *testing.T) {
	c := testClient(t)
	// Add a droplet, then fetch it back directly to check the schema-level default
	// is not involved (Add validates and stores explicitly). Instead, verify the
	// schema default by inserting a row without specifying complexity.
	_, err := c.db.Exec(`INSERT INTO droplets (id, repo, title) VALUES ('bf-test1', 'repo', 'title')`)
	if err != nil {
		t.Fatal(err)
	}
	var got int
	if err := c.db.QueryRow(`SELECT complexity FROM droplets WHERE id = 'bf-test1'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Errorf("schema DEFAULT complexity = %d, want 2 (full)", got)
	}
}

// TestNew_ComplexityMigration_RemapsOldSchemeValues verifies that when New() is
// called on a DB containing old-scheme complexity values (1=trivial, 2=standard,
// 3=full, 4=critical), they are remapped to the new scheme (1=standard, 2=full,
// 3=critical).
func TestNew_ComplexityMigration_RemapsOldSchemeValues(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migrate.db")

	// Seed the DB with old-scheme complexity values using the raw driver,
	// bypassing New() so the migration has not yet run.
	seedDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = seedDB.Exec(`CREATE TABLE IF NOT EXISTS droplets (
		id TEXT PRIMARY KEY,
		repo TEXT NOT NULL,
		title TEXT NOT NULL,
		description TEXT DEFAULT '',
		priority INTEGER DEFAULT 2,
		complexity INTEGER DEFAULT 3,
		status TEXT DEFAULT 'open',
		assignee TEXT DEFAULT '',
		current_cataractae TEXT DEFAULT '',
		outcome TEXT DEFAULT NULL,
		assigned_aqueduct TEXT DEFAULT '',
		last_reviewed_commit TEXT DEFAULT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatal(err)
	}
	// Insert rows with old-scheme values: trivial=1, standard=2, full=3, critical=4.
	for _, row := range []struct {
		id         string
		complexity int
	}{
		{"old-trivial", 1},
		{"old-standard", 2},
		{"old-full", 3},
		{"old-critical", 4},
	} {
		if _, err := seedDB.Exec(`INSERT INTO droplets (id, repo, title, complexity) VALUES (?, 'r', 't', ?)`, row.id, row.complexity); err != nil {
			t.Fatal(err)
		}
	}
	seedDB.Close()

	// Open with New() to trigger the migration.
	c, err := New(dbPath, "bf")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Given: old scheme 1=trivial → new scheme 1=standard (unchanged, trivial removed)
	// Given: old scheme 2=standard → new scheme 1=standard
	// Given: old scheme 3=full    → new scheme 2=full
	// Given: old scheme 4=critical → new scheme 3=critical
	cases := []struct {
		id      string
		wantNew int
	}{
		{"old-trivial", 1},
		{"old-standard", 1},
		{"old-full", 2},
		{"old-critical", 3},
	}
	for _, tc := range cases {
		var got int
		if err := c.db.QueryRow(`SELECT complexity FROM droplets WHERE id = ?`, tc.id).Scan(&got); err != nil {
			t.Fatalf("id=%s: %v", tc.id, err)
		}
		if got != tc.wantNew {
			t.Errorf("id=%s: complexity after migration = %d, want %d", tc.id, got, tc.wantNew)
		}
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
	// Notes are returned newest-first (DESC).
	if notes[0].CataractaeName != "review" || notes[0].Content != "looks good" {
		t.Errorf("note[0] = %+v", notes[0])
	}
	if notes[1].CataractaeName != "implement" || notes[1].Content != "wrote the code" {
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
	// Out-of-range complexity should default to 2 (full).
	item, err := c.Add("myrepo", "Bad cx", "", 2, 99)
	if err != nil {
		t.Fatal(err)
	}
	if item.Complexity != 2 {
		t.Errorf("complexity = %d, want 2 (default)", item.Complexity)
	}
}

func TestGetReady_ReturnsComplexity(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Critical task", "", 1, 3)

	item, err := c.GetReady("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if item.Complexity != 3 {
		t.Errorf("complexity = %d, want 3", item.Complexity)
	}
}

func TestList_ReturnsComplexity(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Standard", "", 1, 1)
	c.Add("myrepo", "Critical", "", 1, 3)

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
	if items[1].Complexity != 3 {
		t.Errorf("items[1].Complexity = %d, want 3", items[1].Complexity)
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

func ptr[T any](v T) *T { return &v }

func TestEditDroplet_Description(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("repo", "Title", "old desc", 2, 3)

	err := c.EditDroplet(item.ID, EditDropletFields{Description: ptr("new desc")})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Description != "new desc" {
		t.Errorf("description = %q, want %q", got.Description, "new desc")
	}
	// Other fields unchanged.
	if got.Priority != 2 {
		t.Errorf("priority = %d, want 2", got.Priority)
	}
	if got.Complexity != 3 {
		t.Errorf("complexity = %d, want 3", got.Complexity)
	}
}

func TestEditDroplet_Complexity(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("repo", "Title", "desc", 2, 3)

	err := c.EditDroplet(item.ID, EditDropletFields{Complexity: ptr(1)})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Complexity != 1 {
		t.Errorf("complexity = %d, want 1", got.Complexity)
	}
	if got.Description != "desc" {
		t.Errorf("description changed unexpectedly: %q", got.Description)
	}
}

func TestEditDroplet_Priority(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("repo", "Title", "", 2, 3)

	err := c.EditDroplet(item.ID, EditDropletFields{Priority: ptr(1)})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Priority != 1 {
		t.Errorf("priority = %d, want 1", got.Priority)
	}
}

func TestEditDroplet_AllFields(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("repo", "Title", "old", 3, 3)

	err := c.EditDroplet(item.ID, EditDropletFields{
		Description: ptr("updated"),
		Complexity:  ptr(2),
		Priority:    ptr(1),
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := c.Get(item.ID)
	if got.Description != "updated" {
		t.Errorf("description = %q, want %q", got.Description, "updated")
	}
	if got.Complexity != 2 {
		t.Errorf("complexity = %d, want 2", got.Complexity)
	}
	if got.Priority != 1 {
		t.Errorf("priority = %d, want 1", got.Priority)
	}
}

func TestEditDroplet_GuardInProgress(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("repo", "Title", "", 1, 3)
	c.UpdateStatus(item.ID, "in_progress")

	err := c.EditDroplet(item.ID, EditDropletFields{Description: ptr("new")})
	if err == nil {
		t.Fatal("expected error for in_progress droplet")
	}
	if !strings.Contains(err.Error(), "cannot edit a droplet that has been picked up") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "in_progress") {
		t.Errorf("error should mention status, got: %v", err)
	}
}

func TestEditDroplet_GuardDelivered(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("repo", "Title", "", 1, 3)
	c.CloseItem(item.ID)

	err := c.EditDroplet(item.ID, EditDropletFields{Description: ptr("new")})
	if err == nil {
		t.Fatal("expected error for delivered droplet")
	}
	if !strings.Contains(err.Error(), "cannot edit a droplet that has been picked up") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEditDroplet_AllowStagnant(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("repo", "Title", "old", 1, 3)
	c.Escalate(item.ID, "stuck")

	err := c.EditDroplet(item.ID, EditDropletFields{Description: ptr("updated")})
	if err != nil {
		t.Fatalf("expected stagnant droplet to be editable, got: %v", err)
	}

	got, _ := c.Get(item.ID)
	if got.Description != "updated" {
		t.Errorf("description = %q, want %q", got.Description, "updated")
	}
}

func TestEditDroplet_NoFields(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("repo", "Title", "desc", 2, 3)

	// No-op: no fields specified should be fine at the client layer.
	err := c.EditDroplet(item.ID, EditDropletFields{})
	if err != nil {
		t.Fatalf("unexpected error for no-op edit: %v", err)
	}
}

func TestEditDroplet_InvalidComplexity(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("repo", "Title", "desc", 2, 3)

	for _, bad := range []int{0, -1, 4, 5, 100} {
		err := c.EditDroplet(item.ID, EditDropletFields{Complexity: ptr(bad)})
		if err == nil {
			t.Errorf("expected error for complexity=%d", bad)
		} else if !strings.Contains(err.Error(), "complexity must be between 1 and 3") {
			t.Errorf("complexity=%d: unexpected error: %v", bad, err)
		}
	}

	// Valid boundary values should succeed.
	for _, ok := range []int{1, 3} {
		if err := c.EditDroplet(item.ID, EditDropletFields{Complexity: ptr(ok)}); err != nil {
			t.Errorf("complexity=%d should be valid, got: %v", ok, err)
		}
	}
}

func TestEditDroplet_NotFound(t *testing.T) {
	c := testClient(t)

	err := c.EditDroplet("bf-xxxxx", EditDropletFields{Description: ptr("x")})
	if err == nil {
		t.Fatal("expected error for unknown droplet")
	}
}

func TestCancel_SetsStatusCancelled(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Superseded feature", "", 1, 3)

	if err := c.Cancel(item.ID, "superseded by newer approach"); err != nil {
		t.Fatal(err)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "cancelled" {
		t.Errorf("status = %q, want %q", got.Status, "cancelled")
	}
}

func TestCancel_RecordsReasonAsNote(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Old feature", "", 1, 3)

	reason := "filed in error"
	if err := c.Cancel(item.ID, reason); err != nil {
		t.Fatal(err)
	}

	notes, err := c.GetNotes(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) == 0 {
		t.Fatal("expected at least one note after cancel with reason")
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n.Content, reason) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("cancel reason %q not found in notes: %v", reason, notes)
	}
}

func TestCancel_EmptyReason_NoNote(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Old feature", "", 1, 3)

	if err := c.Cancel(item.ID, ""); err != nil {
		t.Fatal(err)
	}

	notes, err := c.GetNotes(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("expected no notes for cancel with empty reason, got %d", len(notes))
	}
}

func TestCancel_NotFound(t *testing.T) {
	c := testClient(t)
	if err := c.Cancel("nonexistent", "reason"); err == nil {
		t.Error("expected error for missing droplet")
	}
}

func TestCancel_ExcludedFromGetReady(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Old feature", "", 1, 3)

	if err := c.Cancel(item.ID, "won't do"); err != nil {
		t.Fatal(err)
	}

	// GetReady must not return a cancelled droplet.
	got, err := c.GetReady("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("GetReady returned cancelled droplet %s — cancelled droplets must never be dispatched", got.ID)
	}
}

func TestList_ExcludesCancelledByDefault(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Active", "", 1, 3)
	cancelled, _ := c.Add("myrepo", "Cancelled", "", 1, 3)
	c.Cancel(cancelled.ID, "not needed")

	// List with no status filter must not include cancelled items.
	items, err := c.List("myrepo", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == cancelled.ID {
			t.Errorf("List returned cancelled droplet %s — cancelled droplets must be hidden by default", cancelled.ID)
		}
	}
}

func TestList_CancelledStatus_ReturnsOnlyCancelled(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Active", "", 1, 3)
	cancelled, _ := c.Add("myrepo", "Cancelled", "", 1, 3)
	c.Cancel(cancelled.ID, "not needed")

	items, err := c.List("myrepo", "cancelled")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("List(cancelled) returned %d items, want 1", len(items))
	}
	if items[0].ID != cancelled.ID {
		t.Errorf("returned item %s, want %s", items[0].ID, cancelled.ID)
	}
}

// TestCancel_NotFound_NoOrphanNote verifies that cancelling a nonexistent droplet
// does NOT create an orphan note row (UPDATE must happen before AddNote).
func TestCancel_NotFound_NoOrphanNote(t *testing.T) {
	c := testClient(t)

	// Cancel a droplet that does not exist — must return an error.
	err := c.Cancel("nonexistent-id", "some reason")
	if err == nil {
		t.Fatal("expected error for nonexistent droplet, got nil")
	}

	// No note should have been inserted for the nonexistent droplet.
	notes, err2 := c.GetNotes("nonexistent-id")
	if err2 != nil {
		t.Fatal(err2)
	}
	if len(notes) != 0 {
		t.Errorf("Cancel on nonexistent droplet created %d orphan note(s); want 0", len(notes))
	}
}

// TestPurge_IncludesCancelled verifies that cancelled droplets are cleaned up by Purge.
func TestPurge_IncludesCancelled(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Cancelled task", "", 1, 3)
	c.Cancel(item.ID, "won't do")

	n, err := c.Purge(-time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("Purge returned %d, want 1 (cancelled item)", n)
	}
	if got, _ := c.Get(item.ID); got != nil {
		t.Error("cancelled item should have been purged")
	}
}

// TestGetReady_CancelledDependency_DoesNotBlock verifies that a droplet whose
// dependency was cancelled is still dispatched (cancelled != unresolved).
func TestGetReady_CancelledDependency_DoesNotBlock(t *testing.T) {
	c := testClient(t)
	dep, _ := c.Add("myrepo", "Dependency", "", 1, 3)
	child, _ := c.Add("myrepo", "Child", "", 2, 3, dep.ID)

	// Cancel the dependency instead of delivering it.
	if err := c.Cancel(dep.ID, "no longer needed"); err != nil {
		t.Fatal(err)
	}

	// Child should now be dispatchable — cancelled dep must not block it.
	got, err := c.GetReady("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("GetReady returned nil — cancelled dependency should not block child")
	}
	if got.ID != child.ID {
		t.Errorf("GetReady returned %s, want child %s", got.ID, child.ID)
	}
}

// TestSearch_ExcludesCancelledByDefault verifies that Search omits cancelled
// droplets when no status filter is given (consistent with List behaviour).
func TestSearch_ExcludesCancelledByDefault(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Active task", "", 1, 3)
	cancelled, _ := c.Add("myrepo", "Cancelled task", "", 1, 3)
	c.Cancel(cancelled.ID, "not needed")

	results, err := c.Search("", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.ID == cancelled.ID {
			t.Errorf("Search returned cancelled droplet %s — must be hidden by default", cancelled.ID)
		}
	}
}

// TestGetReady_CaseInsensitiveRepo_ReturnsDropletStoredWithWrongCase verifies that
// GetReady("PortfolioWebsite") returns a droplet stored as "portfoliowebsite".
func TestGetReady_CaseInsensitiveRepo_ReturnsDropletStoredWithWrongCase(t *testing.T) {
	c := testClient(t)
	// Given: a droplet stored with lower-case repo name.
	_, err := c.Add("portfoliowebsite", "My task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}

	// When: GetReady is called with the canonical casing.
	got, err := c.GetReady("PortfolioWebsite")
	if err != nil {
		t.Fatal(err)
	}

	// Then: the droplet is returned.
	if got == nil {
		t.Fatal("GetReady(PortfolioWebsite): expected droplet, got nil")
	}
	if got.Title != "My task" {
		t.Errorf("GetReady(PortfolioWebsite): title = %q, want %q", got.Title, "My task")
	}
}

// TestGetReadyForAqueduct_CaseInsensitiveRepo_ReturnsDroplet verifies that
// GetReadyForAqueduct respects case-insensitive repo matching.
func TestGetReadyForAqueduct_CaseInsensitiveRepo_ReturnsDroplet(t *testing.T) {
	c := testClient(t)
	// Given: a droplet stored with upper-case repo name.
	_, err := c.Add("CISTERN", "Cistern task", "", 1, 2)
	if err != nil {
		t.Fatal(err)
	}

	// When: GetReadyForAqueduct is called with the canonical lower-case name.
	got, err := c.GetReadyForAqueduct("cistern", "default")
	if err != nil {
		t.Fatal(err)
	}

	// Then: the droplet is returned.
	if got == nil {
		t.Fatal("GetReadyForAqueduct(cistern): expected droplet, got nil")
	}
	if got.Title != "Cistern task" {
		t.Errorf("GetReadyForAqueduct(cistern): title = %q, want %q", got.Title, "Cistern task")
	}
}

// TestList_CaseInsensitiveRepo_ReturnsDroplets verifies that List filters by repo
// case-insensitively.
func TestList_CaseInsensitiveRepo_ReturnsDroplets(t *testing.T) {
	c := testClient(t)
	// Given: droplets stored with mixed-case repo names.
	c.Add("scaledtest", "Task A", "", 1, 2)
	c.Add("SCALEDTEST", "Task B", "", 1, 2)
	c.Add("other", "Task C", "", 1, 2)

	// When: List is called with canonical casing.
	items, err := c.List("ScaledTest", "")
	if err != nil {
		t.Fatal(err)
	}

	// Then: both ScaledTest-repo droplets are returned, "other" is excluded.
	if len(items) != 2 {
		t.Fatalf("List(ScaledTest): got %d items, want 2", len(items))
	}
}

// TestNew_RepoCaseMigration_NormalizesCanonicalRepos verifies that when New() is
// called on a DB containing wrong-cased canonical repo values, they are normalized
// to canonical casing (cistern, ScaledTest, PortfolioWebsite).
func TestNew_RepoCaseMigration_NormalizesCanonicalRepos(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migrate_repo.db")

	// Seed the DB with wrong-cased repo values, bypassing New() so the migration
	// has not yet run.
	seedDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = seedDB.Exec(`CREATE TABLE IF NOT EXISTS droplets (
		id TEXT PRIMARY KEY,
		repo TEXT NOT NULL,
		title TEXT NOT NULL,
		description TEXT DEFAULT '',
		priority INTEGER DEFAULT 2,
		complexity INTEGER DEFAULT 2,
		status TEXT DEFAULT 'open',
		assignee TEXT DEFAULT '',
		current_cataractae TEXT DEFAULT '',
		outcome TEXT DEFAULT NULL,
		assigned_aqueduct TEXT DEFAULT '',
		last_reviewed_commit TEXT DEFAULT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id   string
		repo string
	}{
		{"id-1", "CISTERN"},
		{"id-2", "Cistern"},
		{"id-3", "SCALEDTEST"},
		{"id-4", "scaledtest"},
		{"id-5", "portfoliowebsite"},
		{"id-6", "PORTFOLIOWEBSITE"},
		{"id-7", "unrelated"},         // should not be touched
		{"id-8", "cistern"},           // already canonical — should not change
		{"id-9", "ScaledTest"},        // already canonical — should not change
		{"id-10", "PortfolioWebsite"}, // already canonical — should not change
	} {
		if _, err := seedDB.Exec(`INSERT INTO droplets (id, repo, title) VALUES (?, ?, 't')`, row.id, row.repo); err != nil {
			t.Fatal(err)
		}
	}
	seedDB.Close()

	// Open with New() to trigger the migration.
	c, err := New(dbPath, "bf")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	cases := []struct {
		id       string
		wantRepo string
	}{
		{"id-1", "cistern"},
		{"id-2", "cistern"},
		{"id-3", "ScaledTest"},
		{"id-4", "ScaledTest"},
		{"id-5", "PortfolioWebsite"},
		{"id-6", "PortfolioWebsite"},
		{"id-7", "unrelated"},
		{"id-8", "cistern"},
		{"id-9", "ScaledTest"},
		{"id-10", "PortfolioWebsite"},
	}
	for _, tc := range cases {
		var got string
		if err := c.db.QueryRow(`SELECT repo FROM droplets WHERE id = ?`, tc.id).Scan(&got); err != nil {
			t.Fatalf("id=%s: %v", tc.id, err)
		}
		if got != tc.wantRepo {
			t.Errorf("id=%s: repo after migration = %q, want %q", tc.id, got, tc.wantRepo)
		}
	}
}

// TestNew_RepoCaseMigration_IsIdempotent verifies that running New() on an already-
// migrated database does not alter canonical repo values a second time.
func TestNew_RepoCaseMigration_IsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idempotent_repo.db")

	// First open: migration runs.
	c1, err := New(dbPath, "bf")
	if err != nil {
		t.Fatal(err)
	}
	c1.Add("cistern", "Task", "", 1, 2)
	c1.Close()

	// Second open: migration must be a no-op.
	c2, err := New(dbPath, "bf")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	// Verify the migration sentinel exists exactly once.
	var count int
	if err := c2.db.QueryRow(`SELECT COUNT(*) FROM _schema_migrations WHERE id = 'repo_case_normalize'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("_schema_migrations sentinel count = %d, want 1", count)
	}
}

// TestSearch_CancelledStatus_ReturnsCancelled verifies that Search with an explicit
// status="cancelled" filter returns cancelled droplets.
func TestSearch_CancelledStatus_ReturnsCancelled(t *testing.T) {
	c := testClient(t)
	c.Add("myrepo", "Active task", "", 1, 3)
	cancelled, _ := c.Add("myrepo", "Cancelled task", "", 1, 3)
	c.Cancel(cancelled.ID, "not needed")

	results, err := c.Search("", "cancelled", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("Search(cancelled) returned %d items, want 1", len(results))
	}
	if results[0].ID != cancelled.ID {
		t.Errorf("Search(cancelled) returned %s, want %s", results[0].ID, cancelled.ID)
	}
}

// TestCloseItem_ClearsAssignedAqueduct verifies that delivering a droplet removes
// the assigned_aqueduct so no ghost assignments linger after terminal state.
func TestCloseItem_ClearsAssignedAqueduct(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Feature", "", 1, 2)
	c.SetAssignedAqueduct(item.ID, "cistern-alpha")
	pre, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pre.AssignedAqueduct != "cistern-alpha" {
		t.Fatal("precondition failed: SetAssignedAqueduct did not set the field")
	}

	if err := c.CloseItem(item.ID); err != nil {
		t.Fatal(err)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedAqueduct != "" {
		t.Errorf("AssignedAqueduct after CloseItem = %q, want empty string", got.AssignedAqueduct)
	}
}

// TestEscalate_ClearsAssignedAqueduct verifies that escalating a droplet to stagnant
// removes assigned_aqueduct so no ghost assignments linger.
func TestEscalate_ClearsAssignedAqueduct(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Stuck task", "", 1, 2)
	c.SetAssignedAqueduct(item.ID, "cistern-beta")
	pre, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pre.AssignedAqueduct != "cistern-beta" {
		t.Fatal("precondition failed: SetAssignedAqueduct did not set the field")
	}

	if err := c.Escalate(item.ID, "timeout"); err != nil {
		t.Fatal(err)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedAqueduct != "" {
		t.Errorf("AssignedAqueduct after Escalate = %q, want empty string", got.AssignedAqueduct)
	}
}

// TestCancel_ClearsAssignedAqueduct verifies that cancelling a droplet removes
// assigned_aqueduct so no ghost assignments linger.
func TestCancel_ClearsAssignedAqueduct(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Obsolete task", "", 1, 2)
	c.SetAssignedAqueduct(item.ID, "cistern-gamma")
	pre, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pre.AssignedAqueduct != "cistern-gamma" {
		t.Fatal("precondition failed: SetAssignedAqueduct did not set the field")
	}

	if err := c.Cancel(item.ID, "no longer needed"); err != nil {
		t.Fatal(err)
	}

	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedAqueduct != "" {
		t.Errorf("AssignedAqueduct after Cancel = %q, want empty string", got.AssignedAqueduct)
	}
}

// TestSetAssignedAqueduct_WhenAlreadySet_DoesNotOverwrite verifies that the
// conditional WHERE clause prevents a second SetAssignedAqueduct call from
// overwriting an existing assignment.
func TestSetAssignedAqueduct_WhenAlreadySet_DoesNotOverwrite(t *testing.T) {
	c := testClient(t)
	item, _ := c.Add("myrepo", "Contested task", "", 1, 2)

	// First assignment should succeed.
	if err := c.SetAssignedAqueduct(item.ID, "cistern-alpha"); err != nil {
		t.Fatal(err)
	}
	pre, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pre.AssignedAqueduct != "cistern-alpha" {
		t.Fatalf("precondition failed: want AssignedAqueduct = %q, got %q", "cistern-alpha", pre.AssignedAqueduct)
	}

	// Second assignment with a different value must not overwrite.
	if err := c.SetAssignedAqueduct(item.ID, "cistern-beta"); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AssignedAqueduct != "cistern-alpha" {
		t.Errorf("AssignedAqueduct after second SetAssignedAqueduct = %q, want %q (original must not be overwritten)", got.AssignedAqueduct, "cistern-alpha")
	}
}
