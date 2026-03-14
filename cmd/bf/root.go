package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var dbPath string

var rootCmd = &cobra.Command{
	Use:   "bf",
	Short: "Bullet Farm CLI — work queue and farm management",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "path to queue database (default: ~/.bullet-farm/queue.db)")
}

func resolveDBPath() string {
	if dbPath != "" {
		return dbPath
	}
	if env := os.Getenv("BF_DB"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine home directory: %v\n", err)
		os.Exit(1)
	}
	dir := filepath.Join(home, ".bullet-farm")
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "queue.db")
}
