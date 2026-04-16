---
name: cistern-diff-reader
description: Diff reading methodology for Cistern review cataractae. Covers empty-diff early exit and the principle of tracing changes to their callers. Use when a cataractae needs to understand what changed in the current droplet. For the diff command itself, see the cistern-git skill.
---

# Cistern Diff Reader

## Get the Diff

Use the merge-base diff command from the cistern-git skill. Always use
merge-base (not two-dot) — it shows only this branch's own changes.

## Empty Diff

If the diff is empty (0 bytes or whitespace only), signal pass immediately.
There is nothing to review.

## Read Beyond the Diff

The diff shows what changed. The codebase shows what depended on it staying the same.

For every function or variable the diff modifies:
1. Find all callers outside the diff: `grep -rn 'funcName' --include='*.go'`
2. For each caller: does it still work correctly?
3. For deletions: what references are now broken?

For deletions of files, imports, or type values, look for:
- Files that import deleted symbols
- Test files whose subject no longer exists
- Code paths that produced a value no longer consumed anywhere

## User-Visible vs Internal Changes

Classify changes to determine what needs documentation or broader review:
- **User-visible**: CLI flags, config options, API contracts, public types, README-level features
- **Internal**: refactors, test-only changes, internal error handling

Only user-visible changes require documentation updates.