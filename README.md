# Bullet Farm

Agentic workflow orchestrator for software development. Composable AI pipelines
where each step is either an AI agent doing cognitive work or automated code
doing mechanical work — and the two never get confused.

## The Problem with Pure AI Orchestration

When you use an AI agent to decide what to run next, schedule work, and route
outcomes, you're burning tokens on things a state machine does better. CrewAI,
AutoGen — they all have this problem. The coordination layer is AI when it
shouldn't be.

Bullet Farm flips this: **AI does cognitive work, code does mechanical work.**

- Routing between steps → deterministic state machine
- Scheduling when to run → cron/event loop
- Context isolation for adversarial steps → enforced at infrastructure level
- Writing code, reviewing code, making judgment calls → AI agents

## Core Concepts

### Workflows

A workflow is a YAML file defining a sequence of steps. Each step has a role,
a context level, and routing rules for every possible outcome.

```yaml
# workflows/feature.yaml
name: feature
steps:
  - name: implement
    role: implementer
    model: sonnet
    context: full_codebase
    on_pass: adversarial-review
    on_fail: blocked

  - name: adversarial-review
    role: reviewer
    model: sonnet
    context: diff_only          # enforced: agent never sees author context
    adversarial: true
    on_pass: qa
    on_revision: implement      # routed back with reviewer notes attached

  - name: qa
    role: qa
    model: haiku                # cheaper for test running
    context: full_codebase
    on_pass: merge
    on_fail: implement

  - name: merge
    type: automated             # no AI — just runs gh pr merge
    checks: [ci]
    on_pass: done
    on_fail: human
```

### Steps

Steps have a `type`:

| Type | What runs | When to use |
|------|-----------|-------------|
| `agent` | Claude Code session with role CLAUDE.md | Code, review, QA, analysis |
| `automated` | Shell command / script | Git ops, CI checks, PR creation |
| `gate` | Condition check, no action | CI must be green before proceeding |
| `human` | Pause for human input | Escalation, ambiguous cases |

### Roles

Each `agent` step uses a role. Roles are defined by a `CLAUDE.md` in `roles/`:

- **implementer** — writes code for the work item, full codebase context
- **reviewer** — adversarial code review, sees only the diff (no author, no history)
- **qa** — writes and runs tests, full codebase context
- **security** — security-focused audit, diff_only context
- **docs** — updates documentation
- **refiner** — takes a vague work item and sharpens it into an implementable spec

Context levels:
- `full_codebase` — agent has full repo access
- `diff_only` — agent receives only `git diff` output, no repo access (adversarial isolation)
- `spec_only` — agent receives only the work item description

### Context Isolation

Adversarial steps enforce isolation at the infrastructure level, not by prompting.
A reviewer with `context: diff_only` gets:

- A fresh tmux session
- A temp directory with only the diff file
- No git history
- No work item description
- No author attribution

The scheduler controls this. The reviewer agent cannot accidentally see what it
shouldn't — there's nothing there to see.

### Outcomes

Every agent step writes an outcome file when complete:

```json
{
  "result": "pass" | "fail" | "revision" | "escalate",
  "notes": "Human-readable summary of what happened",
  "annotations": []  // optional: file:line level comments
}
```

The scheduler reads this and routes to the next step. Notes from failed steps
are injected as context for the next agent that picks up the work (e.g., the
implementer sees the reviewer's notes when the work comes back).

## Architecture

```
bullet-farm/
  cmd/
    bf/                 # bf CLI — queue management and farm control
    farm/               # farm binary — scheduler + CLI
  internal/
    scheduler/          # step scheduling, state machine
    workflow/           # YAML parser, workflow definitions
    runner/             # Claude Code session management (was agent/)
    queue/              # SQLite-backed work queue
    context/            # context preparation per step type
    automated/          # deterministic step executors (PR create, CI gate, merge)
  workflows/
    feature.yaml        # default feature workflow
    bug.yaml            # bug fix (no refine step needed)
    docs.yaml           # documentation only
    security-audit.yaml # security-focused pipeline
  roles/
    implementer/CLAUDE.md
    reviewer/CLAUDE.md
    qa/CLAUDE.md
    security/CLAUDE.md
    refiner/CLAUDE.md
    docs/CLAUDE.md
  config.yaml           # farm config (queue, agent limits, etc.)
```

## Work Queue

Bullet Farm uses a SQLite-backed work queue. Work items drive the pipeline.
Each item flows through a workflow where the scheduler polls for ready items
and assigns them to the appropriate step agent.

Work item lifecycle:

```
open → in_progress(implement) → in_progress(review) → in_progress(qa) → closed
```

The item's `current_step` field always reflects which step it's at. The
`status` field tracks whether it's `open`, `in_progress`, `closed`, or
`escalated`.

## CLI

The `bf` command manages the work queue and farm:

```
bf queue add --title "..." --description "..." --priority 1 --repo github.com/Org/Repo
bf queue list [--repo <repo>] [--status open|in_progress|closed|escalated]
bf queue show <id>
bf queue note <id> "content"
bf queue close <id>
bf queue reopen <id>
bf queue escalate <id> --reason "stuck"
bf farm start [--config config.yaml]
bf farm status
bf farm config validate <path>
bf version
```

## Key Design Decisions

**Why not AI for routing?**
Routing is deterministic. The reviewer either passes or requests revision. Using
an AI to decide that introduces latency, cost, and nondeterminism where none is
needed.

**Why SQLite for the queue?**
SQLite is embedded, zero-dependency, and handles our concurrency needs. No
external services to manage. The queue database lives at `~/.bullet-farm/queue.db`.

**Why enforce context isolation in infrastructure?**
An adversarial reviewer prompted to "pretend you don't know who wrote this" is
unreliable — the context is still in the window. Actual isolation (fresh session,
diff-only directory) is reliable by construction.

**Why YAML workflows?**
Adding a security step should be a YAML edit, not a code change. Workflows should
be readable by anyone, versionable, and composable. `security-audit.yaml` just
adds a step.

## Status

Under active development. See [issues](https://github.com/MichielDean/bullet-farm/issues) for the build plan.
