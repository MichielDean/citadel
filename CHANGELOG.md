# Changelog

## Unreleased

### Web dashboard
- `ct dashboard --web` starts a Go HTTP server on port 5737 (no ttyd, no terminal emulator required)
- `GET /api/dashboard` returns `DashboardData` as JSON
- Server-sent events (SSE) at `/api/dashboard/stream` push live updates every 2 seconds
- Responsive HTML/CSS UI: aqueduct arch diagram, CURRENT FLOW section, CISTERN queue, footer
- Dark/light theme following OS preference; readable at 375px width (mobile-friendly)
- `--addr` flag sets listen address (default `:5737`)
- `cistern-web.service` systemd user service starts the web dashboard automatically
- TUI dashboard (`ct dashboard` without `--web`) continues to work unchanged

### Peek panel (web dashboard)
- Click any active aqueduct arch to open a live read-only peek panel showing the agent's tmux session output
- Panel is clearly labelled **Observing — read only**; no keyboard input is forwarded
- Auto-scrolls to bottom; click **pin scroll** to lock the scroll position
- `GET /api/aqueducts/{name}/peek` — snapshot of current pane content as plain text; `?lines=N` sets capture depth (default 100)
- `WS /ws/aqueducts/{name}/peek` — WebSocket stream; polls tmux every 500 ms and sends diffs to the client
- Graceful fallback: panel shows "session not active" when the aqueduct is idle or the tmux session is not found

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
