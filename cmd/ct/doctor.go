package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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

	// Extended runtime checks that depend on config and DB being present.
	// Re-parse config here in case it was just fixed above.
	if cfg, err := aqueduct.ParseAqueductConfig(cfgPath); err == nil {
		ok = runDoctorExtendedChecks(cfg, cfgPath, home, dbFile) && ok
	}

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

// runDoctorExtendedChecks performs the extended runtime checks that depend on
// the Cistern config being present and valid:
//  1. CLAUDE.md integrity for each agent identity in the workflow
//  2. Skills installed at ~/.cistern/skills/<name>/SKILL.md
//  3. Aqueduct YAML validity (one check per repo)
//  4. Castellarius process (informational, does not fail the check)
//  5. Stalled in_progress droplets (warnings only, does not fail the check)
func runDoctorExtendedChecks(cfg *aqueduct.AqueductConfig, cfgPath, home, dbPath string) bool {
	ok := true
	cfgDir := filepath.Dir(cfgPath)
	cataractaeDir := filepath.Join(home, ".cistern", "cataractae")
	skillsDir := filepath.Join(home, ".cistern", "skills")

	seenIdentities := map[string]bool{}
	seenSkills := map[string]bool{}

	for _, repo := range cfg.Repos {
		wfPath := repo.WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(cfgDir, wfPath)
		}

		// Check 3: aqueduct YAML valid.
		wf, wfErr := aqueduct.ParseWorkflow(wfPath)
		wfLabel := fmt.Sprintf("aqueduct: %s", wfPath)
		if wfErr == nil {
			wfLabel = fmt.Sprintf("aqueduct: %s (%d cataractae)", wfPath, len(wf.Cataractae))
		}
		errCopy := wfErr
		ok = checkWithFix(wfLabel, func() error { return errCopy }, nil) && ok
		if wf == nil {
			continue
		}

		// Check 1: CLAUDE.md integrity (deduplicated by identity across all repos).
		for _, step := range wf.Cataractae {
			if step.Type != aqueduct.CataractaTypeAgent || step.Identity == "" {
				continue
			}
			if seenIdentities[step.Identity] {
				continue
			}
			seenIdentities[step.Identity] = true

			identity := step.Identity
			mdPath := filepath.Join(cataractaeDir, identity, "CLAUDE.md")
			mdPathCopy := mdPath

			var claudeFix func() error
			if doctorFix {
				wfCopy := wf
				dirCopy := cataractaeDir
				claudeFix = func() error {
					_, err := aqueduct.GenerateCataractaFiles(wfCopy, dirCopy)
					return err
				}
			}
			ok = checkWithFix(identity+" CLAUDE.md", func() error {
				return checkClaudeMdIntegrity(mdPathCopy)
			}, claudeFix) && ok
		}

		// Check 2: Skills installed (deduplicated by name across all repos).
		// In-repo skills (skill.Path != "") are resolved from the repo itself and
		// do not require a ~/.cistern/skills/<name>/SKILL.md installation.
		for _, step := range wf.Cataractae {
			for _, skill := range step.Skills {
				if skill.Path != "" {
					continue // in-repo skill; no install check needed
				}
				if seenSkills[skill.Name] {
					continue
				}
				seenSkills[skill.Name] = true

				name := skill.Name
				mdPath := filepath.Join(skillsDir, name, "SKILL.md")
				mdPathCopy := mdPath
				ok = checkWithFix("skill: "+name, func() error {
					if _, statErr := os.Stat(mdPathCopy); statErr != nil {
						return fmt.Errorf("not installed — run: ct skills install %s <url>", name)
					}
					return nil
				}, nil) && ok
			}
		}
	}

	// Check 4: Castellarius process (informational — does not affect ok).
	checkCastellariusProcess()

	// Check 5: Stalled droplets (warnings only — does not affect ok).
	checkStalledDroplets(dbPath)

	return ok
}

// checkClaudeMdIntegrity verifies that a CLAUDE.md exists and contains the
// required sentinel string "ct droplet pass".
func checkClaudeMdIntegrity(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("missing — run: ct cataractae generate")
		}
		return fmt.Errorf("unreadable (%w) — run: ct cataractae generate", err)
	}
	if !strings.Contains(string(data), "ct droplet pass") {
		return fmt.Errorf("corrupt (missing sentinel) — run: ct cataractae generate")
	}
	return nil
}

// checkCastellariusProcess reports whether a Castellarius process is running.
// This is informational and does not contribute to pass/fail.
func checkCastellariusProcess() {
	out, err := exec.Command("pgrep", "-f", "ct castellarius").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		fmt.Printf("\u2713 castellarius: not running\n")
		return
	}
	pid := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	fmt.Printf("\u2713 castellarius: running (pid %s)\n", pid)
}

// checkStalledDroplets warns about in_progress droplets that have not updated
// in over 30 minutes. Does not affect the overall pass/fail result.
func checkStalledDroplets(dbPath string) {
	if _, err := os.Stat(dbPath); err != nil {
		return // DB not present; skip silently.
	}
	c, err := cistern.New(dbPath, "ct")
	if err != nil {
		return
	}
	defer c.Close()

	droplets, err := c.List("", "in_progress")
	if err != nil {
		return
	}
	for _, d := range droplets {
		elapsed := time.Since(d.UpdatedAt)
		if elapsed > 30*time.Minute {
			fmt.Printf("\u26A0 %s in_progress for %dm \u2014 may be stalled\n", d.ID, int(elapsed.Minutes()))
		}
	}
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
