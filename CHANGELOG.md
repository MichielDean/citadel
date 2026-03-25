# Changelog

## Unreleased

### Web dashboard: add ESC back hint button to exit peek mode (ci-2ejep)
- The web dashboard now displays an "ESC = back" button in the bottom-right corner of the terminal display, providing visual indication that pressing Escape or clicking the button will close the peek overlay
- Button is always visible and clickable: clicking sends an Esc keystroke to the terminal to exit peek mode and return to the main dashboard view, useful for users unfamiliar with the Escape keyboard shortcut or who prefer mouse navigation
- Capture-phase Escape keydown handler forwards Esc keystrokes from the browser keyboard directly to the PTY, ensuring keystrokes reach the TUI even if xterm.js or the browser would normally intercept them (e.g., triggering fullscreen exit instead)
- `preventDefault()` is called on the Escape keydown event to suppress browser default Escape behavior (exit fullscreen, close find bar, page loading) while allowing the TUI to receive the keystroke
- Tests added: `TestDashboardHTML_EscHint` verifies the button element exists, is clickable via `sendEsc()`, and the capture-phase listener is properly attached with `stopPropagation()` and `preventDefault()` calls

### Castellarius: auto-promote recirculate to pass when no route exists, with diagnostic note for escalation (ci-blpza, ci-m5e29)
- When a cataractae signals `ct droplet recirculate` but the aqueduct config has no `on_recirculate` route for that step, the Castellarius now auto-promotes the outcome to `pass` instead of stalling (if a pass route exists)
- Rationale: The work is almost certainly complete — the agent chose the wrong signal. There is no upstream to send it to, so recirculate is semantically meaningless here. Auto-promoting is non-destructive.
- User-visible: When auto-promoted, a note is attached to the droplet: `Auto-promoted: cataractae "<name>" signaled recirculate but has no on_recirculate route — treated as pass. Review agent behavior if this recurs.`
- When escalation is necessary (no on_recirculate and no on_pass routes), a diagnostic note is added: `cataractae "<name>" signaled recirculate but has no on_recirculate route configured — this is likely an agent error (recirculate used instead of pass or block). Manual intervention required.` This surfaces the problem immediately instead of silently stalling.
- Logged at WARN level in the Castellarius logs for visibility without failing the pipeline
- Interim fix: Once ci-amg37 (prompt templating) lands, recirculate will not appear in the agent's context at all, making this edge case unreachable
- Tests added: `TestTick_RecirculateAutoPromotesToPass` verifies routing to the on_pass target with warning note; `TestTick_RecirculateNoPassRoute_StillEscalates` verifies escalation still occurs when neither on_recirculate nor on_pass routes exist; `TestTick_RecirculateNoRouteAddsDescriptiveNote` verifies the diagnostic note is attached before escalation

### Peek: capture full scrollback buffer instead of last 100 lines (ci-tw6gb)
- The `capturePane()` function now uses the `-S` flag with `-` (start of scrollback) to capture the entire tmux pane scrollback buffer, not just the last 100 visible lines
- Previously `defaultPeekLines` was 100, forcing peek to discard early history when an agent produced verbose output; now defaults to 0, meaning "capture everything"
- Updated `newPeekModel()` to accept lines=0 as valid (changed guard from `<= 0` to `< 0`), distinguishing "full scrollback" (0) from "use default" (negative)
- Web dashboard peek overlay and TUI inline peek now show full agent output history instead of truncating to the last screenfull
- Integration test `TestCapturePane_FullScrollback_ReturnsHistoryBeyondVisible` spawns a real tmux session, writes 200 lines of output (far more than the 24-row visible area), and asserts peek returns all lines including early history
- Tests added: full scrollback sentinel polling, first-line and last-line assertions, cleanup on test exit

### Peek: attach read-only to live tmux session instead of polling snapshots (ci-61hbi)
- `ct droplet peek <id>` now attaches read-only to the live agent tmux session by default (`tmux attach-session -r`), providing real-time scrolling output instead of static snapshots
- Session fallback: if the session doesn't exist (agent completed), shows the last 10 lines of the most recent note
- `--snapshot` flag restores the old polling behavior: `tmux capture-pane` every 500ms, for non-interactive use (scripts, web dashboard)
- `--follow` flag (re-capture every 3 seconds) now requires `--snapshot`; using `--follow` without `--snapshot` returns an actionable error
- `--lines` and `--raw` flags apply only with `--snapshot`; without `--snapshot` they are ignored
- Dashboard TUI: pressing `p` when inside a tmux session now opens a new tmux window (`tmux new-window "tmux attach-session -r"`) so the user can observe the live agent in a separate window while the dashboard continues running; when not in tmux, falls back to the inline capture-pane overlay
- Dashboard TUI: multi-aqueduct `p` picker now clears the peek-select mode overlay on successful new-window spawn, so the picker dismisses when the new window opens
- Tests added: `TestPeekCmd_LiveAttach_ExistingSession` verifies live attach path; `TestDashboard_PeekSelect_InTmux_Success_ClearsPeekSelectMode` verifies the picker clears on spawn

### Dashboard TUI: adaptive refresh rate reduces idle CPU usage (ci-bxe4q)
- The dashboard polling loop now automatically backs off from fast refresh (2s) to slow refresh (5s) when the Castellarius is idle and no state changes are detected. Reduces CPU usage from ~3.8% to near-zero when idle, while maintaining responsive updates when droplets are actively flowing.
- `runDashboardWith()` detects idle state by comparing dashboard state hash: if the hash matches the previous poll and `FlowingCount == 0`, the next tick uses the slow interval. Any state change (droplet count change, status update, or manual refresh via 'r' key) immediately resets to fast refresh.
- Web dashboard address changed from `:5737` (all interfaces) to `127.0.0.1:5737` (localhost only) for security — the dashboard now only accepts connections from the local machine. Use `--addr 127.0.0.1:8080` to specify a custom localhost address.
- SSE event stream handler in the web dashboard also implements adaptive backoff with the same pattern, refactored to use the same fetcher and interval injection as the TUI for consistency.
- Adaptive backoff is transparent to the user: the dashboard remains as responsive as before when there is activity, and consumes less CPU when idle.

### Web dashboard: keep TUI child process alive across WebSocket reconnects (ci-8akf7)
- The `/ws/tui` WebSocket endpoint now maintains a singleton `ct dashboard` child process that persists across client disconnects, instead of spawning a new child per connection and killing it on disconnect
- When a WebSocket client disconnects (network switch, screen lock, tab backgrounded, brief network hiccup), the child PTY and its running TUI state remain alive; a reconnecting client reattaches to the same process and sees continuous state
- The PTY output is buffered in a ring buffer (last N lines configurable via `tuiOutputBufChunks`, default 100 chunks); reconnecting clients receive an immediate snapshot before live streaming resumes — no visual "restart" on reconnect
- The child process restarts only if it actually exits (e.g., the TUI is exited by the user), not on every client disconnect
- Spawn failures (missing `ct` binary, PTY allocation failure) are now logged with exponential backoff (500ms → 30s), preventing silent busy-wait loops on permanent failures
- Architecture: `DashboardTUI` struct (initialized once at web-server startup) owns the child process lifecycle, PTY, and ring buffer; a broadcast loop forwards PTY output to all connected WebSocket clients; attach/detach operations are atomic to prevent snapshot-vs-live gaps

### Startup credentials and doctor checks: provider-aware instead of hardcoded to Anthropic (ci-hhj3d)
- `checkStartupCredentials()` in `cmd/ct/castellarius.go` now parses the aqueduct config and checks only the environment variables required by each configured repo's provider preset, instead of always requiring `ANTHROPIC_API_KEY`. Falls back to `ANTHROPIC_API_KEY` when no config exists (new-install path).
- `startupRequiredEnvVars()` now uses a `resolved` flag to distinguish between "providers resolved but need zero env vars" (e.g., opencode provider) and "no providers resolved at all", fixing a bug where opencode users were incorrectly blocked on missing `ANTHROPIC_API_KEY` and expired Claude OAuth tokens.
- `ct doctor` env-file checks now validate provider-specific environment variables instead of hardcoding `ANTHROPIC_API_KEY`: checks each variable listed in the provider preset's `EnvPassthrough`, e.g., `OPENAI_API_KEY` for codex, `GEMINI_API_KEY` for gemini. Falls back to `ANTHROPIC_API_KEY` when no config exists.
- `ct doctor` extended checks now include: (1) provider binary presence for each configured repo, with install hints; (2) required env vars set for each configured preset; (3) provider-specific instructions file integrity (CLAUDE.md, AGENTS.md, or GEMINI.md based on the active provider).
- Claude OAuth token expiry checks (in `doctor.go` and startup) are skipped or downgraded to informational when no configured repo uses the claude provider, eliminating false alarms for non-Claude setups.
- Tests added: `TestStartupRequiredEnvVars_OpencodeConfig_ReturnsEmptyVarsNotClaude` ensures opencode providers return empty env vars and not-uses-Claude correctly; `TestDoctorEnvCheck_GeminiProvider_*` verifies gemini env-var checking; integration test coverage for both claude and non-claude providers.

### CI job: installer integration tests on relevant file changes (ci-l8phc)
- Adds `.github/workflows/installer-integration-tests.yml` — a GitHub Actions workflow named `installer-integration-tests` that triggers on pull requests touching `**/doctor.go`, `**/init.go`, `**/start-castellarius.sh`, or `tests/installer/**`
- Builds the Docker test image and runs the container-based test suite; job fails if any scenario fails (exit code propagates)
- `::error::` annotations emitted for each `[FAIL]` line so GitHub renders failures as visible errors in the Actions log and PR checks UI
- `timeout-minutes: 15` prevents a hung test from occupying a self-hosted runner for the 6-hour default
- Temp file path uses `GITHUB_RUN_ID` (`/tmp/installer-test-output-${GITHUB_RUN_ID}.txt`) to prevent collisions between concurrent runs on the same self-hosted runner
- `persist-credentials: false` on checkout — the workflow only runs Docker commands post-checkout and does not need git credentials
- Cleanup step (`if: always()`) removes the container, image, and temp file on every exit path — no resource leaks on shared self-hosted runners
- Fork protection: job is skipped for PRs from forks via `github.event.pull_request.head.repo.full_name == github.repository` guard (self-hosted Docker not available to forked PRs)
- Run locally with a single command: `bash tests/installer/run-local.sh` (documented in workflow header and `tests/installer/README.md`)

### Observability pass: structured logging throughout Castellarius and cataractae (ci-jgllb)
- Session spawn now logs resolved command path, model, preset name, and context type (fresh vs resume) via structured `slog` key=value fields
- Session resume logs the project directory and prior-session file count so operators can track session continuity
- Quick-exit warning (`session exited quickly — possible auth failure or binary not found`) fires only when the session did not signal a droplet outcome and the exit was not intentional — false positives on fast successful tasks eliminated via a done channel and `DropletSignaledOutcome` check
- Heartbeat stall-reset logs reason (`no_assignee`, `pool_idle`, `tmux_dead`) and `session_duration` so operators can distinguish stuck sessions from idle pool slots
- Dispatch-loop threshold now logs a warning at `Warn` level with the droplet ID and failure count when 5 failures occur within 2 minutes; each recovery attempt logs the specific failure reason (worktree dirty, worktree missing, spawn error)
- Startup credential check (`logStartupCredentials`) logs which required env var names are set, runs `gh auth status` with a 10-second context timeout, and logs whether GitHub authentication succeeded — token values are never logged
- Worktree operations log each git operation (clone, fetch, checkout, worktree add) with duration and outcome; worktree created, resumed, and deleted events each emit a structured log entry; deletion failure logs at `Warn` with the error rather than silently claiming success
- `sandbox.go` git clone, fetch, and worktree-add operations now emit `slog.Info` on success and `slog.Error` on failure with the operation duration
- Security constraints enforced throughout: token values, API keys, and environment variable values are never logged — only variable names, session IDs, droplet IDs, durations, and exit codes

### Credential error handling integration test cases (ci-1oswb)
- Adds `checkStartupCredentials()` to `cmd/ct/castellarius.go` — called at `ct castellarius start` startup; returns an actionable error (non-zero exit) if `ANTHROPIC_API_KEY` is unset ("add it to `~/.cistern/env` and source it before starting") or the Claude OAuth token is expired ("run: `ct doctor --fix`"); prevents silent crashes on missing or stale credentials
- Adds `test_missing_credentials` to `run-installer-tests.sh` — integration test for the missing-env path: `ct init` succeeds, `~/.cistern/env` is removed, then asserts `ct doctor` exits non-zero and names `~/.cistern/env` in its output, and that `ct castellarius start` exits non-zero with `ANTHROPIC_API_KEY` in the output
- Adds `test_wrong_credentials` to `run-installer-tests.sh` — integration test for the expired-token path: writes a syntactically valid but rejected `ANTHROPIC_API_KEY` to `~/.cistern/env` and an expired OAuth credentials file (`expiresAt=1000`), then asserts `ct doctor` exits non-zero with an actionable error (`expired`/`invalid token`/`authentication failed`), and that `ct castellarius start` (with credentials exported via `set -a`) exits non-zero with `authentication failed` in its output
- Total test count in `run-installer-tests.sh` reaches 10

### Docker-based installer integration test suite (ci-nywg3)
- Adds `test_missing_credentials` to `run-installer-tests.sh` — absent-credentials failure path: runs `ct init`, removes `~/.cistern/env`, asserts `ct doctor` exits non-zero and names `.cistern/env` in its output, asserts the `cistern-castellarius` service fails to start (not a silent crash), and asserts the journal contains `not found` (the error from `start-castellarius.sh`)
- Adds `test_wrong_token` to `run-installer-tests.sh` — wrong-credentials failure path: runs `ct init`, overwrites `env` with `ANTHROPIC_API_KEY=` (present but empty), asserts `ct doctor` exits non-zero and names `ANTHROPIC_API_KEY` in its output, asserts the service fails to start, and asserts the journal contains `ANTHROPIC_API_KEY not set`
- Adds `.github/workflows/installer-tests.yml` — GitHub Actions CI job triggered on PRs touching `cmd/ct/doctor.go`, `cmd/ct/init.go`, `cmd/ct/assets/start-castellarius.sh`, `start-castellarius.sh`, `run-installer-tests.sh`, `tests/installer/**`, `internal/testutil/fakeagent/**`; builds Go binaries, runs `./run-installer-tests.sh`, and uploads `installer-test-container.log` as an artifact on failure; all three Actions SHA-pinned to immutable commit hashes; Docker base image pinned to a digest
- Unifies `start-castellarius.sh` (repo root) and `cmd/ct/assets/start-castellarius.sh` (embedded production version) — both now validate credentials (file existence + `ANTHROPIC_API_KEY` non-empty) **and** source `~/.cistern/env` before exec-ing `ct castellarius start`; previously the repo root version validated but never sourced while the embedded version sourced but never validated, creating a false-confidence gap in the credential-error tests
- Adds `set -euo pipefail` to `cmd/ct/assets/start-castellarius.sh` — without it, a syntax error in `~/.cistern/env` would be silently ignored and `ct` would start with an incomplete environment; now matches the safety flags in the repo root version
- Pins `tests/installer/Dockerfile.systemd` base image to a digest (`jrei/systemd-ubuntu@sha256:…`) — eliminates a supply-chain risk where a compromised Docker Hub push could inject arbitrary code with full host access on `--privileged` self-hosted CI runners

### Fresh-install and upgrade integration test cases (ci-z1e9b)
- Adds `test_fresh_install` to `run-installer-tests.sh` — end-to-end first-time install: asserts no `~/.cistern` exists before run, executes `ct init`, writes a minimal `cistern.yaml` (`repos: []`) and a placeholder `ANTHROPIC_API_KEY`, installs and starts `cistern-castellarius` as a system service, verifies the service is active, `claude` is on PATH, and `ct doctor` exits 0
- Adds `test_upgrade` to `run-installer-tests.sh` — upgrade simulation: pre-populates `~/.cistern` with a stale config containing a removed key (`stale_old_key: removed_in_v2`), runs `ct init` again (existing files preserved via `writeFileIfAbsent`), verifies the service restarts cleanly (active) and `ct doctor` still exits 0; `yaml.Unmarshal` ignores the unknown key so no migration is needed
- Adds `install_system_service()` helper — writes a `cistern-castellarius` system service unit to `/etc/systemd/system/` via `docker exec -i` heredoc (outer `'INSTALL_SCRIPT'` quoting keeps host expansion suppressed; inner `EOF` allows `${HOME_DIR}` to expand inside the container's shell), then calls `daemon-reload`, `enable`, `restart`
- Adds `wait_for_service_active()` helper — polls `service_status` (the existing helper) until the named unit reports `active` or a configurable timeout (default 10 s) expires; reuses `service_status` instead of duplicating the `exec_in_container + systemctl is-active` call
- Adds a `gh` stub to `test/docker/installer-test/Dockerfile` — a one-line `exit 0` script at `/usr/local/bin/gh` so `ct doctor`'s "gh CLI installed" and "gh authenticated" checks succeed without a real GitHub CLI or credentials
- Container cleanup on failure is guaranteed by the pre-existing EXIT trap — no orphaned containers; total test count in `run-installer-tests.sh` reaches 8

### Installer: service uses wrapper script; credentials not baked into service file (ci-ynllg)
- `installSystemdService()` (`ct doctor --fix`, auto-install path) now sets `ExecStart=~/.cistern/start-castellarius.sh` — the wrapper script, not `ct` directly; credentials are no longer baked into the systemd service file
- `ANTHROPIC_API_KEY` is removed from the service `Environment=` lines — credentials are loaded at runtime by the wrapper sourcing `~/.cistern/env` on every restart
- `installSystemdService()` creates `~/.cistern/start-castellarius.sh` (chmod 755) and `~/.cistern/env` stub (chmod 600) during install if absent, mirroring what `ct init` does
- `ct doctor` no longer false-alarms on `ANTHROPIC_API_KEY` missing from the systemd environment property — that property only reflects unit-file directives, not runtime variables sourced by the wrapper; the check now lives in the `~/.cistern/env` checks

### ~/.cistern/env credential store; ct init, ct doctor, start-castellarius.sh (ci-qdc7q)
- `~/.cistern/env` is now the canonical credential store — a simple `KEY=VALUE` file (one pair per line, chmod 600)
- `ct init` creates `~/.cistern/env` with chmod 600, adds `env` to `~/.cistern/.gitignore`, and writes `~/.cistern/start-castellarius.sh`
- `~/.cistern/start-castellarius.sh` sources `~/.cistern/env` before exec-ing `ct castellarius start` — updated credentials are picked up on every restart without editing the systemd service drop-in
- `ct doctor` checks that `~/.cistern/env` exists, is chmod 600 (warn if world-readable), and contains `ANTHROPIC_API_KEY`
- `ct doctor --fix` creates a missing `~/.cistern/env` and, in an interactive terminal, prompts for `ANTHROPIC_API_KEY` with masked input (no echo)

### Installer test harness with fakeagent and pass/fail output (ci-f2s0s)
- Adds `run-installer-tests.sh` — top-level test runner: builds the installer-test Docker image, starts a privileged container, runs 6 scaffolding tests, and reports results in GitHub Actions annotation format (`::notice::PASS:` / `::error::FAIL:`); exits 0 on all pass, 1 on any failure
- Adds `test/installer/helpers.sh` — shared Docker and reporting helpers: `build_image()` builds the systemd base then the installer-test image (fails fast on base-image failure via `|| return 1`), `wait_for_systemd()` polls `systemctl is-system-running` and accepts both `running` and `degraded`, `exec_in_container()` runs commands inside the container, `service_status()` queries a systemd unit's active state; container name is PID-namespaced to prevent parallel-run collisions; EXIT trap unconditionally stops the container
- Adds `test/docker/installer-test/Dockerfile` — two-stage image: Go builder stage compiles `ct` and `fakeagent` from source; runtime stage extends `cistern-systemd-test` (from ci-9olg2) with `git`, `tmux`, the compiled binaries, and a `/usr/local/bin/claude → fakeagent` symlink that stubs the Claude CLI without requiring a real API key or OAuth login
- Tests covered: systemd boot state, `ct` binary presence, fakeagent/claude stub presence, `ct init` config creation, `ct doctor` non-crash (exit ≤ 1), `service_status` helper correctness
- No credential or environment manipulation — scaffolding only

### systemd-capable Docker base image for installer tests (ci-9olg2)
- Adds `test/docker/systemd/Dockerfile` — a Debian Bookworm image that boots with `systemd` as PID 1, suitable for testing systemd-managed service installers (e.g. `cistern-castellarius.service` via `install.sh`)
- Sets `ENV container=docker` so systemd skips hardware-only targets; sends `STOPSIGNAL SIGRTMIN+3` so `docker stop` triggers an orderly shutdown instead of `SIGTERM`
- Masks 13 units that require hardware, VT consoles, or kernel interfaces unavailable in Docker (`systemd-udevd`, `getty@tty1`, `sys-kernel-debug.mount`, etc.) — prevents spurious `failed` units on every boot
- No host bind-mounts; `--privileged` grants an isolated cgroup namespace, not a shared one — no host-state leakage between runs
- `test/docker/systemd/README.md` documents the `--privileged` requirement, capability table (`CAP_SYS_ADMIN`, `CAP_SYS_PTRACE`, writable cgroup namespace), masked-unit rationale, and the narrower capability set available for hardened environments

### Four installer integration test scenarios (ci-rc4o9)
- Adds four end-to-end integration scenarios to `tests/installer/run-tests.sh`, each self-contained with `_reset_scenario_state` teardown to prevent cross-contamination
- **Scenario 1 — Fresh install**: runs `ct init` on a clean container, asserts `cistern-castellarius.service` reaches `active (running)` state via systemd, and asserts `ct doctor` exits 0
- **Scenario 2 — Missing credentials**: installs with no `~/.cistern/env`, asserts the service enters `failed` state with a journal message referencing missing credentials (not a silent crash), and asserts `ct doctor` exits non-zero with a diagnostic message; `ct doctor` call uses `env -u ANTHROPIC_API_KEY` to prevent false positives from an inherited env var
- **Scenario 3 — Wrong/expired token**: seeds `~/.cistern/env` with a syntactically valid but rejected API key and `~/.claude/.credentials.json` with an expired OAuth token, asserts the service startup error is actionable (mentions `invalid` or `expired` token), and asserts `ct doctor` surfaces the same error; `python3` added to `Dockerfile.systemd` so the OAuth expiry check in `start-castellarius.sh` is not silently skipped
- **Scenario 4 — Upgrade**: pre-seeds `~/.cistern` with a prior-version fixture (stale config keys, old binary path) and existing credentials, runs `ct init` again, asserts the service comes up cleanly, `ct doctor` passes, and credentials are preserved without silent overwrite
- `_reset_scenario_state` performs complete cleanup: stops and disables the service, removes `~/.cistern`, and removes `~/.claude` (not just `.credentials.json`) — prevents directory artifacts from leaking across scenarios
- `tests/installer/README.md` updated with an Integration scenarios section documenting all four scenarios and their assertions

### Docker systemd test infrastructure for installer tests (ci-chp73)
- Adds `tests/installer/Dockerfile.systemd` — multi-stage build: `golang:1.26` builder compiles `ct` and `fakeagent`; `jrei/systemd-ubuntu:24.04` runtime runs systemd as PID 1 with no `pass` or GPG installed
- Adds `tests/installer/build.sh` — builds the `cistern/installer-test:latest` image from the repository root; image tag overridable via `CISTERN_TEST_IMAGE`
- Adds `tests/installer/run-tests.sh` — 8 smoke tests covering: systemd boot, `ct version`, fakeagent `--print` output, `claude` on PATH, absence of `pass`, `ct init` config creation, `ct doctor` claude check, and `start-castellarius.sh` executable; script waits up to 60 s for `multi-user.target` internally so callers need no external `sleep`
- Adds `tests/installer/README.md` — documents required `--privileged` flag and all `docker run` options, test output format, GitHub Actions integration snippet, and credential story (no API key needed for smoke tests)
- `fakeagent` (from `internal/testutil/fakeagent/`) is installed as `/usr/local/bin/claude` — satisfies `exec.LookPath("claude")` without a real Claude CLI or API key

### Auto-refresh Claude OAuth token on expiry (ci-cms3j)
- `ct doctor --fix` now automatically refreshes the Claude OAuth access token when it is expired or near expiry: reads the stored refresh token, calls the Anthropic OAuth endpoint, writes the new access token to `~/.claude/.credentials.json`, updates the systemd service drop-in (`env.conf`) if present, and reloads/restarts the `cistern-castellarius` systemd service so the new token takes effect immediately
- Before spawning each agent session, the Castellarius silently checks whether the access token is expired or within a 5-minute window. If so, it attempts a background refresh using the stored refresh token and injects the new token into the session environment — sessions no longer fail silently with stale credentials
- If the pre-spawn refresh fails (no refresh token, network error, or token truly expired), the error message directs the user to run `claude` interactively to re-authenticate
- Both refresh paths use a 30-second timeout — a hung OAuth endpoint cannot block a spawn indefinitely
- Extracted shared OAuth logic into `internal/oauth` package (`Read`, `Refresh`, `WriteAccessToken`, `UpdateEnvConf`, `IsExpiredOrNear`) — no duplicate credential-parsing code

### ct doctor: OAuth token expiry and service env token freshness (ci-gr6up)
- `ct doctor` now checks whether the Claude OAuth token in `~/.claude/.credentials.json` is fresh, expiring soon, or already expired — reports ✓ with expiry time, ⚠ with time remaining if within 24 h, or ✗ with a prompt to run `claude` interactively to refresh
- `ct doctor` checks whether `ANTHROPIC_API_KEY` in the systemd service drop-in (`~/.config/systemd/user/cistern-castellarius.service.d/env.conf`) matches the current `accessToken` in `~/.claude/.credentials.json` — reports ✗ and prompts to update env.conf and restart if they differ
- Both checks skip silently when the credentials file or service drop-in is absent — no false positives on non-systemd or non-Claude setups
- Shared `readClaudeCredentials` helper deduplicates credential file reading across the two checks

### Arch renderer: static pixel map + semantic color roles (ci-mj0h3)
- Replaces the inline switch-case arch-shape logic with a static `archPixelMap` (`[14][28]rune`) — pillar shape is now compiler-enforced and visually readable in source
- Extracts named color-role variables (`archRoleBackground`, `archRoleEdge`, `archRoleIdle`, `archRoleActive`, `archRoleDrought`, `archRoleChannelWall`, `archRoleWaterBright/Mid/Dim`) replacing scattered inline hex literals — palette is now easy to retheme from one place
- Introduces `archPillarW = 28` / `archPillarH = 14` constants; removes duplicate local `pillarW = 28` from `tuiAqueductRow`, eliminating a silent-divergence hazard
- Visual output of `ct dashboard` is unchanged — color roles match the previously inlined values exactly

### Agent file compatibility: provider-appropriate instruction files (ci-5lmz1)
- `ct cataractae generate` now writes the provider-specific instructions file (`CLAUDE.md` for claude, `AGENTS.md` for codex/copilot/opencode, `GEMINI.md` for gemini) — filename is determined by the active provider preset
- When the active provider uses a different filename than `CLAUDE.md`, the new file is generated alongside any existing `CLAUDE.md` — `CLAUDE.md` is not deleted in case users switch providers
- `ct doctor` warns when `CLAUDE.md` exists in a cataractae directory but the active provider uses a different instructions file — prevents silent staleness after a provider change
- `ct doctor` reports a check failure when the configured provider name is unknown or invalid (e.g. a misspelling in `cistern.yaml`), instead of silently defaulting to checking `CLAUDE.md`
- Providers without `--add-dir` support (codex, gemini, copilot, opencode) now receive the cataractae instructions file + `PERSONA.md` + `INSTRUCTIONS.md` + referenced skill content concatenated into the prompt preamble, enabling full agent compatibility across all providers
- `SupportsAddDir` bool added to `ProviderPreset` — explicitly marks which providers support filesystem-based context injection; when `false`, Cistern falls back to prompt-text injection

### Refactor filtration: use provider preset for non-interactive LLM invocation (ci-4w2z0)
- Removed `github.com/anthropics/anthropic-sdk-go` — filtration no longer calls the Anthropic API directly; it uses the same agent binary as cataractae
- Added `NonInteractiveConfig` struct to `ProviderPreset` (fields: `Subcommand`, `PrintFlag`, `PromptFlag`) — describes how to invoke each agent CLI in single-shot (exec) mode
- Built-in presets updated: `claude` (`--print -p`), `codex` (`exec -p`), `gemini` (`-p`), `copilot` (`-p`), `opencode` (`run -p`)
- Replaced `callRefineAPI()` with `runNonInteractive(preset, systemPrompt, userPrompt)` — builds the command from the preset's `NonInteractive` config, passes a combined prompt via `PromptFlag`, and captures stdout via the unchanged `extractProposals()`
- `runNonInteractive` validates required env vars from `preset.EnvPassthrough` before executing; forwards `preset.ExtraEnv` into the subprocess environment
- On exec failure, type-asserts `*exec.ExitError` to include stderr output in the error message — agent failures are diagnosable
- Adds `internal/testutil/failagent` — a test binary that exits 1 with a known stderr message; used in `TestRunNonInteractive_AgentExecFailure` to verify that exec failure stderr is surfaced in the returned error
- Backward compatible: default is the `claude` preset; the built command is `claude --dangerously-skip-permissions --print -p '<prompt>'`; `ANTHROPIC_API_KEY` must be set (same requirement as before)

### Provider presets: smoke tests and bug fixes (ci-e014y)
- Adds `TestProviderCommandStrings` — table-driven test covering all 5 built-in presets (`claude`, `codex`, `gemini`, `copilot`, `opencode`) plus a custom user preset loaded from JSON; validates command binary, fixed args, model flag, `--add-dir` flag, env passthrough, and instructions file for each
- Adds `TestClaudeDefaultFallback` — regression gate that parses an `AqueductConfig` with no provider block, resolves the preset (must be `claude`), and asserts the built command is byte-for-byte identical to `buildClaudeCmd()` output
- Adds `TestProviderConfigMerge`, `TestUserPresetsJSON`, `TestLLMProviderDefaults`, and `TestRefineWithMockServer` (multi-provider LLM calls against the mock server from ci-t3xo9); all pass with no env vars set
- Adds `callRefineAPIWith(llm LLMProvider, ...)` — extends the filtration path to support OpenAI-compatible providers (OpenAI, OpenRouter, Ollama) via `/v1/chat/completions`; Anthropic delegates to the existing SDK path
- Fixes OpenRouter `BaseURL` (`https://openrouter.ai/api/v1` → `https://openrouter.ai/api`) — the old value produced a double `/v1/v1/` path when URL construction appended `/v1/chat/completions`; regression guard `TestOpenRouterURL_NoDuplicateV1` added
- Fixes `MergePresets` aliasing — override entries' `Args`, `EnvPassthrough`, and `ProcessNames` slices are now deep-copied before insertion, symmetric with base-entry handling
- Fixes unbounded `io.ReadAll` on error response body in `refine.go` — wrapped with `io.LimitReader(resp.Body, 1<<20)` to cap at 1 MB
- Fixes `ResolvePreset` fallback — replaced positional `builtins[0]` with an explicit `Name == "claude"` search so slice reordering cannot silently change the default

### Provider-agnostic agent spawner in session.go (ci-sc2wl)
- `session.go` now uses `ProviderPreset` to build agent commands — replaces the hardcoded Claude-specific `buildClaudeCmd` with a generic `buildPresetCmd` driven by preset fields (`Command`, `Args`, `AddDirFlag`, `ModelFlag`, `PromptFlag`)
- `GH_TOKEN` is now always forwarded as a platform-level env var regardless of provider, fixing a regression where the preset=claude path silently dropped it (legacy path forwarded it; preset path only forwarded `ANTHROPIC_API_KEY`)
- `provider.model:` in `cistern.yaml` now works correctly: `resolveModelVal` falls back to `preset.DefaultModel` when a step does not specify a model; previously the config option was a no-op
- `PromptFlag` field added to `ProviderPreset` — prompt delivery is no longer hardcoded to `-p`; presets that use a different flag or deliver prompts via stdin/instructions file set `PromptFlag` to the correct value or leave it empty
- Empty `Preset.Command` is now validated at spawn time with a descriptive error (`preset %q has no command configured`) instead of producing a broken tmux command string
- `isAgentAlive()` added to `Session`: queries `pane_current_command` and checks it against `preset.ProcessNames`, enabling the Castellarius to detect zombie sessions (tmux alive, agent exited); conservatively returns true when `ProcessNames` is empty

### Test harness: fake provider binary + mock LLM HTTP server (ci-t3xo9)
- Adds `internal/testutil/fakeagent` — a minimal Go binary that accepts the same flags as the `claude` CLI, reads the droplet ID from `CONTEXT.md`, sleeps 200 ms, then calls `ct droplet pass <id>`. Used in `session_test.go` to exercise the full `Spawn → isAlive → outcome` cycle without a real LLM CLI or API key.
- Adds `internal/testutil/mockllm` — an `httptest.Server` that handles `POST /v1/messages` (Anthropic) and `POST /v1/chat/completions` (OpenAI-compatible). Returns a hardcoded `HardcodedProposalsJSON` payload; records all requests (method, path, headers, body) for test assertions. Both handlers return `405 Method Not Allowed` for non-POST requests.
- Adds `TestClaudePresetBackwardCompat` — regression test asserting that the command built by `buildPresetCmd` with the built-in `claude` preset is byte-for-byte identical to `buildClaudeCmd`. Includes a `LookPath resolution` subtest that patches `claudePathFn` to verify parity when `CLAUDE_PATH` is not set.
- `session.go`: adds `buildPresetCmd`, introduces `claudePathFn` indirection (allows test injection without modifying process environment), and forwards `CT_DB` into the tmux session environment.
- All tests pass with `go test ./...` and no environment variables set.

### Provider configuration in cistern.yaml: select provider globally or per-repo (ci-5o65q)
- New `provider:` block in `cistern.yaml` selects which agent CLI Cistern uses — globally or per-repo
- Five built-in presets: `claude` (default, ANTHROPIC_API_KEY), `codex` (OPENAI_API_KEY), `gemini` (GEMINI_API_KEY), `copilot` (GH_TOKEN), `opencode`
- Top-level `provider:` applies to all repos; individual `repos[].provider:` overrides it for that repo only
- `provider.model:` sets the default model passed via the preset's model flag at launch time
- `provider.command:`, `provider.args:`, `provider.env:` override the executable, append extra args, and inject extra env vars
- When a repo specifies a different `name:` than the top-level, top-level field overrides are not applied (prevents cross-provider contamination)
- Backward compatible: configs without a `provider:` block continue to use the `claude` preset unchanged
- Dispatch-loop recovery: git reset/clean errors are now detected — failed recovery no longer falsely clears the failure counter and claims success
- Dispatch-loop recovery: worktree registration check uses exact path comparison, preventing false positives with prefix-sharing droplet IDs
- `ct update`: copyBinary now surfaces close errors on the restore path, preventing silent binary corruption when disk is full during a failed build's restore

### Provider presets: ProviderPreset struct and built-in registry (ci-x6rof)
- Introduces `internal/provider` package with `ProviderPreset` — the data model describing how to launch any agent CLI (command, fixed args, env passthrough, model flag, resume style, instructions file, and more)
- Built-in presets ship for five providers: `claude` (ANTHROPIC_API_KEY, `--model`, `--add-dir`, `CLAUDE.md`), `codex` (OPENAI_API_KEY, subcommand resume, `AGENTS.md`), `gemini` (GEMINI_API_KEY, `--model`, `GEMINI.md`), `copilot` (GH_TOKEN, 5 s ready delay, `AGENTS.md`), `opencode` (`AGENTS.md`)
- `LoadUserPresets(path)` reads `~/.cistern/providers.json` and merges user entries on top of built-ins — matching by name replaces the built-in; unknown names are appended; missing file returns built-ins unchanged
- `Builtins()` returns a deep copy (slice fields cloned via `slices.Clone`) so callers cannot corrupt global preset state

### Web TUI: fix peek ctrl+c causes disconnect/reconnect loop (ci-rts88)
- Pressing `p` to open peek in the browser (`/ws/tui`) no longer causes the dashboard subprocess to exit and reconnect in a loop
- `ctrl+c` while the peek overlay or picker is active now closes the overlay (same as `q`/`esc`) rather than quitting the program; `ctrl+c` from the bare dashboard still quits as intended
- `peekModel.Update` separates `esc` from the `q`/`ctrl+c` quit case — `esc` returns `nil` instead of `tea.Quit`, preventing accidental quit propagation when the model is embedded in `dashboardTUIModel`

### Dashboard: filter active aqueduct steps by droplet complexity (ci-jefan)
- Active aqueducts now show only the cataractae steps that will actually execute for the droplet's complexity level — steps whose `SkipFor` list includes the droplet's complexity are hidden
- `TotalCataractae` and `CataractaeIndex` are both computed from the filtered step list, keeping progress calculations accurate when skipped steps precede the current step
- Idle aqueducts continue to show all steps as a full-pipeline preview
- `NoteCount` field removed from `CataractaeInfo` and `FlowActivity` JSON (unused by consumers)
- `FlowActivity.RecentNotes` order changed to newest-first (last 3 notes, most recent at index 0)

### TUI dashboard: move droplet info to dedicated line below aqueduct name (ci-rxzft)
- Droplet ID, elapsed time, and progress bar are now displayed on a dedicated info line below the aqueduct name/repo line — no longer embedded in the water channel animation
- Name line (`lines[0]`): aqueduct name (green) + repo name (dim) on one line; info line (`lines[1]`): droplet ID + elapsed + 10-char progress bar in green; empty string when aqueduct is idle
- Water channel row is now a pure wave animation (`renderWave`) — `buildChanWater` and `infoStr` logic removed; channel top and water rows use a plain indent instead of the name/repo prefix
- `tuiAqueductRow` now returns 14 lines (1 name + 1 info + 1 label + 2 channel + 9 pillar rows), up from 12

### TUI dashboard: peek picker — auto-connect if one active, show inline selector if multiple (ci-wpd6w)
- Pressing `p` when exactly one aqueduct is active now connects immediately (unchanged behaviour)
- Pressing `p` when multiple aqueducts are active opens a centered picker overlay listing each active aqueduct: name, repo, droplet ID, and current step
- Up/Down (or `k`/`j`) navigates the picker; Enter connects to the selected aqueduct; Escape or `q` cancels
- If an aqueduct goes idle while the picker is open, `peekSelectIndex` is clamped to the new active count; if all aqueducts go idle the picker closes automatically
- Terminal resize events (`WindowSizeMsg`) while the picker is open update `m.width`/`m.height` so the overlay remains centred on subsequent renders

### TUI dashboard: show dry arch with 'drought' header when all aqueducts are idle (ci-gbb64)
- When all aqueducts are idle, `viewAqueductArches()` now renders a single dry pillar arch centered in the terminal instead of collapsing to idle text rows
- A centered `drought` label in dim styling sits above the arch; the pillar uses dim grey (`#46465a`) to convey emptiness — no water channel, no waterfall, no step labels
- Arch geometry mirrors the existing pillar template (28 chars wide, 14 rows) but without active colour or channel rows, keeping the drought display visually coherent with the live arch style
- `viewDroughtArch()` returns 15 lines (1 label + 14 pillar rows); existing idle row rendering is unchanged when at least one aqueduct is active

### TUI dashboard: water to active step, labels above arch, black backgrounds (ci-jo3fx)
- Channel water now fills only up to and including the active cataractae step — pillars to the right of the active step show a dry channel (empty walls, no water); idle aqueducts (no active droplet) show no water at all
- Step labels moved above the arch: each step name is now centered above its pillar column in a label row that appears before the channel top and water rows (layout: labels → channel top → channel water → pillar rows)
- All grey (color 8) background uses in the pillar template and surrounding rows replaced with black (color 0)
- Waterfall position and width adjusted to exit cleanly from the right edge of the last pillar at channel-row level with the new 28-col pillar width
- `buildChanWater` truncates `infoStr` with an ellipsis when it would exceed the available water-fill width, preventing the channel water row from overflowing the channel top and misaligning the right wall and waterfall
- `wetInnerW` formula corrected to `(activeIdx+1)*pillarW - 1` to account for the left wall column

### TUI dashboard: replace procedural arch with durdraw pillar template (ci-a8j0v)
- Replaced procedural arch rendering in `tuiAqueductRow` with a static durdraw pillar template (14 rows × 28 cols, fg=color3/olive, bg=black) tiled once per cataractae step
- Removed `archCrownAtT`, `colW`, `archTopW`, `taperRows`, `pierRows`, `brickW` constants and `math` import
- Active cataractae step highlighted by rendering ▒ chars in bright green (#4bb96e)
- Channel/water, waterfall, and step label rendering unchanged

### TUI dashboard: apply arch-designer constants from user session (ci-sdvst)
- Updated arch constants in `tuiAqueductRow`: `colW` 14→19, `archTopW` 9→10, `taperRows` 4→3, `pierRows` 1→4, `brickW` 4→2
- Expanded `wfRows` from `[10]string` to `[14]string`; added 4 new settling-pool sub-rows (10–13)

### arch-designer: web UI with xterm.js terminal and on-screen button panel (ci-gyt7d)
- `arch-designer --web` starts an HTTP server on port 5738 (default) serving the TUI in a browser via xterm.js — pixel-perfect block-character rendering, exact 1:1 terminal output
- `--port N` overrides the listen port (e.g. `arch-designer --web --port 5739`)
- On-screen touch-friendly button panel drives the TUI without a keyboard:
  - **Prev / Next** (Shift+Tab / Tab) — cycle through parameters
  - **↑ / ↓** — adjust selected parameter by ±1
  - **+5 / −5** (Shift+↑ / Shift+↓) — adjust by ±5
  - **[L] Preset** — load defaults
  - **[R] Reset** — reset to defaults
  - **[S] Save & Copy** — print Go constants and copy them to clipboard via `navigator.clipboard`
- PTY bridge: browser sends keystrokes as WebSocket text frames; server forwards them to PTY stdin. PTY output is streamed as binary WebSocket frames to xterm.js (same protocol as `/ws/tui` in the dashboard)
- Automatic 3 s reconnection on WebSocket close
- TUI mode (no flags) is unchanged
- `cistern-arch-designer.service` — systemd user service starts `arch-designer --web --port 5738` on login; logs to `~/.cistern/arch-designer.log`

### Castellarius: fix empty diff.patch on repeated adversarial-review cycles (ci-s5eg9)
- `diff.patch` was empty (0 bytes) on the third and subsequent adversarial-review spawns for any recirculated droplet, blocking the pipeline and requiring manual intervention each time
- Root cause: `prepareDropletWorktree` was only called for `full_codebase` context steps — `diff_only` steps (adversarial-review) fell back to the worker's own sandbox, which is on `main` and has no feature-branch changes; `generateDiff` then produced an empty output
- Fix: `prepareDropletWorktree` now runs for every agent context type except `spec_only` — `diff_only` steps receive the per-droplet worktree path so `generateDiff` always reads the correct feature branch
- Defense: `SpawnStep` now fails loudly with an explicit error if a `diff_only` step arrives without a per-droplet `SandboxDir`, rather than silently producing an empty diff

### TUI dashboard: higher-density arch rendering (ci-qijob)
- Arch constants updated for higher visual fidelity at smaller scale: `colW` 20→14 (30% narrower), `archTopW` 10→9, `taperRows` 3→4 (more curve steps = sharper arch shape)
- `wfRows` expanded from 8 to 10 sub-rows to match the new `(taperRows+pierRows)×2 = 10` layout
- `wfRows` array size is now derived from constants at compile time (`[2*(taperRows+pierRows)]string`) — mismatches between the array size and the constants are now caught by the compiler instead of causing a runtime panic

### cistern-reviewer skill: unified multi-language reviewer (ci-1xcm6)
- New bundled skill `cistern-reviewer` merges `adversarial-reviewer` and `critical-code-reviewer` into a single authoritative review skill covering Go, TypeScript/Next.js, and TypeScript/React
- Retains the full adversarial mindset (Guilty Until Proven Exceptional, Evaluate the Artifact), Go-specific red flags (goroutine leaks, bare recover, unguarded map writes, defer in loops), TypeScript red flags (any abuse, missing null checks, unhandled promises, useEffect lies), front-end patterns, SQL/ORM patterns, structured severity tiers (Blocking / Required / Suggestions), the Slop Detector, Structural Contempt, When Uncertain section, and the two-phase pre-finalization checklist
- `adversarial-review` cataractae in `aqueduct.yaml` now references `cistern-reviewer` instead of `adversarial-reviewer`
- `skills/adversarial-reviewer/` and `skills/critical-code-reviewer/` removed from the repo — both are superseded by `cistern-reviewer`

### Replace github-workflow skill with Cistern-native cistern-github (ci-cdc8h)
- New `skills/cistern-github/SKILL.md` replaces the externally-installed `github-workflow` skill
- Explicitly enforces automatic conflict resolution — agents must never stop and ask the user; keep both sets of changes (HEAD adds X, branch adds Y → keep both)
- Includes the `git add $(git diff --name-only --diff-filter=U)` staging step between conflict resolution and `git rebase --continue` — previously missing, which left resolved files unstaged
- Removes all stacked-PR workflow content (Cistern uses per-droplet branches, not stacked PRs)
- `aqueduct.yaml`: `github-workflow` replaced by `cistern-github` in all cataractae that referenced it (`implement`, `adversarial-review`, `delivery`)
- Delivery `timeout_minutes` raised from 45 → 60 to match typical merge + CI wait times

### Castellarius: hot-reload cistern.yaml on change (ci-o3790)
- `cistern.yaml` changes are now detected on each heartbeat and trigger a clean restart — no more `systemctl --user restart cistern-castellarius` required after editing the config.
- Detection uses mtime comparison: the file's modification time at startup is compared to the current mtime on each drought. If newer, a restart is signaled.
- Under a supervisor (systemd, `CT_SUPERVISED=1`, etc.): `os.Exit(0)` — the supervisor restarts the process with the new config, same as binary-update behaviour.
- Unsupervised: a `WARN` log is emitted (`cistern.yaml updated on disk — manual restart required`) and the Castellarius continues running — same behaviour as binary-update detection.
- When both `cistern.yaml` and `aqueduct.yaml` change simultaneously, the workflow hot-reload is suppressed in favour of the clean restart (a restart picks up both changes).
- New `WithConfigPath(path string)` option on `castellarius.New()` wires in the mtime capture at construction time; `ct castellarius start` passes this automatically.
### cistern-git skill — fix diff to use merge-base syntax (ci-7awyb)
- Replaced two-dot diff (`git diff origin/main..HEAD`) with merge-base syntax (`git diff $(git merge-base HEAD origin/main)..HEAD`) — two-dot includes all commits since branch diverged from main, meaning other merged PRs appear in the diff on unrebased branches
- Removed incorrect warning against three-dot diff; merge-base is the correct approach for both rebased and unrebased branches
- Updated `--name-only` and `git log` variants to match
- Updated `cataractae/simplifier/INSTRUCTIONS.md` and `README.md` to reflect corrected advice

### Per-step model selection via model: field in aqueduct.yaml (ci-4ed0h)
- Each cataractae step now accepts an optional `model:` field specifying which LLM to use (e.g. `sonnet`, `opus`, `haiku`, `claude-opus-4-6`)
- If `model:` is absent, the agent uses its default — no behavior change for existing configs
- `WorkflowCataractae.Model` is `*string` so absent vs. empty-string are distinguishable
- `ct doctor` validates that `model:` values are non-empty strings when present
- `simplify` and `adversarial-review` steps in the default `aqueduct.yaml` now set `model: opus` — deep refactoring and adversarial review benefit from the stronger model

### Remove embedded defaults and `ct cataractae reset` (ci-kda7q)
- Removed `internal/aqueduct/defaults/` — embedded role content (`implementer.md`, `qa.md`, `reviewer.md`, `security.md`) is superseded by the `cataractae/` directories introduced in #102.
- Removed `ct cataractae reset` command — there are no more built-in defaults to reset to. Edit `PERSONA.md` / `INSTRUCTIONS.md` directly and use `ct cataractae generate` to regenerate.
- Removed the `CataractaeDefinition` type and `BuiltinCataractaeDefinitions` map from the `aqueduct` package.

### Castellarius: log AddNote and SetLastReviewedCommit errors at WARN (ci-q4npe)
- Errors from `AddNote` and `SetLastReviewedCommit` are now logged at WARN level instead of being silently discarded (`_ = ...`)
- Affected call sites: `adapter.go`, `stuck_delivery.go`, `context.go` — non-blocking; errors do not propagate or affect delivery flow
- Makes diagnostic failures (e.g. DB issues) visible in Castellarius logs without changing behaviour
- Three new tests verify WARN is emitted when each call fails

### Test coverage: internal/castellarius — dispatch loop, stall detection, aqueduct pool, heartbeat (ci-ybfbh)
- Adds `coverage_gaps_test.go` covering the core Castellarius dispatch loop, stall detection, aqueduct pool management, heartbeat, and session lifecycle — all previously untested
- No behaviour changes; all existing tests continue to pass

### Dead code: remove stale var err / _ = err pattern in cmd/ct/repo.go (ci-gs281)
- Removed three dead-code `var err error` / `_ = err` / unreachable `if err != nil` blocks in `repoListCmd`, `repoAddCmd`, `repoCloneCmd`
- No behaviour change

### Test coverage: cmd/aqueduct — runConfigValidate, runStatus, resolveDeliveryDBPath (ci-vh7ii)
- Adds 10 unit tests covering `runConfigValidate`, `runStatus`, and `resolveDeliveryDBPath` in `cmd/aqueduct`
- Package coverage improved from 6.7% to 47%

### Test coverage: internal/skills — IsInstalled, ListInstalled, Remove, removeManifestEntry (ci-swjsh)
- Adds 11 tests covering `IsInstalled`, `ListInstalled`, `Remove`, and `removeManifestEntry` — all previously at 0% coverage

### Test coverage: ct cataractae subcommands (ci-eerdv)
- Adds test coverage for all `ct cataractae` subcommands (`add`, `generate`, `list`, `edit`), previously at 0%

### Test coverage: internal/cistern/client.go — untested state ops (ci-fsomz)
- Adds table-driven tests for `UpdateTitle`, `GetNoteCount`, `SetOutcome`, `SetCataractae`, `Purge`, and `ListRecentEvents` — all previously untested

### Droplet display: remove recirculation counter and yellow color (ci-pkz7a)
- Removed the standalone recirculation counter (`↩ N`) and yellow color styling from droplet list and dashboard displays
- Recirculate events remain visible via `♻` icon prefixed inline in the note text (set by `ct droplet recirculate`)

### aqueduct.yaml: remove cataractae_definitions field (ci-gqwjt)
- `cataractae_definitions:` stanza removed from `aqueduct.yaml` (and the embedded asset) — inline role definitions are no longer supported in workflow config
- All role content has moved to `cataractae/<role>/` directories; `aqueduct.yaml` is now routing config only
- Related parsing types and `ct cataractae` command internals updated accordingly

### ct doctor: fix false failure for skills with path: field (ci-5mvl3)
- `ct doctor` previously reported in-repo skills referenced with a `path:` field in `aqueduct.yaml` as not installed, even when accessible
- Fixed: health check now correctly validates skills regardless of whether `path:` or name-only references are used

### Skills: unified handling — all skills live in ~/.cistern/skills/ (ci-add2g)
- Removed the `path:` field from skill references in `aqueduct.yaml` — all skills are now referenced by name only. Skills that previously used `path:` must be accessible via `~/.cistern/skills/<name>/SKILL.md`.
- The `git_sync` drought hook now automatically deploys skills from the repo's `skills/` tree into `~/.cistern/skills/` after each fetch — no manual `ct skills install` required for repo-bundled skills.
- Skills deployed by `git_sync` are recorded in the manifest as `source_url:local`; `ct skills update` skips these (they are kept up to date by `git_sync` automatically).
- `ct doctor` now checks `~/.cistern/skills/<name>/SKILL.md` for every skill uniformly — the previous exemption for in-repo skills is removed.
- `ct skills list` now shows all installed skills; the old `path:` filter that hid in-repo skills is gone.

### Notes: cataractae name attribution, newest-first order, recirculate icon (ci-mvnq7)
- Notes now show the **cataractae name** instead of `[manual]` — `CT_CATARACTA_NAME` (injected into every agent session) is used for attribution; falls back to `manual` for direct CLI invocations.
- Notes are displayed **newest first** — `ct droplet show <id>` and CONTEXT.md both surface the most recent context at the top without scrolling.
- `ct droplet recirculate --notes "..."` prefixes the note content with ♻ inline — the recirculate icon replaces the now-removed standalone recirculation counter and makes recirculate cycles immediately identifiable in the note list.

### git_sync: deploy cataractae PERSONA.md + INSTRUCTIONS.md (ci-jesew)
- The `git_sync` drought hook now deploys `PERSONA.md` and `INSTRUCTIONS.md` for every role defined in the workflow YAML, writing them to `~/.cistern/cataractae/<role>/` — the same pattern used for the workflow YAML itself (`git show origin/main:<path>`).
- Previously only `aqueduct.yaml` was extracted from `origin/main`; cataractae source files were never synced, so a `git_sync` followed by `cataractae_generate` produced CLAUDE.md files from stale or missing source files, often re-generating the legacy stub content without the sentinel.
- Missing files (role in YAML but no corresponding directory in origin/main) are logged at INFO level and skipped — they do not halt the sync.
- Fixed `worktree_prune` drought hook: `git worktree prune` now runs against the `_primary` clone (`~/.cistern/sandboxes/<repo>/_primary/`), not the repo sandbox root, which had no worktree metadata.

### ct cataractae add: auto-generate CLAUDE.md on scaffold (ci-f4354)
- `ct cataractae add <name>` now runs `ct cataractae generate` automatically after creating the template files — `CLAUDE.md` is ready immediately without a separate generate step.
- Output format updated to `Created:` / `Updated:` / `Generated:` lines matching the actual files produced, followed by an instruction to edit `PERSONA.md` and `INSTRUCTIONS.md` and wire the cataractae into the pipeline.
- Default description in `aqueduct.yaml` is now `TODO: describe this cataractae` instead of `<Name> identity.`

### Cataractae: self-contained directories and ct cataractae add command (ci-cgey2)
- Each cataractae identity is now a self-contained directory under `cataractae/<identity>/` containing `PERSONA.md` (role and guardrails) and `INSTRUCTIONS.md` (task protocol). `CLAUDE.md` remains a generated artifact built from these files.
- `aqueduct.yaml` no longer stores inline `instructions:` blobs — routing config only. Operators who previously edited inline YAML text should move that content into the appropriate `PERSONA.md` / `INSTRUCTIONS.md` files and run `ct cataractae generate`.
- New `ct cataractae add <name>` command scaffolds a new cataractae directory with template files and adds the entry to `aqueduct.yaml`. Run `ct cataractae generate` after editing the templates to produce `CLAUDE.md`.
- All skills now have explicit `path:` references in `aqueduct.yaml`; `adversarial-reviewer` and `github-workflow` skills added to the repo under `skills/`.
- `simplifier` cataractae directory created (was previously missing).

### ct status: --watch flag for auto-refresh (ci-drisq)
- `ct status --watch` continuously refreshes the status display every 5 seconds (Ctrl-C to stop)
- `--interval N` sets the refresh interval in seconds (default 5, minimum 1)
- Outside watch mode, behaviour is unchanged

### ct version: --json flag (ci-4j6up)
- `ct version --json` outputs `{"version":"<version>","commit":"<sha>"}` — machine-readable format for scripting and CI
- Plain `ct version` output unchanged

### cistern-git skill — git conventions for cataractae
- New bundled skill `cistern-git` encodes hard-won git conventions: always exclude `CONTEXT.md` from staging (`git add -A -- ':!CONTEXT.md'`), always use two-dot diff (`origin/main..HEAD`), never stash in per-droplet worktrees
- Wired into implement, simplify, docs, and delivery cataractae; replaces inline git instruction blocks that were previously embedded in each YAML entry
- Two-dot diff prevents three-dot diff from appearing empty on rebased branches — the root cause of several dispatch loops

### Castellarius: dispatch-loop detection and auto-recovery (ci-ae5o8)
- The Castellarius now detects droplets stuck in a tight **dispatch loop** — repeatedly failing to spawn an agent (e.g. dirty worktree, missing worktree) with no session ever starting — and attempts ordered self-recovery automatically
- Detection threshold: 5 or more dispatch failures within any 2-minute window with no successful agent spawn
- Recovery is ordered by invasiveness:
  1. **Dirty worktree**: runs `git reset --hard HEAD && git clean -fd` on the droplet's worktree, then allows the next dispatch to proceed normally
  2. **Missing or corrupt worktree**: removes and recreates the worktree from the primary clone
  3. **Persistent failure**: if recovery fails 3 times without a clean dispatch, the droplet is escalated to `stagnant` with a note — a human can investigate and use `ct droplet restart` to re-enter
- All recovery attempts are attached as notes on the droplet (`ct droplet show <id>`) and logged by the Castellarius with a `dispatch-loop recovery:` prefix
- A successful agent spawn resets the failure counter; a droplet that recovers cleanly leaves no permanent trace

### ct update: self-update command (ci-j5d48)
- New `ct update` subcommand pulls the latest `main` and rebuilds the `ct` binary in-place — no manual `git pull` or `go build` required
- Auto-detects the cistern repo location in priority order: `CT_REPO_PATH` env var → sibling of the binary (e.g. `~/go/bin/ct` → `~/cistern`) → `~/.cistern/repo`; use `--repo-path PATH` to override
- Prints old and new commit SHAs after a successful update; says "already up to date" and exits 0 if nothing changed
- `--dry-run` fetches `origin/main` and shows what would change without building or modifying anything
- If the build fails, the previous binary is automatically restored from a `.bak` copy and a non-zero exit is returned
- Prints a warning if the Castellarius is running (it will restart automatically via binary-mtime detection after the update)

### Castellarius: per-droplet worktrees (ci-ynhgu)
- **Worktrees are now droplet-scoped**, not aqueduct-scoped. Each droplet gets a fresh git worktree at `~/.cistern/sandboxes/<repo>/<droplet-id>/` on branch `feat/<droplet-id>` when it enters the `implement` step.
- Aqueduct names (`virgo`, `marcia`, etc.) are now **concurrency slots only** — they limit how many droplets run in parallel per repo. They no longer correspond to persistent worktree directories.
- **Dirty worktree gate**: before dispatching a droplet, the Castellarius runs `git status --porcelain` on the worktree and recirculates with a diagnostic note if non-`CONTEXT.md` files are uncommitted. Prevents agents from inheriting dirty state from a prior session.
- **Worktree cleanup**: terminal routes (`done`/delivery complete, `block`, `escalate`, `human`) remove the per-droplet worktree. Non-terminal routes (pass to next step, recirculate) preserve it so the next cycle can resume incrementally.
- **Stash policy**: with per-droplet worktrees, manual stashing between cataractae should no longer be needed. Dirty state in these worktrees is detected and recirculated instead; automated delivery flows may still use `git stash` internally where appropriate.
- Fixes the ci-792v7 class of failure: the implementer's uncommitted files were left in the worktree. With per-droplet worktrees, the Castellarius now detects and reports this before dispatch rather than silently proceeding.
- Existing in-flight droplets using aqueduct-named worktrees continue to work during migration.

### Castellarius: stuck delivery detection and recovery (ci-8hhrs)
- The Castellarius now detects delivery agents that have been running past 1.5× the delivery `timeout_minutes` (default 45 m → 67.5 m threshold) and recovers them automatically — no human intervention required
- A background goroutine checks every 5 minutes; a stuck agent is one whose tmux session is still alive past the threshold with no outcome written
- Recovery protocol per PR state:
  - **MERGED**: signals pass — the work is done, the agent just didn't notice
  - **OPEN + branch behind main** (`BEHIND`): rebases onto `origin/main`, force-pushes with lease, enables `--auto` merge, signals pass
  - **OPEN + CI failing** (`BLOCKED`/`UNSTABLE`): recirculates so the pipeline can attempt a fix
  - **OPEN + all checks green** (`CLEAN`): attempts direct merge, falls back to `--auto` merge, signals pass
  - **CLOSED (not merged)** or no PR found: recirculates with notes
- Stuck threshold is configurable: set `timeout_minutes` on the `delivery` step in your aqueduct YAML; the check triggers at 1.5× that value
- Recovery is idempotent — safe to trigger multiple times on the same droplet
- All recovery actions are noted on the droplet (`ct droplet show <id>`) and logged by the Castellarius
- Fixed: `gh pr list` now passes `--state all` so MERGED and CLOSED PRs are visible to the recovery logic (not just OPEN)

### Sandbox worktree optimization (ci-6al33)
- **Reduced disk cost**: N aqueducts per repo now share a single primary clone object store. Each aqueduct gets a lightweight git worktree (~4.7 MB working tree) instead of a full independent clone (~16 MB). At 100 aqueducts this drops sandbox disk cost from ~1.6 GB to ~490 MB.
- Primary clone lives at `~/.cistern/sandboxes/<repo>/_primary/`. Aqueduct worktrees remain at `~/.cistern/sandboxes/<repo>/<aqueduct>/` — same paths as before, no migration required.
- On startup, stale worktree registrations are pruned automatically before adding new ones, preventing `already in use` errors after unexpected exits.
- Legacy dedicated clones at aqueduct paths are automatically replaced by worktrees on next startup.
- Branch lifecycle is now owned by the Castellarius: feature branches (`feat/<id>`) are created and cleaned up by the scheduler, not the runner. Agents do not manage branches directly.
- Non-terminal routes (pass to next step, recirculate) preserve the feature branch so the next cycle can resume incrementally. Terminal routes (deliver, block, escalate) clean up the branch.

### Implementer: strengthened post-commit verification (ci-kxdf5)
- **Post-commit verification section added to `implementer.md`**: after `git commit`, agents must run six checks (a–f) before signaling pass.
- Check (a) confirms HEAD moved; (b) confirms the diff is non-empty; (c) confirms no staged or unstaged implementation files remain; (d) is a hard-gate grep for a key function from the implementation in the diff.
- Check (e) verifies non-trivial (non-.md) files changed — if the commit only touches `.md` files the agent must not pass. **Exception:** when the named deliverable in CONTEXT.md is itself a `.md` file, check (e) does not apply; the agent proceeds to check (f) instead.
- Check (f) confirms that any named deliverable file is present in the commit (`git show HEAD -- <file> | wc -l` must be > 0).
- Prevents the failure mode where an agent commits only CONTEXT.md or docs files, passes the old HEAD-SHA check, and leaves real implementation files uncommitted.

### Delivery: abort on dirty worktree and docs-only branch (ci-3sfr8)
- **Dirty worktree pre-flight check**: before running `git stash`, the delivery cataractae runs `git status --porcelain` and recirculates if any non-CONTEXT.md files are uncommitted. Prevents silently stashing an implementer's work and delivering an empty branch.
- **Docs-only deliverables check**: before creating the PR, the delivery cataractae checks `git diff origin/$BASE...HEAD --name-only` and recirculates if only `.md`/`.txt`/CHANGELOG/README/CONTEXT files changed. A branch must contain at least one implementation file (`.go`, `.yaml`, etc.) unless the droplet is explicitly docs-only.
- Both checks apply to the default aqueduct (`aqueduct/aqueduct.yaml`) and the embedded asset (`cmd/ct/assets/aqueduct/aqueduct.yaml`).

### Web dashboard: pinch-to-zoom, Ctrl+scroll, and scale-aware terminal
- **Pinch-to-zoom on mobile**: touch pinch scales the xterm.js font size proportionally; Safari gesture events for trackpad pinch; font size clamped to 7–28 px
- **Ctrl+scroll on desktop**: keyboard-friendly zoom — same effect as pinch
- **Single-finger pan after zoom**: CSS `transform: scale()` applied to the xterm container; scrollable overflow lets you pan to any part of the zoomed terminal
- **Scale-aware virtual area**: default scale 0.75 renders the TUI as if the screen is 33% larger — FitAddon sees a bigger element, so Bubble Tea shows more aqueducts and content; CSS then scales it back down to fit the viewport
- FitAddon refit called after every font-size or viewport change so PTY dimensions always match displayed size

### Web dashboard: xterm.js TUI terminal (ci-792v7)
- `/ws/tui` WebSocket endpoint streams the TUI render loop as raw ANSI to the browser — the same output the terminal sees, with no reimplementation drift
- Replaces the CSS arch section and all JS rendering functions with a single full-viewport xterm.js terminal
- xterm.js 5.3.0 + FitAddon 0.8.0 loaded from CDN (no build step); handles ANSI codes, Unicode box-drawing chars, and cursor movement natively
- `lipgloss.SetColorProfile(TrueColor)` set in `RunDashboardWeb` so the server produces ANSI colour output even when stdout is not a terminal
- FitAddon auto-sizes the terminal to the browser window on load and on every `window resize` event
- Automatic 3 s reconnection on WebSocket close
- SSE (`/api/dashboard/events`) and peek WebSocket (`/ws/aqueducts/{name}/peek`) endpoints preserved for programmatic consumers

### Web dashboard: responsive CSS arch diagram (ci-jvgk7)
*(superseded by ci-792v7 above — xterm.js replaces the CSS arch section entirely)*
- Arch section replaced block-character rendering with CSS flexbox/grid — readable on mobile (375 px viewport and up)
- CSS `wave-scroll` animation (linear-gradient) replaces `░▒▓≈` scrolling characters; `wf-fall` animation replaces block-char waterfall shimmer
- Responsive breakpoint at 480 px: piers wrap to two-column grid on narrow screens, aqueducts stack vertically
- Touch targets on peek buttons minimum 44 px tall; labels use `rem` units (minimum `0.875rem`)
- Active aqueduct shows droplet ID, elapsed, and progress bar in text — no character art; idle aqueducts remain as a single compact dim row
- TUI dashboard block-char rendering is unchanged; only the web dashboard is affected

### Web dashboard
- `ct dashboard --web` starts a Go HTTP server on port 5737 (no ttyd, no terminal emulator required)
- `GET /api/dashboard` returns `DashboardData` as JSON
- Server-sent events (SSE) at `/api/dashboard/stream` push live updates every 2 seconds
- Aqueduct arch section uses CSS-based rendering; remaining sections (current flow, cistern, recent flow) use `<pre>`-formatted HTML matching the TUI colour palette
- Active aqueducts show full arch diagram with droplet ID, elapsed, progress bar, and repo name; idle aqueducts collapse to a single compact dim row
- CURRENT FLOW section with relative timestamps; CISTERN queue with priority icons (↑ · ↓)
- `--addr` flag sets listen address (default `:5737`)
- `cistern-web.service` systemd user service starts the web dashboard automatically
- TUI dashboard (`ct dashboard` without `--web`) continues to work unchanged

### Peek overlay (TUI dashboard)
- Press `p` or `Enter` in the TUI dashboard to open a read-only live peek overlay showing the first active aqueduct's agent tmux session output
- Overlay is clearly labelled **Observing — read only**; no keyboard input is forwarded to the session
- Press `q` or `Esc` to close the overlay and return to the dashboard
- Footer hint updated to include `p peek`

### Peek panel (web dashboard)
- Click any active aqueduct arch to open a live read-only peek panel showing the agent's tmux session output
- Panel is clearly labelled **Observing — read only**; no keyboard input is forwarded
- Auto-scrolls to bottom; click **pin scroll** to lock the scroll position
- `GET /api/aqueducts/{name}/peek` — snapshot of current pane content as plain text; `?lines=N` sets capture depth (default 100)
- `WS /ws/aqueducts/{name}/peek` — WebSocket stream; polls tmux every 500 ms and sends diffs to the client
- Graceful fallback: panel shows "session not active" when the aqueduct is idle or the tmux session is not found

### Skills — fix: skills unavailable in non-cistern-repo sandboxes
- **Root cause:** runner.go previously copied skill files into `sandbox/.claude/skills/<name>/SKILL.md` at job start; in-repo `path:` skills resolved relative to the sandbox worktree, so any skill defined as `path: skills/…` would fail with a copy warning in ScaledTest or PortfolioWebsite sandboxes (those paths only exist in the cistern repo).
- **Fix:** skills are no longer copied. `~/.cistern/skills/` is now the single source of truth. The runner passes `--add-dir ~/.cistern/skills` to the `claude` CLI so Claude reads skill files directly from the installed store.
- CONTEXT.md skill locations are now written as absolute paths pointing into `~/.cistern/skills/` (via `skills.LocalPath()`), making them valid in any sandbox regardless of which repo it clones.
- Skills must be installed before running an aqueduct (`ct skills install <name> <url>`); the runner logs a warning and continues if a referenced skill is not installed.
- `ct doctor` verifies that all skills referenced in aqueduct YAML are present in `~/.cistern/skills/`.

## v1.0.0 — 2026-03-18

First stable release of Cistern — a Mad Max–themed agentic workflow orchestrator for software development.

### Core pipeline
- **4-cataractae pipeline**: implement → adversarial-review → qa → delivery
- **Non-blocking Castellarius**: observe-dispatch loop; agents write outcomes directly to SQLite via `ct droplet pass/recirculate/block`
- **Dedicated sandbox clones**: each aqueduct gets a full independent git clone; worktree conflicts are impossible
- **Sticky aqueduct assignment**: droplets stay on their first aqueduct for all pipeline steps

### CLI commands
- `ct droplet add` — add a droplet with optional `--filter` (LLM-assisted intake), `--priority`, `--depends-on`, `--complexity`
- `ct droplet list` — list all droplets with status icons and elapsed time
- `ct droplet peek` — tail live agent output from the active tmux session
- `ct droplet stats` — summary counts by status
- `ct droplet approve` — human gate: approve a stalled droplet to continue
- `ct droplet pass/recirculate/block` — agent outcome commands
- `ct castellarius start/stop/status` — manage the Castellarius daemon
- `ct flow status` — show aqueduct and cistern state
- `ct doctor` — health check with CLAUDE.md integrity verification and skills validation
- `ct roles list/generate/edit/reset` — manage cataractae role definitions
- `ct version` — print version

### Dashboard
- TUI dashboard with Roman aqueduct arch diagram (one arch per aqueduct)
- Arch crown material + tapered brick piers with staggered mortar courses
- Active cataractae glows green; semicircle intrados via adaptive formula
- CISTERN section: queued droplets with priority, age, and blocked-by status
- RECENT FLOW: last 10 delivered droplets
- Originally served via ttyd WebSocket at port 5737; replaced by Go HTTP server in unreleased (see above)
- Cascadia Code font embedded in page for consistent rendering

### Cataractae
- **Implementer**: TDD/BDD approach, grep-verify each revision note, `git show HEAD` diff scan before signaling pass
- **Adversarial reviewer**: binary pass/recirculate only; two-phase review (Phase 1: evidence for prior issues; Phase 2: fresh diff)
- **QA**: active verification — run the actual tests, not just read the code
- **Delivery**: PR creation → CI gate → merge in a single agent cataractae
- **Security** (priority 1 only): adversarial security review for critical droplets

### Infrastructure
- Skills local-first: `~/.cistern/skills/<name>/SKILL.md`
- `ensureCataractaeIntegrity()`: validates CLAUDE.md files on startup, regenerates if corrupt
- Revision notes injected at top of CONTEXT.md; capped at 4 most recent
- Two-phase review protocol in feature.yaml
- Droplet dependency blocking
- `ct droplet add --filter`: LLM-assisted intake refines vague ideas into well-specified droplets
- `ct doctor --fix`: auto-repair common configuration issues
- Self-hosted CI runner via GitHub Actions
