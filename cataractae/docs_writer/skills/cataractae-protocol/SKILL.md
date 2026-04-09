---
name: cataractae-protocol
description: Universal behavioral protocol for all cataractae in the Cistern pipeline. Covers pipeline position awareness, pass/recirculate criteria, discourse conventions, and cycle-cap handling. Injected into every cataractae at generation time.
---

# Cataractae Protocol

## Pipeline Position

Injected at generation time (see companion droplet). Each cataractae receives:
- Its own role and responsibilities
- Who comes immediately before it and what they are responsible for
- Who comes immediately after it and what they expect to receive
- First cataractae: only 'after' context. Last cataractae: only 'before' context.

## Pass Criteria

Only signal pass when:
1. Your own work is complete
2. All open issues you raised have been resolved OR you have accepted a counter-argument closing them
3. All open issues raised against your work by prior cataractae have been addressed

## Recirculate Protocol

Signal recirculate when your work is incomplete. Always include in your note:
- What specifically is unresolved
- What the next cycle should focus on
- Whether you are raising a new issue or responding to an existing one

Never recirculate silently. A recirculate note with no reasoning is invalid.

## Discourse Protocol

Any cataractae may open an issue. Any cataractae may respond to any issue. Issue resolution does not require the original opener — any cataractae with sufficient context may close an issue if the resolution is clear.

Note conventions (always prefix with one of these tags):
- `[issue:<id>]` — opening a new finding (id = short slug, e.g. issue:null-check)
- `[counter-argument:<id>]` — disputing an existing issue; must address the specific concern
- `[verified-fix:<id>]` — confirming a fix is present and correct; closes the issue
- `[rejected-argument:<id>]` — rejecting a counter-argument with specific reasoning
- `[discourse-summary]` — written at cycle cap (see below); summarizes unresolved state

When you receive a `[counter-argument]` on one of your issues: you must engage with it directly in your next cycle. Either accept it (`[verified-fix]`) or reject it (`[rejected-argument]`) with specific reasoning. Ignoring a counter-argument is not permitted.

## Cycle Cap

If an issue has appeared in 4 or more consecutive recirculate cycles without resolution, pool the droplet and write a `[discourse-summary]` note listing: the unresolved issue(s), the positions of each cataractae involved, and what would be needed to resolve it. This is the terminal action — it produces an observable artifact for diagnosis rather than infinite cycling.
