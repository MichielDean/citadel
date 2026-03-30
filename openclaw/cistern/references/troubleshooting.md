# Cistern Troubleshooting

## Castellarius Not Running or Stalled

**Quick diagnosis:**

```bash
ct doctor                        # Check scheduler health and runtime status
```

This will show three key issues:
- **Health file missing** — Castellarius may not be running
- **Scheduler stale** — Last poll cycle is too old; scheduler may be hung
- **Drought goroutine hung** — Drought hooks running too long

**If Castellarius has stopped:**

```bash
ct castellarius status
# If stopped:
ct castellarius start
# Or via systemd:
systemctl --user start cistern-castellarius
journalctl --user -u cistern-castellarius -f
```

### Castellarius Health Warnings from ct doctor

`ct doctor` monitors the castellarius.health file and warns about three specific issues:

**⚠ castellarius health file missing: is castellarius running?**

The health file doesn't exist, which may indicate Castellarius is not running or is stuck at startup.

```bash
# Check if Castellarius is running:
ct castellarius status

# If stopped, start it:
ct castellarius start

# If running, the file should be created within the poll interval (typically 10-30 seconds)
# Wait and check:
ls -la ~/.cistern/castellarius.health
```

**⚠ castellarius: last tick Xm ago (expected <Ys) — scheduler may be hung**

The last completed poll cycle is older than expected (threshold = 3× the poll interval). This indicates the scheduler may be stuck and not processing droplets.

```bash
# Diagnosis:
cat ~/.cistern/castellarius.health     # Check the health file
journalctl --user -u cistern-castellarius -n 50   # Check recent logs for errors

# If scheduler is hung, restart it:
ct castellarius restart
# or: systemctl --user restart cistern-castellarius
```

**⚠ castellarius: drought goroutine has been running Xm — possible hang**

Drought hooks have been running for more than 10 minutes, suggesting they may be stuck.

```bash
# Check the health file for drought state:
cat ~/.cistern/castellarius.health

# Check logs for what the drought hook is doing:
journalctl --user -u cistern-castellarius -f | grep -i "drought"

# Kill the stuck goroutine by restarting Castellarius:
ct castellarius restart
```

### Castellarius Health File Missing

If `ct castellarius status` displays `last tick: unknown (health file missing)`:

```bash
# This can occur at startup (file is created on first poll cycle) or if the file was removed
# Run ct doctor for diagnosis:
ct doctor

# If Castellarius is running, the health file will be created within the configured
# poll interval (typically 10-30 seconds):
ls -la ~/.cistern/castellarius.health  # Check if file exists
cat ~/.cistern/castellarius.health     # View the raw health data
```

The health file is written after each poll cycle completes. If you see persistent "health file missing" warnings after waiting for several poll intervals, check the Castellarius logs:

```bash
journalctl --user -u cistern-castellarius -f | grep -i "health"
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

### Droplet Repeatedly Failing with "backing off" Messages

If you see logs like `droplet=<id> backing off <seconds>s after <N> consecutive quick exits`, the session is exiting very quickly (≤30 seconds by default). This usually indicates:
- Missing or expired API credentials (ANTHROPIC_API_KEY, etc.)
- Agent binary not found or permission denied
- Provider-side rejection (rate limit, invalid token, service unavailable)

**Diagnosis:**
1. Check the session output: `ct droplet peek <id>` (or `ct droplet peek <id> --snapshot` for completed sessions)
2. Verify credentials are set: `ct doctor` (checks env vars and API keys)
3. Check provider status: if it's a known outage, the Castellarius will detect it and hold all droplets at max backoff

**Provider Degradation:**
If you see `provider=<name> appears degraded — queued droplets will be held at max backoff on next dispatch`, the provider has experienced multiple failures across different aqueducts. The Castellarius backs off all droplets to reduce API hammering while the provider recovers. When the provider recovers (first successful session), backoff resets automatically.

If the provider remains degraded, investigate:
- Is the provider service actually down? Check its status dashboard
- Is authentication stale? Run `ct doctor --fix` to refresh tokens
- Rate limiting? Reduce concurrent aqueducts or add delays in cataractae timeouts

### Cataractae Signaled Recirculate But No on_recirculate Route Configured

If you see a diagnostic note like: `"cataractae 'foo' signaled recirculate but has no on_recirculate route configured"`, the droplet is blocked because an agent incorrectly used `ct droplet recirculate` instead of `ct droplet pass` or `ct droplet pool`.

**Common causes:**
- Agent mistakenly called recirculate when the task was complete (should be `pass`)
- Agent called recirculate to report a blocking issue (should be `pool` with notes)
- Aqueduct config is missing the `on_recirculate` route for this step (configuration error)

**Fix:**
1. Check the droplet notes to understand what the agent intended: `ct droplet show <id>`
2. If the agent's work is complete, approve it: `ct droplet note <id> "Approving..." && ct droplet pass <id>`
3. If there's a real issue blocking the droplet, pool it: `ct droplet pool <id> --notes "..."`
4. If the aqueduct config is wrong, fix the `aqueduct.yaml` routing for that step and recirculate the droplet manually back to the offending step

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

## Drought Goroutine Appears Hung

If a drought hook goroutine gets stuck and doesn't complete:

1. Check status: `ct castellarius status` will show `drought hooks: running (Xm)` where X is the elapsed time
   - If X > 5 minutes, the Castellarius has logged a warning: `"drought goroutine may be hung"`
2. View health file: `cat ~/.cistern/castellarius.health` shows `"droughtRunning": true` and the start time
3. Check recent warnings in logs: `journalctl --user -u cistern-castellarius -n 50 | grep "may be hung"`
4. **Remedy**: Stop and restart the Castellarius to kill the stuck goroutine:
   ```bash
   ct castellarius stop
   ct castellarius start
   # or: systemctl --user restart cistern-castellarius
   ```

## OAuth Token or API Key Expired

Castellarius automatically detects expired or near-expiry credentials and handles them gracefully.

**Claude OAuth token (auto-refresh):** If Castellarius detects that the OAuth token in `~/.claude/.credentials.json` is expired or expiring within 5 minutes, it automatically attempts to refresh it using the stored refresh token. Most expiries are handled transparently without manual intervention.

**Manual token refresh:** If you need to force a refresh (e.g., after an OAuth endpoint issue), run:

```bash
ct doctor --fix     # Automatically refreshes expired Claude OAuth tokens
```

**Credential check:** `ct doctor` verifies both OAuth and API-key credentials:

```bash
ct doctor
# When using claude provider:
# ✓ Claude OAuth token: fresh (expires in 23h45m)
# ✓ env: ANTHROPIC_API_KEY: (fallback available)

# When using other providers:
# ✗ env: OPENAI_API_KEY: not set (codex)
# ✓ env: GEMINI_API_KEY: set
```

**For API key authentication:** Update the respective API key in `~/.cistern/env` and restart the Castellarius:

```bash
# Edit the ANTHROPIC_API_KEY (or other provider key) line:
nano ~/.cistern/env
# Then restart:
ct castellarius restart
# or: systemctl --user restart cistern-castellarius
```

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
