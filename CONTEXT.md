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

ARCHITECTURAL REVISION: Castellarius owns branch lifecycle, not agents.

The state machine already knows which aqueduct has which droplet. Extend it to own worktree + branch management:

## Castellarius-managed branch lifecycle

On DISPATCH (Castellarius assigns droplet to aqueduct):
1. Ensure worktree exists for this aqueduct (git worktree add --detach if not present)
2. git worktree prune --expire=0 on the primary clone first
3. Create/checkout feat/<droplet-id> branch in that worktree
4. Record branch assignment in scheduler state

On COMPLETION/RECIRCULATION (Castellarius receives outcome signal):
1. Push the branch if delivery step (already done by delivery agent)
2. Delete local branch from worktree (git branch -D feat/<droplet-id>)
3. Return worktree to detached HEAD state: git checkout --detach HEAD
4. Mark aqueduct slot as free

On STARTUP:
1. Prune stale worktree registrations (git worktree prune --expire=0)
2. Reconcile: for each registered worktree, verify it matches Castellarius DB state
3. Any worktree with a branch not matching an in_progress droplet → reset to detached HEAD

## What agents no longer need to do
- Agents never call git checkout -b or manage branches
- PrepareBranch() moves from sandbox.go to castellarius/scheduler.go
- Agents just work in their working directory — branch is already set up on spawn
- On signal (pass/recirculate/block), Castellarius handles cleanup

## Why this eliminates conflicts
- Castellarius is single-threaded dispatch — it's structurally impossible to assign the same branch to two aqueducts
- No race condition: branch exists and is checked out BEFORE the agent process starts
- No stale registration: Castellarius prunes before every worktree operation

This is cleaner than the current model where agents manage their own git state.

### From: manual

Implemented Option A (git worktrees with --detach) with Castellarius-managed branch lifecycle.

Changes:
- sandbox.go: Added EnsurePrimaryClone (shared object store at _primary/); implemented EnsureWorktree (prunes stale registrations, detects legacy dedicated clones and replaces them, adds worktree --detach); kept EnsureDedicatedClone etc for backward compat
- runner.go: Replaced per-worker EnsureDedicatedClone with single EnsurePrimaryClone + per-worker EnsureWorktree; removed PrepareBranch call from SpawnStep (Castellarius now owns branch lifecycle)
- scheduler.go: Added prepareBranchInSandbox (create or resume feat/<id> branch, called before Spawn) and cleanupBranchInSandbox (detach HEAD + delete branch, called after outcome observed); both guarded by s.sandboxRoot != '' to skip in test environments

Disk model: _primary/ = full clone (~16MB); each aqueduct worktree = working tree only (~4.7MB). 100 aqueducts: ~487MB vs ~1.6GB before.

All 9 test packages pass.

### From: manual

Implemented git worktree shared object store. EnsurePrimaryClone + EnsureWorktree in sandbox.go, runner.go switched from N full clones to 1 primary + N worktrees, Castellarius now owns branch lifecycle via prepareBranchInSandbox/cleanupBranchInSandbox in scheduler.go. All tests pass.

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
