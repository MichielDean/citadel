# Context

## Item: ci-ikbj2

**Title:** Add external_ref column to droplets schema
**Status:** in_progress
**Priority:** 1

### Description

Add 'external_ref' TEXT column to the droplets table in schema.sql and migration. Format: 'provider:key' (e.g. 'jira:DPF-456', 'linear:LIN-789'). Column is nullable - only populated for imported issues. Add ExternalRef field to the Droplet Go struct in client.go. Update all CRUD operations (insert, update, scan) to include external_ref. When external_ref is set, delivery cataractae uses it for: (1) PR branch name feat/DPF-456 instead of feat/ci-xxxx, (2) PR title 'DPF-456: description' instead of 'ci-xxxx: description'. Update the delivery CLAUDE.md and gate logic to check for external_ref.

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
    ct droplet pass ci-ikbj2

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-ikbj2
    ct droplet recirculate ci-ikbj2 --to implement

**Pool (cannot currently proceed):**
    ct droplet pool ci-ikbj2

Add notes before signaling:
    ct droplet note ci-ikbj2 "What you did / found"

The `ct` binary is on your PATH.
