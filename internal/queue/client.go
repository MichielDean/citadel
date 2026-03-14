// Package queue provides a SQLite-backed work queue for Bullet Farm.
//
// Each work item flows through a workflow pipeline. The queue stores items,
// step notes, and events. No external dependencies — just SQLite.
package queue

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"fmt"
	"math/big"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schema string

const charset = "abcdefghijklmnopqrstuvwxyz0123456789"

// WorkItem represents a unit of work in the queue.
type WorkItem struct {
	ID           string    `json:"id"`
	Repo         string    `json:"repo"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Priority     int       `json:"priority"`
	Status       string    `json:"status"`
	Assignee     string    `json:"assignee"`
	CurrentStep  string    `json:"current_step"`
	AttemptCount int       `json:"attempt_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// StepNote is a note attached by a workflow step.
type StepNote struct {
	ID        int       `json:"id"`
	ItemID    string    `json:"item_id"`
	StepName  string    `json:"step_name"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Client is a SQLite-backed work queue client.
type Client struct {
	db     *sql.DB
	prefix string
}

// New opens (or creates) a SQLite database at dbPath, runs the schema, and
// returns a Client. The prefix is used when generating work item IDs.
func New(dbPath, prefix string) (*Client, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("queue: open %s: %w", dbPath, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("queue: schema: %w", err)
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

// Add creates a new work item and returns it.
func (c *Client) Add(repo, title, description string, priority int) (*WorkItem, error) {
	id, err := c.generateID()
	if err != nil {
		return nil, fmt.Errorf("queue: generate id: %w", err)
	}

	now := time.Now().UTC()
	_, err = c.db.Exec(
		`INSERT INTO work_items (id, repo, title, description, priority, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'open', ?, ?)`,
		id, repo, title, description, priority, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("queue: add: %w", err)
	}

	return &WorkItem{
		ID:          id,
		Repo:        repo,
		Title:       title,
		Description: description,
		Priority:    priority,
		Status:      "open",
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// GetReady atomically selects the next open work item for a repo and marks it
// in-progress within a single transaction. Ordered by priority (lower number =
// higher priority) then FIFO within the same priority. Returns nil if no work
// is available.
func (c *Client) GetReady(repo string) (*WorkItem, error) {
	tx, err := c.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("queue: begin tx: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRow(
		`SELECT id, repo, title, description, priority, status, assignee, current_step, attempt_count, created_at, updated_at
		 FROM work_items
		 WHERE repo = ? AND status = 'open'
		 ORDER BY priority ASC, created_at ASC
		 LIMIT 1`,
		repo,
	)

	var item WorkItem
	err = row.Scan(
		&item.ID, &item.Repo, &item.Title, &item.Description,
		&item.Priority, &item.Status, &item.Assignee, &item.CurrentStep,
		&item.AttemptCount, &item.CreatedAt, &item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("queue: scan ready item: %w", err)
	}

	now := time.Now().UTC()
	if _, err := tx.Exec(
		`UPDATE work_items SET status = 'in_progress', updated_at = ? WHERE id = ?`,
		now, item.ID,
	); err != nil {
		return nil, fmt.Errorf("queue: mark in_progress %s: %w", item.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("queue: commit: %w", err)
	}

	item.Status = "in_progress"
	item.UpdatedAt = now
	return &item, nil
}

// Assign records the worker and step on a work item. When worker is non-empty
// it only updates the assignee and step (status is already in-progress from
// GetReady). When worker is empty the item is set back to "open" (used when
// advancing to the next step without a specific worker assignment).
func (c *Client) Assign(id, worker, step string) error {
	now := time.Now().UTC()
	var res sql.Result
	var err error
	if worker == "" {
		res, err = c.db.Exec(
			`UPDATE work_items SET assignee = ?, current_step = ?, status = 'open',
			 attempt_count = CASE WHEN current_step != ? THEN 0 ELSE attempt_count END,
			 updated_at = ?
			 WHERE id = ?`,
			worker, step, step, now, id,
		)
	} else {
		res, err = c.db.Exec(
			`UPDATE work_items SET assignee = ?, current_step = ?,
			 attempt_count = CASE WHEN current_step != ? THEN 0 ELSE attempt_count END,
			 updated_at = ?
			 WHERE id = ?`,
			worker, step, step, now, id,
		)
	}
	if err != nil {
		return fmt.Errorf("queue: assign %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// UpdateStatus sets the status field on a work item.
func (c *Client) UpdateStatus(id, status string) error {
	res, err := c.db.Exec(
		`UPDATE work_items SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("queue: update status %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// IncrementAttempts bumps the attempt counter and returns the new count.
func (c *Client) IncrementAttempts(id string) (int, error) {
	_, err := c.db.Exec(
		`UPDATE work_items SET attempt_count = attempt_count + 1, updated_at = ? WHERE id = ?`,
		time.Now().UTC(), id,
	)
	if err != nil {
		return 0, fmt.Errorf("queue: increment attempts %s: %w", id, err)
	}

	var count int
	err = c.db.QueryRow(`SELECT attempt_count FROM work_items WHERE id = ?`, id).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("queue: read attempts %s: %w", id, err)
	}
	return count, nil
}

// AddNote attaches a step note to a work item.
func (c *Client) AddNote(id, step, content string) error {
	_, err := c.db.Exec(
		`INSERT INTO step_notes (item_id, step_name, content, created_at) VALUES (?, ?, ?, ?)`,
		id, step, content, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("queue: add note %s: %w", id, err)
	}
	return nil
}

// GetNotes returns all step notes for a work item, ordered by creation time.
func (c *Client) GetNotes(id string) ([]StepNote, error) {
	rows, err := c.db.Query(
		`SELECT id, item_id, step_name, content, created_at
		 FROM step_notes
		 WHERE item_id = ?
		 ORDER BY created_at ASC`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("queue: get notes %s: %w", id, err)
	}
	defer rows.Close()

	var notes []StepNote
	for rows.Next() {
		var n StepNote
		if err := rows.Scan(&n.ID, &n.ItemID, &n.StepName, &n.Content, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("queue: scan note: %w", err)
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// Escalate marks a work item as needing human attention and records the reason.
func (c *Client) Escalate(id, reason string) error {
	res, err := c.db.Exec(
		`UPDATE work_items SET status = 'escalated', updated_at = ? WHERE id = ?`,
		time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("queue: escalate %s: %w", id, err)
	}
	if err := checkRowsAffected(res, id); err != nil {
		return err
	}

	_, err = c.db.Exec(
		`INSERT INTO events (item_id, event_type, payload, created_at) VALUES (?, 'escalate', ?, ?)`,
		id, reason, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("queue: escalate event %s: %w", id, err)
	}
	return nil
}

// Close marks a work item as closed.
func (c *Client) CloseItem(id string) error {
	res, err := c.db.Exec(
		`UPDATE work_items SET status = 'closed', updated_at = ? WHERE id = ?`,
		time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("queue: close %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// Get retrieves a single work item by ID. Returns an error if not found.
func (c *Client) Get(id string) (*WorkItem, error) {
	row := c.db.QueryRow(
		`SELECT id, repo, title, description, priority, status, assignee, current_step, attempt_count, created_at, updated_at
		 FROM work_items WHERE id = ?`,
		id,
	)
	item, err := scanWorkItem(row)
	if err != nil {
		return nil, fmt.Errorf("queue: get %s: %w", id, err)
	}
	if item == nil {
		return nil, fmt.Errorf("queue: item %s not found", id)
	}
	return item, nil
}

// List returns work items filtered by repo and/or status. Empty strings mean no filter.
func (c *Client) List(repo, status string) ([]*WorkItem, error) {
	query := `SELECT id, repo, title, description, priority, status, assignee, current_step, attempt_count, created_at, updated_at
		 FROM work_items WHERE 1=1`
	var args []any
	if repo != "" {
		query += ` AND repo = ?`
		args = append(args, repo)
	}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY priority ASC, created_at ASC`

	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("queue: list: %w", err)
	}
	defer rows.Close()

	var items []*WorkItem
	for rows.Next() {
		var item WorkItem
		if err := rows.Scan(
			&item.ID, &item.Repo, &item.Title, &item.Description,
			&item.Priority, &item.Status, &item.Assignee, &item.CurrentStep,
			&item.AttemptCount, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("queue: scan item: %w", err)
		}
		items = append(items, &item)
	}
	return items, rows.Err()
}

// scanWorkItem scans a single row into a WorkItem. Returns nil, nil for sql.ErrNoRows.
func scanWorkItem(row *sql.Row) (*WorkItem, error) {
	var item WorkItem
	err := row.Scan(
		&item.ID, &item.Repo, &item.Title, &item.Description,
		&item.Priority, &item.Status, &item.Assignee, &item.CurrentStep,
		&item.AttemptCount, &item.CreatedAt, &item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func checkRowsAffected(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("queue: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("queue: item %s not found", id)
	}
	return nil
}
