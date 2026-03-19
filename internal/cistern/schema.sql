CREATE TABLE IF NOT EXISTS droplets (
    id TEXT PRIMARY KEY,
    repo TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    priority INTEGER DEFAULT 2,
    complexity INTEGER DEFAULT 3,
    status TEXT DEFAULT 'open',
    assignee TEXT DEFAULT '',
    current_cataracta TEXT DEFAULT '',
    outcome TEXT DEFAULT NULL,
    assigned_aqueduct TEXT DEFAULT '',
    last_reviewed_commit TEXT DEFAULT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS cataracta_notes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    droplet_id TEXT NOT NULL,
    cataracta_name TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    droplet_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS droplet_dependencies (
    droplet_id TEXT NOT NULL REFERENCES droplets(id),
    depends_on TEXT NOT NULL REFERENCES droplets(id),
    PRIMARY KEY (droplet_id, depends_on)
);
CREATE TABLE IF NOT EXISTS droplet_issues (
    id          TEXT PRIMARY KEY,
    droplet_id  TEXT NOT NULL REFERENCES droplets(id),
    flagged_by  TEXT NOT NULL,
    flagged_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    description TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open',
    evidence    TEXT,
    resolved_at DATETIME
);
