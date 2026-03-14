package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
)

// Result is the outcome of a workflow step.
type Result string

const (
	ResultPass     Result = "pass"
	ResultFail     Result = "fail"
	ResultRevision Result = "revision"
	ResultEscalate Result = "escalate"
)

// Outcome is the structured output from a completed workflow step.
type Outcome struct {
	Result      Result       `json:"result"`
	Notes       string       `json:"notes"`
	Annotations []Annotation `json:"annotations,omitempty"`
	// MetaNotes are key-value pairs persisted as step notes (e.g., "meta:pr_url=...").
	// They allow automated steps to pass data to subsequent steps.
	MetaNotes []string `json:"-"`
}

// Annotation is a file-level comment from a step outcome.
type Annotation struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Comment string `json:"comment"`
}

// ReadOutcome reads and parses an outcome.json file.
func ReadOutcome(path string) (*Outcome, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read outcome: %w", err)
	}
	var o Outcome
	if err := json.Unmarshal(data, &o); err != nil {
		return nil, fmt.Errorf("parse outcome: %w", err)
	}
	return &o, nil
}
