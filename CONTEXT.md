# Context

## Item: ci-07t3j

**Title:** fix: /ws/tui — run TUI in a PTY so xterm.js renders the real Bubble Tea dashboard
**Status:** in_progress
**Priority:** 2

### Description

The current /ws/tui WebSocket endpoint pipes RunDashboard() (plain text renderer) to xterm.js. This produces the older monochrome text output, not the Bubble Tea TUI with aqueduct arches, colors, and animations.

The correct approach: run RunDashboardTUI() inside a PTY and pipe the PTY output to xterm.js. Bubble Tea requires a real PTY to render properly — it won't output ANSI correctly to a plain io.Writer.

## Implementation

Add github.com/creack/pty to go.mod (MIT license, widely used, minimal dependency).

Replace the /ws/tui handler body in newDashboardMux:

  1. Start RunDashboardTUI in a goroutine attached to a PTY:
       ptmx, err := pty.Start(exec.Command(os.Args[0], "dashboard")) 
     OR since we can't exec ourselves cleanly, use pty.Open() + run the TUI directly:
       ptmx, tty, err := pty.Open()
       go RunDashboardTUI(cfgPath, dbPath) -- but wired to tty's fd
     
     The cleanest approach: use os/exec to run 'ct dashboard' as a subprocess attached to the PTY:
       cmd := exec.Command(os.Args[0], "dashboard", "--db", dbPath)
       ptmx, err := pty.Start(cmd)
     Then pipe ptmx reads → WebSocket frames.

  2. On WebSocket close: kill the subprocess, close the PTY.

  3. Set PTY window size to match xterm.js terminal size. xterm.js sends resize events — handle them by calling pty.Setsize().

## Notes
- os.Args[0] is the ct binary itself — this is safe and self-contained
- The subprocess gets its own PTY so Bubble Tea renders fully
- No changes to RunDashboardTUI needed
- creack/pty is the standard Go PTY library (used by VS Code server, ttyd, etc.)
- Remove the RunDashboard (plain text) wiring from the current /ws/tui handler

## Current Step: simplify

- **Type:** agent
- **Role:** simplifier
- **Context:** full_codebase

## Recent Step Notes

### From: scheduler

Dispatch blocked: worktree has uncommitted files from a prior session: md/ct/dashboard_web.go, go.mod, go.sum. These must be committed or discarded before proceeding.

### From: scheduler

Dispatch blocked: worktree has uncommitted files from a prior session: md/ct/dashboard_web.go, go.mod, go.sum. These must be committed or discarded before proceeding.

### From: scheduler

Dispatch blocked: worktree has uncommitted files from a prior session: md/ct/dashboard_web.go, go.mod, go.sum. These must be committed or discarded before proceeding.

### From: scheduler

Dispatch blocked: worktree has uncommitted files from a prior session: md/ct/dashboard_web.go, go.mod, go.sum. These must be committed or discarded before proceeding.

<available_skills>
  <skill>
    <name>cistern-droplet-state</name>
    <description>Manage droplet state in the Cistern agentic pipeline using the `ct` CLI.</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-droplet-state/SKILL.md</location>
  </skill>
  <skill>
    <name>code-simplifier</name>
    <description>code-simplifier</description>
    <location>/home/lobsterdog/.cistern/skills/code-simplifier/SKILL.md</location>
  </skill>
</available_skills>

## Signaling Completion

When your work is done, signal your outcome using the `ct` CLI:

**Pass (work complete, move to next step):**
    ct droplet pass ci-07t3j

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-07t3j
    ct droplet recirculate ci-07t3j --to implement

**Block (genuinely blocked, cannot proceed):**
    ct droplet block ci-07t3j

Add notes before signaling:
    ct droplet note ci-07t3j "What you did / found"

The `ct` binary is on your PATH.
