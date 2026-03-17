package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/spf13/cobra"
)

var doctorFix bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system prerequisites and configuration",
	RunE:  runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "attempt to auto-repair common issues")
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, args []string) error {
	ok := true

	ok = checkWithFix("tmux installed", func() error {
		_, err := exec.LookPath("tmux")
		return err
	}, nil) && ok

	ok = checkWithFix("claude CLI found", func() error {
		_, err := exec.LookPath("claude")
		return err
	}, nil) && ok

	ok = checkWithFix("git installed", func() error {
		_, err := exec.LookPath("git")
		return err
	}, nil) && ok

	ok = checkWithFix("gh CLI installed", func() error {
		_, err := exec.LookPath("gh")
		return err
	}, nil) && ok

	ok = checkWithFix("gh authenticated", func() error {
		out, err := exec.Command("gh", "auth", "status").CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s", out)
		}
		return nil
	}, nil) && ok

	home, _ := os.UserHomeDir()
	cfgPath := filepath.Join(home, ".cistern", "cistern.yaml")

	var cfgFix func() error
	if doctorFix {
		cfgFix = func() error { return fixCisternConfig(cfgPath) }
	}
	ok = checkWithFix("config exists and parses", func() error {
		_, err := aqueduct.ParseAqueductConfig(cfgPath)
		return err
	}, cfgFix) && ok

	dbFile := filepath.Join(home, ".cistern", "cistern.db")

	var dbFix func() error
	if doctorFix {
		dbFix = func() error { return fixCisternDB(dbFile) }
	}
	ok = checkWithFix("cistern.db accessible", func() error {
		f, err := os.OpenFile(dbFile, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		f.Close()
		return nil
	}, dbFix) && ok

	sandboxDir := filepath.Join(home, ".cistern", "sandboxes")
	ok = checkWithFix("sandboxes/ writable", func() error {
		if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
			return err
		}
		tmp := filepath.Join(sandboxDir, ".doctor-test")
		if err := os.WriteFile(tmp, []byte("ok"), 0o644); err != nil {
			return err
		}
		os.Remove(tmp)
		return nil
	}, nil) && ok

	if !ok {
		return fmt.Errorf("one or more checks failed")
	}
	fmt.Println("\nAll checks passed.")
	return nil
}

// fixCisternConfig creates ~/.cistern/cistern.yaml from the embedded default template.
func fixCisternConfig(cfgPath string) error {
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	return os.WriteFile(cfgPath, defaultCisternConfig, 0o644)
}

// fixCisternDB creates and initialises a new cistern SQLite database at dbFile.
func fixCisternDB(dbFile string) error {
	if err := os.MkdirAll(filepath.Dir(dbFile), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	c, err := cistern.New(dbFile, "ct")
	if err != nil {
		return err
	}
	return c.Close()
}

// checkWithFix runs fn. If fn fails and fix is non-nil, it runs fix then
// re-runs fn. Returns true if the check ultimately passes, false otherwise.
func checkWithFix(name string, fn func() error, fix func() error) bool {
	if err := fn(); err != nil {
		if fix != nil {
			if fixErr := fix(); fixErr != nil {
				fmt.Printf("\u2717 %s: fix failed: %v\n", name, fixErr)
				return false
			}
			if err2 := fn(); err2 != nil {
				fmt.Printf("\u2717 %s: still failing after fix: %v\n", name, err2)
				return false
			}
			fmt.Printf("\u21bb %s: fixed\n", name)
			return true
		}
		fmt.Printf("\u2717 %s: %v\n", name, err)
		return false
	}
	fmt.Printf("\u2713 %s\n", name)
	return true
}
