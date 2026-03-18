# Context

## Item: ci-s76ho

**Title:** ct droplet add --refine: LLM-assisted intake that refines rough ideas into well-specified droplets
**Status:** in_progress
**Priority:** 2

### Description

Add a --refine flag to 'ct droplet add' that runs a Claude reasoning pass before creating the droplet(s).

## Behaviour

### Interactive mode (default with --refine)
1. User provides a rough idea via --title (and optionally --description)
2. Claude reasons (extended thinking) about scope, acceptance criteria, complexity,
   and whether the idea should be split into multiple focused droplets
3. Presents one or more draft droplets: title, description, complexity, suggested deps
4. User can iterate ('too big', 'focus just on X', 'split differently')
5. User confirms each → added to the cistern

### Non-interactive mode (--refine --yes)
Same reasoning pass, but skips confirmation and adds all proposed droplets immediately.
Designed for agent/script use — Lobsterdog or a CI hook can call this to turn rough
ideas into well-specified droplets without human input.

## Implementation

### New flags on 'ct droplet add'
  --refine          enable LLM-assisted refinement before creating
  --yes             skip confirmation prompts (for non-interactive/agent use)

### LLM integration
Use github.com/anthropics/anthropic-sdk-go with extended thinking enabled
(budget_tokens ~8000). ANTHROPIC_API_KEY already in environment.

The system prompt must include:
- Cistern vocabulary (droplet, aqueduct, cataracta, complexity levels)
- Complexity guide: trivial (typo/config), standard (single feature), full (multi-system), critical (breaking change)
- Output format: JSON array of droplet proposals, each with title/description/complexity/depends_on

### Splitting
If the idea is too large for one droplet, the LLM should propose multiple focused
droplets and suggest depends_on relationships between them (using the dependency
blocking system from PR #28).

### Interactive TUI
Use Bubble Tea (already in go.mod) for the confirmation/iteration loop:
- Show each proposed droplet with title, description, complexity badge
- Keys: Enter to confirm, e to edit, s to skip, q to quit
- After iteration, show a summary of what will be added and prompt for final confirm

### Output
Non-interactive mode prints the added droplet IDs to stdout so callers can
chain commands: ct droplet add --refine --yes --title '...' | xargs ct droplet deps ...

## Rules
- --refine without --title should print usage error (idea required)
- If ANTHROPIC_API_KEY is not set, print clear error pointing to 'pass anthropic/claude'
- All existing 'ct droplet add' behaviour must be preserved when --refine is not used
- Tests: unit test the JSON parsing and proposal extraction; integration tests can be skipped

## Current Step: implement

- **Type:** agent
- **Role:** implementer
- **Context:** full_codebase

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
    ct droplet pass ci-s76ho

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-s76ho
    ct droplet recirculate ci-s76ho --to implement

**Block (genuinely blocked, cannot proceed):**
    ct droplet block ci-s76ho

Add notes before signaling:
    ct droplet note ci-s76ho "What you did / found"

The `ct` binary is on your PATH.
