package main

import (
	"testing"
)

func TestStatusWatchFlagRegistered(t *testing.T) {
	f := statusCmd.Flags().Lookup("watch")
	if f == nil {
		t.Fatal("--watch flag not registered on statusCmd")
	}
	if f.DefValue != "false" {
		t.Fatalf("expected --watch default false, got %q", f.DefValue)
	}
}

func TestStatusIntervalFlagRegistered(t *testing.T) {
	f := statusCmd.Flags().Lookup("interval")
	if f == nil {
		t.Fatal("--interval flag not registered on statusCmd")
	}
	if f.DefValue != "5" {
		t.Fatalf("expected --interval default 5, got %q", f.DefValue)
	}
}

func TestStatusIntervalZeroReturnsError(t *testing.T) {
	origInterval := statusInterval
	defer func() { statusInterval = origInterval }()

	statusInterval = 0
	err := statusCmd.RunE(statusCmd, nil)
	if err == nil {
		t.Fatal("expected error for --interval 0, got nil")
	}
	if err.Error() != "--interval must be at least 1" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStatusIntervalNegativeReturnsError(t *testing.T) {
	origInterval := statusInterval
	defer func() { statusInterval = origInterval }()

	statusInterval = -5
	err := statusCmd.RunE(statusCmd, nil)
	if err == nil {
		t.Fatal("expected error for --interval -5, got nil")
	}
	if err.Error() != "--interval must be at least 1" {
		t.Fatalf("unexpected error: %v", err)
	}
}
