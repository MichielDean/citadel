# Context

## Item: ci-jvgk7

**Title:** Web dashboard: replace block-char arch diagram with CSS-based responsive layout for mobile
**Status:** in_progress
**Priority:** 2

### Description

The web dashboard renders the aqueduct arch diagrams using Unicode block characters (░▒▓≈█▀) copied from the TUI. This is unreadable on mobile — characters render as fragmented pixel art, the layout is fixed-width, and there is no responsive adaptation.

Seen in screenshot from Michiel's phone (375px viewport): arch diagrams show as disconnected gray rectangles, cataractae labels are illegible, animated wave tiles display as dotted patterns, content extends far beyond viewport.

Required fix: the web dashboard must use CSS-based rendering for the arch diagram, not character-based rendering.

## Specific changes

### Arch diagram → CSS flexbox/grid
Replace the monospace character arch with CSS:
- Channel row: a horizontal bar with CSS animation (scrolling gradient, not block chars)
- Piers: CSS boxes (divs) with borders, stacked vertically
- Active pier: green background/border, idle pier: dim border
- Waterfall: CSS animation (gradient + falling effect), not block chars
- Labels: below each pier column, truncated with ellipsis on small screens

### Responsive layout
- viewport meta: already set, but content must actually be responsive
- Max-width: none. Layout must fit the viewport at any width
- On narrow screens (< 480px): show fewer piers per row, or stack aqueducts vertically
- Active aqueduct: show droplet ID + elapsed + progress bar — all text, not char-art
- Idle aqueducts: single compact row (already done, keep this)

### Animated water
Replace ░▒▓≈▒░ scrolling chars with a CSS gradient animation:
- background: linear-gradient(90deg, transparent, #0891b2, transparent)
- background-size: 200% 100%
- animation: wave-scroll 2s linear infinite
- keyframes: from { background-position: 200% 0 } to { background-position: -200% 0 }

### Waterfall
Replace block char shimmer with CSS:
- A vertical gradient strip with opacity animation
- Falls from channel bottom to a pool shape

### Mobile-first sizing
- Font: use rem units, minimum 14px for labels
- Touch targets: at least 44px tall for any interactive element
- Text contrast: minimum 4.5:1 against background

The TUI version remains unchanged — it still uses block chars. Only the web dashboard gets CSS-based rendering. The design language should match (dark theme, same colors, same sections) but implemented with proper web primitives.

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
    <name>github-workflow</name>
    <description>---</description>
    <location>/home/lobsterdog/.cistern/skills/github-workflow/SKILL.md</location>
  </skill>
</available_skills>

## Signaling Completion

When your work is done, signal your outcome using the `ct` CLI:

**Pass (work complete, move to next step):**
    ct droplet pass ci-jvgk7

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-jvgk7
    ct droplet recirculate ci-jvgk7 --to implement

**Block (genuinely blocked, cannot proceed):**
    ct droplet block ci-jvgk7

Add notes before signaling:
    ct droplet note ci-jvgk7 "What you did / found"

The `ct` binary is on your PATH.
