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
| `--complexity` | trivial / standard / full / critical | full |
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
| trivial | 1 | review, qa |
| standard | 2 | qa |
| full | 3 | none (default) |
| critical | 4 | none + human approval required |

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

## Status & Health

```bash
ct status                        # High-level pipeline health
ct doctor                        # Check prereqs, credentials, service env
ct doctor --fix                  # Auto-repair common issues
```

## Config

Default config: `~/.cistern/cistern.yaml`
Default DB: `~/.cistern/cistern.db`
Credentials: `~/.cistern/env` (chmod 600)
