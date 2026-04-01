# Context

## Item: ci-bz0t3

**Title:** Wire cataractae-protocol skill into ct cataractae generate: inject skill + pipeline position
**Status:** in_progress
**Priority:** 2

### Description

Update ct cataractae generate to automatically wire the cataractae-protocol skill (created in companion droplet ci-dtg02) into every generated cataractae directory.

Two changes:

1. Skill injection: when generating a cataractae directory, copy or symlink the cataractae-protocol skill into the cataractae's skills/ directory (same mechanism used for other injected skills). Every cataractae agent then has access to the discourse protocol, pass criteria, and cycle cap rules without any per-cataractae manual config.

2. Pipeline position context injection: during generation, compute each cataractae's position in the workflow (index, predecessor name+role, successor name+role). Inject this into the cataractae's CONTEXT.md or a dedicated PIPELINE_POSITION.md file with the structure:
   - Your role: <name> — <one-line description>
   - Predecessor: <name> — <one-line description> (or 'none — you are first')
   - Successor: <name> — <one-line description> (or 'none — you are last')

This gives every agent the minimal context needed to understand handoff expectations without reading the full workflow definition.

Also update regeneration (ct cataractae generate when cataractae already exist) to refresh PIPELINE_POSITION.md if the workflow changes.

Depends on: ci-dtg02 (skill must exist before it can be injected)

Acceptance criteria: ct cataractae generate produces cataractae directories that include the protocol skill and a correct PIPELINE_POSITION.md; regeneration updates position context correctly; all existing cataractae repos (cistern, ScaledTest, PortfolioWebsite) work correctly after regeneration.

## Current Step: delivery

- **Type:** agent
- **Role:** delivery

## ⚠️ REVISION REQUIRED — Fix these issues before anything else

This droplet was recirculated. The following issues were found and **must** be fixed.
Do not proceed to implementation until you have read and understood each issue.

### Issue 1 (from: reviewer)

♻ 1 finding. (1) readSkillDescription (context.go:390-403) does not skip YAML frontmatter — returns '---' as description for any SKILL.md that has frontmatter (all 5 in the repo do). The injected cataractae-protocol SKILL.md is the primary victim: agents see <description>---</description> instead of the actual description. Test uses frontmatter-free SKILL.md content, masking the bug. Fix: skip lines between opening and closing --- delimiters before scanning for description.

### Issue 2 (from: reviewer)

Phase 1: resolved ci-bz0t3-oqhlg — readSkillDescription correctly skips YAML frontmatter; 6 tests cover the fix. Phase 2: fresh adversarial review of full diff (17 files, +2422/-367 lines) — no findings. Verified: security (XML escaping, no injection), logic (pipeline position, frame accumulation, recirculate-no-route restart), error handling (graceful degradation), concurrency (mutex serialization, generation-guarded timers, buffer cloning), and orphaned code checks. All 4 packages pass.

### Issue 3 (from: qa)

♻ Phase 1 RESOLVED: readSkillDescription correctly skips YAML frontmatter (context.go:401-409); 6 tests confirm the fix.

Phase 2 — 2 findings:

(1) ci-bz0t3-nyf2f: injectProtocolSkill (parse.go:197) comment says 'Returns the destination path and true when copied' but the signature is (string, error) — no boolean return. 'and true' is a stale remnant; the comment misrepresents the return type.

(2) ci-bz0t3-oi5em: writePipelinePositionFile (parse.go:184) writes '- Your role: <identity>' but the spec requires '- Your role: <name> — <one-line description>' (same format as predecessor/successor lines). neighborLine is used for pred/succ but not for self. Tests only assert the name is present, not that a description follows. Fix: read own PERSONA.md and append ' — ' + personaDescription, or use an equivalent of neighborLine for the own step. Update tests to assert the description is included.

### Issue 4 (from: reviewer)

Phase 1: resolved both QA issues — (1) ci-bz0t3-nyf2f: injectProtocolSkill comment now correctly describes (string, error) return, no stale bool reference. (2) ci-bz0t3-oi5em: writePipelinePositionFile uses neighborLine for own role, tests assert description present. Phase 2: fresh adversarial review of full diff (17 files, +2457/-367) — no findings. Verified: security (XML escaping, no injection, step names from config), logic (frameAccumulate marker detection, flushPendingFrame aliasing safety, pipeline position edge cases, recirculate-restart guard), error handling (graceful degradation in skill injection, readSkillDescription, stall note DB fallback), concurrency (mutex serialization, timer generation counter), resource management (no goroutine/timer leaks), and orphaned code (ring buffer properly removed). All 4 packages pass.

### Issue 5 (from: reviewer)

Phase 1: resolved both QA issues (ci-bz0t3-nyf2f, ci-bz0t3-oi5em). Phase 2: fresh adversarial review — no findings. All 4 packages pass.

### Issue 6 (from: qa)

Phase 1: both prior QA issues confirmed resolved — (1) ci-bz0t3-nyf2f: injectProtocolSkill comment at parse.go:203 correctly describes (string, error) return, no stale bool. (2) ci-bz0t3-oi5em: writePipelinePositionFile uses neighborLine(roleStep, ...) for own role, producing name — description format; tests assert description present. Phase 2: fresh adversarial review — no findings. Logic correct across all new functions. Test coverage solid: 6 pipeline-position tests (three-step, single-step, own-role, description-from-persona, automated steps, regeneration), 2 skill-injection tests, 6 readSkillDescription tests (frontmatter skip regression included), 1 integration test for injected-skills ordering. All file operations use real temp dirs — no mock-masking concern. skillDescription fallback via readSkillDescription equivalent to old behavior. All 9 packages pass.

### Issue 7 (from: security)

Phase 1: no open security issues from prior cycles (all 6 prior issues previously resolved or no-findings). Phase 2: fresh adversarial review of full diff (17 files, +2457/-367) — no security issues found. Verified: injection safety (xmlEscape on all dynamic XML output, step names from config not user input, parameterized addNote), data exposure (UnassignedItems same Droplet struct already exposed, json.NewEncoder no XSS), path safety (all paths from config-derived identities + fixed dir names, injectedSkillsForIdentity uses os.ReadDir entries), resource safety (frameAccumulate pending bounded by TUI repaint cycles, colW n>=1 guaranteed, timer lifecycle correct with generation counter), concurrency (mutex serialization, bytes.Clone prevents aliasing), scheduler (guard correct, fail-closed on empty cataractae, DB fallback for rate-limiting across restarts). All 4 packages pass.

---

## Recent Step Notes

### From: docs_writer

Updated docs: CHANGELOG.md (added ci-bz0t3 entry describing skill injection and pipeline position), README.md (documented new PIPELINE_POSITION.md and skills/ directory structure), commands.md (expanded ct cataractae generate description).

### From: security

Phase 1: no open security issues from prior cycles (all 6 prior issues previously resolved or no-findings). Phase 2: fresh adversarial review of full diff (17 files, +2457/-367) — no security issues found. Verified: injection safety (xmlEscape on all dynamic XML output, step names from config not user input, parameterized addNote), data exposure (UnassignedItems same Droplet struct already exposed, json.NewEncoder no XSS), path safety (all paths from config-derived identities + fixed dir names, injectedSkillsForIdentity uses os.ReadDir entries), resource safety (frameAccumulate pending bounded by TUI repaint cycles, colW n>=1 guaranteed, timer lifecycle correct with generation counter), concurrency (mutex serialization, bytes.Clone prevents aliasing), scheduler (guard correct, fail-closed on empty cataractae, DB fallback for rate-limiting across restarts). All 4 packages pass.

### From: qa

Phase 1: both prior QA issues confirmed resolved — (1) ci-bz0t3-nyf2f: injectProtocolSkill comment at parse.go:203 correctly describes (string, error) return, no stale bool. (2) ci-bz0t3-oi5em: writePipelinePositionFile uses neighborLine(roleStep, ...) for own role, producing name — description format; tests assert description present. Phase 2: fresh adversarial review — no findings. Logic correct across all new functions. Test coverage solid: 6 pipeline-position tests (three-step, single-step, own-role, description-from-persona, automated steps, regeneration), 2 skill-injection tests, 6 readSkillDescription tests (frontmatter skip regression included), 1 integration test for injected-skills ordering. All file operations use real temp dirs — no mock-masking concern. skillDescription fallback via readSkillDescription equivalent to old behavior. All 9 packages pass.

### From: reviewer

Phase 1: resolved both QA issues (ci-bz0t3-nyf2f, ci-bz0t3-oi5em). Phase 2: fresh adversarial review — no findings. All 4 packages pass.

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
    ct droplet pass ci-bz0t3

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-bz0t3
    ct droplet recirculate ci-bz0t3 --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-bz0t3

Add notes before signaling:
    ct droplet note ci-bz0t3 "What you did / found"

The `ct` binary is on your PATH.
