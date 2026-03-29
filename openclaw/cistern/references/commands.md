# Cistern Command Reference

## Filtration (Interactive Refinement)

```bash
ct filter --title "..." [--description "..."]              # New refinement session
ct filter --resume <session-id> '<feedback>'              # Continue refinement
ct filter --resume <session-id> --file --repo <repo>      # Persist final result
ct filter --output-format json                            # Scriptable JSON output
```

Interactive LLM-powered refinement **without persisting** until you're ready. Same model and prompt as `ct droplet add --filter`, but non-destructive and resumable.

## Droplet Management

```bash
ct droplet list                          # All droplets
ct droplet list --status <status>        # Filter: open|in_progress|delivered|stagnant
ct droplet list --cancelled              # Show only cancelled droplets (audit purposes)
ct droplet list --repo <repo>            # Filter by repo
ct droplet list --watch                  # Live-refresh every 2 seconds (Ctrl-C to stop)
ct droplet show <id>                     # Full detail
ct droplet stats                         # Show counts by status (flowing, queued, delivered, stagnant)
ct droplet add --title "..." --repo <r>  # Add new droplet (direct)
ct droplet add --filter --title "..." --repo <r>  # Add with filtration (LLM-assisted)
ct droplet restart <id>                  # Retry failed droplet
ct droplet escalate <id>                 # Bump priority
ct droplet cancel <id>                   # Cancel droplet — won't be implemented or no longer needed
ct droplet note <id> "..."               # Add a note
```

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

### Complexity Matrix

| Level | Code | Stages skipped |
|-------|------|---------------|
| standard | 1 | review, qa |
| full | 2 | none (default) |
| critical | 3 | none + human approval required |

### Droplet Signaling (Terminal Outcomes)

Agents use these commands to signal the outcome of their work:

```bash
ct droplet pass <id>                     # Work complete — advance to next stage
ct droplet pass <id> --notes "..."       # Pass with optional note

ct droplet recirculate <id>              # Needs revision — send back for rework
ct droplet recirculate <id> --notes "..." # Include feedback/issues
ct droplet recirculate <id> --to <stage> # Recirculate to specific stage

ct droplet block <id>                    # Blocked — waiting on external dependency
ct droplet block <id> --notes "..."      # Include reason (e.g., "awaiting API key")

ct droplet cancel <id>                   # Cancel — won't be implemented
ct droplet cancel <id> --notes "..."     # Include reason (e.g., "superseded by X")
```

**Distinction:**
- **block** = Waiting on external dependency, cannot proceed. Droplet will retry when unblocked.
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
```

## Aqueducts

```bash
ct aqueduct list                 # All configured aqueducts
ct aqueduct show <name>
```

## Dashboard

```bash
ct dashboard                     # Launch TUI (requires active tmux session)
```

Web dashboard (if configured): `http://<host>:5737`

### TUI Dashboard

The terminal UI dashboard (`ct dashboard`) provides two views:

**Droplets List (default)**
- Shows all active droplets with ID, status, current step, and title
- Navigate: `↑↓` or `jk` to move cursor, `q` to quit
- Open detail: Press `enter` or `d` to view full droplet details

**Detail View**
- **Header**: Droplet ID and title
- **Meta**: Repo name, status (colored: green=in_progress, yellow=open, red=stagnant), current pipeline step
- **Pipeline**: Visual indicator of your workflow steps with current step highlighted
  - Example: `implement → **review** → qa → delivery`
- **Notes Timeline**: Chronological list of all cataractae notes with timestamps and author attribution
  - Timestamps in local time
  - Multi-line note content with continuation line indentation
  - Scrollable: use `↑↓` or `jk` to scroll, `g` for top, `G` for bottom
  - Press `esc` to return to Droplets list

## Status & Health

```bash
ct status                        # High-level pipeline health
ct doctor                        # Check system health and configuration
ct doctor --fix                  # Auto-repair common issues (credentials, permissions)
```

### `ct doctor` Checks

Verifies your Cistern installation is functional. Runs several categories of checks:

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
