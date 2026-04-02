# Context

## Item: ci-gg7gp

**Title:** TUI cockpit: add command palette overlay with per-panel action registry
**Status:** in_progress
**Priority:** 2

### Description

Add : keybinding at the cockpit level that opens a searchable overlay listing all actions available in the current panel + selection context. Filter-as-you-type (substring match on action name), arrow navigation, enter to execute, esc to dismiss. Action registry is per-panel: each TUIPanel exposes PaletteActions(droplet *cistern.Droplet) []PaletteAction where PaletteAction has Name string, Description string, Run func() tea.Cmd. Context is the full Droplet struct for the currently selected droplet (nil if no selection). Initial palette is populated in the Droplets action droplets (5a, 5b). Acceptance: pressing : opens the palette, typing filters actions, enter executes.

## Current Step: delivery

- **Type:** agent
- **Role:** delivery

---

<available_skills>
  <skill>
    <name>cistern-github</name>
    <description>Use `gh` CLI for all GitHub operations. Prefer CLI over GitHub MCP servers for lower context usage.</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-github/SKILL.md</location>
  </skill>
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
    ct droplet pass ci-gg7gp

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-gg7gp
    ct droplet recirculate ci-gg7gp --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-gg7gp

Add notes before signaling:
    ct droplet note ci-gg7gp "What you did / found"

The `ct` binary is on your PATH.
