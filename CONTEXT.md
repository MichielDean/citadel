# Context

## Item: ci-5wvs2

**Title:** fix: prepareDropletWorktree — reset to origin/main on new worktree creation
**Status:** in_progress
**Priority:** 1

### Description

New worktrees created via 'git worktree add -b feat/<id> <path> origin/main' can still have dirty state if the _primary clone has local modifications (from manual hotfixes, direct edits, etc). After creating the worktree, run 'git reset --hard origin/main && git clean -fd' to guarantee a clean baseline before handing to the agent.

This prevents the dirty worktree loop caused by manual work bleeding into new worktrees.

## Current Step: delivery

- **Type:** agent
- **Role:** delivery

## Recent Step Notes

### From: manual

Fixed directly in scheduler.go — added git reset --hard origin/main + git clean -fd after new worktree creation. Committed inline.

### From: manual

Implemented git reset --hard origin/main + git clean -fd after new worktree creation in prepareDropletWorktree. Added 2 tests: TestPrepareDropletWorktree_NewWorktree_CreatesOnFeatureBranch and TestPrepareDropletWorktree_NewWorktree_ResetsToOriginMain (confirmed failing before fix, passing after). Committed b07873e. All 9 packages pass.

<available_skills>
  <skill>
    <name>github-workflow</name>
    <description>---</description>
    <location>/home/lobsterdog/.cistern/skills/github-workflow/SKILL.md</location>
  </skill>
  <skill>
    <name>cistern-droplet-state</name>
    <description>Manage droplet state in the Cistern agentic pipeline using the `ct` CLI.</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-droplet-state/SKILL.md</location>
  </skill>
</available_skills>

## Signaling Completion

When your work is done, signal your outcome using the `ct` CLI:

**Pass (work complete, move to next step):**
    ct droplet pass ci-5wvs2

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-5wvs2
    ct droplet recirculate ci-5wvs2 --to implement

**Block (genuinely blocked, cannot proceed):**
    ct droplet block ci-5wvs2

Add notes before signaling:
    ct droplet note ci-5wvs2 "What you did / found"

The `ct` binary is on your PATH.
