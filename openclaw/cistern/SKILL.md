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
export ANTHROPIC_API_KEY=$(pass anthropic/claude)
ct droplet add \
  --title "Short imperative description" \
  --repo <repo-name> \
  --complexity standard \
  --description "What, why, acceptance criteria" \
  --yes
```

**Filtered** — when the idea is rough or complex. Filtration is a **conversation**, not a batch pass. The filtering agent reads the idea, asks clarifying questions, and iterates with you until the spec is tight.

### Filtration workflow (always use this for non-trivial droplets)

**Step 1 — Start a filter session:**
```bash
export ANTHROPIC_API_KEY=$(pass anthropic/claude)
ct filter --repo <repo> --title "Rough idea" --description "Intent..."
```
This prints a refined draft + a `session_id`. The agent may ask clarifying questions in its output.

**Step 2 — Resume with answers/context:**
```bash
ct filter --resume <session-id> "Here are my answers: ..."
```
Continue until the spec feels complete. Typically 2-4 rounds.

**Step 3 — File when satisfied:**
```bash
ct filter --resume <session-id> --file --repo <repo>
```
This files the final version. Only run this after confirming with the user.

**Rules:**
- Never use `ct droplet add --filter` — that fires-and-forgets with no conversation
- Always show the user a **single summary** of how the description improved across iterations — not a message per round
- Get explicit user confirmation before running `--file`
- Minimum 3 iterations unless the user says it's ready sooner

**When to use filtration:**
- The idea spans multiple files or concerns
- The description is intent, not a spec
- The user says "plan this out" or "figure out what we need"

**When to file directly:**
- Requirements are already fully specified
- It's a small, well-understood fix (typo, config change)

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
implement → simplify → review → qa → security-review → docs → delivery
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
