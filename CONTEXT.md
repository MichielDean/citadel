# Context

## Item: ci-vlpsw

**Title:** Add docs cataracta between QA and delivery
**Status:** in_progress
**Priority:** 2

### Description

Add a documentation writer cataracta that runs between qa and delivery. Reviews the diff, finds documentation gaps, updates or creates docs in the repo.

Changes required:

1. cataractae_definitions: add docs_writer role. Instructions: read CONTEXT.md; run git diff main...HEAD; find all .md files; for each changed area (CLI, config, pipeline, architecture) check if docs exist and are accurate; if no user-visible changes pass with 'No documentation updates required'; otherwise update outdated sections, add missing docs, commit with '<id>: docs: update documentation for changes'; signal pass with file list or recirculate with specific ambiguity.

2. cataractae list: insert docs step between qa and delivery. Fields: name=docs, type=agent, identity=docs_writer, model=sonnet, context=full_codebase, skills=[cistern-droplet-state], timeout_minutes=20, skip_for=[1], on_pass=delivery, on_fail=implement, on_recirculate=implement, on_escalate=human

3. Routing: qa on_pass changes delivery -> docs. Trivial complexity skip_cataractae gains docs.

4. Mirror: copy updated aqueduct/feature.yaml to cmd/ct/assets/aqueduct/feature.yaml - both identical.

5. Tests: update any tests referencing cataracta list or pipeline step names.

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
    ct droplet pass ci-vlpsw

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-vlpsw
    ct droplet recirculate ci-vlpsw --to implement

**Block (genuinely blocked, cannot proceed):**
    ct droplet block ci-vlpsw

Add notes before signaling:
    ct droplet note ci-vlpsw "What you did / found"

The `ct` binary is on your PATH.
