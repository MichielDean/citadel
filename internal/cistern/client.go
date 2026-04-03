// Package queue provides a SQLite-backed work queue for Cistern.
//
// Each droplet flows through an aqueduct. The queue stores droplets,
// cataractae notes, and events. No external dependencies — just SQLite.
package cistern

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// externalRefRE validates the 'provider:key' format for external_ref values.
// Both parts must consist solely of characters safe for use in git branch names
// and shell awk extraction: letters, digits, hyphens, underscores, and (key only)
// dots. Spaces and git-invalid characters (~, ^, :, ?, *, [, \) are rejected.
var externalRefRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+:[a-zA-Z0-9._-]+$`)

//go:embed schema.sql
var schema string

const charset = "abcdefghijklmnopqrstuvwxyz0123456789"

// Droplet represents a unit of work flowing through the cistern.
type Droplet struct {
	ID                string `json:"id"`
	Repo              string `json:"repo"`
	Title             string `json:"title"`
	Description       string `json:"description"`
	Priority          int    `json:"priority"`
	Complexity        int    `json:"complexity"`
	Status            string `json:"status"`
	Assignee          string `json:"assignee"` // empty string when unassigned
	CurrentCataractae string `json:"current_cataractae"`
	// Outcome is set by agents via `ct droplet pass/recirculate/pool`.
	// Empty string means no outcome yet (NULL in DB).
	Outcome string `json:"outcome,omitempty"`
	// AssignedAqueduct records which aqueduct operator is currently holding this
	// droplet. Set when first dispatched; cleared on terminal states (delivered,
	// pooled, cancelled) so no ghost assignments linger.
	AssignedAqueduct string `json:"assigned_aqueduct,omitempty"`
	// LastReviewedCommit is the HEAD commit hash at the time the last review
	// diff was generated. Used to detect phantom commits (implement pass without
	// any new commits since the last review).
	LastReviewedCommit string `json:"last_reviewed_commit,omitempty"`
	// ExternalRef is the external issue reference for imported issues.
	// Format: 'provider:key' (e.g. 'jira:DPF-456', 'linear:LIN-789').
	// Empty string means no external reference (NULL in DB).
	ExternalRef string `json:"external_ref,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// StageDispatchedAt is set only when a worker is assigned to this droplet
	// (Assign(id, worker, step) with non-empty worker). Unlike UpdatedAt, it is
	// not bumped by notes, outcome signals, or other state changes — making it
	// the reliable anchor for the zombie detection age guard.
	StageDispatchedAt time.Time `json:"stage_dispatched_at,omitempty"`
}

// CataractaeNote is a note attached by a workflow cataractae.
type CataractaeNote struct {
	ID             int       `json:"id"`
	DropletID      string    `json:"droplet_id"`
	CataractaeName string    `json:"cataractae_name"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

// Client is a SQLite-backed work queue client.
type Client struct {
	db     *sql.DB
	prefix string
}

// New opens (or creates) a SQLite database at dbPath, runs the schema, and
// returns a Client. The prefix is used when generating droplet IDs.
func New(dbPath, prefix string) (*Client, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("cistern: open %s: %w", dbPath, err)
	}
	// SQLite only supports one concurrent writer. Limit the connection pool to
	// a single connection so concurrent goroutines (dispatch, heartbeat, observe)
	// queue behind the same connection rather than racing across independent
	// *sql.DB pools, which causes "database is locked" errors even with WAL mode.
	db.SetMaxOpenConns(1)
	// Migrations: rename legacy tables/columns before applying schema.
	// Each statement is idempotent — errors are ignored (already-renamed or fresh DB).
	db.Exec(`ALTER TABLE work_items RENAME TO droplets`)
	db.Exec(`ALTER TABLE drops RENAME TO droplets`)
	db.Exec(`ALTER TABLE step_notes RENAME TO cataractae_notes`)
	db.Exec(`ALTER TABLE cataractae_notes RENAME COLUMN item_id TO droplet_id`)
	db.Exec(`ALTER TABLE cataractae_notes RENAME COLUMN drop_id TO droplet_id`)
	db.Exec(`ALTER TABLE cataractae_notes RENAME COLUMN step_name TO cataractae_name`)
	db.Exec(`ALTER TABLE events RENAME COLUMN item_id TO droplet_id`)
	db.Exec(`ALTER TABLE events RENAME COLUMN drop_id TO droplet_id`)
	db.Exec(`ALTER TABLE droplets RENAME COLUMN current_step TO current_cataractae`)
	db.Exec(`ALTER TABLE droplets ADD COLUMN complexity INTEGER DEFAULT 2`)
	// Idempotent one-time migration: remap old 4-level complexity scheme
	// (1=trivial, 2=standard, 3=full, 4=critical) to new 3-level scheme
	// (1=standard, 2=full, 3=critical). Tracked in _schema_migrations so it
	// runs exactly once per database.
	db.Exec(`CREATE TABLE IF NOT EXISTS _schema_migrations (id TEXT PRIMARY KEY)`)
	var migrationDone int
	db.QueryRow(`SELECT COUNT(*) FROM _schema_migrations WHERE id = 'complexity_renumber'`).Scan(&migrationDone)
	if migrationDone == 0 {
		tx, err := db.Begin()
		if err == nil {
			tx.Exec(`UPDATE droplets SET complexity = complexity - 1 WHERE complexity >= 2`)
			tx.Exec(`INSERT OR IGNORE INTO _schema_migrations (id) VALUES ('complexity_renumber')`)
			tx.Commit()
		}
	}
	// Idempotent one-time migration: normalize stored repo values to canonical casing
	// (cistern, ScaledTest, PortfolioWebsite). Tracked in _schema_migrations so it
	// runs exactly once per database.
	var repoCaseMigrationDone int
	db.QueryRow(`SELECT COUNT(*) FROM _schema_migrations WHERE id = 'repo_case_normalize'`).Scan(&repoCaseMigrationDone)
	if repoCaseMigrationDone == 0 {
		tx, err := db.Begin()
		if err == nil {
			for _, canonical := range []string{"cistern", "ScaledTest", "PortfolioWebsite"} {
				tx.Exec(
					`UPDATE droplets SET repo = ? WHERE LOWER(repo) = LOWER(?) AND repo != ?`,
					canonical, canonical, canonical,
				)
			}
			tx.Exec(`INSERT OR IGNORE INTO _schema_migrations (id) VALUES ('repo_case_normalize')`)
			tx.Commit()
		}
	}
	db.Exec(`ALTER TABLE droplets ADD COLUMN outcome TEXT DEFAULT NULL`)
	// Vocabulary migrations: update legacy status values to canonical vocabulary.
	db.Exec(`UPDATE droplets SET status = 'pooled' WHERE status IN ('stagnant', 'blocked', 'escalated')`)
	// Dependency table migration for existing DBs (idempotent — IF NOT EXISTS).
	db.Exec(`CREATE TABLE IF NOT EXISTS droplet_dependencies (
		droplet_id TEXT NOT NULL REFERENCES droplets(id),
		depends_on TEXT NOT NULL REFERENCES droplets(id),
		PRIMARY KEY (droplet_id, depends_on)
	)`)
	// Issues table migration for existing DBs (idempotent — IF NOT EXISTS).
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS droplet_issues (
		id          TEXT PRIMARY KEY,
		droplet_id  TEXT NOT NULL REFERENCES droplets(id),
		flagged_by  TEXT NOT NULL,
		flagged_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
		description TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'open',
		evidence    TEXT,
		resolved_at DATETIME
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("cistern: droplet_issues migration: %w", err)
	}
	db.Exec(`UPDATE droplets SET status = 'delivered' WHERE status = 'closed'`)
	// Sticky aqueduct assignment migration (idempotent).
	db.Exec(`ALTER TABLE droplets ADD COLUMN assigned_aqueduct TEXT DEFAULT ''`)
	// Phantom commit prevention: track the last reviewed commit hash (idempotent).
	db.Exec(`ALTER TABLE droplets ADD COLUMN last_reviewed_commit TEXT DEFAULT NULL`)
	// External reference for imported issues (idempotent).
	db.Exec(`ALTER TABLE droplets ADD COLUMN external_ref TEXT DEFAULT NULL`)
	// Stage dispatch timestamp: set only when a worker is assigned (idempotent).
	db.Exec(`ALTER TABLE droplets ADD COLUMN stage_dispatched_at DATETIME DEFAULT NULL`)
	// Vocabulary: cataracta → cataractae (idempotent — errors ignored on already-renamed DBs).
	db.Exec(`ALTER TABLE cataracta_notes RENAME TO cataractae_notes`)
	db.Exec(`ALTER TABLE cataractae_notes RENAME COLUMN cataracta_name TO cataractae_name`)
	db.Exec(`ALTER TABLE droplets RENAME COLUMN current_cataracta TO current_cataractae`)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("cistern: schema: %w", err)
	}
	return &Client{db: db, prefix: prefix}, nil
}

// Close closes the underlying database connection.
func (c *Client) Close() error {
	return c.db.Close()
}

func (c *Client) generateID() (string, error) {
	b := make([]byte, 5)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		b[i] = charset[n.Int64()]
	}
	return c.prefix + "-" + string(b), nil
}

// Add creates a new droplet and returns it. Optional deps are dependency IDs
// that must be delivered before this droplet can be dispatched.
func (c *Client) Add(repo, title, description string, priority, complexity int, deps ...string) (*Droplet, error) {
	if complexity < 1 || complexity > 3 {
		complexity = 2
	}
	id, err := c.generateID()
	if err != nil {
		return nil, fmt.Errorf("cistern: generate id: %w", err)
	}

	// Validate dep IDs before inserting.
	for _, dep := range deps {
		var exists int
		if err := c.db.QueryRow(`SELECT COUNT(*) FROM droplets WHERE id = ?`, dep).Scan(&exists); err != nil {
			return nil, fmt.Errorf("cistern: validate dep %s: %w", dep, err)
		}
		if exists == 0 {
			return nil, fmt.Errorf("cistern: dependency %s not found", dep)
		}
	}

	tx, err := c.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("cistern: begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	if _, err = tx.Exec(
		`INSERT INTO droplets (id, repo, title, description, priority, complexity, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'open', ?, ?)`,
		id, repo, title, description, priority, complexity, now, now,
	); err != nil {
		return nil, fmt.Errorf("cistern: add: %w", err)
	}

	for _, dep := range deps {
		if _, err := tx.Exec(
			`INSERT INTO droplet_dependencies (droplet_id, depends_on) VALUES (?, ?)`,
			id, dep,
		); err != nil {
			return nil, fmt.Errorf("cistern: add dep %s: %w", dep, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("cistern: commit: %w", err)
	}

	return &Droplet{
		ID:          id,
		Repo:        repo,
		Title:       title,
		Description: description,
		Priority:    priority,
		Complexity:  complexity,
		Status:      "open",
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// AddDroplet is a convenience method that adds a droplet and sets its external reference.
func (c *Client) AddDroplet(repo, title, description, externalRef string, priority, complexity int) (*Droplet, error) {
	droplet, err := c.Add(repo, title, description, priority, complexity)
	if err != nil {
		return nil, err
	}
	if err := c.SetExternalRef(droplet.ID, externalRef); err != nil {
		return nil, err
	}
	droplet.ExternalRef = externalRef
	return droplet, nil
}

// GetReady atomically selects the next open droplet for a repo and marks it
// in-progress within a single transaction. Ordered by priority (lower number =
// higher priority) then FIFO within the same priority. Returns nil if no work
// is available.
func (c *Client) GetReady(repo string) (*Droplet, error) {
	tx, err := c.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("cistern: begin tx: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRow(
		`SELECT id, repo, title, description, priority, complexity, status, assignee, current_cataractae, outcome, assigned_aqueduct, last_reviewed_commit, external_ref, created_at, updated_at, stage_dispatched_at
		 FROM droplets d
		 WHERE d.repo = ? COLLATE NOCASE AND d.status = 'open'
		   AND NOT EXISTS (
		     SELECT 1 FROM droplet_dependencies dep
		     JOIN droplets dep_d ON dep_d.id = dep.depends_on
		     WHERE dep.droplet_id = d.id AND dep_d.status NOT IN ('delivered', 'cancelled')
		   )
		 ORDER BY d.priority ASC, d.created_at ASC
		 LIMIT 1`,
		repo,
	)

	var droplet Droplet
	var assignee, currentCataracta, outcome, assignedAqueduct, lastReviewedCommit, externalRef sql.NullString
	var stageDispatchedAt sql.NullTime
	err = row.Scan(
		&droplet.ID, &droplet.Repo, &droplet.Title, &droplet.Description,
		&droplet.Priority, &droplet.Complexity, &droplet.Status, &assignee, &currentCataracta, &outcome, &assignedAqueduct, &lastReviewedCommit, &externalRef,
		&droplet.CreatedAt, &droplet.UpdatedAt, &stageDispatchedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cistern: scan ready droplet: %w", err)
	}
	droplet.Assignee = assignee.String
	droplet.CurrentCataractae = currentCataracta.String
	droplet.Outcome = outcome.String
	droplet.AssignedAqueduct = assignedAqueduct.String
	droplet.LastReviewedCommit = lastReviewedCommit.String
	droplet.ExternalRef = externalRef.String
	if stageDispatchedAt.Valid {
		droplet.StageDispatchedAt = stageDispatchedAt.Time
	}

	now := time.Now().UTC()
	if _, err := tx.Exec(
		`UPDATE droplets SET status = 'in_progress', updated_at = ? WHERE id = ?`,
		now, droplet.ID,
	); err != nil {
		return nil, fmt.Errorf("cistern: mark in_progress %s: %w", droplet.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("cistern: commit: %w", err)
	}

	droplet.Status = "in_progress"
	droplet.UpdatedAt = now
	return &droplet, nil
}

// GetReadyForAqueduct is like GetReady but only returns droplets that are either
// unassigned (assigned_aqueduct = '') or already assigned to aqueductName.
// This enforces sticky aqueduct assignment: once a droplet enters an aqueduct
// it stays there for its entire lifecycle.
func (c *Client) GetReadyForAqueduct(repo, aqueductName string) (*Droplet, error) {
	tx, err := c.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("cistern: begin tx: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRow(
		`SELECT id, repo, title, description, priority, complexity, status, assignee, current_cataractae, outcome, assigned_aqueduct, last_reviewed_commit, external_ref, created_at, updated_at, stage_dispatched_at
		 FROM droplets d
		 WHERE d.repo = ? COLLATE NOCASE AND d.status = 'open'
		   AND (d.assigned_aqueduct = '' OR d.assigned_aqueduct IS NULL OR d.assigned_aqueduct = ?)
		   AND NOT EXISTS (
		     SELECT 1 FROM droplet_dependencies dep
		     JOIN droplets dep_d ON dep_d.id = dep.depends_on
		     WHERE dep.droplet_id = d.id AND dep_d.status NOT IN ('delivered', 'cancelled')
		   )
		 ORDER BY d.priority ASC, d.created_at ASC
		 LIMIT 1`,
		repo, aqueductName,
	)

	var droplet Droplet
	var assignee, currentCataracta, outcome, assignedAqueduct, lastReviewedCommit, externalRef sql.NullString
	var stageDispatchedAt sql.NullTime
	now := time.Now().UTC()
	err = row.Scan(
		&droplet.ID, &droplet.Repo, &droplet.Title, &droplet.Description,
		&droplet.Priority, &droplet.Complexity, &droplet.Status, &assignee, &currentCataracta, &outcome, &assignedAqueduct, &lastReviewedCommit, &externalRef,
		&droplet.CreatedAt, &droplet.UpdatedAt, &stageDispatchedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cistern: scan ready droplet: %w", err)
	}
	droplet.Assignee = assignee.String
	droplet.CurrentCataractae = currentCataracta.String
	droplet.Outcome = outcome.String
	droplet.AssignedAqueduct = assignedAqueduct.String
	droplet.LastReviewedCommit = lastReviewedCommit.String
	droplet.ExternalRef = externalRef.String
	if stageDispatchedAt.Valid {
		droplet.StageDispatchedAt = stageDispatchedAt.Time
	}

	if _, err := tx.Exec(
		`UPDATE droplets SET status = 'in_progress', updated_at = ? WHERE id = ?`,
		now, droplet.ID,
	); err != nil {
		return nil, fmt.Errorf("cistern: mark in_progress %s: %w", droplet.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("cistern: commit: %w", err)
	}
	droplet.Status = "in_progress"
	droplet.UpdatedAt = now
	return &droplet, nil
}

// Assign records the worker and cataractae on a droplet. When worker is non-empty
// it only updates the assignee and cataractae (status is already in-progress from
// GetReady). When worker is empty the droplet is set back to "open" (used when
// advancing to the next cataractae without a specific worker assignment).
func (c *Client) Assign(id, worker, step string) error {
	now := time.Now().UTC()
	var res sql.Result
	var err error
	if worker == "" {
		res, err = c.db.Exec(
			`UPDATE droplets SET assignee = ?, current_cataractae = ?, outcome = NULL, status = 'open',
			 assigned_aqueduct = '', updated_at = ? WHERE id = ?`,
			worker, step, now, id,
		)
	} else {
		res, err = c.db.Exec(
			`UPDATE droplets SET assignee = ?, current_cataractae = ?, outcome = NULL,
			 updated_at = ?, stage_dispatched_at = ? WHERE id = ?`,
			worker, step, now, now, id,
		)
	}
	if err != nil {
		return fmt.Errorf("cistern: assign %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// SetAssignedAqueduct records the aqueduct operator currently holding this
// droplet. Only updates when the field is currently empty; CloseItem, Cancel,
// and Pool clear it as part of their terminal-state transitions.
func (c *Client) SetAssignedAqueduct(id, aqueductName string) error {
	_, err := c.db.Exec(
		`UPDATE droplets SET assigned_aqueduct = ? WHERE id = ? AND (assigned_aqueduct = '' OR assigned_aqueduct IS NULL)`,
		aqueductName, id,
	)
	if err != nil {
		return fmt.Errorf("cistern: set assigned_aqueduct %s: %w", id, err)
	}
	return nil
}

// SetLastReviewedCommit records the HEAD commit hash at the time the review diff
// was generated. Called by the runner when preparing a diff_only context.
func (c *Client) SetLastReviewedCommit(id, commitHash string) error {
	_, err := c.db.Exec(
		`UPDATE droplets SET last_reviewed_commit = ? WHERE id = ?`,
		commitHash, id,
	)
	if err != nil {
		return fmt.Errorf("cistern: set last_reviewed_commit %s: %w", id, err)
	}
	return nil
}

// GetLastReviewedCommit returns the HEAD commit hash from the last time a review
// diff was generated for this droplet. Returns an empty string if not yet set.
func (c *Client) GetLastReviewedCommit(id string) (string, error) {
	var commit sql.NullString
	err := c.db.QueryRow(
		`SELECT last_reviewed_commit FROM droplets WHERE id = ?`, id,
	).Scan(&commit)
	if err != nil {
		return "", fmt.Errorf("cistern: get last_reviewed_commit %s: %w", id, err)
	}
	return commit.String, nil
}

// SetExternalRef sets the external_ref field on a droplet. Pass an empty string
// to clear the field (stores NULL). Format should be 'provider:key'
// (e.g. 'jira:DPF-456', 'linear:LIN-789').
func (c *Client) SetExternalRef(id, ref string) error {
	if ref != "" {
		if !externalRefRE.MatchString(ref) {
			return fmt.Errorf("cistern: invalid external_ref %q: must match provider:key with git-safe characters", ref)
		}
		_, key, _ := strings.Cut(ref, ":")
		if strings.Contains(key, "..") || strings.HasSuffix(key, ".") || strings.HasSuffix(key, ".lock") || strings.HasPrefix(key, ".") {
			return fmt.Errorf("cistern: invalid external_ref %q: key produces git-invalid branch name", ref)
		}
	}
	var val any
	if ref != "" {
		val = ref
	}
	now := time.Now().UTC()
	res, err := c.db.Exec(
		`UPDATE droplets SET external_ref = ?, updated_at = ? WHERE id = ?`,
		val, now, id,
	)
	if err != nil {
		return fmt.Errorf("cistern: set external_ref %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// UpdateTitle sets the title field on a droplet.
func (c *Client) UpdateTitle(id, title string) error {
	res, err := c.db.Exec(
		`UPDATE droplets SET title = ?, updated_at = ? WHERE id = ?`,
		title, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("cistern: update title %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// EditDropletFields holds the optional fields for EditDroplet.
// A nil pointer means "do not update this field".
type EditDropletFields struct {
	Description *string
	Complexity  *int
	Priority    *int
}

// EditDroplet updates mutable fields on a droplet that has not yet been picked
// up. Allowed statuses: open, pooled. Returns an error if the droplet is
// in_progress or delivered.
func (c *Client) EditDroplet(id string, fields EditDropletFields) error {
	if fields.Description == nil && fields.Complexity == nil && fields.Priority == nil {
		return nil
	}

	if fields.Complexity != nil && (*fields.Complexity < 1 || *fields.Complexity > 3) {
		return fmt.Errorf("cistern: complexity must be between 1 and 3, got %d", *fields.Complexity)
	}

	var setClauses []string
	var args []any
	if fields.Description != nil {
		setClauses = append(setClauses, "description = ?")
		args = append(args, *fields.Description)
	}
	if fields.Complexity != nil {
		setClauses = append(setClauses, "complexity = ?")
		args = append(args, *fields.Complexity)
	}
	if fields.Priority != nil {
		setClauses = append(setClauses, "priority = ?")
		args = append(args, *fields.Priority)
	}
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, time.Now().UTC())
	args = append(args, id)

	query := "UPDATE droplets SET " + strings.Join(setClauses, ", ") + " WHERE id = ? AND status IN ('open', 'pooled')"
	res, err := c.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("cistern: edit %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cistern: rows affected: %w", err)
	}
	if n == 0 {
		d, err := c.Get(id)
		if err != nil {
			return err
		}
		return fmt.Errorf("droplet %s is %s — cannot edit a droplet that has been picked up", id, d.Status)
	}
	return nil
}

// UpdateStatus sets the status field on a droplet.
func (c *Client) UpdateStatus(id, status string) error {
	res, err := c.db.Exec(
		`UPDATE droplets SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("cistern: update status %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// AddNote attaches a cataractae note to a droplet.
func (c *Client) AddNote(id, step, content string) error {
	_, err := c.db.Exec(
		`INSERT INTO cataractae_notes (droplet_id, cataractae_name, content, created_at) VALUES (?, ?, ?, ?)`,
		id, step, content, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("cistern: add note %s: %w", id, err)
	}
	return nil
}

// GetNotes returns all cataractae notes for a droplet, newest first.
func (c *Client) GetNotes(id string) ([]CataractaeNote, error) {
	rows, err := c.db.Query(
		`SELECT id, droplet_id, cataractae_name, content, created_at
		 FROM cataractae_notes
		 WHERE droplet_id = ?
		 ORDER BY created_at DESC`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("cistern: get notes %s: %w", id, err)
	}
	defer rows.Close()

	var notes []CataractaeNote
	for rows.Next() {
		var n CataractaeNote
		if err := rows.Scan(&n.ID, &n.DropletID, &n.CataractaeName, &n.Content, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("cistern: scan note: %w", err)
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// Pool marks a droplet as pooled — cannot currently flow forward — and records the reason.
// assigned_aqueduct is cleared atomically so no ghost assignments linger.
func (c *Client) Pool(id, reason string) error {
	res, err := c.db.Exec(
		`UPDATE droplets SET status = 'pooled', assigned_aqueduct = '', updated_at = ? WHERE id = ?`,
		time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("cistern: pool %s: %w", id, err)
	}
	if err := checkRowsAffected(res, id); err != nil {
		return err
	}

	_, err = c.db.Exec(
		`INSERT INTO events (droplet_id, event_type, payload, created_at) VALUES (?, 'pool', ?, ?)`,
		id, reason, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("cistern: pool event %s: %w", id, err)
	}
	return nil
}

// Cancel marks a droplet as cancelled. Cancelled droplets are excluded from the
// dispatch queue and from default list views. They can still be retrieved with
// List(repo, "cancelled"). If reason is non-empty it is recorded as a note.
// assigned_aqueduct is cleared atomically so no ghost assignments linger.
func (c *Client) Cancel(id, reason string) error {
	res, err := c.db.Exec(
		`UPDATE droplets SET status = 'cancelled', assigned_aqueduct = '', updated_at = ? WHERE id = ?`,
		time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("cistern: cancel %s: %w", id, err)
	}
	if err := checkRowsAffected(res, id); err != nil {
		return err
	}
	if reason != "" {
		if err := c.AddNote(id, "cancel", reason); err != nil {
			return err
		}
	}
	return nil
}

// FileDroplet creates a new droplet in the given repo. It is a convenience
// wrapper around Add used by the Architecti to file structural fix work items.
func (c *Client) FileDroplet(repo, title, description string, priority, complexity int) (*Droplet, error) {
	return c.Add(repo, title, description, priority, complexity)
}

// CloseItem marks a droplet as delivered.
// assigned_aqueduct is cleared atomically so no ghost assignments linger.
func (c *Client) CloseItem(id string) error {
	res, err := c.db.Exec(
		`UPDATE droplets SET status = 'delivered', assigned_aqueduct = '', updated_at = ? WHERE id = ?`,
		time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("cistern: close %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// SetOutcome records the agent outcome on a droplet. Pass empty string to clear
// (sets the column to NULL). Agents call this via `ct droplet pass/recirculate/pool`.
func (c *Client) SetOutcome(id, outcome string) error {
	var err error
	var res sql.Result
	now := time.Now().UTC()
	if outcome == "" {
		res, err = c.db.Exec(
			`UPDATE droplets SET outcome = NULL, updated_at = ? WHERE id = ?`,
			now, id,
		)
	} else {
		res, err = c.db.Exec(
			`UPDATE droplets SET outcome = ?, updated_at = ? WHERE id = ?`,
			outcome, now, id,
		)
	}
	if err != nil {
		return fmt.Errorf("cistern: set outcome %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// SetCataractae updates the current_cataractae field on a droplet without changing
// any other fields. Used by the scheduler to mark a droplet as awaiting human approval.
func (c *Client) SetCataractae(id, cataractaeName string) error {
	res, err := c.db.Exec(
		`UPDATE droplets SET current_cataractae = ?, updated_at = ? WHERE id = ?`,
		cataractaeName, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("cistern: set cataractae %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// Get retrieves a single droplet by ID. Returns an error if not found.
func (c *Client) Get(id string) (*Droplet, error) {
	row := c.db.QueryRow(
		`SELECT id, repo, title, description, priority, complexity, status, assignee, current_cataractae, outcome, assigned_aqueduct, last_reviewed_commit, external_ref, created_at, updated_at, stage_dispatched_at
		 FROM droplets WHERE id = ?`,
		id,
	)
	droplet, err := scanDroplet(row)
	if err != nil {
		return nil, fmt.Errorf("cistern: get %s: %w", id, err)
	}
	if droplet == nil {
		return nil, fmt.Errorf("cistern: droplet %s not found", id)
	}
	return droplet, nil
}

// List returns droplets filtered by repo and/or status. Empty strings mean no filter.
// Cancelled droplets are always excluded unless status is explicitly "cancelled".
func (c *Client) List(repo, status string) ([]*Droplet, error) {
	query := `SELECT id, repo, title, description, priority, complexity, status, assignee, current_cataractae, outcome, assigned_aqueduct, last_reviewed_commit, external_ref, created_at, updated_at, stage_dispatched_at
		 FROM droplets WHERE 1=1`
	var args []any
	if repo != "" {
		query += ` AND repo = ? COLLATE NOCASE`
		args = append(args, repo)
	}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	} else {
		// Exclude cancelled from default views; they are only shown when explicitly
		// requested with status="cancelled".
		query += ` AND status != 'cancelled'`
	}
	query += ` ORDER BY priority ASC, created_at ASC`

	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cistern: list: %w", err)
	}
	defer rows.Close()

	var droplets []*Droplet
	for rows.Next() {
		var droplet Droplet
		var assignee, currentCataracta, outcome, assignedAqueduct, lastReviewedCommit, externalRef sql.NullString
		var stageDispatchedAt sql.NullTime
		if err := rows.Scan(
			&droplet.ID, &droplet.Repo, &droplet.Title, &droplet.Description,
			&droplet.Priority, &droplet.Complexity, &droplet.Status, &assignee, &currentCataracta, &outcome, &assignedAqueduct, &lastReviewedCommit, &externalRef,
			&droplet.CreatedAt, &droplet.UpdatedAt, &stageDispatchedAt,
		); err != nil {
			return nil, fmt.Errorf("cistern: scan droplet: %w", err)
		}
		droplet.Assignee = assignee.String
		droplet.CurrentCataractae = currentCataracta.String
		droplet.Outcome = outcome.String
		droplet.AssignedAqueduct = assignedAqueduct.String
		droplet.LastReviewedCommit = lastReviewedCommit.String
		droplet.ExternalRef = externalRef.String
		if stageDispatchedAt.Valid {
			droplet.StageDispatchedAt = stageDispatchedAt.Time
		}
		droplets = append(droplets, &droplet)
	}
	return droplets, rows.Err()
}

// Search returns droplets matching the given filters. query is a case-insensitive
// substring match on title (empty means all). status is an exact match on status
// (empty means all). priority is an exact match on priority (0 means all).
// Results are ordered by priority ASC, created_at ASC.
func (c *Client) Search(query, status string, priority int) ([]*Droplet, error) {
	qry := `SELECT id, repo, title, description, priority, complexity, status, assignee, current_cataractae, outcome, assigned_aqueduct, last_reviewed_commit, external_ref, created_at, updated_at, stage_dispatched_at
		 FROM droplets WHERE 1=1`
	var args []any
	if query != "" {
		qry += ` AND lower(title) LIKE lower(?)`
		args = append(args, "%"+query+"%")
	}
	if status != "" {
		qry += ` AND status = ?`
		args = append(args, status)
	} else {
		// Exclude cancelled from default views; they are only shown when explicitly
		// requested with status="cancelled".
		qry += ` AND status != 'cancelled'`
	}
	if priority != 0 {
		qry += ` AND priority = ?`
		args = append(args, priority)
	}
	qry += ` ORDER BY priority ASC, created_at ASC`

	rows, err := c.db.Query(qry, args...)
	if err != nil {
		return nil, fmt.Errorf("cistern: search: %w", err)
	}
	defer rows.Close()

	var droplets []*Droplet
	for rows.Next() {
		var droplet Droplet
		var assignee, currentCataracta, outcome, assignedAqueduct, lastReviewedCommit, externalRef sql.NullString
		var stageDispatchedAt sql.NullTime
		if err := rows.Scan(
			&droplet.ID, &droplet.Repo, &droplet.Title, &droplet.Description,
			&droplet.Priority, &droplet.Complexity, &droplet.Status, &assignee, &currentCataracta, &outcome, &assignedAqueduct, &lastReviewedCommit, &externalRef,
			&droplet.CreatedAt, &droplet.UpdatedAt, &stageDispatchedAt,
		); err != nil {
			return nil, fmt.Errorf("cistern: scan droplet: %w", err)
		}
		droplet.Assignee = assignee.String
		droplet.CurrentCataractae = currentCataracta.String
		droplet.Outcome = outcome.String
		droplet.AssignedAqueduct = assignedAqueduct.String
		droplet.LastReviewedCommit = lastReviewedCommit.String
		droplet.ExternalRef = externalRef.String
		if stageDispatchedAt.Valid {
			droplet.StageDispatchedAt = stageDispatchedAt.Time
		}
		droplets = append(droplets, &droplet)
	}
	return droplets, rows.Err()
}

// scanDroplet scans a single row into a Droplet. Returns nil, nil for sql.ErrNoRows.
func scanDroplet(row *sql.Row) (*Droplet, error) {
	var droplet Droplet
	var assignee, currentCataracta, outcome, assignedAqueduct, lastReviewedCommit, externalRef sql.NullString
	var stageDispatchedAt sql.NullTime
	err := row.Scan(
		&droplet.ID, &droplet.Repo, &droplet.Title, &droplet.Description,
		&droplet.Priority, &droplet.Complexity, &droplet.Status, &assignee, &currentCataracta, &outcome, &assignedAqueduct, &lastReviewedCommit, &externalRef,
		&droplet.CreatedAt, &droplet.UpdatedAt, &stageDispatchedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	droplet.Assignee = assignee.String
	droplet.CurrentCataractae = currentCataracta.String
	droplet.Outcome = outcome.String
	droplet.AssignedAqueduct = assignedAqueduct.String
	droplet.LastReviewedCommit = lastReviewedCommit.String
	droplet.ExternalRef = externalRef.String
	if stageDispatchedAt.Valid {
		droplet.StageDispatchedAt = stageDispatchedAt.Time
	}
	return &droplet, nil
}

// Purge deletes delivered/pooled/cancelled droplets older than olderThan, cascading to
// cataractae_notes and events. Returns the count of droplets deleted (or that would be
// deleted in dry-run mode).
func (c *Client) Purge(olderThan time.Duration, dryRun bool) (int, error) {
	cutoff := time.Now().UTC().Add(-olderThan)

	if dryRun {
		var count int
		err := c.db.QueryRow(
			`SELECT COUNT(*) FROM droplets WHERE status IN ('delivered', 'pooled', 'cancelled') AND updated_at < ?`,
			cutoff,
		).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("cistern: purge dry-run: %w", err)
		}
		return count, nil
	}

	tx, err := c.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("cistern: purge begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`DELETE FROM cataractae_notes WHERE droplet_id IN (
			SELECT id FROM droplets WHERE status IN ('delivered', 'pooled', 'cancelled') AND updated_at < ?
		)`, cutoff,
	); err != nil {
		return 0, fmt.Errorf("cistern: purge cataractae_notes: %w", err)
	}

	if _, err := tx.Exec(
		`DELETE FROM events WHERE droplet_id IN (
			SELECT id FROM droplets WHERE status IN ('delivered', 'pooled', 'cancelled') AND updated_at < ?
		)`, cutoff,
	); err != nil {
		return 0, fmt.Errorf("cistern: purge events: %w", err)
	}

	if _, err := tx.Exec(
		`DELETE FROM droplet_issues WHERE droplet_id IN (
			SELECT id FROM droplets WHERE status IN ('delivered', 'pooled', 'cancelled') AND updated_at < ?
		)`, cutoff,
	); err != nil {
		return 0, fmt.Errorf("cistern: purge droplet_issues: %w", err)
	}

	res, err := tx.Exec(
		`DELETE FROM droplets WHERE status IN ('delivered', 'pooled', 'cancelled') AND updated_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("cistern: purge delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cistern: purge rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("cistern: purge commit: %w", err)
	}
	return int(n), nil
}

// RecentEvent is a summary entry from the events or step_notes table.
type RecentEvent struct {
	Time    time.Time `json:"time"`
	Droplet string    `json:"droplet"`
	Event   string    `json:"event"`
}

// ListRecentEvents returns up to limit recent entries from the events and
// cataractae_notes tables, ordered newest-first.
func (c *Client) ListRecentEvents(limit int) ([]RecentEvent, error) {
	rows, err := c.db.Query(`
		SELECT droplet_id, event_type, created_at FROM events
		UNION ALL
		SELECT droplet_id, cataractae_name, created_at FROM cataractae_notes
		ORDER BY created_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("cistern: list recent events: %w", err)
	}
	defer rows.Close()

	var events []RecentEvent
	for rows.Next() {
		var e RecentEvent
		if err := rows.Scan(&e.Droplet, &e.Event, &e.Time); err != nil {
			return nil, fmt.Errorf("cistern: scan recent event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// DropletStats holds counts of droplets grouped by display status.
type DropletStats struct {
	Flowing   int // status=in_progress
	Queued    int // status=open
	Delivered int // status=delivered
	Pooled    int // status=pooled
}

// Stats returns counts of droplets grouped by status.
func (c *Client) Stats() (DropletStats, error) {
	rows, err := c.db.Query(`SELECT status, COUNT(*) FROM droplets GROUP BY status`)
	if err != nil {
		return DropletStats{}, fmt.Errorf("cistern: stats: %w", err)
	}
	defer rows.Close()

	var s DropletStats
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return DropletStats{}, fmt.Errorf("cistern: stats scan: %w", err)
		}
		switch status {
		case "in_progress":
			s.Flowing += count
		case "open":
			s.Queued += count
		case "delivered":
			s.Delivered += count
		case "pooled":
			s.Pooled += count
		}
	}
	return s, rows.Err()
}

// AddDependency adds a dependency edge: dropletID must wait for dependsOnID.
// Returns an error if either ID does not exist.
func (c *Client) AddDependency(dropletID, dependsOnID string) error {
	for _, id := range []string{dropletID, dependsOnID} {
		var exists int
		if err := c.db.QueryRow(`SELECT COUNT(*) FROM droplets WHERE id = ?`, id).Scan(&exists); err != nil {
			return fmt.Errorf("cistern: validate %s: %w", id, err)
		}
		if exists == 0 {
			return fmt.Errorf("cistern: droplet %s not found", id)
		}
	}
	_, err := c.db.Exec(
		`INSERT OR IGNORE INTO droplet_dependencies (droplet_id, depends_on) VALUES (?, ?)`,
		dropletID, dependsOnID,
	)
	if err != nil {
		return fmt.Errorf("cistern: add dependency %s->%s: %w", dropletID, dependsOnID, err)
	}
	return nil
}

// RemoveDependency removes a dependency edge.
func (c *Client) RemoveDependency(dropletID, dependsOnID string) error {
	_, err := c.db.Exec(
		`DELETE FROM droplet_dependencies WHERE droplet_id = ? AND depends_on = ?`,
		dropletID, dependsOnID,
	)
	if err != nil {
		return fmt.Errorf("cistern: remove dependency %s->%s: %w", dropletID, dependsOnID, err)
	}
	return nil
}

// GetDependencies returns the IDs of all droplets that dropletID depends on.
func (c *Client) GetDependencies(dropletID string) ([]string, error) {
	rows, err := c.db.Query(
		`SELECT depends_on FROM droplet_dependencies WHERE droplet_id = ? ORDER BY depends_on`,
		dropletID,
	)
	if err != nil {
		return nil, fmt.Errorf("cistern: get dependencies %s: %w", dropletID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("cistern: scan dependency: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetBlockedBy returns the IDs of undelivered dependencies that are blocking dropletID.
func (c *Client) GetBlockedBy(dropletID string) ([]string, error) {
	rows, err := c.db.Query(
		`SELECT dep.depends_on
		 FROM droplet_dependencies dep
		 JOIN droplets d ON d.id = dep.depends_on
		 WHERE dep.droplet_id = ? AND d.status != 'delivered'
		 ORDER BY dep.depends_on`,
		dropletID,
	)
	if err != nil {
		return nil, fmt.Errorf("cistern: get blocked-by %s: %w", dropletID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("cistern: scan blocked-by: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func checkRowsAffected(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cistern: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("cistern: droplet %s not found", id)
	}
	return nil
}

// DropletIssue is a reviewer finding tracked as a first-class DB record.
type DropletIssue struct {
	ID          string     `json:"id"`
	DropletID   string     `json:"droplet_id"`
	FlaggedBy   string     `json:"flagged_by"`
	FlaggedAt   time.Time  `json:"flagged_at"`
	Description string     `json:"description"`
	Status      string     `json:"status"` // open | resolved | unresolved
	Evidence    string     `json:"evidence,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

// generateIssueID returns a unique issue ID derived from the droplet ID.
func generateIssueID(dropletID string) (string, error) {
	b := make([]byte, 5)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		b[i] = charset[n.Int64()]
	}
	return dropletID + "-" + string(b), nil
}

// AddIssue creates a new open issue for a droplet and returns it.
func (c *Client) AddIssue(dropletID, flaggedBy, description string) (*DropletIssue, error) {
	id, err := generateIssueID(dropletID)
	if err != nil {
		return nil, fmt.Errorf("cistern: generate issue id: %w", err)
	}
	now := time.Now().UTC()
	_, err = c.db.Exec(
		`INSERT INTO droplet_issues (id, droplet_id, flagged_by, flagged_at, description, status)
		 VALUES (?, ?, ?, ?, ?, 'open')`,
		id, dropletID, flaggedBy, now.Format("2006-01-02T15:04:05Z"), description,
	)
	if err != nil {
		return nil, fmt.Errorf("cistern: add issue: %w", err)
	}
	return &DropletIssue{
		ID:          id,
		DropletID:   dropletID,
		FlaggedBy:   flaggedBy,
		FlaggedAt:   now,
		Description: description,
		Status:      "open",
	}, nil
}

// ResolveIssue marks an issue as resolved with supporting evidence.
func (c *Client) ResolveIssue(issueID, evidence string) error {
	now := time.Now().UTC()
	res, err := c.db.Exec(
		`UPDATE droplet_issues SET status = 'resolved', evidence = ?, resolved_at = ? WHERE id = ?`,
		evidence, now.Format("2006-01-02T15:04:05Z"), issueID,
	)
	if err != nil {
		return fmt.Errorf("cistern: resolve issue %s: %w", issueID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cistern: resolve issue rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("cistern: issue %s not found", issueID)
	}
	return nil
}

// RejectIssue marks an issue as unresolved (still present) with evidence.
func (c *Client) RejectIssue(issueID, evidence string) error {
	res, err := c.db.Exec(
		`UPDATE droplet_issues SET status = 'unresolved', evidence = ? WHERE id = ?`,
		evidence, issueID,
	)
	if err != nil {
		return fmt.Errorf("cistern: reject issue %s: %w", issueID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cistern: reject issue rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("cistern: issue %s not found", issueID)
	}
	return nil
}

// ListIssues returns all issues for a droplet. If openOnly is true, only open issues are returned.
// If flaggedBy is non-empty, only issues with that flagged_by value are returned.
func (c *Client) ListIssues(dropletID string, openOnly bool, flaggedBy string) ([]DropletIssue, error) {
	query := `SELECT id, droplet_id, flagged_by, flagged_at, description, status, COALESCE(evidence,''), resolved_at
	          FROM droplet_issues WHERE droplet_id = ?`
	args := []any{dropletID}
	if openOnly {
		query += ` AND status = 'open'`
	}
	if flaggedBy != "" {
		query += ` AND flagged_by = ?`
		args = append(args, flaggedBy)
	}
	query += ` ORDER BY flagged_at ASC`

	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cistern: list issues %s: %w", dropletID, err)
	}
	defer rows.Close()

	var issues []DropletIssue
	for rows.Next() {
		var iss DropletIssue
		var resolvedAt sql.NullString
		var flaggedAt string
		if err := rows.Scan(&iss.ID, &iss.DropletID, &iss.FlaggedBy, &flaggedAt,
			&iss.Description, &iss.Status, &iss.Evidence, &resolvedAt); err != nil {
			return nil, fmt.Errorf("cistern: scan issue: %w", err)
		}
		if t, err := time.Parse("2006-01-02T15:04:05Z", flaggedAt); err == nil {
			iss.FlaggedAt = t
		}
		if resolvedAt.Valid && resolvedAt.String != "" {
			if t, err := time.Parse("2006-01-02T15:04:05Z", resolvedAt.String); err == nil {
				iss.ResolvedAt = &t
			}
		}
		issues = append(issues, iss)
	}
	return issues, rows.Err()
}

// CountOpenIssues returns the number of open issues for a droplet.
func (c *Client) CountOpenIssues(dropletID string) (int, error) {
	var count int
	err := c.db.QueryRow(
		`SELECT COUNT(*) FROM droplet_issues WHERE droplet_id = ? AND status = 'open'`,
		dropletID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("cistern: count open issues %s: %w", dropletID, err)
	}
	return count, nil
}
