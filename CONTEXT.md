# Context

## Item: ci-6al33

**Title:** Reduce sandbox disk multiplier: shared object store via git --local clone or proper worktrees
**Status:** in_progress
**Priority:** 2

### Description

Current: N aqueducts per repo = N full independent clones. For a large repo with 5 aqueducts, the disk cost multiplies 5×. At 100 aqueducts this becomes prohibitive.

## Root cause of original worktree failures (commit 60216f8)
The failure was NOT an inherent git worktree limitation. It was two specific bugs:
1. Branch contention: two aqueducts could check out the same branch, causing 'already in use' errors
2. Stale worktree registrations: paths that no longer existed were still registered

Both are fixable. The switch to dedicated clones over-corrected.

## Research results (tested)

### Option A: git worktrees with --detach (RECOMMENDED)
One primary clone per repo. Each aqueduct slot gets a worktree added with --detach:
  git worktree add --detach ~/.cistern/sandboxes/<repo>/<aqueduct>

Working tree is isolated. Branches are checked out per-step (feat/<droplet-id>). No two worktrees ever share a branch. Object store is shared — no duplication.

Disk cost measured:
- Primary clone (ScaledTest): 16 MB (objects + working tree)
- Each additional worktree: 4.7 MB (working tree only — objects shared)
- 100 aqueducts: 16 MB + 100×4.7 MB = ~487 MB (vs current 100×16 MB = 1.6 GB)

Startup fix needed: git worktree prune --expire=0 on every startup to clear stale registrations before adding new ones.

### Option B: git clone --local (object hardlinks)
One reference clone per repo. Additional clones via git clone --local (hardlinks, not copies):
  git clone --local ~/.cistern/sandboxes/<repo>/_ref ~/.cistern/sandboxes/<repo>/<aqueduct>

Disk cost: identical to Option A (4.7 MB per aqueduct). Advantage: completely independent .git dirs — no worktree registration issues. Disadvantage: aqueduct clones must fetch from remote (not reference) because hardlinks are point-in-time, not live.

## Recommended implementation: Option A (worktrees with --detach)

Changes to internal/cataractae/sandbox.go:
1. EnsureDedicatedClone → EnsurePrimaryClone: ensure ~/.cistern/sandboxes/<repo>/_primary/ exists (full clone)
2. EnsureWorktree: ACTUALLY implement this — git worktree add --detach <path> if not already registered; git worktree prune --expire=0 first to clear stale entries
3. PrepareBranch: unchanged — checkout feat/<droplet-id> in the worktree as before
4. On startup in runner.go: prune stale worktrees before registering new ones

Changes to runner.go:
- Replace EnsureDedicatedClone(w.SandboxDir) with EnsurePrimaryClone + EnsureWorktree
- Primary clone dir: filepath.Join(sandboxBase, '_primary')
- Worktree dirs: filepath.Join(sandboxBase, workerName) — same paths as current

Migration: existing dedicated clones at ~/.cistern/sandboxes/<repo>/<aqueduct>/ can stay as-is. On next startup, runner detects no primary clone and converts: treats first aqueduct's existing clone as primary, worktree-adds the rest. Or simpler: just delete sandboxes and let them re-clone. Doctor can detect and suggest migration.

This is backward compatible — worktree directories have the same paths as current dedicated clone directories.

## Current Step: simplify

- **Type:** agent
- **Role:** simplifier
- **Context:** full_codebase

## Recent Step Notes

### From: manual

Committed the 9 test files (sandbox_test.go, branch_lifecycle_test.go) that were written but untracked. All 9 packages pass. HEAD now at 90a5ff8.

### From: manual

Simplified: inlined ensureClone into EnsurePrimaryClone — removed a single-caller indirection layer with a misleading 'shared implementation' comment. Tests: all 9 packages pass.

### From: manual

scheduler.go:369 — cleanupBranchInSandbox runs every time an aqueduct is released (observeRepo, 'if item.Assignee \!= ""' block). It unconditionally deletes feat/<item.ID> from the worktree after each step completes, recirculates, or blocks. prepareBranchInSandbox (scheduler.go:441-448) has a resume path that checks whether the branch already exists and checks it out to preserve prior commits — but that path can never execute in production: the branch is always deleted by cleanup before the next dispatch cycle. TestPrepareBranchInSandbox_ResumeBranch (branch_lifecycle_test.go:279-316) tests a code path that is dead in normal operation. Consequence: multi-revision cycles (recirculate → implement again) always start fresh from origin/main, discarding all prior agent commits. Fix: cleanupBranchInSandbox should only run when the droplet reaches a truly terminal state (final pass through the pipeline), not on every aqueduct release. Or: remove the resume logic and the test, and document that each revision cycle always starts from origin/main (but then the git identity configure + branch-exists check is wasted work on every dispatch).

### From: manual

Fixed cleanupBranchInSandbox to only run at terminal states. Separated pool worker release (always unconditional) from branch cleanup (terminal-only: isTerminal routes + no-route escalation). Non-terminal routes (recirculate, pass-to-next-step) now preserve the feature branch so the same aqueduct can resume incrementally. The resume path in prepareBranchInSandbox is now reachable in production. All 9 packages pass. HEAD: 509aee7.

<available_skills>
  <skill>
    <name>cistern-droplet-state</name>
    <description>Manage droplet state in the Cistern agentic pipeline using the `ct` CLI.</description>
    <location>.claude/skills/cistern-droplet-state/SKILL.md</location>
  </skill>
  <skill>
    <name>code-simplifier</name>
    <description>code-simplifier</description>
    <location>.claude/skills/code-simplifier/SKILL.md</location>
  </skill>
</available_skills>

## Signaling Completion

When your work is done, signal your outcome using the `ct` CLI:

**Pass (work complete, move to next step):**
    ct droplet pass ci-6al33

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-6al33
    ct droplet recirculate ci-6al33 --to implement

**Block (genuinely blocked, cannot proceed):**
    ct droplet block ci-6al33

Add notes before signaling:
    ct droplet note ci-6al33 "What you did / found"

The `ct` binary is on your PATH.
