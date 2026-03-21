<p align="center">
  <img src="cistern_logo.png" alt="Cistern Logo" />
</p>

Cistern is an agentic delivery system built around a water metaphor. Droplets of work enter the cistern, flow through named aqueducts cataractae by cataractae, and what emerges at the other end is clean enough to ship.

## The Vocabulary

| Term | Meaning |
|---|---|
| **Droplet** | A unit of work — one issue, one feature, one fix. The atomic thing that flows. |
| **Complexity** | A droplet's weight: trivial, standard, full, or critical. Controls which cataractae it passes through. |
| **Filtration** | Optional LLM refinement step. Refine a raw idea before it enters the Cistern. |
| **Cistern** | The reservoir. Droplets queue here waiting to flow into the aqueduct. |
| **Drought** | Idle state. The cistern is dry. Drought protocols run maintenance automatically. A drought may also be a forced maintenance window where processing is stopped. |
| **Aqueduct** | The full pipeline — from intake through cataractae gates to delivery. Named aqueducts are independent instances the Castellarius routes droplets into. |
| **Castellarius** | The overseer. Watches all aqueducts, routes droplets into aqueducts, runs drought protocols. External to the cistern — pure state machine, no AI. |
| **Cataractae** | A gate along the aqueduct. Each cataractae implements, reviews, or diverts (LLMs working). |
| **Recirculate** | Send a droplet back to a previous cataractae for further processing — revision from reviewer or QA. |
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

# Add a critical droplet (runs all cataractae including security review + human gate)
ct droplet add --title "Rewrite auth layer" --repo myproject --complexity critical

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

Every droplet flows through a sequence of cataractae. Which cataractae run depends on the droplet's **complexity level**:

```
trivial:   implement                                               → delivery → done
standard:  implement → adversarial-review                         → delivery → done
full:      implement → adversarial-review → qa                    → delivery → done
critical:  implement → adversarial-review → qa → security-review → [human gate] → delivery → done
```

Filtration is an optional pre-intake step (`--filter`) that refines vague ideas before they enter the pipeline.

1. **Implement** (`implement`) — The Implementer cataractae reads the droplet, writes tests first (TDD/BDD), implements, commits. No outcome until tests pass.

2. **Adversarial Review** (`adversarial-review`) — The Adversarial Reviewer cataractae receives *only the diff*. No codebase access, no author context. Finds problems: bugs, security holes, missing tests, logic errors. Context isolation is enforced at the infrastructure level. Files structured issues via `ct droplet issue add`. *Skipped for trivial droplets.*

3. **QA** (`qa`) — The QA cataractae checks test quality, not just whether tests pass. Finds test gaps, weak assertions, missing error paths, coverage theater. Recirculates to implement on revision. *Skipped for trivial and standard droplets.*

4. **Security Review** (`security-review`) — An adversarial security audit of the diff. Checks for auth bypass, injection, prompt injection, exposed secrets, resource safety, and path traversal. *Runs only for critical droplets.*

5. **Human Gate** — Critical droplets pause before delivery and require explicit human approval: `ct droplet approve <id>`. This ensures a human signs off before any critical change ships.

6. **Delivery** (`delivery`) — The Delivery cataractae owns all git operations: stash, rebase, PR creation, CI monitoring, PR review response, and merge. One agent cataractae handles the full branch-to-merged lifecycle.

7. **Recirculation** — Revision sends the droplet back upstream to a prior cataractae for another pass. No retry limits. The water flows until it's pure.

## Complexity Levels

Set complexity when adding a droplet with `--complexity` (or `-x`):

| Level | Name | Pipeline |
|---|---|---|
| 1 | trivial | implement → delivery |
| 2 | standard | implement → adversarial-review → delivery |
| 3 | full *(default)* | implement → adversarial-review → qa → delivery |
| 4 | critical | implement → adversarial-review → qa → security-review → [human] → delivery |

```bash
ct droplet add --title "Fix typo in README" --repo myproject --complexity trivial
ct droplet add --title "Add pagination to list endpoint" --repo myproject --complexity standard
ct droplet add --title "Implement JWT refresh" --repo myproject --complexity full
ct droplet add --title "Replace auth middleware" --repo myproject --complexity critical
```

Accepts numeric (`1`–`4`) or named values.

## Two-Phase Review

The adversarial-review step uses a structured two-phase protocol that prevents reviewer anchoring and ensures prior issues are actually fixed.

**Phase 1 — Verify prior issues.** If the droplet has been recirculated, the reviewer checks each previously filed issue first: mark it `RESOLVED` with evidence (test name, line number) or `UNRESOLVED` with the gap. The reviewer cannot skip to fresh review until all prior issues are assessed.

**Phase 2 — Fresh review.** After verifying prior work, the reviewer performs a clean-slate review of the diff. New findings are filed as structured issues via `ct droplet issue add`.

This protocol prevents common failure modes: rubber-stamping recirculations, anchoring on prior notes, or missing regressions introduced during fixes.

## Issue Tracking

Cistern maintains a `droplet_issues` table for structured findings from adversarial-review. Each issue has a description, a filer, and a resolution state.

```bash
ct droplet issue add <id> "<description>"         File a finding against a droplet
ct droplet issue list <id>                        List all issues for a droplet
ct droplet issue list <id> --open                 List only open issues
ct droplet issue resolve <issue-id> --evidence "" Resolve with proof (reviewer only — not implementer)
ct droplet issue reject <issue-id> --evidence ""  Reject as invalid with proof (reviewer only)
```

Key invariants:
- Implementers cannot resolve or reject issues — only reviewer cataractae may.
- A droplet with open issues cannot be passed — it must recirculate.
- Resolution requires evidence (test name, line reference, or command output).

## Named Aqueducts

Each repo in `cistern.yaml` gets a set of named aqueducts — independent processing lanes that run concurrently. Configure the names under `names:` for each repo:

```yaml
repos:
  - name: myproject
    url: https://github.com/org/myproject
    workflow_path: aqueduct/feature.yaml
    cataractae: 2
    names:
      - virgo
      - marcia
```

Each aqueduct runs its own isolated git worktree, backed by a shared primary clone at `~/.cistern/sandboxes/<repo>/_primary/`. Objects are shared — only the working tree is duplicated per aqueduct, reducing disk cost roughly 3× at scale. Each tmux session is named `<repo>-<aqueduct>`. Every `tmux ls` shows the cistern in motion:

```
myproject-virgo: 1 windows (adversarial-review)
myproject-marcia: 1 windows (implement)
```

By convention, aqueduct names are drawn from historic Roman aqueducts (`virgo`, `marcia`, `claudia`, `traiana`, `julia`, `appia`, `anio`, `tepula`, `alexandrina`, …), but any names work.

## Customizing Cataractae Definitions

Cataractae definitions are stored in your aqueduct YAML — they're yours to edit. Cistern adapts.

```bash
ct cataractae list                  # See all cataractae definitions and how to edit them
ct cataractae edit implementer      # Open in $EDITOR, save, CLAUDE.md regenerates
ct cataractae reset qa              # Restore to built-in default (with confirmation)
ct cataractae generate              # Regenerate all CLAUDE.md files from YAML
ct cataractae status                # Show which cataractae are actively processing droplets
```

Cataractae content lives in `~/.cistern/aqueduct/feature.yaml` under the `cataractae_definitions:` key. CLAUDE.md files are generated artifacts — the YAML is the source of truth.

## Drought Protocols

When the cistern is dry, Cistern runs maintenance automatically. Configure in `~/.cistern/cistern.yaml`:

```yaml
# Drought protocols — run when Cistern is idle
drought_hooks:
  - name: sync-cataractae
    action: cataractae_generate   # Regenerate cataractae files when YAML is newer

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
ct doctor --fix                # Auto-repair common configuration issues
```

Config lives at `~/.cistern/cistern.yaml`. Key options:

```yaml
# Heartbeat: how often the Castellarius scans for stalled sessions
heartbeat_interval: 30s

# Rate limit: protect the delivery cataractae API endpoint
# Omit to use defaults (60 req/min per IP, 120 req/min per token)
# rate_limit:
#   per_ip_requests: 60
#   per_token_requests: 120
#   window: 1m

# Drought protocols run when the cistern goes idle
drought_hooks:
  - name: sync-cataractae
    action: cataractae_generate
  - name: prune-worktrees
    action: worktree_prune
```

See `cistern.yaml` in this repo for all options.

## Docker

Cistern ships a multi-stage Dockerfile. The image includes `tmux`, `git`, `gh`, and both `ct` and `aqueduct` binaries.

```bash
docker build -t cistern .

# Run the Castellarius — mount ~/.cistern for config, auth, and the database
docker run -v ~/.cistern:/root/.cistern cistern
```

The `/root/.cistern` volume persists config, skills, the SQLite database, and gh auth state across container restarts. `GH_CONFIG_DIR` is set automatically to `/root/.cistern/auth/gh`.

## CLI Reference

```
# Castellarius — the overseer that watches the cistern and routes droplets
ct castellarius start          Wake the Castellarius (start processing)
ct castellarius status         Show aqueduct flow — which are flowing, which are idle

# Dashboard
ct dashboard                   Live TUI aqueduct arch diagram with cistern and recent flow
ct dashboard --web             HTTP web dashboard — pre-based TUI port with animated arches, waves, and waterfall (SSE live updates, port 5737)
                               # Click an active arch to open a read-only live peek panel (WebSocket)
ct dashboard --web --addr :8080  Custom listen address for web dashboard
ct feed                        Alias for dashboard

# Status — observe the system
ct status                      Overall status: cistern level, aqueduct flow, cataractae chains
ct aqueduct status             Aqueduct definitions: repos and their cataractae chains

# Aqueduct — inspect and validate aqueduct definitions
ct aqueduct validate           Validate cistern.yaml and all referenced workflow files
ct aqueduct inspect            JSON snapshot of current Cistern state
ct aqueduct inspect --table    Human-readable table instead of JSON

# Droplets — manage work items
ct droplet add --title "..." --repo myproject                     Add a droplet
ct droplet add --title "..." --repo myproject --filter            LLM-assisted filtration before adding
ct droplet add --title "..." --repo myproject --filter --yes      Non-interactive filtration (agent use)
ct droplet add --title "..." --depends-on <id>                    Add with dependency on another droplet
ct droplet add --title "..." --complexity trivial                  Set complexity (trivial/standard/full/critical or 1–4)
ct droplet add --title "..." --priority 1                         Set priority (1=highest)
ct droplet list                                                   List active droplets
ct droplet list --all                                             Include delivered droplets (dimmed)
ct droplet list --watch                                           Live-refresh every 2 seconds (Ctrl-C to stop)
ct droplet list --status in_progress                              Filter by status
ct droplet list --output json                                     JSON output
ct droplet search --query "retry"                                 Search by title substring
ct droplet search --status in_progress --priority 1               Filter by status and priority
ct droplet search --output json                                   JSON search output
ct droplet export --format json                                   Export all droplets as JSON
ct droplet export --format csv --status delivered                 Export delivered droplets as CSV
ct droplet show <id>                                              Show droplet details and notes
ct droplet rename <id> "New title"                                Rename a droplet
ct droplet note <id> "What you found"                             Add a note to a droplet
ct droplet stats                                                  Show droplet counts by status
ct droplet deps <id>                                              List dependency chain for a droplet
ct droplet deps <id> --add <dep-id>                               Add a dependency
ct droplet deps <id> --remove <dep-id>                            Remove a dependency
ct droplet close <id>                                             Mark delivered
ct droplet reopen <id>                                            Return to cistern (status=open, cataractae unchanged)
ct droplet restart <id> --cataractae delivery                     Re-enter at a specific cataractae (recovery)
ct droplet restart <id> --cataractae delivery --notes "..."       Re-enter with a recovery note
ct droplet purge --older-than 30d                                 Delete old delivered/stagnant droplets
ct droplet purge --older-than 24h --dry-run                       Preview what would be purged
ct droplet escalate <id> --reason "..."                           Mark a droplet stagnant

# Droplet outcomes — used by agent cataractae to signal completion
ct droplet pass <id>                                              Advance to next cataractae
ct droplet pass <id> --notes "..."                                Advance with notes
ct droplet recirculate <id>                                       Send back to previous cataractae
ct droplet recirculate <id> --to implement                        Send back to a named cataractae
ct droplet recirculate <id> --notes "..."                         Recirculate with notes
ct droplet block <id>                                             Mark genuinely blocked
ct droplet block <id> --notes "..."                               Block with notes

# Human gate — critical droplets pause here before delivery
ct droplet approve <id>                                           Approve a critical droplet for delivery

# Peek — observe live agent output
ct droplet peek <id>                                              Tail the active tmux session
ct droplet peek <id> --lines 100                                  Show more lines (default: 50)
ct droplet peek <id> --follow                                     Re-capture every 3 seconds (Ctrl-C to stop)
ct droplet peek <id> --raw                                        Include ANSI color codes

# Droplet issues — structured findings from adversarial-review
ct droplet issue add <id> "<description>"                         File a finding
ct droplet issue list <id>                                        List all issues
ct droplet issue list <id> --open                                 List only open issues
ct droplet issue resolve <issue-id> --evidence "..."              Resolve with proof (reviewer only)
ct droplet issue reject <issue-id> --evidence "..."               Reject as still present (reviewer only)

# Cataractae — manage cataractae definitions
ct cataractae list                   See all cataractae definitions
ct cataractae status                 Show which cataractae are active and what they're processing
ct cataractae edit <cataractae>       Edit cataractae definition in $EDITOR
ct cataractae generate               Regenerate CLAUDE.md files from YAML
ct cataractae reset <cataractae>      Restore cataractae definition to built-in default

# Skills — manage cataractae skills
ct skills install <name> <url>       Install a skill from a URL
ct skills list                       List installed skills and which cataractae reference them
ct skills update <name>              Re-fetch a skill from its source URL
ct skills update                     Re-fetch all skills
ct skills remove <name>              Remove a skill

# Utilities
ct doctor                      Full health check (prerequisites, config, CLAUDE.md integrity, skills)
ct doctor --fix                Auto-repair common issues
ct version                     Version info
```

---

## Recovery

When a delivery fails mid-flight (merge conflict, CI failure, permission issue) or a droplet gets
incorrectly marked delivered before the PR actually merged, use `ct droplet restart` to send it
back into the pipeline at the exact cataractae it needs:

```bash
# Re-enter delivery after manually resolving conflicts
ct droplet restart sc-uvfhw --cataractae delivery

# Re-enter with a note explaining why
ct droplet restart sc-uvfhw --cataractae delivery \
  --notes "PR #157 had webhook store signature conflict — resolved manually, re-entering delivery"

# Send back to implement if the feature itself needs rework
ct droplet restart sc-gh7lg --cataractae implement \
  --notes "GetMe and UpdateMe handlers collided with main — needs clean rewrite"
```

`restart` clears the assignee, outcome, and sets status back to `open` at the named cataractae.
The Castellarius picks it up on the next tick. Works from any terminal state: delivered, blocked,
stagnant, or open.

This differs from `reopen` (which returns to `open` with the cataractae unchanged) and
`recirculate` (which is an agent-issued signal during active processing). `restart` is for
human-initiated recovery after something went wrong.
