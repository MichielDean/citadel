<p align="center">
  <img src="cistern_logo.png" alt="Cistern Logo" />
</p>

Cistern is an agentic delivery system built around a water metaphor. Droplets of work enter the cistern, flow through named aqueducts cataracta by cataracta, and what emerges at the other end is clean enough to ship.

## The Vocabulary

| Term | Meaning |
|---|---|
| **Droplet** | A unit of work — one issue, one feature, one fix. The atomic thing that flows. |
| **Filtration** | Optional LLM refinement step. Refine a raw idea before it enters the Cistern. |
| **Cistern** | The reservoir. Droplets queue here waiting to flow into the aqueduct. |
| **Drought** | Idle state. The cistern is dry. Drought protocols run maintenance automatically. A drought may also be a forced maintenance window where processing is stopped. |
| **Aqueduct** | The full pipeline — from intake through cataracta gates to delivery. Named aqueducts (e.g. virgo, marcia) are independent instances the Castellarius routes droplets into. |
| **Castellarius** | The overseer. Watches all aqueducts, routes droplets into aqueducts, runs drought protocols. External to the cistern — pure state machine, no AI. |
| **Cataracta** | A gate along the aqueduct. Each cataracta implements, reviews, or diverts (LLMs working). |
| **Recirculate** | Send a droplet back to a previous cataracta for further processing — revision from reviewer or QA. |
| **Delivered** | A droplet that made it: PR merged, delivered. |
| **Stagnant** | A droplet that can't flow without human intervention. |

![Cistern](Cistern.png)

## Quick Start

```bash
# Install
curl -sSL https://raw.githubusercontent.com/MichielDean/cistern/main/install.sh | bash

# Initialize — creates ~/.cistern/cistern.yaml and default aqueduct files
ct init

# Add a droplet to the cistern
ct droplet add --title "Add retry logic to fetch" --repo myproject

# Wake the Castellarius — he watches the cistern and routes droplets automatically
ct castellarius start

# After rebuilding ct (go build), restart the Castellarius to pick up changes:
# ct binary changes → restart required (long-running process uses old binary)
# feature.yaml / CLAUDE.md / skills changes → no restart (read per spawn)

# See the overall picture
ct status

# See what's in the cistern
ct droplet list

# Watch the live flow-graph dashboard
ct dashboard
```

## How It Works

Every droplet flows through a sequence of cataractae:

```
Filtration (optional) → implement → adversarial-review → qa → delivery → done
```

1. **Implement** (`implement`) — The Implementer cataracta reads the droplet, writes tests first (TDD/BDD), implements, commits. No outcome until tests pass.

2. **Adversarial Review** (`adversarial-review`) — The Adversarial Reviewer cataracta receives *only the diff*. No codebase access, no author context. Finds problems: bugs, security holes, missing tests, logic errors. Context isolation is enforced at the infrastructure level. Files structured issues via `ct droplet issue add`.

3. **QA** (`qa`) — The QA cataracta checks test quality, not just whether tests pass. Finds test gaps, weak assertions, missing error paths, coverage theater. Recirculates to implement on revision.

4. **Delivery** (`delivery`) — The Delivery cataracta owns all git operations: stash, rebase, PR creation, CI monitoring, PR review response, and merge. One agent cataracta handles the full branch-to-merged lifecycle.

5. **Recirculation** — Revision sends the droplet back upstream to a prior cataracta for another pass. No retry limits. The water flows until it's pure.

## Two-Phase Review

The adversarial-review step uses a structured two-phase protocol that prevents reviewer anchoring and ensures prior issues are actually fixed.

**Phase 1 — Verify prior issues.** If the droplet has been recirculated, the reviewer checks each previously filed issue first: mark it `RESOLVED` with evidence (test name, line number) or `UNRESOLVED` with the gap. The reviewer cannot skip to fresh review until all prior issues are assessed.

**Phase 2 — Fresh review.** After verifying prior work, the reviewer performs a clean-slate review of the diff. New findings are filed as structured issues via `ct droplet issue add`.

This protocol prevents common failure modes: rubber-stamping recirculations, anchoring on prior notes, or missing regressions introduced during fixes.

## Issue Tracking

Cistern maintains a `droplet_issues` table for structured findings from adversarial-review. Each issue has a severity, location, description, and resolution state.

```bash
ct droplet issue add <id> --severity critical --title "..." --body "..."   File a finding
ct droplet issue list <id>                                                  List open issues
ct droplet issue resolve <id> <issue-id> --evidence "..."                  Resolve (reviewer only)
ct droplet issue reject <id> <issue-id> --reason "..."                     Reject as invalid (reviewer only)
```

Key invariants:
- Only the reviewer who filed an issue can resolve or reject it.
- A droplet with open critical or required issues cannot be passed by the reviewer — it must recirculate.
- Resolution requires evidence (test name, line reference, or explanation).

## Named Aqueducts

Aqueducts are named from a pool of Roman aqueducts. The default pool:

```
virgo, marcia, claudia, traiana, julia, appia,
anio, tepula, gier, eifel, alexandrina, barbegal
```

Each tmux session is named `<aqueduct>-<droplet-id>`. Every `tmux ls` shows the cistern in motion:

```
virgo-ct-x7k: 1 windows (adversarial-review)
marcia-ct-m3j: 1 windows (implement)
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
- Go 1.22+
- `claude` CLI with OAuth login (`claude login`)
- `gh` CLI authenticated (`gh auth login`)
- `git`, `tmux`

## Configuration

```bash
ct init                        # Create ~/.cistern/ with default config and aqueduct files
ct aqueduct validate           # Check config and all aqueduct files
ct doctor                      # Full health check
```

Config lives at `~/.cistern/cistern.yaml`. See `cistern.yaml` for all options.

## CLI Reference

```
# Castellarius — the overseer that watches the cistern and routes droplets
ct castellarius start          Wake the Castellarius (start processing)
ct castellarius status         Show aqueduct flow — which are flowing, which are idle

# Dashboard — live TUI flow-graph
ct dashboard                   Live flow-graph showing droplets moving through the aqueduct
ct feed                        Streaming event feed (alternative TUI view)

# Status — observe the system
ct status                      Overall status: cistern level, aqueduct flow, cataracta chains
ct aqueduct status             Aqueduct definitions: repos and their cataracta chains

# Aqueduct — inspect and validate aqueduct definitions
ct aqueduct validate           Validate cistern.yaml and all referenced workflow files
ct aqueduct inspect            JSON snapshot of current Cistern state

# Droplets — manage work items
ct droplet add --title "..." --repo myproject           Add a droplet to the cistern
ct droplet add --title "..." --repo myproject --filter  LLM-assisted filtration before adding
ct droplet add --title "..." --depends-on <id>          Add with dependency on another droplet
ct droplet list                                         List droplets
ct droplet show <id>                                    Show droplet details
ct droplet stats                                        Show droplet counts by status
ct droplet deps <id>                                    Show dependency chain for a droplet
ct droplet close <id>                                   Mark delivered
ct droplet reopen <id>                                  Return to cistern
ct droplet purge --older-than 30d                       Drain old droplets
ct droplet escalate <id> --reason "..."                 Mark a droplet stagnant

# Droplet issues — structured findings from adversarial-review
ct droplet issue add <id> --severity critical --title "..." --body "..."   File a finding
ct droplet issue list <id>                                                  List issues
ct droplet issue resolve <id> <issue-id> --evidence "..."                  Resolve (reviewer only)
ct droplet issue reject <id> <issue-id> --reason "..."                     Reject as invalid (reviewer only)

# Cataractae — manage cataracta definitions
ct cataractae list                   See all cataracta definitions and how to edit them
ct cataractae edit <cataracta>       Edit cataracta definition in $EDITOR
ct cataractae generate               Regenerate CLAUDE.md files from YAML
ct cataractae reset <cataracta>      Restore cataracta definition to built-in default

# Skills — manage cataracta skills
ct skills install <skill>      Install a skill
ct skills list                 List installed skills
ct skills update <skill>       Update a skill to latest version
ct skills remove <skill>       Remove a skill

# Utilities
ct doctor                      Health check
ct version                     Version info
```

---
