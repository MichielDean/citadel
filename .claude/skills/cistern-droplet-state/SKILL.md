# Cistern Droplet State

Manage droplet state in the Cistern agentic pipeline using the `ct` CLI.

## When to use this skill

Use at the end of every cataracta session to signal your outcome.
You MUST signal before exiting. A cataracta that exits without signaling leaves
the droplet stranded in the cistern.

## Your Droplet ID

Your droplet ID is in CONTEXT.md under `## Item: <id>`. Use it in every command.

## Signaling Commands

### Pass — work complete, ready to flow forward
```bash
ct droplet pass <id> --notes "Summary of what was done and verified."
```

### Recirculate — needs revision, send back upstream
```bash
ct droplet recirculate <id> --notes "Specific issues: 1. <issue> 2. <issue>"
```

### Recirculate to a specific cataracta
```bash
ct droplet recirculate <id> --to implement --notes "Reason for routing to implement."
```

### Block — cannot proceed, needs human intervention
```bash
ct droplet block <id> --notes "Blocked because: <specific reason and what is needed>"
```

### Add a note (without signaling)
```bash
ct droplet note <id> "Intermediate finding or progress update."
```

## Rules

1. Always include `--notes` when signaling — describe what you did or found
2. Never signal pass if required issues remain unresolved
3. Implementer: never push to origin — local commits only
4. Be specific in notes — "Fixed 3 issues in client.go" not "fixed it"
5. If CONTEXT.md has revision notes from prior cycles, address every single one
