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
