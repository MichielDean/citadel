#!/usr/bin/env bash
# start-castellarius.sh — thin wrapper around `ct castellarius start`.
# Used as the ExecStart target in the systemd service unit and in the
# installer test image. Validates required credentials before delegating to ct.
set -euo pipefail

# Pre-flight credential check.
# ANTHROPIC_API_KEY is forwarded into every agent session; without it every
# dispatch would fail silently. Fail fast here with an actionable message instead.
if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
    echo "ERROR: missing credentials — ANTHROPIC_API_KEY is not set." \
         "Set it in ~/.cistern/env before starting the Castellarius." >&2
    exit 1
fi

# Check for an expired Claude OAuth token when the credentials file is present.
# An expired token causes authentication errors at agent dispatch time; detecting
# it at startup surfaces an actionable message rather than a silent failure.
_creds="${HOME:-/root}/.claude/.credentials.json"
if [ -f "${_creds}" ] && command -v python3 >/dev/null 2>&1; then
    _expired=$(python3 -c "
import json, sys, time
try:
    d = json.load(open(sys.argv[1]))
    ea = d.get('claudeAiOauth', {}).get('expiresAt', 0)
    if ea > 0 and ea < int(time.time() * 1000):
        print('expired')
except Exception:
    pass
" "${_creds}" 2>/dev/null || true)
    if [ "${_expired:-}" = "expired" ]; then
        echo "ERROR: invalid or expired token — Claude OAuth token has expired." \
             "Run 'ct doctor --fix' or 'claude' interactively to refresh." >&2
        exit 1
    fi
fi

exec ct castellarius start "$@"
