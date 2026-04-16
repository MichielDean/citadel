# Cistern Command Reference

## Filtration (Interactive Refinement)

```bash
ct filter --title "..." [--description "..."]              # New refinement session
ct filter --resume <session-id> '<feedback>'              # Continue refinement
ct filter --output-format json                            # Scriptable JSON output
```

Interactive LLM-powered refinement that produces a spec, but does not file droplets. Same model and prompt as `ct droplet add --filter`, but non-destructive and resumable. File each droplet separately using `ct droplet add --title "..." --repo <repo> ...` with `--depends-on` to wire dependencies.

### Context Injection

`ct filter` always prepends codebase context to the filtration prompt, giving the LLM knowledge of:
- Database schema (all tables from `cistern.db`)
- Cataractae INSTRUCTIONS.md files (agent role documentation)
- Relevant `ct` subcommand help output

This helps the LLM reject or refine proposals for functionality that already exists, and avoid suggesting workarounds for present-in-codebase solutions.

## Droplet Management

```bash
ct droplet list                          # All droplets
ct droplet list --status <status>        # Filter: open|in_progress|delivered|pooled
ct droplet list --cancelled              # Show only cancelled droplets (audit purposes)
ct droplet list --repo <repo>            # Filter by repo
ct droplet list --watch                  # Live-refresh every 2 seconds (Ctrl-C to stop)
ct droplet show <id>                     # Full detail
ct droplet stats                         # Show counts by status (flowing, queued, delivered, pooled)
ct droplet add --title "..." --repo <r>  # Add new droplet (direct)
ct droplet add --filter --title "..." --repo <r>  # Add with filtration (LLM-assisted)
ct droplet edit <id>                       # Interactive: open in $EDITOR
ct droplet edit <id> -t "title"            # Edit title
ct droplet edit <id> --description "desc"  # Edit description
ct droplet edit <id> -x critical -p 1     # Edit complexity and priority
ct droplet edit <id> --description -        # Read description from stdin
ct droplet restart <id>                  # Restart from current cataractae
ct droplet restart <id> --cataractae delivery   # Re-enter at a specific cataractae (validated against aqueduct config)
ct droplet restart <id> --cataractae delivery --notes "..."   # Re-enter with a recovery note
ct droplet pool <id>                    # Pool — cannot currently proceed
ct droplet cancel <id> --reason "..."    # Cancel droplet — won't be implemented or no longer needed (reason required)
ct droplet note <id> "..."               # Add a note
ct droplet heartbeat <id>                # Record agent heartbeat (called by agents every 60 seconds)
```

### Tail — Stream Droplet Events

```bash
ct droplet tail <id>                     # Show last 20 status change events and exit
ct droplet tail <id> --follow            # Stream events continuously (like tail -f); exits on terminal state
ct droplet tail <id> --lines 50          # Show last 50 events on start (default: 20)
ct droplet tail <id> --format json        # Output events as NDJSON (one JSON object per line)
ct droplet tail <id> --follow --format json  # Continuous JSON event stream
```

Streams cataractae notes and events (status changes, stage transitions, assignee changes) for a droplet. With `--follow`, polls every 2 seconds and exits automatically when the droplet reaches a terminal state (delivered, pooled, cancelled). Press Ctrl-C to stop following early.

In text mode (default), each line shows: `2026-04-13 14:30:00 [note] reviewer: Found type mismatch in handler`. In JSON mode, each line is a JSON object with `time`, `kind`, and `value` fields.

### Log — Chronological Activity Timeline

```bash
ct droplet log <id>                       # Show chronological activity log for a droplet
ct droplet log <id> --format text         # Tab-aligned table with timestamps (default)
ct droplet log <id> --format json          # One JSON object per line (NDJSON)
```

Displays a structured timeline of events for a droplet, parsed from its notes and change history. Includes creation event, stage transitions, outcome signals (pass/recirculate/pool), scheduler events, and heartbeat records.

In text mode, the output starts with a header line (`Droplet: <id>  Title: <title>  Status: <status>`) followed by a tab-aligned table with columns: `TIME`, `CATARACTAE`, `EVENT`, `DETAIL`. In JSON mode, each line is a JSON object with `time`, `cataractae`, `event`, and `detail` fields.

Events include: `created` (droplet creation), stage transition names, `pooled` (with reason), `heartbeat` (last known heartbeat), and `note` (cataractae-prefixed notes).

### Add Options

| Flag | Values | Default |
|------|--------|---------|
| `--title` | string (required) | — |
| `--repo` | repo name (required) | — |
| `--complexity` | standard / full / critical | full |
| `--priority` | 1–4 (1 = highest) | 2 |
| `--depends-on` | droplet ID (repeatable) | — |
| `--description` | multiline text | — |
| `--filter` | flag — runs LLM filtration pass | off |
| `--yes` | flag — skip confirmation prompts | off |

### `--filter` (Filtration)

Filtration sends the rough title + description through an LLM that:
- Clarifies scope and acceptance criteria
- May split one idea into multiple well-specified droplets
- Sets appropriate complexity and priority

Requires a TTY — run via tmux. Example wrapper pattern:

```bash
cat > /tmp/add-droplet.sh << 'EOF'
#!/bin/bash
export ANTHROPIC_API_KEY=$(cat ~/.cistern/env | grep ANTHROPIC_API_KEY | cut -d= -f2)
export PATH="$HOME/go/bin:$HOME/.local/bin:$PATH"
ct droplet add --repo cistern --filter --title "My idea" --description "Rough description here"
EOF
chmod +x /tmp/add-droplet.sh
tmux new-session -d -s filtration
tmux send-keys -t filtration "/tmp/add-droplet.sh" Enter
```

## Import from External Trackers

```bash
ct import <provider> <issue-key> --repo <repo> [options]
ct import jira PROJ-123 --repo myrepo
ct import jira PROJ-456 --repo myrepo --filter
ct import jira PROJ-789 --repo myrepo --priority 1 --complexity full
```

Import an issue from an external tracker (e.g. Jira) and file it as a droplet. The provider name must match a registered TrackerProvider (e.g. "jira") and have a corresponding entry in the `trackers` section of `cistern.yaml`.

### Import Options

| Flag | Values | Default | Required |
|------|--------|---------|----------|
| `--repo` | repo name | — | Yes |
| `--filter` | flag — runs LLM filtration pass | off | No |
| `--priority` | 1–4 (1 = highest) | Tracker value | No |
| `--complexity` | 1/standard / 2/full / 3/critical | 1 | No |

**Workflow:**
1. Fetches the issue from the tracker using the provided issue key
2. Maps tracker fields (title, description, priority) to droplet fields
3. If `--filter` is set: sends title + description through LLM filtration for refinement (may create multiple droplets)
4. If `--filter` is not set: files the issue directly as a single droplet
5. Prints the created droplet ID(s) on success
6. Sets `external_ref` field to enable bi-directional tracing (e.g., `jira:PROJ-123`)

**Provider Configuration:**

Add a tracker entry to `cistern.yaml`:

```yaml
trackers:
  - name: jira
    base_url: https://your-jira-instance.atlassian.net
    user_env: JIRA_USER         # env var with username/email
    token_env: JIRA_TOKEN       # env var with API token
    priority_map:               # optional: override default priority mapping
      Highest: 1
      High: 1
      Medium: 2
      Low: 3
      Lowest: 3
```

Required environment variables vary by provider. For Jira, set:
```bash
export JIRA_USER=your-email@example.com
export JIRA_TOKEN=your-api-token
```

### Complexity Matrix

| Level | Code | Human Approval Required |
|-------|------|------------------------|
| standard | 1 | No — auto-merges after delivery |
| full | 2 | No — auto-merges after delivery (default) |
| critical | 3 | Yes — pauses for `ct droplet approve <id>` before delivery |

### Droplet Issues (Structured Findings)

Agents file specific findings as structured issues for tracking and resolution:

```bash
ct droplet issue add <id> "specific finding description"          # File a new issue
ct droplet issue list <id>                                        # List all issues
ct droplet issue list <id> --open                                 # List open issues
ct droplet issue list <id> --flagged-by <cataractae-name>        # Issues filed by specific cataractae
ct droplet issue list <id> --open --flagged-by <cataractae-name> # Combine filters
```

**Usage:**
- `ct droplet issue add` files a structured issue that persists and tracks resolution
- `--flagged-by` filters by the cataractae that filed the issue (e.g., `--flagged-by qa`, `--flagged-by reviewer`)
- Use issues for specific, actionable findings that need tracking
- Use `ct droplet note` (below) for narrative summaries only

**Example:**
```bash
ct droplet issue add my-droplet "File system operations need integration test verification"
ct droplet issue list my-droplet --flagged-by qa --open
```

### Droplet Signaling (Terminal Outcomes)

Agents use these commands to signal the outcome of their work. These commands work on both in-progress and pooled droplets, automatically updating status as needed:

```bash
ct droplet pass <id>                     # Work complete — advance to next stage
ct droplet pass <id> --notes "..."       # Pass with optional note

ct droplet recirculate <id>              # Needs revision — send back for rework
ct droplet recirculate <id> --notes "..." # Include feedback/issues
ct droplet recirculate <id> --to <stage> # Recirculate to specific stage

ct droplet pool <id>                    # Pooled — cannot currently proceed
ct droplet pool <id> --notes "..."      # Include reason (e.g., "awaiting API key")

ct droplet cancel <id>                    # Cancel — won't be implemented
ct droplet cancel <id> --reason "..."     # Include reason (e.g., "superseded by X")

ct droplet note <id> "..."               # Add a narrative note (for summaries only)
```

**Status Updates:**
- **In-progress droplets**: Signal commands update the outcome field; Castellarius detects the outcome and routes accordingly
- **Pooled droplets**: Signal commands immediately update the status:
  - `pass` → `status=delivered` (directly closed)
  - `pool` → Sets status to pooled with reason recorded in events
  - `recirculate` → Reopens for the target stage (clears outcome to prevent routing loops)
- **Terminal states** (delivered, cancelled): All signal commands reject with a clear error message — droplets in terminal states cannot be modified

**Distinction:**
- **pool** = Cannot currently proceed. Droplet becomes pooled for recovery.
- **cancel** = Won't be implemented. Droplet is closed; will not dispatch. Use for superseded work, filed-in-error items, or scope out-of-reach.

## Castellarius Daemon

```bash
ct castellarius start
ct castellarius stop
ct castellarius status
ct castellarius restart

# System service
systemctl --user start cistern-castellarius
systemctl --user stop cistern-castellarius
systemctl --user restart cistern-castellarius
systemctl --user status cistern-castellarius
journalctl --user -u cistern-castellarius -f   # Live log tail
cat ~/.cistern/castellarius.log                # Log file
```

### `ct castellarius status` Output

Displays the health and flow of all configured aqueducts:

```
4 of 4 aqueducts flowing

  repo-1 (queue: 2 open, 1 active)
  repo-2 (queue: 0 open, 0 active)
  repo-3 (queue: 5 open, 2 active)
  repo-4 (queue: 0 open, 0 active)

last tick: 5s ago
drought hooks: running (2m)
```

- **Active aqueducts**: Shows how many of your configured aqueducts have a droplet currently flowing
- **Per-repo summaries**: Lists each repo with queue depth (open droplets waiting) and active session count (droplets currently being processed)
- **Last tick**: Time since the Castellarius last completed a full poll cycle
  - `last tick: 5s ago` — Castellarius is healthy and actively polling
  - `last tick: unknown (health file missing)` — Health file not yet written (startup) or removed unexpectedly
- **Drought hooks** *(optional)*: Shows when a drought goroutine is running and how long it has been active
  - `drought hooks: running (2m)` — A drought hook cycle is active (has been running for 2 minutes)
  - *(line omitted)* — No drought goroutine currently active (idle state)

The health file is written atomically to `~/.cistern/castellarius.health` after each poll cycle completes. It includes liveness tracking fields: `droughtRunning` (boolean) and `droughtStartedAt` (RFC3339 timestamp or null).

## Cataractae (Pipeline Stages)

```bash
ct cataractae list               # All stages across all aqueducts
ct cataractae list --aqueduct <name>
ct cataractae generate           # Generate any missing stage configs
ct cataractae render --step <name> [--droplet <id>]  # Preview rendered template for authoring
```

**`ct cataractae generate`** generates configuration files for all cataractae defined in the workflow. For each step, it creates or updates:
- `CLAUDE.md` (or `AGENTS.md`, `GEMINI.md` depending on the configured provider) — the rendered instructions template for the agent
- `PIPELINE_POSITION.md` — documents the step's role, predecessor, and successor in the workflow
- `skills/cataractae-protocol/SKILL.md` — injects the universal behavioral protocol skill (copied from the installed skill)

Run this command after modifying `PERSONA.md`, `INSTRUCTIONS.md`, or the workflow configuration. Missing configurations are skipped gracefully.

**`ct cataractae render`** previews the rendered CLAUDE.md template for a given step, substituting all template variables (step metadata, droplet info, etc.). Useful for authoring and debugging pipeline stage configurations.
Without `--droplet`, uses placeholder values so you can inspect the template structure without a real droplet.

## Aqueducts

```bash
ct aqueduct list                 # All configured aqueducts
ct aqueduct show <name>
```

## Dashboard

### Flow Dashboard (`ct dashboard`)

```bash
ct dashboard                     # Launch flow dashboard in TUI (requires active tmux session)
ct dashboard --web              # HTTP web dashboard on 127.0.0.1:5737
ct dashboard --web --addr 127.0.0.1:8080  # Custom listen address
```

The flow dashboard displays a live view of the aqueduct system with sections:

**Aqueduct Arches** — ASCII art showing configured aqueducts and their status
- For each active aqueduct: displays the progress bar with the droplet's current flow notes below it
  - Shows droplet ID, current step, elapsed time, and title
  - Indicates which cataractae is currently processing the droplet
- Idle aqueducts display as compact single-line rows

**Unassigned** — In-progress droplets with no aqueduct assignment
- Shows orphaned droplets that are stuck in the pipeline (empty assignee or assigned to a removed/renamed aqueduct)
- Lists ID, elapsed time, current step, and title
- When count is zero, this section is omitted from the display

**Cistern** — Queued droplets waiting to enter the flow
- Lists all open droplets not yet started
- Sorted by priority (highest first)

**Pooled** — Droplets that cannot currently proceed
- Shows all droplets with pooled status (cannot proceed)
- Lists ID, time since last state change, and title
- When count is zero, displays "Pooled: 0" as a compact indicator

**Recent Flow** — Recently completed or pooled droplets
- Shows delivered, cancelled, and pooled droplets with timestamps
- Includes the most recent notes from each droplet

**Refresh rate** — Dashboard polls every 2 seconds when droplets are flowing. During idle periods (no active flow and state unchanged), polling backs off to 5 seconds to reduce CPU usage.

### Cockpit (`ct tui`)

```bash
ct tui                           # Launch interactive cockpit (requires active tmux session)
```

The cockpit provides a two-pane interface: persistent left sidebar for module navigation, and a right pane showing the active module's content.

**Cockpit Layout**
- **Left sidebar**: Lists all available modules (Droplets, Dashboard, Status, Castellarius, Inspect, Filter, Doctor) with keyboard shortcuts (1–8)
  - Cursor highlight indicates focus: `▶` = panel focused (green), `▷` = sidebar focused (yellow)
  - Currently, Droplets, Status, Castellarius, Filter, and Doctor modules are fully implemented; others ship as placeholders
- **Right pane**: Displays the active module's content

**Navigation**
- **Sidebar mode**: `↑↓` or `k/j` to navigate, `1–8` to jump to a specific module, `enter`/`tab` to open the module
- **Panel mode**: Module content receives all keyboard input; `esc` returns to sidebar (unless the module has an active overlay)
- **Global**: `q`/`Q` quit (sidebar mode only), `ctrl+c` always quits

**Droplets Module** (the primary implemented panel)

The Droplets module provides three views within the active pane:

**Droplets List (default)**
- Shows all active droplets with ID, status, current step, and title
- Navigate: `↑↓` or `jk` to move cursor
- Open detail: Press `enter` or `d` to view full droplet details

**Detail View**
- **Header**: Droplet ID and title
- **Meta**: Repo name, status (colored: green=in_progress, yellow=open, red=pooled), current pipeline step
- **Pipeline**: Visual indicator of your workflow steps with current step highlighted
  - Example: `implement → **review** → qa → delivery`
- **Issues Section** (collapsible): Inline list of issues filed by cataractae during pipeline execution
  - Shows issue description, filing cataractae, and status (open/resolved)
  - Navigate with `[`/`]` or bracket keys to move cursor between issues
  - Press `v` to resolve the selected issue (opens overlay for evidence), `u` to reject
  - Issues marked as resolved appear with strikethrough text
- **Notes Timeline**: Chronological list of all cataractae notes with timestamps and author attribution
  - Timestamps in local time
  - Multi-line note content with continuation line indentation
  - Scrollable: use `↑↓` or `jk` to scroll, `g` for top, `G` for bottom
  - Press `esc` to return to Droplets list
- Press `p` to peek at the live agent session output for the selected droplet

**Peek View**
- Shows live terminal output captured from the agent session for the currently selected flowing droplet
- Refreshed each tick; displays a placeholder if no flowing droplet is selected or no session is active
- Press `esc` to return to the Detail panel

**Detail View Actions** (dispatch directly without leaving the TUI)
- `r` — **Restart** — Re-enter the pipeline at the start; prompts for optional reason
- `x` — **Cancel** — Mark as cancelled (confirmation required: `y` or `n`)
- `e` — **Pool** — Mark droplet as pooled (confirmation required: `y` or `n`)
- `n` — **Add Note** — Append a manual note to the droplet; enter text and press Enter
- `s` — **Set Step** — Jump to a different pipeline step; enter step name and press Enter

**Structural Actions** (accessible via command palette or keyboard)
- `N` — **Create Droplet** — File a new droplet with sequential form: repo, title, description, complexity
- **Edit Metadata** — Update droplet metadata via sequential form: title, priority, complexity, description (via command palette: `:` then type `editmeta`)
- **Add/Remove Dependencies** — Add or remove droplet dependencies (via command palette: `:` then type `adddep` or `removedep`)
- **File Issue** — File a new issue on the droplet (via command palette: `:` then type `fileissue`)
- **Resolve/Reject Issue** — Resolve or reject a selected issue from the issue list (keyboard: `v` to resolve, `u` to reject; via command palette: `:` then type `resolveissue` or `rejectissue`)

**Command Palette** (while in a panel, `:` opens a searchable overlay)
- Press `:` to open the command palette — a searchable list of all actions available for the currently selected droplet
- Type to filter actions by name (substring match, case-insensitive)
- Navigate with `↑↓` or `jk`, execute the highlighted action with `enter`, dismiss with `esc`
- **Actions available in Droplets module** (vary by droplet status):
  - **Workflow actions** (active droplets): `pass`, `recirculate`, `close`, `cancel`, `pool`, `restart`, `add note`, `approve` (only when human-gated)
  - **Terminal droplets**: `reopen`
  - **Structural actions** (all droplets): `create` (new droplet), `editmeta` (edit title/priority/complexity/description), `adddep` (add dependency), `removedep` (remove dependency), `fileissue` (file issue), `resolveissue` (resolve selected issue), `rejectissue` (reject selected issue)
- Particularly useful when you want to find a specific action without memorizing single-key bindings

All actions execute immediately through the cistern database. After any action completes, the detail view re-fetches and displays updated state.

**Status Module** (key: 3)

The Status module displays real-time system health and pipeline status:

- **Cistern Counts**: Total and per-status droplet counts (in progress, open, pooled)
- **Aqueduct Flow**: Summary of each aqueduct with active/queued droplet counts and current step breakdown
- **Castellarius Health**: Daemon status, last poll time, and scheduler liveness

The status view auto-refreshes every 5 seconds. When the display hasn't changed for 2+ cycles, it backs off to a 30-second refresh interval to reduce database load. Press `r` at any time to force an immediate refresh.

**Castellarius Module** (key: 4)

The Castellarius module provides direct control of the Castellarius daemon (the aqueduct scheduler) from the cockpit:

- **Status View**: Displays live output from `ct castellarius status`, showing aqueduct flow, queue depth, and scheduler health
- **Auto-refresh**: Status updates automatically every 5 seconds
- **Scroll controls**: Use `↑↓` or `j/k` to navigate the status output
- **Manual refresh**: Press `r` to force an immediate refresh
- **Daemon control**: Start, stop, and restart the Castellarius daemon via command palette actions with confirmation overlays
  - Confirmation prompt: Press `y` to confirm action, `n` or `esc` to cancel
  - Actions automatically refresh status after completion

**Doctor Module** (key: 5)

The Doctor module runs `ct doctor` on activation and displays system health and configuration checks in a scrollable pane:

- **Credentials & Auth**: Claude OAuth token, API key fallback, provider binary availability, and required environment variables
- **Configuration**: Agent instruction files, installed skills, aqueduct YAML validity
- **Runtime Health**: Castellarius daemon status, scheduler liveness, stalled droplet warnings

The module shows the last-run timestamp and supports the following controls:
- `r` — **Re-run** — Execute `ct doctor` again (disabled while a run is in progress)
- `↑↓` or `j/k` — **Scroll** — Navigate through the output
- `g` — **Go to top** — Jump to the beginning
- `G` — **Go to bottom** — Jump to the end

**Repos & Skills Module** (key: 7)

The Repos & Skills module displays registered repositories and installed skills in a read-only view:

- **Repositories Section**: Lists configured repos (from `ct repo list`) with columns:
  - NAME: Repository identifier
  - PREFIX: Droplet ID prefix for repos
  - URL: Repository URL
  - Shows "No repositories configured" if empty; run `ct repo add --url <url>` to add repos

- **Skills Section**: Lists installed skills (from `ct skills list`) with columns:
  - NAME: Skill identifier
  - SOURCE: Source URL where the skill was installed from
  - Shows "No skills installed" if empty; run `ct skills install <name> <url>` to install skills

- **Navigation**: `↑↓` or `jk` to scroll, `g` to jump to top, `G` to jump to bottom, `r` to refresh
- **Auto-refresh**: Displays fetch timestamp and supports `r` to force immediate refresh

**Filter Module** (key: 8)

The Filter module provides an interactive multi-turn conversation for refining ideas and specifications. It's a thinking tool, not a filing tool — use it to clarify concepts before creating formal droplets with `ct droplet add`.

**First Use (Initial Setup)**
- On first activation, a single text box appears
- Enter your idea in the format: `title\ndetailed context`
  - First line becomes the session title
  - Remaining lines are the initial context
- Press `enter` to submit and begin the conversation

**Conversation View**
- **Message history**: Displays alternating user (you) and LLM (Claude) messages in a scrollable pane
- **Text input**: Single-line input at the bottom; press `enter` to submit
- **Session indicator**: Current session ID shown in the header (maintained for `--resume` across sessions)
- **Submission feedback**: Brief spinner displays during processing; full LLM response renders when complete (no streaming)

**Controls**
- `enter` — **Submit** — Send your message and receive a response
- `n` — **New Session** — Start a fresh conversation, clearing history and session ID
- `↑↓` or `j/k` — **Scroll history** — Navigate through past messages
- `esc` — **Return to sidebar** — Close the filter panel

**Session Persistence**
- Each session receives a unique ID from the Claude agent
- Session ID is displayed at the top of the conversation
- Use `ct filter --resume <session-id>` in the terminal to reconnect to a previous session and continue the conversation

**Example Workflow**
1. Open Filter module (press `8`)
2. Enter: `Authorization refactor\nWe need to standardize auth across services. Currently using JWT in some, basic auth in others. SAML requirements coming Q3.`
3. Have a multi-turn conversation to refine the spec
4. When ready, use `ct droplet add` from the terminal to file a formal work item based on the session output

## Status & Health

```bash
ct status                        # High-level pipeline health
ct doctor                        # Check system health and configuration
ct doctor --fix                  # Auto-repair common issues (credentials, permissions)
ct doctor --skills               # List all skills referenced by any aqueduct and their install status
```

### `ct doctor` Checks

Verifies your Cistern installation is functional. Runs several categories of checks:

**With `--skills`:**
Lists every skill referenced by any aqueduct across all configured repos and reports whether each is installed at `~/.cistern/skills/<name>/`. Shows a table with skill name, install status (✓ installed / ✗ missing), and which cataractae use each skill. Replaces the normal doctor check suite when set.

**Credentials & Auth:**
- Claude OAuth token (auto-refresh via `--fix` if expired)
- API key fallback (`ANTHROPIC_API_KEY` in `~/.cistern/env`)
- Provider binary availability for configured providers
- Required env vars for each provider in `~/.cistern/env`

**Configuration:**
- Agent instruction files (`CLAUDE.md`, `AGENTS.md`, etc.) for each role in workflow
- Skills installed at `~/.cistern/skills/<name>/`
- Aqueduct YAML validity and configuration consistency

**Runtime Health:**
- Castellarius daemon status and scheduler liveness via health file
- **Scheduler staleness**: warns if last poll cycle is too old (may indicate hung scheduler)
- **Drought goroutine hung**: warns if drought hooks have been running > 10 minutes
- Systemd service health (on systemd systems only)
- Stalled droplets (informational warnings, does not fail the check)

**With `--fix`:**
Automatically repairs: missing agent files, credential token refresh, permissions, service enablement.

## Config

Default config: `~/.cistern/cistern.yaml`
Default DB: `~/.cistern/cistern.db`
Credentials: `~/.cistern/env` (chmod 600)
