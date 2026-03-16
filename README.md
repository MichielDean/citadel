# Cistern

> Water is life. Code is water. The Cistern keeps it moving.

Cistern is an agentic workflow orchestrator built around a water metaphor. Droplets of work enter the cistern, flow through an aqueduct tended by named cataracta operators, are refined by adversarial reviewers and quality gatekeepers, and what flows free at the other end is clean enough to ship.

## The Vocabulary

| Term | Meaning |
|---|---|
| **Droplet** | A unit of work — one issue, one feature, one fix. The atomic thing that flows. |
| **Filtration** | Optional LLM refinement step. Refine a raw idea before it enters the Cistern. |
| **Cistern** | The reservoir. Droplets queue here waiting to flow into the aqueduct. |
| **Drought** | Idle state. The cistern is dry. Drought protocols run maintenance automatically. A drought may also be a forced maintenance window where processing is stopped. |
| **Aqueduct** | The full pipeline — from intake through cataracta gates to delivery. |
| **Castellarius** | The overseer. Watches all aqueducts, routes droplets into aqueducts, runs drought protocols. External to the cistern — pure state machine, no AI. |
| **Cataracta** | A gate along the aqueduct. Each cataracta implements, inspects, or diverts (LLMs working). |
| **Recirculate** | Send a droplet back to a previous cataracta for further processing — revision from reviewer or QA. |
| **Flows free** | A droplet that made it: PR merged, delivered. |
| **Stagnant** | A droplet that can't flow without human intervention. |
||

Cistern

![Cistern](Cistern.png)

## Quick Start

```bash
# Install
curl -sSL https://raw.githubusercontent.com/MichielDean/cistern/main/install.sh | bash

# Initialize — creates ~/.cistern/cistern.yaml and default aqueduct files
ct init

# Add a droplet to the cistern
ct droplet add --title "Add retry logic to fetch" --repo myproject

# Open the aqueducts
ct flow start

# Watch the water flow
ct flow status

# See what's in the cistern
ct droplet list
```

## How It Works

Every droplet flows through a sequence of cataractae:

```
Intake → Filtration (optional) → Implement → Inspect → QA → PR opens → CI gate → Flows free
```

1. **Implement** (`implement`) — The Implementer cataracta reads the droplet, writes tests first (TDD/BDD), implements, commits. No outcome until tests pass.

2. **Inspect** (`adversarial-review`) — The Adversarial Reviewer cataracta receives *only the diff*. No codebase access, no author context. Finds problems: bugs, security holes, missing tests, logic errors. Context isolation is enforced at the infrastructure level.

3. **QA** (`qa`) — The QA cataracta checks test quality, not just whether tests pass. Finds test gaps, weak assertions, missing error paths, coverage theater. Recirculates to implement on revision.

4. **Automated cataractae** — PR opens via `gh pr create`, CI runs and must pass, `gh pr merge` fires. Droplet delivered.

5. **Recirculation** — Revision sends the droplet back upstream to a prior cataracta for another pass. No retry limits. The water flows until it's pure.

## Cataracta Operators

Operators are named from a pool. The default pool is water-themed:

```
upstream, downstream, tributary, confluence, headwater,
seep, spring, torrent, cascade, eddy
```

Each tmux session is named `<operator>-<droplet-id>`. Every `tmux ls` shows the aqueduct in motion:

```
upstream-ct-x7k: 1 windows (adversarial-review)
tributary-ct-m3j: 1 windows (implement)
```

Change names in `~/.cistern/cistern.yaml` under `names:`.

## Customizing Cataracta Definitions

Cataracta definitions are stored in your aqueduct YAML — they're yours to edit. Cistern adapts.

```bash
ct cataractae list                  # See all cataracta definitions and how to edit them
ct cataractae edit implementer      # Open in $EDITOR, save, CLAUDE.md regenerates
ct cataractae reset qa              # Restore to built-in default (with confirmation)
ct cataractae generate              # Regenerate all CLAUDE.md files from YAML
```

Cataracta content lives in `~/.cistern/aqueduct/feature.yaml` under the `cataracta_definitions:` key. CLAUDE.md files are generated artifacts — the YAML is the source of truth.

## Drought Protocols

When the cistern is dry, Cistern runs maintenance automatically. Configure in `~/.cistern/cistern.yaml`:

```yaml
# Drought protocols — run when Cistern is idle
drought_hooks:
  - name: sync-cataractae
    action: cataractae_generate   # Regenerate cataracta files when YAML is newer

  - name: prune-worktrees
    action: worktree_prune     # Prune stale aqueduct registrations

  # - name: vacuum-cistern
  #   action: db_vacuum        # Compact the cistern database

  # - name: custom
  #   action: shell
  #   command: "echo $(date): cistern dry >> ~/.cistern/drought.log"
```

Protocols fire once on the `flowing → idle` transition, not on every tick. Safe to add your own.

## Installation

```bash
curl -sSL https://raw.githubusercontent.com/MichielDean/cistern/main/install.sh | bash
```

Requirements:
- Go 1.21+
- `claude` CLI with OAuth login (`claude login`)
- `gh` CLI authenticated (`gh auth login`)
- `git`, `tmux`

## Configuration

```bash
ct init                        # Create ~/.cistern/ with default config and aqueduct files
ct flow config validate        # Check config and all aqueduct files
ct doctor                      # Full health check
```

Config lives at `~/.cistern/cistern.yaml`. See `cistern.yaml` for all options.

## CLI Reference

```
ct flow start                  Open the aqueducts (start processing)
ct flow status                 Show cataractae and cistern state
ct flow config validate        Validate config

ct droplet add --title "..." --repo myproject   Add a droplet
ct droplet list                                 List droplets
ct droplet show <id>                            Show droplet details
ct droplet close <id>                           Mark flows free
ct droplet reopen <id>                          Return to cistern
ct droplet purge --older-than 30d               Drain old droplets
ct droplet escalate <id> --reason "..."         Mark a droplet stagnant

ct cataractae list                List cataracta definitions with edit hints
ct cataractae edit <cataracta>       Edit cataracta definition in $EDITOR
ct cataractae generate            Regenerate CLAUDE.md files from YAML
ct cataractae reset <cataracta>      Restore cataracta definition to built-in default

ct doctor                      Health check
ct version                     Version info
```

---
