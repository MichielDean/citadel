# Context

## Item: ci-wng6e

**Title:** TUI cockpit: define TUIPanel interface and cockpit root model with persistent nav sidebar
**Status:** in_progress
**Priority:** 2

### Description

Introduce the TUIPanel interface: Init() tea.Cmd, Update(tea.Msg) (tea.Model, tea.Cmd), View() string, Title() string, KeyHelp() string, PaletteActions(droplet *cistern.Droplet) []PaletteAction. Implement cockpitModel as the new root Bubble Tea model launched by ct tui. Renders a persistent left-column nav sidebar listing all modules (lazygit-style). Number keys 1-9 jump directly to modules. Arrow keys navigate the sidebar when no module is active. Enter activates the focused module; the active panel occupies the right pane. Ships with placeholder not-yet-implemented views for all panels except the first. ct tui now launches cockpitModel. No breaking change to existing behavior. Acceptance: ct tui opens a two-pane layout with a persistent sidebar and placeholder panels for all planned modules.

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
    ct droplet pass ci-wng6e

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-wng6e
    ct droplet recirculate ci-wng6e --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-wng6e

Add notes before signaling:
    ct droplet note ci-wng6e "What you did / found"

The `ct` binary is on your PATH.
