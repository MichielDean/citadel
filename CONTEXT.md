# Context

## Item: ci-vwi46

**Title:** Add rate limiting to the delivery cataracta API endpoint
**Status:** in_progress
**Priority:** 1

### Description

Prevent abuse of the droplet ingestion endpoint. Apply per-IP and per-token limits with configurable thresholds in cistern.yaml.

## Current Step: implement

- **Type:** agent
- **Role:** implementer
- **Context:** full_codebase

## Recent Step Notes

### From: manual

Phase 2: two issues found.

1. cmd/aqueduct/start.go:229 — Delivery server startup error not propagated. If srv.ListenAndServe fails immediately (port in use, permission denied), the error goes only to stderr; runStart returns nil and the scheduler continues running without the delivery endpoint. The 'listening' message (line 235) is also printed unconditionally before the goroutine runs, so it prints even on bind failure. Fix: open a net.Listener synchronously first and check the error before launching the goroutine, then call srv.Serve(ln).

2. internal/delivery/handler.go:412 — No request body size limit before json.NewDecoder(r.Body).Decode. An authenticated, non-rate-limited client can send an arbitrarily large body, causing unbounded server memory consumption. ReadTimeout (10s) constrains time but not bytes; at 60 req/min per IP an attacker can still consume 60 x body_size bytes/min. Fix: wrap r.Body with http.MaxBytesReader(w, r.Body, maxBytes) before the decode call.

### From: manual

Phase 3 fixes: (1) cmd/aqueduct/start.go: open net.Listener synchronously before goroutine; bind error now propagated and listening message only prints on success. (2) internal/delivery/handler.go: added maxBodyBytes=1MiB constant and http.MaxBytesReader wrapping r.Body before decode, preventing unbounded memory consumption. (3) Added TestHandler_BodyTooLarge covering 400 response on oversized body. All 29 tests pass.

### From: manual

Fixed both Phase 3 issues: (1) net.Listen called synchronously in start.go so bind errors propagate immediately; (2) http.MaxBytesReader(1MiB) applied before body decode in handler.go. TestHandler_BodyTooLarge added. All 29 tests pass.

### From: scheduler

Implement pass rejected: HEAD has not advanced since last review (commit: c002d5e3058452c7277d087718aa04c24bb9df4d). No new commits were found. You must commit your changes before signaling pass.

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
    ct droplet pass ci-vwi46

**Recirculate (needs rework — send back upstream):**
    ct droplet recirculate ci-vwi46
    ct droplet recirculate ci-vwi46 --to implement

**Block (genuinely blocked, cannot proceed):**
    ct droplet block ci-vwi46

Add notes before signaling:
    ct droplet note ci-vwi46 "What you did / found"

The `ct` binary is on your PATH.
