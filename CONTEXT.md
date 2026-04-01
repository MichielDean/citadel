# Context

## Item: ci-vhn73

**Title:** Scheduler: restart at implement when cataractae signals recirculate with no route
**Status:** in_progress
**Priority:** 2

### Description

When a cataractae signals recirculate but the workflow has no on_recirculate route defined for it, the scheduler currently logs a warning and auto-promotes to pass. This silently advances a droplet that the agent explicitly did not want to pass, masking the problem.

Fix: instead of auto-promoting, restart the droplet at implement (the first cataractae in the workflow). Write a single structured note: '[scheduler:routing] cataractae=X signaled recirculate but has no on_recirculate route — restarting at implement'. This ensures the work is re-attempted rather than silently skipped, and the note makes the routing anomaly visible in ct droplet show for later analysis.

Do not pool or cancel — the agent may have recirculated for a legitimate reason that implement can address. Restarting at implement is the safe recovery path.

Acceptance criteria: a recirculate signal with no route restarts the droplet at implement rather than advancing it; exactly one structured routing note is written; the droplet flows normally from implement.

## Current Step: docs

- **Type:** agent
- **Role:** docs_writer
- **Context:** full_codebase

## ⚠️ REVISION REQUIRED — Fix these issues before anything else

This droplet was recirculated. The following issues were found and **must** be fixed.
Do not proceed to implementation until you have read and understood each issue.

### Issue 1 (from: reviewer)

No findings. The scheduler change correctly replaces auto-promote with restart-at-implement: the guard condition handles recirculate with no on_recirculate route by reading wf.Cataractae[0].Name, writes exactly one structured [scheduler:routing] note, and falls through to the normal Assign path. The len(wf.Cataractae)>0 guard is defensive (workflow validation rejects empty cataractae). The removed diagnostic note in the pool fallthrough is correctly dead code since recirculate now always resolves to a step. Three tests verify the core behavior: with on_pass present, without on_pass, and with a custom workflow. All tests pass.

### Issue 2 (from: qa)

♻ Tests pass but two test quality gaps require fixing:

1. TestTick_RecirculateNoOnRecirculateOrPassRoute_RestartsAtImplement has no note assertion. The test checks no-pooling and restart-at-implement but never verifies that the [scheduler:routing] note is written. The acceptance criterion requires the note in all recirculate-with-no-route cases. Add the same note assertion present in Tests 1 and 3: loop client.attached, assert strings.Contains(n.notes, "[scheduler:routing]") for the droplet.

2. Tests 1 and 3 comment 'Exactly one structured routing note must be attached' but assert only a boolean (at least one). The requirement explicitly says 'exactly one'. Change the note-counting logic from a bool flag to a counter and assert count == 1 so a regression that writes two notes is caught.

### Issue 3 (from: reviewer)

Phase 1: both QA issues verified fixed — (1) ci-vhn73-dsx3y: Test 2 now has note assertion with noteCount==1, (2) ci-vhn73-veoog: all three tests use counter, assert noteCount==1. Phase 2: fresh adversarial review of scheduler.go and scheduler_test.go changes — no new findings. Guard condition correct, note written exactly once, dead code properly removed, all three tests pass.

### Issue 4 (from: qa)

♻ Phase 1: both prior QA issues confirmed resolved — all three tests use noteCount counter asserting noteCount==1, and Test 2 now has the note assertion. Phase 2 finding (ci-vhn73-penkv): all three recirculate tests have implement (step 0) as the recirculating cataractae. The code does wf.Cataractae[0].Name — but since all tests have the current step == step 0, no test distinguishes this from 'restart at current step.' Add a test with a non-first step (e.g. qa) recirculating with no on_recirculate route, asserting restart lands at implement (step 0), not qa.

### Issue 5 (from: reviewer)

Phase 1: ci-vhn73-penkv resolved — TestTick_RecirculateNonFirstStep_RestartsAtImplementNotCurrentStep (scheduler_test.go:775) uses qa (step 1) as the recirculating step, asserts restart lands at implement (step 0) not qa, proving wf.Cataractae[0].Name usage. Phase 2: fresh adversarial review of scheduler.go and scheduler_test.go changes — guard condition correct (next=="" && ResultRecirculate && len>0), note written exactly once, dead auto-promote code properly removed, downstream Assign path correct, all 4 recirculate tests pass. Pre-existing flaky test (TestGracefulShutdown_CleanDrain) confirmed flaky on main too — not a regression.

### Issue 6 (from: reviewer)

No findings.

### Issue 7 (from: qa)

Phase 1: both prior QA issues confirmed resolved — (1) all three tests use noteCount counter asserting noteCount==1, (2) TestTick_RecirculateNoOnRecirculateOrPassRoute_RestartsAtImplement has note assertion, (3) TestTick_RecirculateNonFirstStep_RestartsAtImplementNotCurrentStep at :775 uses qa (step 1) and correctly asserts restart at implement (step 0). Phase 2: fresh adversarial review — guard condition correct, note written exactly once, dead diagnostic-note code properly removed, wf.Cataractae[0].Name used dynamically, all 4 recirculate tests pass, full suite passes.

### Issue 8 (from: security)

Phase 1: no open security issues from prior cycles (7 prior issues all previously resolved or no-findings). Phase 2: fresh adversarial review of scheduler.go and dashboard changes — no security issues found. Scheduler: guard condition correct (next=="" && ResultRecirculate && len>0), step names from workflow config (not user input), note written via parameterized addNote, fail-closed on empty cataractae (falls to pool). Dashboard: UnassignedItems exposes same Droplet struct already in CisternItems/PooledItems (no new data exposure), web endpoint uses json.NewEncoder (no XSS), colW division safe (n≥1 guaranteed by existing guard).

---

## Recent Step Notes

### From: security

Phase 1: no open security issues from prior cycles (7 prior issues all previously resolved or no-findings). Phase 2: fresh adversarial review of scheduler.go and dashboard changes — no security issues found. Scheduler: guard condition correct (next=="" && ResultRecirculate && len>0), step names from workflow config (not user input), note written via parameterized addNote, fail-closed on empty cataractae (falls to pool). Dashboard: UnassignedItems exposes same Droplet struct already in CisternItems/PooledItems (no new data exposure), web endpoint uses json.NewEncoder (no XSS), colW division safe (n≥1 guaranteed by existing guard).

### From: qa

Phase 1: both prior QA issues confirmed resolved — (1) all three tests use noteCount counter asserting noteCount==1, (2) TestTick_RecirculateNoOnRecirculateOrPassRoute_RestartsAtImplement has note assertion, (3) TestTick_RecirculateNonFirstStep_RestartsAtImplementNotCurrentStep at :775 uses qa (step 1) and correctly asserts restart at implement (step 0). Phase 2: fresh adversarial review — guard condition correct, note written exactly once, dead diagnostic-note code properly removed, wf.Cataractae[0].Name used dynamically, all 4 recirculate tests pass, full suite passes.

### From: reviewer

No findings.

### From: reviewer

Phase 1: ci-vhn73-penkv resolved — TestTick_RecirculateNonFirstStep_RestartsAtImplementNotCurrentStep (scheduler_test.go:775) uses qa (step 1) as the recirculating step, asserts restart lands at implement (step 0) not qa, proving wf.Cataractae[0].Name usage. Phase 2: fresh adversarial review of scheduler.go and scheduler_test.go changes — guard condition correct (next=="" && ResultRecirculate && len>0), note written exactly once, dead auto-promote code properly removed, downstream Assign path correct, all 4 recirculate tests pass. Pre-existing flaky test (TestGracefulShutdown_CleanDrain) confirmed flaky on main too — not a regression.

<available_skills>
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
    ct droplet pass ci-vhn73

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-vhn73
    ct droplet recirculate ci-vhn73 --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-vhn73

Add notes before signaling:
    ct droplet note ci-vhn73 "What you did / found"

The `ct` binary is on your PATH.
