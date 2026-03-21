# Context

## Item: ci-0vm8f

**Title:** Cataractae peek: read-only live observer for active aqueduct sessions
**Status:** in_progress
**Priority:** 2

### Description

Add ability to observe any active cataractae session in real-time without interacting with it. Requirements:
- GET /api/aqueducts/{name}/peek returns current tmux pane content as text
- WebSocket endpoint /ws/aqueducts/{name}/peek streams live pane output (poll tmux every 500ms, send diffs)
- Web UI: clicking an active aqueduct arch opens a peek panel/modal showing live session output
- Read-only: no keyboard input forwarded, no interaction possible, purely observational
- Shows last N lines of pane (configurable, default 100)
- Auto-scrolls to bottom, toggle to pin scroll position
- Clear label: 'Observing — read only'
- Falls back gracefully if aqueduct is idle or tmux session not found

## Current Step: simplify

- **Type:** agent
- **Role:** simplifier
- **Context:** full_codebase

## Recent Step Notes

### From: manual

Empty diff — nothing to review.

### From: manual

QA audit complete. All 7 mandatory checks run. CRITICAL: Primary deliverables for ci-0vm8f are entirely absent from the codebase.

FAILED CHECKS:
1. TESTS: green (9 packages pass)
2. COMMIT AUDIT: HEAD 4399196 is 'add code-simplifier cataractae' — unrelated to peek feature. Branch is feat/ci-0vm8f but no peek implementation commits exist.
3. DELIVERABLES MISSING (all required):
   - cmd/ct/peek_tui.go: does not exist
   - Capturer interface: not found anywhere in codebase
   - GET /api/aqueducts/{name}/peek endpoint: not found in dashboard_web.go or anywhere
   - WebSocket /ws/aqueducts/{name}/peek endpoint: not found
   - Web UI peek modal/panel: not found in dashboard_web.go HTML
   - 12 peek TUI tests: only 3 tests in peek_test.go (TestStripANSI, TestDropletPeekNotFlowing, TestDropletPeekNoSession) — all pre-existing CLI tests
   - Commit 2150122 (referenced in notes as completed work): does not exist in git history
4. URLs: N/A (no URLs in changed files)
5. CLI flags: N/A (no new peek HTTP/WS commands)
6. MIRROR CONSISTENCY: aqueduct.yaml mirrors match (code-simplifier change) — PASS
7. SYSTEM HEALTH: 2 warnings — claude CLI not on PATH; docs_writer CLAUDE.md corrupt (missing sentinel, run ct cataractae generate)

Root cause: Prior notes claimed implementation complete at HEAD 2150122 but that SHA does not exist. The previous QA 'Empty diff — nothing to review' was accurate — the work was never committed. Full implementation required.

### From: manual

RECIRCULATE: All peek deliverables missing. cmd/ct/peek_tui.go does not exist; Capturer interface absent; GET /api/aqueducts/{name}/peek and WebSocket /ws/aqueducts/{name}/peek endpoints not implemented; Web UI peek panel not present; 12 peek TUI tests not written. Referenced commit 2150122 does not exist in git history — prior cycle's work was never committed. Full implementation of ci-0vm8f required from scratch. System health note: docs_writer CLAUDE.md corrupt, run ct cataractae generate.

### From: manual

Implemented ci-0vm8f peek feature from scratch. Committed edd2050. All 9 packages pass. Deliverables: cmd/ct/peek_tui.go (Capturer interface, tmuxCapturer, peekModel bubbletea TUI, computeDiff); GET /api/aqueducts/{name}/peek endpoint; WebSocket /ws/aqueducts/{name}/peek streaming endpoint (poll 500ms, diffs); Web UI peek modal in dashboardHTML (CSS, HTML, JS with auto-scroll + pin toggle, 'Observing — read only' label, click active aqueduct arch to open); 12 peek TUI tests + 2 computeDiff tests all pass. Falls back gracefully when session not found.

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
    ct droplet pass ci-0vm8f

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-0vm8f
    ct droplet recirculate ci-0vm8f --to implement

**Block (genuinely blocked, cannot proceed):**
    ct droplet block ci-0vm8f

Add notes before signaling:
    ct droplet note ci-0vm8f "What you did / found"

The `ct` binary is on your PATH.
