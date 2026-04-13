package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// notifyCastellarius reads the castellarius PID from its health file and sends
// SIGUSR1 to trigger an immediate observe+dispatch tick. This is called after
// ct droplet pass/recirculate/pool writes an outcome so the castellarius
// processes it immediately instead of waiting for the next poll cycle.
func notifyCastellarius() {
	dbPath := resolveDBPath()
	dbDir := filepath.Dir(dbPath)
	hPath := filepath.Join(dbDir, "castellarius.health")

	data, err := os.ReadFile(hPath)
	if err != nil {
		return
	}

	var hf struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &hf); err != nil || hf.PID == 0 {
		return
	}

	proc, err := os.FindProcess(hf.PID)
	if err != nil {
		return
	}

	if err := proc.Signal(syscall.SIGUSR1); err != nil {
		fmt.Fprintf(os.Stderr, "ct: notify castellarius (pid %d): %v\n", hf.PID, err)
	}
}