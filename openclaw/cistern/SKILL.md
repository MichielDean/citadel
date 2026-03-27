---
name: cistern
description: Manage a local Cistern installation — an agentic workflow orchestrator that routes work through LLM-powered pipelines. Use when the user wants to: (1) add, view, or manage droplets (units of work), (2) check pipeline status or aqueduct health, (3) start or restart the Castellarius daemon, (4) view or interact with the dashboard, (5) troubleshoot stuck or failed work, (6) understand Cistern's pipeline stages or vocabulary. Triggers on: "add droplet", "cistern status", "ct status", "ct droplet", "castellarius", "aqueduct", "cataractae", "check the pipeline", or any question about Cistern.
metadata: {"openclaw": {"requires": {"bins": ["ct"]}, "emoji": "🏛️"}}
---

# Cistern

Cistern is an agentic workflow orchestrator. It routes units of work called **droplets** through configurable pipelines called **aqueducts**, where each stage is a **cataractae** handled by an LLM-powered agent.

## Vocabulary

| Term | Meaning |
|------|---------|
| **Droplet** | Atomic unit of work — always say "droplet", never "task/item/ticket" |
| **Cistern** | The reservoir — droplets queue here before processing |
| **Aqueduct** | Named pipeline (e.g., `virgo`, `marcia`, `julia`, `appia`) |
| **Cataractae** | A stage within an aqueduct (implement → review → qa → delivery) |
| **Castellarius** | The overseer daemon — routes droplets, manages pipelines |
| **Recirculate** | Send a droplet back for revision |
| **Drought** | Idle state — maintenance hooks run here |
| **Filtration** | LLM refinement that sharpens a rough idea into well-specified droplets before they enter the pipeline |

## Adding a Droplet

**Always get the user's confirmation before filing any droplet.**

### Two modes: direct or filtered

**Direct** — when requirements are already clear and well-specified:
```bash
ct droplet add \
  --title "Short imperative description" \
  --repo <repo-name> \
  --complexity standard \
  --priority 2 \
  --description "What, why, acceptance criteria"
```

**Filtered** — when the idea is rough, vague, or complex enough to benefit from LLM decomposition. Filtration calls the LLM, clarifies scope, and may split the idea into multiple well-specified droplets:
```bash
ct droplet add \
  --repo <repo-name> \
  --filter \
  --title "Rough idea title" \
  --description "Rough description of what you want"
```

Filtration requires a TTY. Run it in a tmux session:
```bash
tmux new-session -d -s filtration
tmux send-keys -t filtration "ANTHROPIC_API_KEY=\$(cat ~/.cistern/env | grep ANTHROPIC_API_KEY | cut -d= -f2) PATH=\$HOME/go/bin:\$HOME/.local/bin:\$PATH ct droplet add --repo <repo> --filter --title '...' --description '...'" Enter
# Then watch: tmux attach -t filtration
```

Or write the command to a script and run it in tmux to avoid shell quoting issues with multiline descriptions.

### `ct filter` — Non-Persistent Refinement

If you want to **iterate and refine an idea without immediately filing a droplet**, use `ct filter` instead:

```bash
# Start a refinement session
ct filter --title "Rough idea" --description "Some context"

# Continue refining (copy the session-id from output)
ct filter --resume <session-id> "Here's my feedback..."

# When ready, persist the final result
ct filter --resume <session-id> --file --repo <repo>
```

This lets you **converge on a good idea iteratively** before committing anything to the pipeline. When you use `--file`, the refined title and description become a real droplet.

For scripting, use `--output-format json` to get structured output (session_id + proposals).

**When to use `ct filter` vs `ct droplet add --filter`:**
- Use `ct filter` when you want to **iterate safely** without filing a droplet yet
- Use `ct droplet add --filter` when you're ready to **file immediately after refinement**

**When to use filtration:**
- The idea is exploratory or spans multiple concerns
- You're not sure of the right complexity or decomposition
- The description is a few sentences of intent, not a spec
- The user says something like "plan this out" or "figure out what we need"

**When to file directly:**
- Requirements are already clear and specific
- It's a small, well-understood fix
- The user already described it in detail

### Complexity

| Level | Code | Stages skipped |
|-------|------|---------------|
| trivial | 1 | review, qa — fast lane for obvious fixes |
| standard | 2 | qa |
| full | 3 | all stages — default |
| critical | 4 | all stages + human approval before merge |

## Key Commands

```bash
# Status
ct status                        # Pipeline overview
ct droplet list                  # All droplets
ct droplet list --status pending # Filter by status
ct droplet list --repo <repo>    # Filter by repo
ct droplet show <id>             # Detail view

# Manage flowing work
ct droplet restart <id>          # Retry a failed droplet
ct droplet escalate <id>         # Bump priority
ct droplet cancel <id>           # Cancel — won't be implemented
ct droplet note <id> "..."       # Add a note to a droplet

# Daemon control
ct castellarius start
ct castellarius status
journalctl --user -u cistern-castellarius -f   # Live logs
cat ~/.cistern/castellarius.log                # Log file

# Cataractae
ct cataractae list               # List all stages
ct cataractae generate           # Generate missing stage configs

# Dashboard
ct dashboard                     # Live TUI (requires tmux)
```

See [references/commands.md](references/commands.md) for the full command reference.

## Pipeline

```
implement → simplify → adversarial-review → qa → security-review → docs → delivery
```

Castellarius routes each droplet through the stages configured for its aqueduct. Completed droplets move to the next stage automatically; recirculated ones go back for revision.

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Castellarius not running | `systemctl --user status cistern-castellarius` → `systemctl --user start cistern-castellarius` |
| Sessions crashing immediately | Token mismatch — check `~/.cistern/env` has valid `ANTHROPIC_API_KEY`; run `claude -p "hi"` with that key to verify |
| Droplet stuck looping | `ct droplet show <id>` — check notes for dispatch-loop recovery messages |
| Logs for a failed stage | `journalctl --user -u cistern-castellarius -f` |
| Binary out of date | `ct update` or rebuild: see [references/setup.md](references/setup.md) |

See [references/troubleshooting.md](references/troubleshooting.md) for detailed recovery workflows.

## Worktree Rule

**Never edit `~/cistern` directly.** That's the primary clone — touching it corrupts all agent worktrees.

All manual work goes in the dedicated lobsterdog worktree:
```bash
cd ~/.cistern/sandboxes/cistern/lobsterdog
git checkout -B lobsterdog-work origin/main   # Sync before starting
```
