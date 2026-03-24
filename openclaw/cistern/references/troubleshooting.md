# Cistern Troubleshooting

## Castellarius Not Running

```bash
ct castellarius status
# If stopped:
ct castellarius start
# Or via systemd:
systemctl --user start cistern-castellarius
journalctl --user -u cistern-castellarius -f
```

## Droplet Stuck in a Stage

```bash
ct droplet show <id>          # Check status + last error
ct droplet restart <id>       # Retry the current stage
```

If repeatedly failing, check logs for the specific cataractae:
```bash
journalctl --user -u cistern-castellarius --since "1 hour ago"
```

## Missing Skills (stage does nothing / skipped)

Castellarius loads skills from `~/.cistern/skills/`. If a skill is missing, the stage is skipped silently.

```bash
ls ~/.cistern/skills/          # Check what's installed
# Skills should be in the repo under skills/
```

If skills were added to the repo after your last sync, copy them manually:
```bash
cp -r <worktree>/skills/<skill-name> ~/.cistern/skills/
```

## Binary Out of Date

Castellarius self-restarts when it detects a new binary (mtime check). To force:

```bash
# Rebuild
cd <worktree-path>
PATH="/usr/local/go/bin:$PATH" go build -o ~/go/bin/ct ./cmd/ct/

# Then restart
ct castellarius restart
# or: systemctl --user restart cistern-castellarius
```

**Warning:** Never build from `~/cistern` directly if worktrees are in use — it diverges from origin and corrupts worktree state. Always build from a synced worktree.

## Worktree Corruption

If your worktree has diverged or has unexpected state:

```bash
cd ~/.cistern/sandboxes/cistern/lobsterdog
git status                           # Assess damage
git checkout -B lobsterdog-work origin/main  # Nuke and re-sync (loses local changes)
```

## Drought Protocol Not Running

Drought hooks run during idle periods. If they're not firing:

1. Confirm Castellarius is running: `ct castellarius status`
2. Check if the aqueduct has active droplets (drought only triggers when empty)
3. Check logs: `journalctl --user -u cistern-castellarius | grep drought`

## Claude OAuth Token Expired

Sessions crash immediately with no clear error when the Claude OAuth token is expired. `ct doctor` catches this before it becomes a mystery:

```bash
ct doctor
# ✗ Claude OAuth token: expired 2h15m ago — run 'claude' interactively to refresh
# ✗ service ANTHROPIC_API_KEY: stale — update env.conf with the current token and restart
```

To recover:

1. Run `claude` interactively in a terminal — it will detect the expired token and prompt you to log in again
2. After refreshing, update `~/.cistern/env` with the new key and restart:
   ```bash
   # Edit the ANTHROPIC_API_KEY line:
   nano ~/.cistern/env
   # Then restart the Castellarius to pick up the new value:
   ct castellarius start
   # or: systemctl --user restart cistern-castellarius
   ```
3. Run `ct doctor` again to confirm both checks pass

## Database Issues

The SQLite DB at `~/.cistern/cistern.db` is the source of truth.

Direct inspection (read-only, for diagnostics only):
```bash
sqlite3 ~/.cistern/cistern.db ".tables"
sqlite3 ~/.cistern/cistern.db "SELECT id, title, status FROM droplets ORDER BY created_at DESC LIMIT 10;"
```

For direct status fixes (last resort only):
```bash
sqlite3 ~/.cistern/cistern.db "UPDATE droplets SET status='pending' WHERE id='<id>';"
```
