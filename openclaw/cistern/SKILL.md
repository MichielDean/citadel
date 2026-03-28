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

Never say "drop/item/task/ticket/issue" for work units — always **droplet**.

## Installation

- **CLI:** `~/go/bin/ct`
- **Config:** `~/.cistern/cistern.yaml`
- **DB:** `~/.cistern/cistern.db`
- **Log:** `~/.cistern/castellarius.log`
- **Dashboard:** `http://192.168.0.138:5737` (ttyd)
- **Build:** `cd ~/cistern && PATH="/usr/local/go/bin:$PATH" go build -o ~/go/bin/ct ./cmd/ct/`

## Repos configured

| Repo | Prefix | Aqueducts |
|------|--------|-----------|
| cistern | `ci-` | virgo, marcia |
| ScaledTest | `st-` | julia, appia |
| PortfolioWebsite | `pw-` | anio |

## Worktree Rule

**Never edit `~/cistern` directly.** That's the primary clone — touching it corrupts all agent worktrees.

All manual Cistern work goes in the dedicated lobsterdog worktree:
```bash
cd ~/.cistern/sandboxes/cistern/lobsterdog
git checkout -B lobsterdog-work origin/main   # Always sync before starting
```
ScaledTest worktree: `~/.cistern/sandboxes/ScaledTest/lobsterdog`

## Pipeline

```
implement → review → qa → security-review → docs → delivery
```

| Complexity | Code | Notes |
|------------|------|-------|
| standard | 1 | default minimum — always includes review |
| full | 2 | all stages |
| critical | 3 | all stages + human approval before merge |

## Adding a Droplet

**⛔ Always get explicit confirmation before filing any droplet.**

### Direct — when requirements are already clear:
```bash
export ANTHROPIC_API_KEY=$(pass anthropic/claude)
ct droplet add \
  --title "Short imperative description" \
  --repo <repo-name> \
  --complexity standard \
  --description "What, why, acceptance criteria" \
  --yes
```

### Filtered — for non-trivial or exploratory work:

Filtration is a **conversation**, not a batch pass.

**Step 1 — Start:**
```bash
export ANTHROPIC_API_KEY=$(pass anthropic/claude)
ct filter --repo <repo> --title "Rough idea" --description "Intent..."
```

**Step 2 — Resume with answers:**
```bash
ct filter --resume <session-id> "answers and context..."
```

**Step 3 — File after confirmation:**
```bash
ct filter --resume <session-id> --file --repo <repo>
```

**Rules:**
- Never use `ct droplet add --filter` — fires-and-forgets, no conversation
- Minimum 3 rounds. Keep going past 3 until the spec is unambiguous — every cataracta (implement, reviewer, QA, delivery) should be able to read CONTEXT.md and have the same understanding of what needs to change, with no guessing about scope, file locations, or acceptance criteria. Stop when the spec is concrete, not when the count hits a number.
- After each session, give a recommendation: ready to file, or needs more passes? Say why.
- Report a single summary of improvements across all rounds, not one message per round
- Get explicit "yes" before `--file`
- File follow-up droplets with `--depends-on <id>` rather than injecting notes into flowing work

## Key Commands

```bash
# Status
ct status
ct droplet list
ct droplet list --repo <repo>
ct droplet show <id>

# Manage
ct droplet restart <id>
ct droplet escalate <id>
ct droplet cancel <id>
ct droplet note <id> "..."
ct droplet deps <id> --add <dep-id>

# Daemon
ct castellarius start/status
journalctl --user -u cistern-castellarius -f
systemctl --user restart cistern-castellarius

# Cataractae
ct cataractae list
ct cataractae generate

# Dashboard (reload after rebuild)
kill $(ss -tlnp | grep 5737 | grep -o 'pid=[0-9]*' | cut -d= -f2) 2>/dev/null
systemctl --user start cistern-ttyd.service
```

## Infrastructure

- Castellarius: systemd user service `cistern-castellarius.service` (Restart=always)
- Auth: Claude CLI manages its own credentials via `~/.claude/.credentials.json` — no ANTHROPIC_API_KEY env var needed in service
- `start-castellarius.sh` just runs `exec ct castellarius start` — no credential setup
- ttyd dashboard: port 5737, managed by `cistern-ttyd.service`
- Self-restart: git_sync drought hook + binary mtime detection → os.Exit(0) → systemd restarts

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Castellarius not running | `systemctl --user status cistern-castellarius` → start it |
| Sessions failing auth | `claude auth status` — if that shows logged in, Claude's own auth is fine. Check nothing is setting ANTHROPIC_API_KEY in the env |
| Droplet stuck | `ct droplet show <id>` — check notes; `ct droplet restart <id>` |
| Logs | `journalctl --user -u cistern-castellarius -f` or `cat ~/.cistern/castellarius.log` |
| Dashboard stale after rebuild | Kill old process on port 5737, restart cistern-ttyd.service |
| Binary out of date | Rebuild: `cd ~/cistern && PATH="/usr/local/go/bin:$PATH" go build -o ~/go/bin/ct ./cmd/ct/` |
