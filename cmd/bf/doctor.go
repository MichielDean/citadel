package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MichielDean/bullet-farm/internal/workflow"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system prerequisites and configuration",
	RunE:  runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, args []string) error {
	ok := true

	ok = check("tmux installed", func() error {
		_, err := exec.LookPath("tmux")
		return err
	}) && ok

	ok = check("claude CLI found", func() error {
		_, err := exec.LookPath("claude")
		return err
	}) && ok

	ok = check("git installed", func() error {
		_, err := exec.LookPath("git")
		return err
	}) && ok

	ok = check("gh CLI installed", func() error {
		_, err := exec.LookPath("gh")
		return err
	}) && ok

	ok = check("gh authenticated", func() error {
		out, err := exec.Command("gh", "auth", "status").CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s", out)
		}
		return nil
	}) && ok

	home, _ := os.UserHomeDir()
	cfgPath := filepath.Join(home, ".bullet-farm", "config.yaml")
	ok = check("config exists and parses", func() error {
		_, err := workflow.ParseFarmConfig(cfgPath)
		return err
	}) && ok

	dbFile := filepath.Join(home, ".bullet-farm", "queue.db")
	ok = check("queue.db accessible", func() error {
		f, err := os.OpenFile(dbFile, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		f.Close()
		return nil
	}) && ok

	sandboxDir := filepath.Join(home, ".bullet-farm", "sandboxes")
	ok = check("sandboxes/ writable", func() error {
		if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
			return err
		}
		tmp := filepath.Join(sandboxDir, ".doctor-test")
		if err := os.WriteFile(tmp, []byte("ok"), 0o644); err != nil {
			return err
		}
		os.Remove(tmp)
		return nil
	}) && ok

	if !ok {
		return fmt.Errorf("one or more checks failed")
	}
	fmt.Println("\nAll checks passed.")
	return nil
}

func check(name string, fn func() error) bool {
	if err := fn(); err != nil {
		fmt.Printf("\u2717 %s: %v\n", name, err)
		return false
	}
	fmt.Printf("\u2713 %s\n", name)
	return true
}
