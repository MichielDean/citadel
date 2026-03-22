package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
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
			if step.Type != aqueduct.CataractaeTypeAgent || step.Identity == "" {
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
					_, err := aqueduct.GenerateCataractaeFiles(wfCopy, dirCopy)
					return err
				}
			}
			ok = checkWithFix(identity+" CLAUDE.md", func() error {
				return checkClaudeMdIntegrity(mdPathCopy)
			}, claudeFix) && ok
		}

		// Check 2: Skills installed at ~/.cistern/skills/<name>/SKILL.md.
		// All skills must be present in ~/.cistern/skills/ — in-repo skills are
		// deployed there automatically by the git_sync drought hook.
		for _, step := range wf.Cataractae {
			for _, skill := range step.Skills {
				if seenSkills[skill.Name] {
					continue
				}
				seenSkills[skill.Name] = true

				name := skill.Name
				mdPath := filepath.Join(skillsDir, name, "SKILL.md")
				mdPathCopy := mdPath
				ok = checkWithFix("skill: "+name, func() error {
					if _, statErr := os.Stat(mdPathCopy); statErr != nil {
						return fmt.Errorf("not installed — run git_sync or: ct skills install %s <url>", name)
					}
					return nil
				}, nil) && ok
			}
		}
	}

	// Check 4: Castellarius process (informational — does not affect ok).
	checkCastellariusProcess()

	// Check 5: Systemd service health (only on systemd systems).
	checkSystemdService()

	// Check 6: Repo sandbox health — one check per configured repo.
	checkRepoSandboxes(cfg)

	// Check 7: Stalled droplets (warnings only — does not affect ok).
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

// checkSystemdService verifies the Castellarius systemd user service is installed,
// enabled, active, and that linger is on. Informational — does not affect ok.
// With --fix it installs/enables/starts the service and enables linger.
func checkSystemdService() {
	// Skip entirely on non-systemd systems.
	if _, err := exec.LookPath("systemctl"); err != nil {
		return
	}
	// Check if user session is available.
	if err := exec.Command("systemctl", "--user", "status").Run(); err != nil {
		// systemctl exits non-zero when no units are running but session exists;
		// if we get "Failed to connect" it means no user session.
		if strings.Contains(err.Error(), "exit status 1") {
			return // no user session — skip
		}
	}

	serviceName := "cistern-castellarius.service"

	// --- enabled? ---
	enabledOut, _ := exec.Command("systemctl", "--user", "is-enabled", serviceName).Output()
	enabled := strings.TrimSpace(string(enabledOut)) == "enabled"

	// --- active? ---
	activeOut, _ := exec.Command("systemctl", "--user", "is-active", serviceName).Output()
	active := strings.TrimSpace(string(activeOut)) == "active"

	// --- linger? ---
	lingerOn := false
	if u, err := user.Current(); err == nil {
		out, _ := exec.Command("loginctl", "show-user", u.Username).Output()
		lingerOn = strings.Contains(string(out), "Linger=yes")
	}

	// Report.
	if !enabled {
		msg := "not enabled"
		if doctorFix {
			// Write service file and enable.
			if installErr := installSystemdService(); installErr != nil {
				fmt.Printf("✗ systemd service: fix failed: %v\n", installErr)
			} else {
				fmt.Printf("↻ systemd service: installed and enabled\n")
				enabled = true
			}
		} else {
			fmt.Printf("✗ systemd service: %s — run: ct doctor --fix\n", msg)
		}
	} else if !active {
		if doctorFix {
			exec.Command("systemctl", "--user", "start", serviceName).Run() //nolint:errcheck
			activeOut2, _ := exec.Command("systemctl", "--user", "is-active", serviceName).Output()
			if strings.TrimSpace(string(activeOut2)) == "active" {
				fmt.Printf("↻ systemd service: started\n")
				active = true
			} else {
				fmt.Printf("✗ systemd service: enabled but failed to start\n")
			}
		} else {
			fmt.Printf("✗ systemd service: enabled but not active — run: systemctl --user start %s\n", serviceName)
		}
	} else {
		fmt.Printf("✓ systemd service: enabled + active\n")
	}

	if !lingerOn {
		if doctorFix {
			if u, err := user.Current(); err == nil {
				if lingerErr := exec.Command("loginctl", "enable-linger", u.Username).Run(); lingerErr != nil {
					fmt.Printf("✗ linger: fix failed: %v\n", lingerErr)
				} else {
					fmt.Printf("↻ linger: enabled — service will survive SSH logout\n")
				}
			}
		} else {
			fmt.Printf("✗ linger: not enabled — service dies on SSH logout. Run: ct doctor --fix\n")
		}
	} else if enabled && active {
		fmt.Printf("✓ linger: enabled\n")
	}

	_ = active // suppress unused warning when fix path is taken
}

// installSystemdService writes the cistern-castellarius.service file and enables it.
func installSystemdService() error {
	gobin, err := resolveGoBin()
	if err != nil {
		return fmt.Errorf("cannot resolve Go bin dir: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	ctBin := filepath.Join(gobin, "ct")
	serviceDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(serviceDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(home, ".cistern", "castellarius.log")
	content := fmt.Sprintf(`[Unit]
Description=Cistern Castellarius — aqueduct scheduler
After=network.target

[Service]
Type=simple
ExecStart=%s castellarius start
Restart=always
RestartSec=5
StartLimitIntervalSec=120
StartLimitBurst=10
StartLimitAction=none
TimeoutStopSec=15
KillMode=mixed
KillSignal=SIGTERM
StandardOutput=append:%s
StandardError=append:%s
Environment=HOME=%s
Environment=PATH=%s:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=default.target
`, ctBin, logPath, logPath, home, gobin)

	svcPath := filepath.Join(serviceDir, "cistern-castellarius.service")
	if err := os.WriteFile(svcPath, []byte(content), 0o644); err != nil {
		return err
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run() //nolint:errcheck
	if err := exec.Command("systemctl", "--user", "enable", "cistern-castellarius").Run(); err != nil {
		return fmt.Errorf("enable failed: %w", err)
	}
	exec.Command("systemctl", "--user", "start", "cistern-castellarius").Run() //nolint:errcheck
	return nil
}

// resolveGoBin returns the directory where `go install` places binaries.
func resolveGoBin() (string, error) {
	out, err := exec.Command("go", "env", "GOBIN").Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		return strings.TrimSpace(string(out)), nil
	}
	out, err = exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return "", fmt.Errorf("cannot determine GOPATH: %w", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "bin"), nil
}

// checkRepoSandboxes checks that each configured repo has accessible sandboxes.
// Informational — does not affect overall pass/fail.
func checkRepoSandboxes(cfg *aqueduct.AqueductConfig) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	sandboxRoot := filepath.Join(home, ".cistern", "sandboxes")

	for _, repo := range cfg.Repos {
		names := repo.Names
		if len(names) == 0 {
			for i := 0; i < repo.Cataractae; i++ {
				names = append(names, fmt.Sprintf("worker-%d", i+1))
			}
		}

		allCloned := true
		for _, name := range names {
			dir := filepath.Join(sandboxRoot, repo.Name, name)
			if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
				allCloned = false
				break
			}
		}

		if allCloned {
			fmt.Printf("✓ repo: %s (%d aqueduct(s) cloned)\n", repo.Name, len(names))
		} else {
			if doctorFix {
				// Attempt to clone missing sandboxes.
				cloneErr := preCloneSandboxesDoctor(repo, sandboxRoot)
				if cloneErr != nil {
					fmt.Printf("✗ repo: %s — clone failed: %v\n", repo.Name, cloneErr)
				} else {
					fmt.Printf("↻ repo: %s — sandboxes cloned\n", repo.Name)
				}
			} else {
				fmt.Printf("✗ repo: %s — sandbox(es) not cloned. Run: ct repo clone %s\n", repo.Name, repo.Name)
			}
		}
	}
}

// preCloneSandboxesDoctor is the doctor variant of preCloneSandboxes.
// Defined here to avoid import cycle; mirrors cmd/ct/repo.go:preCloneSandboxes.
func preCloneSandboxesDoctor(repo aqueduct.RepoConfig, sandboxRoot string) error {
	names := repo.Names
	if len(names) == 0 {
		for i := 0; i < repo.Cataractae; i++ {
			names = append(names, fmt.Sprintf("worker-%d", i+1))
		}
	}
	repoRoot := filepath.Join(sandboxRoot, repo.Name)
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		return err
	}
	for _, name := range names {
		dir := filepath.Join(repoRoot, name)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			continue // already exists
		}
		out, err := exec.Command("git", "clone", repo.URL, dir).CombinedOutput()
		if err != nil {
			return fmt.Errorf("clone %s/%s: %w — %s", repo.Name, name, err, string(out))
		}
	}
	return nil
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
