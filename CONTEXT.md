# Context

## Item: ci-bwnj1

**Title:** fix: ct update — use git pull --ff-only and detect diverged repo
**Status:** in_progress
**Priority:** 2

### Description

ct update fails with 'divergent branches' when the repo has local commits not in origin/main. Fix: use 'git pull --ff-only' instead of 'git pull'. If fast-forward fails, print a clear error: 'local repo has commits not in origin/main — rebase or reset manually before updating'. Also: auto-detect the repo from the binary path more robustly — ~/cistern may be stale if the lobsterdog worktree is the active one.

## Current Step: implement

- **Type:** agent
- **Role:** implementer
- **Context:** full_codebase

<available_skills>
  <skill>
    <name>cistern-droplet-state</name>
    <description>Manage droplet state in the Cistern agentic pipeline using the `ct` CLI.</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-droplet-state/SKILL.md</location>
  </skill>
  <skill>
    <name>github-workflow</name>
    <description>---</description>
    <location>/home/lobsterdog/.cistern/skills/github-workflow/SKILL.md</location>
  </skill>
</available_skills>

## Signaling Completion

When your work is done, signal your outcome using the `ct` CLI:

**Pass (work complete, move to next step):**
    ct droplet pass ci-bwnj1

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-bwnj1
    ct droplet recirculate ci-bwnj1 --to implement

**Block (genuinely blocked, cannot proceed):**
    ct droplet block ci-bwnj1

Add notes before signaling:
    ct droplet note ci-bwnj1 "What you did / found"

The `ct` binary is on your PATH.
