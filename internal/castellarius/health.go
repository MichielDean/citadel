package castellarius

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// HealthFile is the schema written to {cistern_db_dir}/castellarius.health after each tick.
type HealthFile struct {
	LastTickAt      time.Time `json:"lastTickAt"`
	PollIntervalSec int       `json:"pollIntervalSec"`
}

// writeHealthFile atomically writes the health file to
// {cistern_db_dir}/castellarius.health. It writes to a .tmp sibling first
// then calls os.Rename for atomicity. Errors are logged but do not fail tick.
func (s *Castellarius) writeHealthFile() {
	if s.dbPath == "" {
		s.logger.Warn("health file: dbPath is empty, skipping write")
		return
	}

	dir := filepath.Dir(s.dbPath)
	hPath := filepath.Join(dir, "castellarius.health")
	tmpPath := hPath + ".tmp"

	data := HealthFile{
		LastTickAt:      time.Now().UTC(),
		PollIntervalSec: int(s.pollInterval.Seconds()),
	}

	b, err := json.Marshal(data)
	if err != nil {
		s.logger.Error("health file: marshal failed", "error", err)
		return
	}
	b = append(b, '\n')

	if err := os.WriteFile(tmpPath, b, 0o644); err != nil {
		s.logger.Error("health file: write tmp failed", "path", tmpPath, "error", err)
		return
	}

	if err := os.Rename(tmpPath, hPath); err != nil {
		s.logger.Error("health file: rename failed", "path", hPath, "error", err)
		os.Remove(tmpPath) //nolint:errcheck
		return
	}
}

// ReadHealthFile reads and parses the castellarius health file from
// {dbDir}/castellarius.health. Returns a non-nil error if the file is absent
// or cannot be parsed.
func ReadHealthFile(dbDir string) (*HealthFile, error) {
	path := filepath.Join(dbDir, "castellarius.health")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var hf HealthFile
	if err := json.Unmarshal(data, &hf); err != nil {
		return nil, err
	}
	return &hf, nil
}
