#!/usr/bin/env bash
# start-castellarius.sh — thin wrapper around `ct castellarius start`.
# Used as the ExecStart target in the systemd service unit and in the
# installer test image. Passes all arguments through to ct.
#
# Pre-flight: checks that ~/.cistern/env exists and that at least one Claude
# credential source is available. Exits 1 with an actionable error message so
# that systemd captures a useful log entry rather than a silent process exit.
#
# Credential resolution order (performed by ct itself at startup):
#   1. ~/.claude/.credentials.json — OAuth token managed by the Claude CLI.
#      Automatically refreshed when the token rotates; no manual sync needed.
#   2. ANTHROPIC_API_KEY in ~/.cistern/env — fallback for API-key auth setups.
set -euo pipefail

CISTERN_ENV="${HOME}/.cistern/env"
CLAUDE_CREDS="${HOME}/.claude/.credentials.json"

# Check 1: credential file must exist.
if [[ ! -f "${CISTERN_ENV}" ]]; then
    echo "cistern: ${CISTERN_ENV} not found — run: ct init" >&2
    exit 1
fi

# Check 2: at least one Claude credential source must be present.
# Either an OAuth credentials file written by the Claude CLI, or an explicit
# ANTHROPIC_API_KEY entry in the env file.
if ! grep -q '"accessToken"' "${CLAUDE_CREDS}" 2>/dev/null && ! grep -q '^ANTHROPIC_API_KEY=[^ ]' "${CISTERN_ENV}"; then
    echo "cistern: no Claude credentials found — run 'claude' interactively to authenticate, or add ANTHROPIC_API_KEY to ${CISTERN_ENV}" >&2
    exit 1
fi

# Source the env file to load any additional credentials (e.g. GH_TOKEN) into
# the process environment. ANTHROPIC_API_KEY is injected by ct from the OAuth
# credentials file when present, so it need not appear here.
set -a
# shellcheck source=/dev/null
. "${CISTERN_ENV}"
set +a

exec ct castellarius start "$@"
