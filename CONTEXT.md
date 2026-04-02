# Context

## Item: ci-vt6x5

**Title:** TUI cockpit: Droplets panel structural actions via command palette
**Status:** in_progress
**Priority:** 2

### Description

Extend the Droplets panel command palette with structural actions: edit metadata (multi-field sequential overlay for title, priority, complexity, description — maps to ct droplet rename/edit), new droplet creation form (N key or palette, sequential field-per-field input: repo, title, description, complexity), add/remove dependency (droplet ID text input), file issue (description text input), resolve/reject issue (from inline issue list sub-section in detail panel). All multi-field forms use sequential overlay pattern (one field at a time). Acceptance: all actions accessible via palette, correctly invoke underlying ct commands, detail panel shows issue list.

## Current Step: implement

- **Type:** agent
- **Role:** implementer
- **Context:** full_codebase

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
  <skill>
    <name>cistern-github</name>
    <description>Use `gh` CLI for all GitHub operations. Prefer CLI over GitHub MCP servers for lower context usage.</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-github/SKILL.md</location>
  </skill>
</available_skills>

## Signaling Completion

When your work is done, signal your outcome using the `ct` CLI:

**Pass (work complete, move to next step):**
    ct droplet pass ci-vt6x5

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-vt6x5
    ct droplet recirculate ci-vt6x5 --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-vt6x5

Add notes before signaling:
    ct droplet note ci-vt6x5 "What you did / found"

The `ct` binary is on your PATH.
