# Context

## Item: ci-t8sjc

**Title:** Remove Architecti from castellarius scheduler — dead code removal
**Status:** in_progress
**Priority:** 2

### Description

The Architecti autonomous recovery agent has been disabled (config commented out in cistern.yaml) and the decision is to not bake it into the castellarius. Remove all Architecti integration from the scheduler: (1) Delete architecti.go entirely. (2) Remove architecti queue initialization from scheduler.go New()/NewFromParts(). (3) Remove startArchitectiQueue() call from Run(). (4) Remove all tryEnqueueArchitecti() calls from observeRepo(). (5) Remove ArchitectiConfig from aqueduct/types.go. (6) Remove architecti config parsing from aqueduct/parse.go. (7) Update/remove architecti_test.go. (8) Keep the ct architecti run CLI command intact — it may be used as a standalone CLI tool in the future. Acceptance: (1) go build succeeds. (2) All existing tests pass. (3) No references to architecti remain in scheduler.go. (4) ct architecti run still compiles and shows help.

## Current Step: delivery

- **Type:** agent
- **Role:** delivery

## ⚠️ REVISION REQUIRED — Fix these issues before anything else

This droplet was recirculated. The following issues were found and **must** be fixed.
Do not proceed to implementation until you have read and understood each issue.

### Issue 1 (from: reviewer)

No findings. Verified: (1) no orphaned references to removed architecti scheduler types/functions across the codebase, (2) go build succeeds, (3) all tests pass, (4) ct architecti run compiles and shows help, (5) no architecti references remain in scheduler.go, (6) rate-limit removal safe due to note-based escalation still in place, (7) drainInFlight remaining-budget logic correct, (8) RunArchitectiAdHoc signature change reflected at sole call site.

### Issue 2 (from: qa)

♻ Tests pass but architecti_test.go was deleted in full while architecti.go was not — the retained code has zero test coverage.

Specifically: RunArchitectiAdHoc (the public API backing ct architecti run), parseArchitectiOutput, extractJSONArray, buildArchitectiSnapshot, architectiRestartOrEscalate, architectiRestart, architectiCancel, architectiFile, architectiNote, and architectiRestartCastellarius all remain in architecti.go with no tests.

The original file had 8 RunArchitectiAdHoc tests (DryRun, Normal, EmptyActions, ExecError, SnapshotContainsTriggerDropletID, MarkdownWrappedJSON, ParseError, ReturnsFilteredActions_MaxFilesPerRun) and 2 buildArchitectiSnapshot tests. All were deleted.

The requirement says 'Update/remove architecti_test.go' — this means remove tests for deleted scheduler-integration code (tryEnqueueArchitecti, startArchitectiQueue, runArchitecti, rate-limit machinery). Tests for retained functions must be preserved.

Fix: restore the RunArchitectiAdHoc tests, the buildArchitectiSnapshot tests, and coverage for the action dispatchers. The scheduler-queue tests (tryEnqueueArchitecti, startArchitectiQueue, singleton guard, rate-limit) may be deleted as their code was removed.

### Issue 3 (from: reviewer)

♻ 1 finding. Prior QA issue ci-t8sjc-ki6ta resolved (24 tests covering all retained functions, all passing). New finding: internal/aqueduct/validate_test.go contains orphaned minimalValidConfig() helper — its 4 callers were deleted but the function was left behind with zero references.

### Issue 4 (from: reviewer)

Review (2nd cycle): Phase 1 — resolved ci-t8sjc-97kry (validate_test.go confirmed deleted, minimalValidConfig zero references). Phase 2 — fresh adversarial review found no new issues. All removed scheduler fields (architectiQueue, architectiWg, architectiRunning, architectiStuckRouting, architectiRestarts, runArchitectiFn, architectiRestartCastellariusFn) have zero stale references. ArchitectiConfig type fully removed from aqueduct package. Rate-limit removal is consistent with scheduler integration removal — escalation check (prior restart note) remains intact for ad-hoc path. YAML backward compatibility preserved (omitempty + Go yaml ignore-extra-fields). Build: ok. Tests: all 9 packages pass. No orphaned code, no broken contracts, no security issues.

### Issue 5 (from: reviewer)

No findings. Prior issue ci-t8sjc-97kry resolved. Fresh review: clean removal — no orphans, no broken contracts, no security issues. Build and all tests pass.

### Issue 6 (from: qa)

Phase 1: Prior issue 2 (QA) resolved — 20+ tests covering all retained architecti functions. Prior issue 3 (reviewer) resolved — validate_test.go deleted, confirmed absent. Phase 2: Simplifier changes to TestArchitectiAction_Note and TestArchitectiAction_File are correct (t.Fatalf guards slice access before direct index). TestDrainInFlight_SessionDrainUsesRemainingBudget correctly deleted — it tested the removed architectiWg drain. ArchitectiDefaultMaxFilesPerRun exported constant replaces magic number cleanly. Build ok. All 9 packages pass. ct architecti run --help confirmed.

### Issue 7 (from: security)

Phase 1: All 6 prior issues verified resolved — tests for retained functions present (20+ tests), validate_test.go deleted, no orphaned references. Phase 2: Fresh adversarial review found no security issues. Rate-limit removal safe (scheduler path removed; note-based escalation still guards ad-hoc restart path). restartCastellariusFn nil-check + health-file guard intact (fail-closed). architectiExecFn nil-check falls through to defaultArchitectiExec. No new user input surfaces, no injection vectors, no auth changes. Build ok. All tests pass.

### Issue 8 (from: security)

No security issues found. Dead code removal with no new attack surface. Prior issues all resolved. Rate-limit removal safe (note-based escalation intact for ad-hoc path). Health-file fail-closed guard intact. Build and all tests pass.

---

## Recent Step Notes

### From: docs_writer

Updated docs: Architecti standalone tool. Removed references to automatic scheduler triggering and rate limits. Updated commands.md and README.md to clarify manual-invocation-only workflow. Configuration section preserved for backward compatibility.

### From: security

No security issues found. Dead code removal with no new attack surface. Prior issues all resolved. Rate-limit removal safe (note-based escalation intact for ad-hoc path). Health-file fail-closed guard intact. Build and all tests pass.

### From: security

Phase 1: All 6 prior issues verified resolved — tests for retained functions present (20+ tests), validate_test.go deleted, no orphaned references. Phase 2: Fresh adversarial review found no security issues. Rate-limit removal safe (scheduler path removed; note-based escalation still guards ad-hoc restart path). restartCastellariusFn nil-check + health-file guard intact (fail-closed). architectiExecFn nil-check falls through to defaultArchitectiExec. No new user input surfaces, no injection vectors, no auth changes. Build ok. All tests pass.

### From: qa

Phase 1: Prior issue 2 (QA) resolved — 20+ tests covering all retained architecti functions. Prior issue 3 (reviewer) resolved — validate_test.go deleted, confirmed absent. Phase 2: Simplifier changes to TestArchitectiAction_Note and TestArchitectiAction_File are correct (t.Fatalf guards slice access before direct index). TestDrainInFlight_SessionDrainUsesRemainingBudget correctly deleted — it tested the removed architectiWg drain. ArchitectiDefaultMaxFilesPerRun exported constant replaces magic number cleanly. Build ok. All 9 packages pass. ct architecti run --help confirmed.

<available_skills>
  <skill>
    <name>cistern-github</name>
    <description>---</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-github/SKILL.md</location>
  </skill>
  <skill>
    <name>cistern-droplet-state</name>
    <description>Manage droplet state in the Cistern agentic pipeline using the `ct` CLI.</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-droplet-state/SKILL.md</location>
  </skill>
  <skill>
    <name>cistern-git</name>
    <description>---</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-git/SKILL.md</location>
  </skill>
</available_skills>

## Signaling Completion

When your work is done, signal your outcome using the `ct` CLI:

**Pass (work complete, move to next step):**
    ct droplet pass ci-t8sjc

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-t8sjc
    ct droplet recirculate ci-t8sjc --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-t8sjc

Add notes before signaling:
    ct droplet note ci-t8sjc "What you did / found"

The `ct` binary is on your PATH.
