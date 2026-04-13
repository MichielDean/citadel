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

## Reviewer-Opened Issues and Loop Recovery

When a reviewer cataractae opens an issue on a droplet, only that reviewer can close it. If the implementer recirculates while the issue is still open, the scheduler automatically detects this loop condition and routes the droplet back to the reviewer for verification and closure.

**How loop detection works:**

1. An implementer step recirculates back to itself
2. The scheduler checks if there's an open issue filed by a different cataractae (usually `reviewer`)
3. On the **first recirculation** with an open reviewer issue, a `[scheduler:loop-recovery-pending]` note is added and the droplet recirculates normally
4. On the **second consecutive recirculation** with the same open issue, loop is confirmed and the droplet is automatically routed to the reviewer

**Identifying loop recovery in action:**

Check droplet notes:
```bash
ct droplet show <id>              # Look for [scheduler:loop-recovery*] notes
```

You'll see notes like:
- `[scheduler:loop-recovery-pending] issue=<issue-id> — open reviewer issue found at implement, routing back to implement (cycle 1/2)`
- `[scheduler:loop-recovery] detected implement→implement loop on reviewer issue <issue-id> — routing to reviewer`

**No action required** — this is automatic recovery. The reviewer cataractae will run, verify the implementer's fix, close the issue, and the droplet will advance normally.

**If a droplet stays stuck despite loop recovery:**

This suggests the reviewer issue is not being closed by the reviewer cataractae. Check:
1. Is the reviewer issue still open? `ct droplet issues <id>`
2. Did the reviewer cataractae run? Check its logs: `journalctl --user -u cistern-castellarius --since "10 minutes ago" | grep reviewer`
3. Is there an issue with the reviewer cataractae itself (crashes, missing skills)? Check `ct doctor` and recent logs

## Orphaned In-Progress Droplets

Occasionally a droplet can enter `in_progress` status without being assigned to an aqueduct. This may happen due to:
- Manual database edits or recovery procedures
- Droplet assignments to aqueducts that have since been removed or renamed from the config
- System crashes or rollbacks

**Identifying orphaned droplets:**

View the flow dashboard:

```bash
ct dashboard              # Look for the UNASSIGNED section showing orphaned droplets
ct dashboard --web       # Web view also shows unassigned_items in the JSON response
```

The UNASSIGNED section displays:
- Droplet ID
- Elapsed time since last state change
- Current cataractae (step name, if any)
- Title

**Recovering orphaned droplets:**

Once you see an orphaned droplet, you have three options:

1. **Restart the droplet** to re-enter the pipeline:
    ```bash
    ct droplet restart <id>                       # Returns to open status at the current cataractae
    ct droplet restart <id> --cataractae implement  # Re-enter at a specific cataractae
    ```

2. **Cancel the droplet** if it's no longer needed:
   ```bash
   ct droplet cancel <id> --notes "Orphaned; no longer applicable"
   ```

3. **Pool the droplet** if it requires manual intervention:
   ```bash
   ct droplet pool <id> --notes "Orphaned droplet; awaiting manual recovery decision"
   ```

## Droplet Stuck in a Stage

```bash
ct droplet show <id>          # Check status + last error
ct droplet restart <id>                      # Retry the current stage
ct droplet restart <id> --cataractae review  # Retry at a specific cataractae
```

If repeatedly failing, check logs for the specific cataractae:
```bash
journalctl --user -u cistern-castellarius --since "1 hour ago"
```

### Understanding Scheduler Stall Notes

When a droplet has been inactive for a prolonged period (default: 45 minutes), the Castellarius scheduler detects it as "stalled" and appends a structured stall note. These notes use the prefix `[scheduler:stall]` and include diagnostic signals to help identify why the droplet is not progressing.

**Stall note format:**
```
[scheduler:stall] elapsed=2h30m heartbeat=2026-01-15T10:30:45Z
```
or, if no heartbeat has been emitted:
```
[scheduler:stall] elapsed=2h30m heartbeat=none
```

**Fields explained:**
- `elapsed` — How long the droplet has been inactive (rounded to nearest minute; e.g., `2h30m`, `45m`)
- `heartbeat` — The RFC3339 timestamp of the agent's most recent `ct droplet heartbeat <id>` call, or `none` if no heartbeat has ever been emitted (pre-feature agents or agents that exited before their first heartbeat)

**Rate-limiting:** To prevent log spam from long-stalled droplets, the scheduler writes at most one stall note per hour for the same droplet. If a droplet remains stalled beyond the first detection, no new note is written until 60 minutes have passed since the last note (or until the heartbeat advances, which resets the window).

**What to do when you see stall notes:**
1. Check the `heartbeat` field: if it shows `none`, the agent never emitted a heartbeat — it likely died before starting work. Check exit detection logs and consider restarting:
   - `ct droplet restart <id>` — restart at current cataractae
   - `ct droplet restart <id> --cataractae implement` — restart at a specific cataractae
2. If `heartbeat` shows a timestamp, the agent was alive at that point. Check if the agent session is still running: `ct droplet peek <id>` (shows live session output)
3. A stall note does **not** trigger an automatic respawn — the agent may simply be slow (e.g., waiting on an LLM response). Monitor before acting.
4. If the droplet can proceed, restart it:
   - `ct droplet restart <id>` — restart at current cataractae
   - `ct droplet restart <id> --cataractae review` — restart at a specific cataractae
5. If it's a known limitation or awaiting external resources, pool it: `ct droplet pool <id> --notes "..."`

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

### Droplet Reset With "Session Exited Without Outcome" Note

If you see a droplet note like: `"[scheduler:exit-no-outcome] Session <id> exited without outcome (worker=<name>, cataractae=<step>). [<timestamp>]"`, the Castellarius detected that an agent session exited without signaling an outcome.

**What this means:**
- The agent's tmux session is gone (agent finished and exited, or crashed)
- The agent never called `ct droplet pass/recirculate/pool` before exiting
- The Castellarius heartbeat detected this and checked the DB — no outcome was written, and the cataractae stage hadn't advanced — so it reset the droplet for re-dispatch

**Expected behavior:**
1. The note is added to the droplet history
2. The aqueduct pool slot is released
3. The droplet is reset to `open` status at the current cataractae
4. It will be re-dispatched on the next cycle

**Diagnosis (optional — automatic recovery handles this):**
If you want to understand why the agent exited:
```bash
ct droplet show <id>              # View the note timestamp and cataractae
ct droplet peek <id> --raw        # Read the session log file directly
journalctl --user -u cistern-castellarius --since "1h ago" | grep <id>  # Check scheduler logs
```

**Root cause investigation** (if this happens repeatedly for the same step):
- Check the agent process resource limits: `ulimit -a` in the cataractae environment
- Review provider logs for token exhaustion or quota issues
- Check system memory/disk during the time window from the note timestamp
- Look for patterns (e.g., always fails at the same percentage of work) that suggest a hard limit

**Recovery action:**
No action is needed — the droplet will be re-dispatched automatically. If it keeps hitting the same limit, pool it with `ct droplet pool <id> --notes "..."` and investigate.

### Droplet Pooled With "Spawn-Cycle Limit" Note

If you see a droplet note like: `"spawn-cycle limit: 5 spawns in window with no outcome recorded"`, the Castellarius detected a droplet that spawned multiple times in succession without signaling an outcome. This is an automatic circuit breaker for repeated exits without outcome.

**What this means:**
- The droplet was dispatched and spawned successfully 5 times within a 10-minute window
- Each time, the agent session was killed (by timeout, OOM, or other failure) before calling `ct droplet pass/recirculate/pool`
- This pattern indicates a regression where the agent keeps respawning but cannot make progress
- The Castellarius automatically pooled the droplet to prevent runaway token burn

**Expected behavior:**
1. The spawn-cycle limit note is added to the droplet history
2. The droplet is moved to `pooled` status
3. The agent session is killed to stop token consumption
4. The aqueduct pool slot is released
5. The droplet remains pooled until manually restarted or advanced

**Diagnosis:**
If this happens repeatedly for a specific cataractae:
```bash
ct droplet show <id>              # View the spawn-cycle note and last outcome
ct droplet peek <id> --raw        # Check the agent session log for errors
journalctl --user -u cistern-castellarius --since "1h ago" | grep "spawn-cycle"  # Check scheduler logs
```

**Root cause investigation:**
- Check if the agent is crashing: `ct droplet peek <id> --raw` — look for stack traces or exit messages
- Verify the aqueduct config is correct: `ct aqueduct show` — wrong cataractae or missing skill?
- Check if there's a resource limit being hit: memory, disk, timeout
- Look for regressions in the aqueduct (recent code changes that broke that stage)

**Recovery action:**
Once you've fixed the root cause (fixed the agent code, restored a missing skill, increased timeouts):
```bash
ct droplet restart <id>                         # Resets the spawn-cycle counter and re-dispatches
ct droplet restart <id> --cataractae implement  # Re-dispatch at a specific cataractae
```

If the issue persists after restart, the droplet will be pooled again. Investigate the agent logs before attempting another restart.

### Droplet Reset With "Orphan Recovery" Note

If you see a droplet note like: `"[scheduler:recovery] reset orphaned in_progress droplet to open — no assignee, no active session"`, the Castellarius detected and recovered a droplet that was stuck with no active worker session.

**What this means:**
- The droplet was in `in_progress` status but had an empty `assignee` field
- The droplet had no assignee, so the Castellarius could not identify a tmux session to resume and triggered orphan recovery after the stall threshold elapsed
- The droplet was invisible to any aqueduct and could not make progress
- This typically occurs after Castellarius crash/restart or failed dispatch where the droplet was never assigned to a worker
- The Castellarius heartbeat automatically recovered it (check interval: 30 seconds by default)

**Expected behavior:**
1. The recovery note is added to the droplet history
2. The droplet is reset to `open` status at its current cataractae
3. The assignee and assigned_aqueduct fields are cleared
4. It will be re-dispatched on the next cycle as if freshly queued

**No action needed** — the droplet will be re-dispatched automatically. This recovery prevents permanently stuck droplets after infrastructure events.

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
COMMIT=$(git rev-parse --short HEAD) && PATH="/usr/local/go/bin:$PATH" go build -ldflags "-X main.version=${COMMIT} -X main.commit=${COMMIT}" -o ~/go/bin/ct ./cmd/ct/

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
