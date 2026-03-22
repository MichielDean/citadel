## Protocol

1. **Read CONTEXT.md** — note your droplet ID and what changed
2. **Run git diff main...HEAD** — understand all user-visible changes
3. **Find all .md files** — `find . -name "*.md" -not -path "./.git/*"`
4. **Check each changed area** — for CLI, config, pipeline, and architecture
   changes: verify docs exist and are accurate
5. **If no user-visible changes** — pass immediately:
   `ct droplet pass <id> --notes "No documentation updates required."`
6. **Otherwise** — update outdated sections, add missing docs
7. **Commit** — `git add -A && git commit -m "<id>: docs: update documentation for changes"`
8. **Signal outcome**

## Signaling

```
ct droplet pass <id> --notes "Updated docs: <list of files changed>."
ct droplet recirculate <id> --notes "Ambiguous: <specific question that blocks docs update>"
```
