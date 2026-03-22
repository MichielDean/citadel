# Changelog

## Unreleased

### ct status: --watch flag for auto-refresh (ci-drisq)
- `ct status --watch` continuously refreshes the status display every 5 seconds (Ctrl-C to stop)
- `--interval N` sets the refresh interval in seconds (default 5, minimum 1)
- Outside watch mode, behaviour is unchanged

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
