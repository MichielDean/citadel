# Changelog

## Unreleased

### ~/.cistern/env credential store; ct init, ct doctor, start-castellarius.sh (ci-qdc7q)
- `~/.cistern/env` is now the canonical credential store — a simple `KEY=VALUE` file (one pair per line, chmod 600)
- `ct init` creates `~/.cistern/env` with chmod 600, adds `env` to `~/.cistern/.gitignore`, and writes `~/.cistern/start-castellarius.sh`
- `~/.cistern/start-castellarius.sh` sources `~/.cistern/env` before exec-ing `ct castellarius start` — updated credentials are picked up on every restart without editing the systemd service drop-in
- `ct doctor` checks that `~/.cistern/env` exists, is chmod 600 (warn if world-readable), and contains `ANTHROPIC_API_KEY`
- `ct doctor --fix` creates a missing `~/.cistern/env` and, in an interactive terminal, prompts for `ANTHROPIC_API_KEY` with masked input (no echo)

### systemd-capable Docker base image for installer tests (ci-9olg2)
- Adds `test/docker/systemd/Dockerfile` — a Debian Bookworm image that boots with `systemd` as PID 1, suitable for testing systemd-managed service installers (e.g. `cistern-castellarius.service` via `install.sh`)
- Sets `ENV container=docker` so systemd skips hardware-only targets; sends `STOPSIGNAL SIGRTMIN+3` so `docker stop` triggers an orderly shutdown instead of `SIGTERM`
- Masks 13 units that require hardware, VT consoles, or kernel interfaces unavailable in Docker (`systemd-udevd`, `getty@tty1`, `sys-kernel-debug.mount`, etc.) — prevents spurious `failed` units on every boot
- No host bind-mounts; `--privileged` grants an isolated cgroup namespace, not a shared one — no host-state leakage between runs
- `test/docker/systemd/README.md` documents the `--privileged` requirement, capability table (`CAP_SYS_ADMIN`, `CAP_SYS_PTRACE`, writable cgroup namespace), masked-unit rationale, and the narrower capability set available for hardened environments

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
