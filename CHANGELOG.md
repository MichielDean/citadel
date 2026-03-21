# Changelog

## Unreleased

### Sandbox worktree optimization (ci-6al33)
- **Reduced disk cost**: N aqueducts per repo now share a single primary clone object store. Each aqueduct gets a lightweight git worktree (~4.7 MB working tree) instead of a full independent clone (~16 MB). At 100 aqueducts this drops sandbox disk cost from ~1.6 GB to ~490 MB.
- Primary clone lives at `~/.cistern/sandboxes/<repo>/_primary/`. Aqueduct worktrees remain at `~/.cistern/sandboxes/<repo>/<aqueduct>/` — same paths as before, no migration required.
- On startup, stale worktree registrations are pruned automatically before adding new ones, preventing `already in use` errors after unexpected exits.
- Legacy dedicated clones at aqueduct paths are automatically replaced by worktrees on next startup.
- Branch lifecycle is now owned by the Castellarius: feature branches (`feat/<id>`) are created and cleaned up by the scheduler, not the runner. Agents do not manage branches directly.
- Non-terminal routes (pass to next step, recirculate) preserve the feature branch so the next cycle can resume incrementally. Terminal routes (deliver, block, escalate) clean up the branch.

### Web dashboard: responsive CSS arch diagram (ci-jvgk7)
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
