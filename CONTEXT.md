# Context

## Item: ci-japrh

**Title:** Remove Architecti system prompt, docs, and CHANGELOG references
**Status:** in_progress
**Priority:** 2

### Description

Companion to the scheduler code removal droplet. Clean up all non-code Architecti artifacts: (1) Delete cataractae/architecti/SYSTEM_PROMPT.md. (2) Remove Architecti section from README.md. (3) Remove Architecti entries from CHANGELOG.md (or mark as removed). (4) Remove Architecti references from CONTEXT.md. (5) Remove architecti field from cistern.yaml documentation/comments. (6) Update any skill references that mention architecti. Acceptance: grep -r architecti across the repo returns only the ct architecti CLI command code and its tests.

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
    <description>---</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-git/SKILL.md</location>
  </skill>
  <skill>
    <name>cistern-github</name>
    <description>---</description>
    <location>/home/lobsterdog/.cistern/skills/cistern-github/SKILL.md</location>
  </skill>
</available_skills>

## Signaling Completion

When your work is done, signal your outcome using the `ct` CLI:

**Pass (work complete, move to next step):**
    ct droplet pass ci-japrh

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-japrh
    ct droplet recirculate ci-japrh --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-japrh

Add notes before signaling:
    ct droplet note ci-japrh "What you did / found"

The `ct` binary is on your PATH.
