# Context

## Item: ci-56nul

**Title:** Dashboard redesign: flow-graph TUI showing droplets moving through the aqueduct
**Status:** in_progress
**Priority:** 2

### Description

Redesign ct dashboard to visualise the pipeline as a horizontal flow graph rather than flat tables. The aqueduct is a pipeline — the TUI should look like one.

## Inspiration
- https://hatchet.run/blog/tuis-are-easy-now (Bubble Tea / Lip Gloss patterns)
- https://github.com/AlexanderGrooff/mermaid-ascii (ASCII flow graphs)

## Core concept
Render each cataracta chain as a left-to-right flow graph with nodes and edges.
Active node (where a droplet currently is) rendered highlighted/filled.
Droplet ID and elapsed time shown below/above the active node.

Example target layout:
  implement ──●──▶ review ──○──▶ qa ──○──▶ delivery ──○──▶ done
                   ↑ virgo
                   ci-s76ho  3m 12s  ████░░░░

When two aqueducts are active, show both rows:
  implement ──●──▶ review ──○──▶ qa ──○──▶ delivery ──○──▶ done
                   ↑ virgo · ci-s76ho  3m 12s
  implement ──○──▶ review ──●──▶ qa ──○──▶ delivery ──○──▶ done
                             ↑ marcia · ci-abc  1m 04s

## Sections (top to bottom)
1. Logo header — cistern_logo_ascii.txt, scaled or condensed to fit terminal width
2. Flow graph — one row per aqueduct (virgo/marcia) showing cataracta chain with active node
3. Cistern counts — ● N flowing  ○ N queued  ✓ N delivered
4. Recent flow — last 5 delivered items with timestamp
5. Footer — q quit  r refresh  ? help

## Implementation notes
- Use Lip Gloss styles for node rendering: filled box for active, hollow for inactive
- Use mermaid-ascii or hand-rolled ASCII arrows for edges (──▶)
- Active node: bold + colour (green flowing, yellow queued, red stagnant)
- Edges between inactive nodes: dim
- Responsive: recalculate layout on terminal resize (Bubble Tea WindowSizeMsg)
- Min width: 100 cols; min height: 24 rows; graceful message if too small
- Keep --html mode working with no regressions

## Remove
- The old box-drawing ╔═╗ border layout
- The flat AQUEDUCTS / CISTERN / RECENT FLOW table layout

## Rules
- No new dependencies beyond what is already in go.mod (bubbletea, lipgloss already present)
- All tests must pass
- dashboard.go is the file to rework

## Current Step: implement

- **Type:** agent
- **Role:** implementer
- **Context:** full_codebase

## Prior Step Notes

### From: manual

Redesigned ct dashboard as horizontal flow graph TUI. Removed old box-drawing border layout and flat table sections. New design: flow graph with ● active / ○ idle nodes and ──▶ edges, aqueduct name prefix on each row, ↑-aligned info line showing droplet ID + elapsed + progress bar. Added Steps []string to CataractaInfo. Updated TUI min size to 100x24. HTML mode unchanged. Added 4 new tests. All tests pass.

### From: manual

dashboard.go renderFlowGraphRow (loop ~line 155): prevStep == ch.Step places ● on the OUTGOING edge from the active step. For Step="review" with steps [implement,review,qa] the code emits implement ──○──▶ review ──●──▶ qa; the spec and correct semantics require implement ──●──▶ review ──○──▶ qa (● on the INCOMING edge, before the active step name). Fix: change the condition to step == ch.Step so the ● connector is written as the incoming edge to the active step; update activeCol to point at the active step name column, not the connector. Same bug in dashboard_tui.go tuiFlowGraphRow (~line 201). Tests were written to match the wrong behavior: TestRenderFlowGraphRow_ActiveStep only checks that ● exists (not its position relative to the active step name); TestRenderFlowGraphRow_PointerAligned checks ● and ↑ are co-aligned (they are — both wrong together). Fix the logic and update the tests to assert ● appears before the active step name.

<available_skills>
  <skill>
    <name>cistern-droplet-state</name>
    <description>Manage droplet state in the Cistern agentic pipeline using the `ct` CLI.</description>
    <location>.claude/skills/cistern-droplet-state/SKILL.md</location>
  </skill>
  <skill>
    <name>github-workflow</name>
    <description>---</description>
    <location>.claude/skills/github-workflow/SKILL.md</location>
  </skill>
</available_skills>

## Signaling Completion

When your work is done, signal your outcome using the `ct` CLI:

**Pass (work complete, move to next step):**
    ct droplet pass ci-56nul

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-56nul
    ct droplet recirculate ci-56nul --to implement

**Block (genuinely blocked, cannot proceed):**
    ct droplet block ci-56nul

Add notes before signaling:
    ct droplet note ci-56nul "What you did / found"

The `ct` binary is on your PATH.
