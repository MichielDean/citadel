# Context

## Item: ci-6b62p

**Title:** TUI cockpit: Repos and Skills panel
**Status:** in_progress
**Priority:** 2

### Description

Panel registered as module 7 (key: 7). Two-section panel showing registered repos (ct repo list) and installed skills (ct skills list). Read-only MVP with r to refresh. Acceptance: pressing 7 shows repos and skills; r refreshes.

## Current Step: docs

- **Type:** agent
- **Role:** docs_writer
- **Context:** full_codebase

## ⚠️ REVISION REQUIRED — Fix these issues before anything else

This droplet was recirculated. The following issues were found and **must** be fixed.
Do not proceed to implementation until you have read and understood each issue.

### Issue 1 (from: security)

No security issues found. Diff: (1) repos_skills_panel_tui.go — reads local user-owned config/manifest files, renders to terminal TUI only; no injection vectors (no SQL, shell, HTML), no auth surface, no hardcoded secrets, scroll bounds correctly clamped; (2) cockpit_tui.go — pure UI framework, no input handling beyond key events; (3) dashboard_web.go — internal WebSocket frame buffer fixes, no new attack surface; (4) deletions of audit.go, fakeauditagent, audit_test.go — surface reduction only.

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
    ct droplet pass ci-6b62p

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-6b62p
    ct droplet recirculate ci-6b62p --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-6b62p

Add notes before signaling:
    ct droplet note ci-6b62p "What you did / found"

The `ct` binary is on your PATH.
