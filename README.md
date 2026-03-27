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
# feature.yaml / CLAUDE.md / AGENTS.md / GEMINI.md / skills changes → no restart (read per spawn)

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
trivial:   implement                                                                    → delivery → done
standard:  implement → simplify → adversarial-review                                   → delivery → done
full:      implement → simplify → adversarial-review → qa → docs                       → delivery → done
critical:  implement → simplify → adversarial-review → qa → security-review → docs → [human gate] → delivery → done
```

Filtration is an optional pre-intake step (`--filter`) that refines vague ideas before they enter the pipeline.

1. **Implement** (`implement`) — Reads the droplet description, implements the feature, writes tests, commits. Verifies every concrete deliverable from the description exists in the commit before signaling pass.

2. **Simplify** (`simplify`) — Refines the implementation for clarity, consistency, and maintainability without changing behaviour. Runs only on branches with new commits since `origin/main`. *Skipped for trivial droplets.*

3. **Adversarial Review** (`adversarial-review`) — Receives *only the diff* — no codebase access, no author context. First verifies all required deliverables are present in the diff (Phase 0), then checks for bugs, security issues, missing tests, and logic errors. Context isolation enforced at the infrastructure level. *Skipped for trivial droplets.*

4. **QA** (`qa`) — Active verification with full codebase access: runs tests, checks each deliverable exists via `grep`, verifies CLI flags, checks mirror file consistency. Recirculates to implement on any failure. *Skipped for trivial and standard droplets.*

5. **Security Review** (`security-review`) — Adversarial security audit of the diff. Checks for auth bypass, injection, prompt injection, exposed secrets, resource safety, and path traversal. *Runs only for critical droplets.*

6. **Docs** (`docs`) — Reviews the diff and updates documentation for all user-visible changes: README, CHANGELOG, CLI reference, config docs. Skips if there are no user-visible changes. *Skipped for trivial droplets.*

7. **Human Gate** — Critical droplets pause before delivery and require explicit human approval: `ct droplet approve <id>`. Ensures a human signs off before any critical change ships.

8. **Delivery** (`delivery`) — Owns all git operations: rebase, PR creation, CI monitoring, PR review response, and merge. One agent handles the full branch-to-merged lifecycle. If a delivery agent stalls, the Castellarius detects and recovers automatically — see [Automatic Stuck Delivery Recovery](#automatic-stuck-delivery-recovery).

9. **Recirculation** — Revision sends the droplet back upstream to a prior cataractae for another pass. No retry limits. The water flows until it's pure.

## Complexity Levels

Set complexity when adding a droplet with `--complexity` (or `-x`):

| Level | Name | Pipeline |
|---|---|---|
| 1 | trivial | implement → delivery |
| 2 | standard | implement → simplify → adversarial-review → delivery |
| 3 | full *(default)* | implement → simplify → adversarial-review → qa → docs → delivery |
| 4 | critical | implement → simplify → adversarial-review → qa → security-review → docs → [human] → delivery |

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

Aqueduct names are **concurrency slots** — they control how many droplets run in parallel per repo. Each active droplet gets its own isolated git worktree at `~/.cistern/sandboxes/<repo>/<droplet-id>/` on branch `feat/<droplet-id>`. Worktrees are created when a droplet enters the `implement` step and removed once it reaches a terminal state (`done`, `blocked`, `escalated`, or `human`).

All per-droplet worktrees share a single primary clone object store at `~/.cistern/sandboxes/<repo>/_primary/` — objects are shared, only the working tree is per-droplet, keeping disk cost low. Each tmux session is named `<repo>-<aqueduct>`. Every `tmux ls` shows the cistern in motion:

```
myproject-virgo: 1 windows (adversarial-review)
myproject-marcia: 1 windows (implement)
```

Before dispatching a droplet, the Castellarius checks the worktree for uncommitted files. If non-`CONTEXT.md` files are dirty, the droplet is recirculated with a diagnostic note rather than spawning an agent into inconsistent state.

By convention, aqueduct names are drawn from historic Roman aqueducts (`virgo`, `marcia`, `claudia`, `traiana`, `julia`, `appia`, `anio`, `tepula`, `alexandrina`, …), but any names work.

## Customizing Cataractae Definitions

Each cataractae is a self-contained directory under `cataractae/<identity>/` in your aqueduct repo:

```
cataractae/
  implementer/
    PERSONA.md        # Who this cataractae is — role, guardrails (hand-authored, stable)
    INSTRUCTIONS.md   # Task protocol and steps (hand-authored)
    CLAUDE.md         # Generated: concatenated from PERSONA.md + INSTRUCTIONS.md (filename depends on provider)
  reviewer/
  qa/
  ...
```

The generated instructions file is a generated artifact — edit `PERSONA.md` and `INSTRUCTIONS.md` directly and regenerate. The filename matches the active provider: `CLAUDE.md` for claude, `AGENTS.md` for codex/copilot/opencode, `GEMINI.md` for gemini.

```bash
ct cataractae add <name>            # Scaffold a new cataractae directory with template files; auto-generates the provider's instructions file
ct cataractae list                  # See all cataractae definitions and how to edit them
ct cataractae edit implementer      # Open INSTRUCTIONS.md in $EDITOR, save, instructions file regenerates
ct cataractae generate              # Regenerate provider instructions files (CLAUDE.md, AGENTS.md, or GEMINI.md) from source files
ct cataractae status                # Show which cataractae are actively processing droplets
```

The `aqueduct.yaml` holds routing configuration (which cataractae run at each step, skill references, timeouts, model selection). Persona and instruction content lives in the directory files, not inline in YAML.

### Per-step model selection

Each cataractae step can specify an LLM model with the optional `model:` field:

```yaml
cataractae:
  - name: implement
    type: agent
    identity: implementer
    model: sonnet       # passed to claude CLI as --model sonnet
    context: full_codebase

  - name: simplify
    type: agent
    identity: simplifier
    model: opus         # stronger model for deep refactoring
    context: full_codebase
```

Valid values are any string accepted by the configured provider's CLI (e.g. `sonnet`, `opus`, `haiku`, `claude-opus-4-6` for `claude`). If `model:` is omitted, the agent uses the `provider.model:` default from `cistern.yaml`, or the CLI's own default if neither is set. `ct doctor` validates that the value is a non-empty string when present.

## Skills

Skills are reusable knowledge packages injected into cataractae at spawn time. Providers with `--add-dir` support (`claude`) receive skills via filesystem injection; providers without it (codex, gemini, copilot, opencode) receive skill content as text in the prompt preamble. Either way, skills keep cataractae prompts concise by factoring out shared conventions.

```bash
ct skills install <name> <url>   Install a skill from a URL
ct skills list                   List installed skills and which cataractae reference them
ct skills update <name>          Re-fetch from source URL
ct skills update                 Re-fetch all skills
ct skills remove <name>          Remove a skill
```

Skills are referenced by name in your aqueduct YAML under each cataractae's `skills:` list. They live in `~/.cistern/skills/<name>/SKILL.md`. Skills bundled with the repo live under `skills/` and are deployed automatically into `~/.cistern/skills/` by the `git_sync` drought hook — no manual install required.

`ct skills update` re-fetches skills from their source URL. Skills managed by `git_sync` (recorded as `source_url:local`) are skipped — they stay in sync via `git_sync` automatically.

**Built-in skills:**

| Skill | Purpose | Cataractae |
|---|---|---|
| `cistern-droplet-state` | Signal pass/recirculate/block with `ct` CLI | All |
| `cistern-git` | Git conventions: exclude CONTEXT.md, merge-base diff, no stash | implement, simplify, docs, delivery |
| `cistern-github` | PR creation, CI checks, squash-merge, and automatic conflict resolution for Cistern delivery | implement, adversarial-review, delivery |
| `code-simplifier` | Simplification heuristics and patterns | simplify |
| `cistern-reviewer` | Adversarial code review for Go, TypeScript/Next.js, and TypeScript/React — all findings equal, recirculate on any finding, pass only when nothing remains | adversarial-review |

The `cistern-git` skill encodes hard-won rules: always use `git add -A -- ':!CONTEXT.md'`, always use merge-base diff (`git diff $(git merge-base HEAD origin/main)..HEAD`) instead of two-dot — two-dot includes other PRs that merged to main after branching on unrebased branches, never stash in per-droplet worktrees.

## Drought Protocols

When the cistern is dry, Cistern runs maintenance automatically. Configure in `~/.cistern/cistern.yaml`:

```yaml
# Drought protocols — run when Cistern is idle
drought_hooks:
  - name: sync-workflow
    action: git_sync             # Pull aqueduct.yaml + cataractae source files from origin/main
    restart_if_updated: true     # Hot-reload the Castellarius when the workflow changes

  - name: sync-cataractae
    action: cataractae_generate  # Regenerate CLAUDE.md files from PERSONA.md + INSTRUCTIONS.md

  - name: prune-worktrees
    action: worktree_prune       # Prune stale aqueduct registrations

  # - name: git-sync
  #   action: git_sync         # Fetch origin/main: redeploy aqueduct.yaml and skills/ into ~/.cistern/skills/

  # - name: vacuum-cistern
  #   action: db_vacuum          # Compact the cistern database

  # - name: custom
  #   action: shell
  #   command: "echo $(date): cistern dry >> ~/.cistern/drought.log"
```

| Action | What it does |
|---|---|
| `git_sync` | Fetches `origin/main` and deploys `aqueduct.yaml` + `cataractae/<role>/PERSONA.md` + `cataractae/<role>/INSTRUCTIONS.md` to `~/.cistern/`. Skips files that are already up to date. |
| `cataractae_generate` | Regenerates the provider-specific instructions file (`CLAUDE.md`, `AGENTS.md`, or `GEMINI.md`) for each cataractae from its `PERSONA.md` + `INSTRUCTIONS.md`. Run after `git_sync` to pick up new source files. |
| `worktree_prune` | Runs `git worktree prune` on each repo's primary clone to remove stale worktree registrations. |
| `db_vacuum` | Compacts the SQLite cistern database. |
| `shell` | Runs an arbitrary shell command. Use for custom maintenance. |

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

The Castellarius automatically refreshes the Claude OAuth access token before each agent spawn when it is expired or within 5 minutes of expiry. If the refresh fails (e.g. the refresh token itself has expired), the spawn fails with a clear error directing you to run `claude` interactively to re-authenticate. Run `ct doctor --fix` to refresh the token on demand and reload the systemd service.

## Credentials

Cistern resolves credentials in the following order:

1. **~/.claude/.credentials.json** — OAuth token managed by the Claude CLI. When you run `claude` interactively, it updates this file with a fresh access token. Castellarius automatically detects token expiry and triggers refresh via the OAuth endpoint. No manual sync required.

2. **ANTHROPIC_API_KEY in ~/.cistern/env** — Fallback for API-key auth setups or non-OAuth configurations. A simple `KEY=VALUE` file, one pair per line, chmod 600.

**For Claude users (recommended):** Run `claude` interactively once to authenticate. Castellarius will read your OAuth credentials from `~/.claude/.credentials.json` and keep them fresh automatically. You do not need to set `ANTHROPIC_API_KEY` in `~/.cistern/env`.

```bash
claude          # Authenticate once — updates ~/.claude/.credentials.json
ct castellarius start  # Reads OAuth token; automatic refresh on expiry
```

**For API key authentication:** Add `ANTHROPIC_API_KEY` to `~/.cistern/env`:

```bash
# Plaintext (simplest)
echo 'ANTHROPIC_API_KEY=sk-ant-...' >> ~/.cistern/env
echo 'GH_TOKEN=ghp_...' >> ~/.cistern/env
chmod 600 ~/.cistern/env

# From pass
echo "ANTHROPIC_API_KEY=$(pass show anthropic/api-key)" >> ~/.cistern/env
chmod 600 ~/.cistern/env

# From 1Password CLI
echo "ANTHROPIC_API_KEY=$(op read 'op://Personal/Anthropic/api-key')" >> ~/.cistern/env
chmod 600 ~/.cistern/env
```

`ct init` creates `~/.cistern/env` automatically with the correct permissions (600). The file is added to `~/.cistern/.gitignore` so it is never accidentally committed.

`ct doctor` checks both credential sources: it verifies that `~/.claude/.credentials.json` tokens are fresh (and prompts to refresh if expired), and validates any `ANTHROPIC_API_KEY` in `~/.cistern/env`. `ct doctor --fix` can refresh expired OAuth tokens automatically or prompt for missing API keys.

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

# Stall detection: threshold for inactivity before marking a droplet as stalled
# Monitors three progress signals: newest note timestamp, worktree file mtime,
# and session log mtime. Droplet is stalled if all three are older than this threshold.
# When detected, a diagnostic note is appended and further notes are suppressed
# until one of the signals advances.
# Default: 45 minutes
stall_threshold_minutes: 45

# Exponential backoff for quick session exits and provider degradation detection
# When a session exits quickly (within this threshold) without an outcome,
# trigger per-droplet exponential backoff. When 3+ sessions fail across 2+ aqueducts
# within 5 minutes, fast-forward all affected droplets to max backoff (provider appears degraded).
# Defaults: 30s for quick-exit threshold, 30m for max backoff
quick_exit_threshold_seconds: 30
max_backoff_minutes: 30

# Rate limit: protect the delivery cataractae API endpoint
# Omit to use defaults (60 req/min per IP, 120 req/min per token)
# rate_limit:
#   per_ip_requests: 60
#   per_token_requests: 120
#   window: 1m

# Drought protocols run when the cistern goes idle
drought_hooks:
  - name: sync-workflow
    action: git_sync
    restart_if_updated: true
  - name: sync-cataractae
    action: cataractae_generate
  - name: prune-worktrees
    action: worktree_prune
```

See `cistern.yaml` in this repo for all options.

## Provider Configuration

Cistern supports multiple agent CLIs through a provider preset system. Configure the provider in `~/.cistern/cistern.yaml` using the top-level `provider:` block or on a per-repo basis.

**Built-in presets:**

| Name | CLI | Env variable required | Instructions file |
|---|---|---|---|
| `claude` *(default)* | `claude` | `ANTHROPIC_API_KEY` | `CLAUDE.md` |
| `codex` | `codex` | `OPENAI_API_KEY` | `AGENTS.md` |
| `gemini` | `gemini` | `GEMINI_API_KEY` | `GEMINI.md` |
| `copilot` | `copilot` | `GH_TOKEN` | `AGENTS.md` |
| `opencode` | `opencode` | — | `AGENTS.md` |

**Top-level provider (applies to all repos):**

```yaml
provider:
  name: claude          # built-in preset name, or 'custom'
  model: opus           # default model passed via the preset's model flag
  command: ""           # override the executable (e.g. a wrapper script)
  args: []              # extra args appended to the preset's fixed args
  env: {}               # extra env vars injected into the agent process
```

**Per-repo override (overrides the top-level for that repo only):**

```yaml
repos:
  - name: myproject
    url: https://github.com/org/myproject
    workflow_path: aqueduct/feature.yaml
    cataractae: 2
    names:
      - virgo
    provider:
      name: gemini      # this repo uses gemini instead of claude
      model: gemini-2.0-flash
```

**Resolution order:** built-in preset defaults → top-level `provider:` overrides → repo-level `provider:` overrides. When a repo specifies a different `name:` than the top-level, top-level field overrides are not applied — only the repo-level overrides take effect.

If no `provider:` block is present, the `claude` preset is used. Existing configs work unchanged.

The configured provider is also used for **filtration** (`ct droplet add --filter`). There is no separate API key or config for filtration — the same preset, binary, and env var requirements apply to both cataractae sessions and the filtration pass.

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
ct castellarius status         Show aqueduct flow — which are flowing, which are idle; includes per-repo queue depth and active session counts

# Dashboard
ct dashboard                   Live TUI aqueduct arch diagram with cistern and recent flow
ct dashboard --web             HTTP web dashboard on 127.0.0.1:5737 — renders the real TUI via xterm.js
                               Full ANSI color, box-drawing chars, animations. Pinch-to-zoom on
                               mobile (or Ctrl+scroll on desktop). Single-finger pan after zooming.
                               Resize protocol: browser sends {resize:{cols,rows}} on viewport change
                               so Bubble Tea renders at the correct terminal size.
                               Programmatic endpoints preserved: /api/dashboard/events (SSE),
                               /ws/aqueducts/{name}/peek (WebSocket)
ct dashboard --web --addr 127.0.0.1:8080  Custom listen address (must include hostname or IP)
ct feed                        Alias for dashboard

# Status — observe the system
ct status                      Overall status: cistern level, aqueduct flow, cataractae chains
ct status --watch              Continuously refresh every 5 seconds (Ctrl-C to stop)
ct status --watch --interval N  Refresh every N seconds (min 1)
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
ct droplet peek <id>                                              Attach read-only to the live tmux session (or show last notes if session ended)
ct droplet peek <id> --snapshot                                   Capture a static snapshot instead of live attach
ct droplet peek <id> --snapshot --lines 100                       With --snapshot: show only last 100 lines (default: full scrollback)
ct droplet peek <id> --snapshot --follow                          With --snapshot: re-capture every 3 seconds (Ctrl-C to stop)
ct droplet peek <id> --snapshot --raw                             With --snapshot: include ANSI color codes

# Droplet issues — structured findings from adversarial-review
ct droplet issue add <id> "<description>"                         File a finding
ct droplet issue list <id>                                        List all issues
ct droplet issue list <id> --open                                 List only open issues
ct droplet issue resolve <issue-id> --evidence "..."              Resolve with proof (reviewer only)
ct droplet issue reject <issue-id> --evidence "..."               Reject as still present (reviewer only)

# Cataractae — manage cataractae definitions
ct cataractae add <name>             Scaffold a new cataractae directory with PERSONA.md and INSTRUCTIONS.md; auto-generates the provider's instructions file
ct cataractae list                   See all cataractae definitions
ct cataractae status                 Show which cataractae are active and what they're processing
ct cataractae edit <cataractae>       Edit cataractae definition in $EDITOR
ct cataractae generate               Regenerate provider instructions files (CLAUDE.md/AGENTS.md/GEMINI.md) from source files

# Skills — manage cataractae skills
ct skills install <name> <url>       Install a skill from a URL
ct skills list                       List installed skills and which cataractae reference them
ct skills update <name>              Re-fetch a skill from its source URL
ct skills update                     Re-fetch all skills
ct skills remove <name>              Remove a skill

# Utilities
ct doctor                      Full health check (prerequisites, config, instructions file integrity, skills)
ct doctor --fix                Auto-repair common issues (includes OAuth token refresh)
ct version                     Print version string
ct version --json              Machine-readable: {"version":"...","commit":"..."}
ct update                      Pull latest main and rebuild ct in-place; warns if Castellarius is running
ct update --dry-run            Show what would change without building
ct update --repo-path PATH     Override repo path (default: sibling of binary or CT_REPO_PATH env)
```

---

## Automatic Stuck Delivery Recovery

The Castellarius detects and recovers stuck delivery agents automatically — no human intervention required for the common failure modes.

A delivery agent is considered **stuck** when all of the following are true:
- The droplet has been in the `delivery` step for longer than 1.5× the delivery `timeout_minutes` (default: 60 m → 90 m)
- The agent's tmux session is still alive
- No outcome has been written yet

Every 5 minutes, the Castellarius scans all active delivery droplets and recovers any that qualify:

| PR State | Recovery Action |
|---|---|
| **MERGED** | Signals pass — agent just didn't notice |
| **OPEN**, branch behind main | Rebase onto `origin/main`, push, enable auto-merge, signal pass |
| **OPEN**, CI failing | Recirculate for another pipeline pass |
| **OPEN**, all checks green | Attempt direct merge → auto-merge, signal pass |
| **CLOSED** (not merged) | Recirculate with notes |
| No PR found | Recirculate with notes |

Recovery actions are noted on the droplet (`ct droplet show <id>`) and logged by the Castellarius. Recovery is idempotent — safe to trigger multiple times.

The stuck threshold is configurable via `timeout_minutes` on the `delivery` step in your aqueduct YAML. The check fires at 1.5× that value.

---

## Automatic Dispatch-Loop Recovery

The Castellarius detects and recovers droplets stuck in a **dispatch loop** — where the Castellarius repeatedly tries to spawn an agent but fails every time, leaving no tmux session and no progress.

A droplet is considered dispatch-looping when it accumulates **5 or more dispatch failures within any 2-minute window** with no successful agent spawn.

When a dispatch loop is detected, the Castellarius attempts ordered self-recovery before the next dispatch:

| Failure Pattern | Recovery Action |
|---|---|
| Dirty worktree | `git reset --hard HEAD && git clean -fd` on the droplet worktree |
| Worktree missing or corrupt | Remove and recreate the worktree from the primary clone |
| No applicable pattern found | Note the failure and retry next cycle |

After **3 failed self-fix attempts**, the droplet is escalated to `stagnant` with a note describing the failure. Use `ct droplet show <id>` to inspect the recovery history, then `ct droplet restart <id> --cataractae <step>` to re-enter once the underlying issue is resolved.

Recovery attempts are attached as notes on the droplet and logged by the Castellarius with the prefix `dispatch-loop recovery:`. A successful agent spawn resets all counters — a droplet that recovers cleanly leaves no permanent trace.

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

## OpenClaw Integration

An [AgentSkills](https://agentskills.io)-compatible skill lives in `openclaw/cistern/`. It teaches
OpenClaw bots how to interact with a Cistern installation — vocabulary, `ct` commands, pipeline
overview, and troubleshooting.

**Install on any OpenClaw bot:**

```bash
cp -r openclaw/cistern ~/.openclaw/skills/cistern
```

The skill gates on `ct` being present on `PATH`, so it only surfaces when Cistern is installed.
Once installed, your OpenClaw agent will automatically understand droplets, aqueducts, cataractae,
and how to manage work through the pipeline.

**Contents:**

| File | Purpose |
|------|---------|
| `SKILL.md` | Core skill — vocabulary, key commands, pipeline overview |
| `references/commands.md` | Full `ct` command reference |
| `references/setup.md` | Install, config, and binary rebuild instructions |
| `references/troubleshooting.md` | Daemon, stuck droplets, DB recovery |
