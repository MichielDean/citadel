package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/oauth"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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

	// ~/.cistern/env credential file checks.
	envPath := filepath.Join(home, ".cistern", "env")

	var envFileFix func() error
	if doctorFix {
		envFileFix = func() error { return fixCisternEnvFile(envPath) }
	}
	envExists := checkWithFix("~/.cistern/env exists", func() error {
		_, err := os.Stat(envPath)
		if os.IsNotExist(err) {
			return fmt.Errorf("not found — run: ct init")
		}
		return err
	}, envFileFix)
	ok = envExists && ok

	if envExists {
		// Permissions check — informational warning, does not fail the run.
		checkCisternEnvPermissions(envPath)

		// Check that each env var required by the configured provider(s) is
		// present in the env file. For the claude provider (the default), no env
		// vars are required — claude uses its own OAuth credentials file and needs
		// no ANTHROPIC_API_KEY. Non-claude providers (codex → OPENAI_API_KEY, etc.)
		// still require their keys in the env file.
		requiredEnvVars, _ := startupRequiredEnvVars(cfgPath)
		for _, key := range requiredEnvVars {
			var envKeyFix func() error
			if doctorFix {
				envKeyFix = func() error { return fixCisternEnvAddKey(envPath, key) }
			}
			ok = checkWithFix("~/.cistern/env: "+key, func() error {
				return checkCisternEnvHasKey(envPath, key)
			}, envKeyFix) && ok
		}
	}

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
//  1. Provider binary present for each configured repo preset
//  2. Required env vars set for each configured preset
//  3. Agent instructions file present; warn on CLAUDE.md/AGENTS.md mismatch
//  4. LLM block: llm.provider=custom requires base_url
//  5. Provider + LLM mismatch advisory (informational)
//  6. CLAUDE.md integrity for each agent identity in the workflow
//  7. Skills installed at ~/.cistern/skills/<name>/SKILL.md
//  8. Aqueduct YAML validity (one check per repo)
//  9. Castellarius process (informational, does not fail the check)
// 10. Stalled in_progress droplets (warnings only, does not fail the check)
func runDoctorExtendedChecks(cfg *aqueduct.AqueductConfig, cfgPath, home, dbPath string) bool {
	ok := true
	cfgDir := filepath.Dir(cfgPath)
	cataractaeDir := filepath.Join(home, ".cistern", "cataractae")
	skillsDir := filepath.Join(home, ".cistern", "skills")

	seenIdentities := map[string]bool{}
	seenSkills := map[string]bool{}
	seenBinaries := map[string]bool{}
	seenEnvVars := map[string]bool{}
	usesClaude := false

	for _, repo := range cfg.Repos {
		wfPath := repo.WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(cfgDir, wfPath)
		}

		// Resolve the effective provider preset for this repo.
		preset, presErr := cfg.ResolveProvider(repo.Name)
		if presErr == nil {
			if preset.Name == "claude" {
				usesClaude = true
			}
			// Check 1: provider binary present (deduplicated by command across repos).
			if !seenBinaries[preset.Command] {
				seenBinaries[preset.Command] = true
				cmd := preset.Command
				name := preset.Name
				ok = checkWithFix("provider binary: "+cmd, func() error {
					if _, lookErr := exec.LookPath(cmd); lookErr != nil {
						hint := providerInstallHint(name)
						if hint != "" {
							return fmt.Errorf("not found in PATH — run: %s", hint)
						}
						return fmt.Errorf("not found in PATH")
					}
					return nil
				}, nil) && ok

				// Check 2: required env vars set (deduplicated across repos).
				for _, envVar := range preset.EnvPassthrough {
					if seenEnvVars[envVar] {
						continue
					}
					seenEnvVars[envVar] = true
					key := envVar
					ok = checkWithFix("env: "+key, func() error {
						if os.Getenv(key) == "" {
							return fmt.Errorf("not set")
						}
						return nil
					}, nil) && ok
				}
			}
		}

		// Check 8: aqueduct YAML valid.
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

		// Determine the active InstructionsFile for this repo's provider.
		// If the provider name is unknown or invalid, report it as a check failure
		// rather than silently defaulting to CLAUDE.md.
		presErrCopy := presErr
		ok = checkWithFix(fmt.Sprintf("provider: %s", repo.Name), func() error { return presErrCopy }, nil) && ok
		instrFile := preset.InstrFile()

		// Check 6: InstructionsFile integrity (deduplicated by identity across all repos).
		for _, step := range wf.Cataractae {
			if step.Type != aqueduct.CataractaeTypeAgent || step.Identity == "" {
				continue
			}
			if seenIdentities[step.Identity] {
				continue
			}
			seenIdentities[step.Identity] = true

			identity := step.Identity
			mdPath := filepath.Join(cataractaeDir, identity, instrFile)
			mdPathCopy := mdPath

			var claudeFix func() error
			if doctorFix {
				wfCopy := wf
				dirCopy := cataractaeDir
				instrFileCopy := instrFile
				claudeFix = func() error {
					_, err := aqueduct.GenerateCataractaeFiles(wfCopy, dirCopy, instrFileCopy)
					return err
				}
			}
			ok = checkWithFix(identity+" "+instrFile, func() error {
				return checkClaudeMdIntegrity(mdPathCopy)
			}, claudeFix) && ok

			// Note if CLAUDE.md exists but the active provider uses a different filename.
			// This is informational — CLAUDE.md is preserved for easy provider switching.
			if instrFile != "CLAUDE.md" {
				claudeMdPath := filepath.Join(cataractaeDir, identity, "CLAUDE.md")
				if _, statErr := os.Stat(claudeMdPath); statErr == nil {
					fmt.Printf("\u2139 %s: CLAUDE.md exists but active provider uses %s\n", identity, instrFile)
				}
			}
		}

		// Check 7: Skills installed at ~/.cistern/skills/<name>/SKILL.md.
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

	// Check 4: LLM block validation — custom provider requires base_url.
	if cfg.LLM != nil && cfg.LLM.Provider == "custom" {
		baseURL := cfg.LLM.BaseURL
		ok = checkWithFix("llm: custom provider requires base_url", func() error {
			if baseURL == "" {
				return fmt.Errorf("llm.provider=custom but llm.base_url is not set")
			}
			return nil
		}, nil) && ok
	}

	// Check 5: Provider + LLM mismatch advisory (informational — does not affect ok).
	if cfg.Provider != nil && cfg.LLM != nil && cfg.Provider.Name != "" && cfg.LLM.Provider != "" {
		inferredLLM := inferLLMProviderFromPreset(cfg.Provider.Name)
		if inferredLLM != "" && inferredLLM != cfg.LLM.Provider {
			fmt.Printf("⚠ provider mismatch: agent CLI=%s (typically uses %s), llm.provider=%s — filtration and agent sessions use different backends (valid but unusual)\n",
				cfg.Provider.Name, inferredLLM, cfg.LLM.Provider)
		}
	}

	// Check 9: Castellarius process (informational — does not affect ok).
	checkCastellariusProcess()

	// Check 10: Systemd service health (only on systemd systems).
	checkSystemdService(cfg)

	// Check 11: Repo sandbox health — one check per configured repo.
	checkRepoSandboxes(cfg)

	// Check 12: Stalled droplets (warnings only — does not affect ok).
	checkStalledDroplets(dbPath)

	// Check 13: Claude OAuth token expiry.
	// Skipped when no configured repo uses the claude provider.
	if usesClaude {
		oauthOk := checkOAuthTokenExpiry(home)
		if !oauthOk && doctorFix {
			if err := fixOAuthToken(home); err != nil {
				fmt.Printf("  fix failed: %v\n", err)
			} else {
				fmt.Printf("↻ Claude OAuth token: refreshed\n")
				oauthOk = checkOAuthTokenExpiry(home)
			}
		}
		ok = oauthOk && ok

		// Check 14: Service env ANTHROPIC_API_KEY matches current credentials token.
		// fixOAuthToken (above) updates env.conf when --fix is set, so re-running the
		// freshness check here reflects the updated state.
		ok = checkServiceTokenFreshness(home) && ok
	}

	return ok
}

// providerInstallHint returns a suggested install command for a known provider.
// Returns an empty string if no hint is available.
func providerInstallHint(presetName string) string {
	switch presetName {
	case "claude":
		return "npm install -g @anthropic-ai/claude-code"
	case "codex":
		return "npm install -g @openai/codex"
	case "gemini":
		return "npm install -g @google/gemini-cli"
	}
	return ""
}

// inferLLMProviderFromPreset returns the expected LLM API provider name for a
// given agent CLI preset name, based on its typical API key usage.
// Returns an empty string for presets with no clear LLM backend mapping.
func inferLLMProviderFromPreset(presetName string) string {
	switch presetName {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	case "gemini":
		return "gemini"
	}
	return ""
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
func checkSystemdService(cfg *aqueduct.AqueductConfig) {
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

	// If active, also validate the service environment:
	// 1. Agent binary is reachable from the service PATH.
	// 2. ANTHROPIC_API_KEY (or other required env vars) are set in the service env.
	if active {
		checkSystemdServiceEnv(serviceName, cfg)
	}
}

// checkSystemdServiceEnv validates that the running service has the agent binary
// on its PATH and required env vars set. Catches the common misconfiguration where
// ~/.local/bin (or similar) is missing from the systemd service PATH.
func checkSystemdServiceEnv(serviceName string, cfg *aqueduct.AqueductConfig) {
	// Read the service's effective environment from systemd.
	out, err := checkSystemdEnvFn(serviceName)
	if err != nil {
		return // can't read env — skip silently
	}
	serviceEnv := string(out)

	// Extract PATH from service env.
	servicePATH := ""
	for _, line := range strings.Split(serviceEnv, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Environment=") {
			for _, kv := range strings.Fields(strings.TrimPrefix(line, "Environment=")) {
				if strings.HasPrefix(kv, "PATH=") {
					servicePATH = strings.TrimPrefix(kv, "PATH=")
				}
			}
		}
	}

	if servicePATH != "" && cfg != nil {
		// Check that each configured agent binary is findable on the service PATH.
		seenCmds := map[string]bool{}
		for _, repo := range cfg.Repos {
			preset, pErr := cfg.ResolveProvider(repo.Name)
			if pErr != nil || seenCmds[preset.Command] {
				continue
			}
			seenCmds[preset.Command] = true
			found := false
			for _, dir := range filepath.SplitList(servicePATH) {
				candidate := filepath.Join(dir, preset.Command)
				if _, statErr := os.Stat(candidate); statErr == nil {
					found = true
					break
				}
			}
			if !found {
				// Find where the binary actually lives so we can give a helpful hint.
				actualPath, _ := exec.LookPath(preset.Command)
				if actualPath == "" {
					actualPath = "(not found in current shell PATH either)"
				}
				fmt.Printf("✗ service PATH missing %s — binary is at %s but service PATH=%s\n"+
					"  Fix: add its directory to Environment=PATH in the service drop-in\n",
					preset.Command, actualPath, servicePATH)
			} else {
				fmt.Printf("✓ service PATH: %s reachable\n", preset.Command)
			}
		}
	}
}

// resolveGoBinFn wraps resolveGoBin to allow injection in tests.
var resolveGoBinFn = resolveGoBin

// osStatFn wraps os.Stat to allow injection in tests.
var osStatFn = os.Stat

// installSystemdService writes the cistern-castellarius.service file and enables it.
// It also writes ~/.cistern/start-castellarius.sh, creates ~/.cistern/env if absent,
// and adds "env" to ~/.cistern/.gitignore.
func installSystemdService() error {
	gobin, err := resolveGoBinFn()
	if err != nil {
		return fmt.Errorf("cannot resolve Go bin dir: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	cisternDir := filepath.Join(home, ".cistern")
	if err := os.MkdirAll(cisternDir, 0o755); err != nil {
		return fmt.Errorf("create .cistern dir: %w", err)
	}

	// Write start-castellarius.sh wrapper if not present (chmod 755).
	wrapperPath := filepath.Join(cisternDir, "start-castellarius.sh")
	if _, statErr := osStatFn(wrapperPath); os.IsNotExist(statErr) {
		if err := os.WriteFile(wrapperPath, defaultStartCastellarius, 0o755); err != nil {
			return fmt.Errorf("write wrapper script: %w", err)
		}
	} else if statErr != nil {
		return fmt.Errorf("stat wrapper script: %w", statErr)
	}

	// Create ~/.cistern/env credential stub if absent.
	envPath := filepath.Join(cisternDir, "env")
	if err := fixCisternEnvFile(envPath); err != nil {
		return fmt.Errorf("create env file: %w", err)
	}

	// Add "env" to ~/.cistern/.gitignore.
	gitignorePath := filepath.Join(cisternDir, ".gitignore")
	if err := addLineToGitignore(gitignorePath, "env"); err != nil {
		return fmt.Errorf("update .gitignore: %w", err)
	}

	serviceDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(serviceDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(cisternDir, "castellarius.log")
	content := fmt.Sprintf(`[Unit]
Description=Cistern Castellarius — aqueduct scheduler
After=network.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=5
StartLimitIntervalSec=120
StartLimitBurst=10
StartLimitAction=none
TimeoutStopSec=15
KillMode=mixed
KillSignal=SIGTERM
EnvironmentFile=-%s/env
StandardOutput=append:%s
StandardError=append:%s
Environment=HOME=%s
Environment=PATH=%s:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=default.target
`, wrapperPath, cisternDir, logPath, logPath, home, gobin)

	svcPath := filepath.Join(serviceDir, "cistern-castellarius.service")
	if err := os.WriteFile(svcPath, []byte(content), 0o644); err != nil {
		return err
	}
	execCommandFn("systemctl", "--user", "daemon-reload").Run() //nolint:errcheck
	if err := execCommandFn("systemctl", "--user", "enable", "cistern-castellarius").Run(); err != nil {
		return fmt.Errorf("enable failed: %w", err)
	}
	execCommandFn("systemctl", "--user", "start", "cistern-castellarius").Run() //nolint:errcheck
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

// doctorOAuthHTTPDo is the HTTP transport used by fixOAuthToken.
// Replaced in tests with a test server client.
var doctorOAuthHTTPDo func(*http.Request) (*http.Response, error) = http.DefaultClient.Do

// doctorOAuthTokenURL is the OAuth token endpoint used by fixOAuthToken.
// Replaced in tests with a test server URL.
var doctorOAuthTokenURL = oauth.DefaultTokenURL

// execCommandFn wraps exec.Command to allow injection in tests.
var execCommandFn = exec.Command

// checkSystemdEnvFn reads the effective environment of a systemd user service.
// It is a variable to allow injection in tests.
var checkSystemdEnvFn = func(serviceName string) ([]byte, error) {
	return exec.Command("systemctl", "--user", "show", serviceName, "--property=Environment").Output()
}

// fixOAuthToken refreshes the Claude OAuth access token using the stored refresh
// token and writes the new access token to ~/.claude/.credentials.json and the
// service env.conf. It then reloads the systemd service so the new token takes effect.
func fixOAuthToken(home string) error {
	creds := oauth.Read(home)
	if creds == nil {
		return fmt.Errorf("cannot read credentials — run 'claude' interactively to authenticate")
	}
	if creds.RefreshToken == "" {
		return fmt.Errorf("no refresh token available — run 'claude' interactively to re-authenticate")
	}

	result, err := oauth.Refresh(creds.RefreshToken, doctorOAuthTokenURL, doctorOAuthHTTPDo)
	if err != nil {
		return fmt.Errorf("OAuth refresh failed: %w", err)
	}

	if err := oauth.WriteAccessToken(home, result.AccessToken, result.ExpiresAt); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}

	envConfPath := filepath.Join(home, ".config", "systemd", "user",
		"cistern-castellarius.service.d", "env.conf")
	if _, statErr := os.Stat(envConfPath); statErr == nil {
		if err := oauth.UpdateEnvConf(envConfPath, result.AccessToken); err != nil {
			return fmt.Errorf("update env.conf: %w", err)
		}
		// Reload the systemd unit so the new token takes effect.
		if out, err := execCommandFn("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %w: %s", err, out)
		}
		if out, err := execCommandFn("systemctl", "--user", "restart", "cistern-castellarius").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl restart: %w: %s", err, out)
		}
	}

	return nil
}

// checkOAuthTokenExpiry reports whether the Claude OAuth token is fresh,
// expiring within 24h (warning), or expired (fail).
// Skipped silently when the credentials file is absent or has no expiry.
func checkOAuthTokenExpiry(home string) bool {
	creds := oauth.Read(home)
	if creds == nil || creds.ExpiresAt == 0 {
		return true
	}

	expiresAt := time.UnixMilli(creds.ExpiresAt)
	now := time.Now()

	if now.After(expiresAt) {
		fmt.Printf("✗ Claude OAuth token: expired %s ago — run 'claude' interactively to refresh\n",
			now.Sub(expiresAt).Truncate(time.Minute))
		return false
	}

	remaining := expiresAt.Sub(now)
	if remaining < 24*time.Hour {
		fmt.Printf("⚠ Claude OAuth token: expiring in %s — run 'claude' interactively to refresh\n",
			remaining.Truncate(time.Minute))
		return true
	}

	fmt.Printf("✓ Claude OAuth token: fresh (expires %s)\n", expiresAt.Format(time.RFC3339))
	return true
}

// checkServiceTokenFreshness compares the ANTHROPIC_API_KEY in the systemd
// service drop-in against the current OAuth access token.
// Skipped silently when either file is absent or ANTHROPIC_API_KEY is not set.
func checkServiceTokenFreshness(home string) bool {
	envConfPath := filepath.Join(home, ".config", "systemd", "user",
		"cistern-castellarius.service.d", "env.conf")
	envData, err := os.ReadFile(envConfPath)
	if err != nil {
		return true // no drop-in — skip silently
	}

	var serviceToken string
	for _, line := range strings.Split(string(envData), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "Environment=ANTHROPIC_API_KEY="); ok {
			serviceToken = after
			break
		}
	}
	if serviceToken == "" {
		return true
	}

	creds := oauth.Read(home)
	if creds == nil || creds.AccessToken == "" {
		return true
	}

	if serviceToken != creds.AccessToken {
		fmt.Printf("✗ service ANTHROPIC_API_KEY: stale — update env.conf with the current token and restart\n")
		return false
	}

	fmt.Printf("✓ service ANTHROPIC_API_KEY: matches current credentials\n")
	return true
}

// checkCisternEnvPermissions prints a warning if ~/.cistern/env is readable by
// group or other (mode &0o077 != 0). With --fix it applies chmod 600.
// This is informational and never affects the ok result.
func checkCisternEnvPermissions(envPath string) {
	info, err := os.Stat(envPath)
	if err != nil {
		return
	}
	mode := info.Mode().Perm()
	if mode&0o077 == 0 {
		fmt.Printf("✓ ~/.cistern/env: chmod 600\n")
		return
	}
	if doctorFix {
		if chErr := os.Chmod(envPath, 0o600); chErr != nil {
			fmt.Printf("⚠ ~/.cistern/env: permissions %04o — chmod 600 failed: %v\n", mode, chErr)
		} else {
			fmt.Printf("↻ ~/.cistern/env: chmod 600 applied\n")
		}
	} else {
		fmt.Printf("⚠ ~/.cistern/env: permissions %04o (world-readable) — run: chmod 600 %s\n", mode, envPath)
	}
}

// checkCisternEnvHasKey verifies that key=<non-empty-value> appears in the env
// file at envPath. Lines beginning with '#' and blank lines are ignored.
func checkCisternEnvHasKey(envPath, key string) error {
	data, err := os.ReadFile(envPath)
	if err != nil {
		return fmt.Errorf("cannot read env file: %w", err)
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if after, ok := strings.CutPrefix(line, prefix); ok && after != "" {
			return nil
		}
	}
	return fmt.Errorf("not set in %s", envPath)
}

// cisternEnvStub is the default content written to a new ~/.cistern/env file.
const cisternEnvStub = "# Cistern credentials — add your API key here\n# ANTHROPIC_API_KEY=sk-ant-...\n# GH_TOKEN=ghp_...\n"

// fixCisternEnvFile creates envPath with mode 0o600 if it does not exist.
// New files are populated with a commented-out stub.
// Parent directories are created as needed. Existing files are not modified.
func fixCisternEnvFile(envPath string) error {
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if _, err := osStatFn(envPath); os.IsNotExist(err) {
		if err := os.WriteFile(envPath, []byte(cisternEnvStub), 0o600); err != nil {
			return fmt.Errorf("create env file: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("stat env file: %w", err)
	}
	return nil
}

// fixCisternEnvAddKey appends key=<value> to the env file at envPath.
// In an interactive terminal it prompts the user for the value.
// In non-interactive mode it returns an error with manual-edit instructions.
func fixCisternEnvAddKey(envPath, key string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("add %s=<your-key> to %s manually", key, envPath)
	}
	fmt.Printf("Enter %s: ", key)
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println() // move to next line after masked input
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return fmt.Errorf("no value provided for %s", key)
	}
	f, err := os.OpenFile(envPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open env file: %w", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s=%s\n", key, value); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	return nil
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
