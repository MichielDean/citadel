# Context

<<<<<<< HEAD
## Item: ci-z3jf8

**Title:** TUI cockpit: Status panel with live-refresh
=======
## Item: ci-tj79f

**Title:** TUI cockpit: port Flow/Dashboard panel (dashboardTUIModel) as TUIPanel
>>>>>>> b9cf525 (ci-tj79f: docs: fix cockpit modules list — 'Dashboard' → 'Flow')
**Status:** in_progress
**Priority:** 2

### Description

<<<<<<< HEAD
Read-only panel registered as module 3 (key: 3) rendering ct status output: cistern counts, aqueduct flow summary, castellarius health. Auto-refreshes on a 5-second ticker with idle backoff following dashboardTUIModel pattern. r key force-refreshes. Acceptance: pressing 3 shows current system status; data refreshes automatically; r triggers immediate refresh.
=======
Wrap dashboardTUIModel as a TUIPanel registered in the cockpit as module 2 (key: 2). ct dashboard command remains as a standalone entry point (no breaking change) but now delegates to the same panel model. Acceptance: pressing 2 from the cockpit navigates to the Flow panel showing live aqueduct state; ct dashboard still works as before.
>>>>>>> b9cf525 (ci-tj79f: docs: fix cockpit modules list — 'Dashboard' → 'Flow')

## Current Step: docs

- **Type:** agent
- **Role:** docs_writer
- **Context:** full_codebase

## ⚠️ REVISION REQUIRED — Fix these issues before anything else

This droplet was recirculated. The following issues were found and **must** be fixed.
Do not proceed to implementation until you have read and understood each issue.

### Issue 1 (from: security)

<<<<<<< HEAD
No security issues found. Diff adds a read-only local TUI panel (status panel, module 3) with no network-facing surface. User keyboard input controls only scroll position and a refresh trigger — no user input reaches queries, file paths, or shell calls. fetchDashboardData reads local SQLite (pre-existing, unchanged). Scroll clamping is correct. Refresh loop is rate-limited (5s/30s idle). Dashboard web changes (attach/resize repaint-marker) are internal PTY rendering fixes. Deletion of audit.go reduces attack surface.
=======
No security issues found. Diff: (1) cockpit_tui.go — pure Bubble Tea rendering layer, no network/SQL/shell surface, all display strings hardcoded; (2) dashboard_web.go attach() injects constant repaintMarker bytes, no user input involved; (3) resize() calls frameAccumulate(repaintMarker) inside mutex (correct usage, frameAccumulate expects lock held), cols/rows uint16 flow only to PTY ioctl, existing payload limits and origin checks intact; (4) audit.go deletion reduces attack surface. No injection, auth bypass, secrets exposure, or resource safety issues.
>>>>>>> b9cf525 (ci-tj79f: docs: fix cockpit modules list — 'Dashboard' → 'Flow')

---

<available_skills>
  <skill>
    <name>cistern-droplet-state</name>
    <description>Manage droplet state in the Cistern agentic pipeline using the `ct` CLI.</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-droplet-state/SKILL.md</location>
  </skill>
  <skill>
    <name>cistern-git</name>
    <description>Each droplet has an isolated worktree at `~/.cistern/sandboxes/&lt;repo&gt;/&lt;droplet-id&gt;/`.</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-git/SKILL.md</location>
  </skill>
</available_skills>

## Signaling Completion

When your work is done, signal your outcome using the `ct` CLI:

**Pass (work complete, move to next step):**
<<<<<<< HEAD
    ct droplet pass ci-z3jf8

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-z3jf8
    ct droplet recirculate ci-z3jf8 --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-z3jf8

Add notes before signaling:
    ct droplet note ci-z3jf8 "What you did / found"
=======
    ct droplet pass ci-tj79f

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-tj79f
    ct droplet recirculate ci-tj79f --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-tj79f

Add notes before signaling:
    ct droplet note ci-tj79f "What you did / found"
>>>>>>> b9cf525 (ci-tj79f: docs: fix cockpit modules list — 'Dashboard' → 'Flow')

The `ct` binary is on your PATH.
