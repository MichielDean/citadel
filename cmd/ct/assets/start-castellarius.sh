#!/usr/bin/env bash
# start-castellarius.sh — Cistern Castellarius startup wrapper.
#
# Validates credentials, sources ~/.cistern/env to load them, then launches ct.
# Update ~/.cistern/env with new credentials; the Castellarius picks them up
# on each restart without needing to edit the systemd service drop-in.
#
# Pre-flight: checks that ~/.cistern/env exists and ANTHROPIC_API_KEY is set
# to a non-empty value. Exits 1 with an actionable error message so that
# systemd captures a useful log entry rather than a silent process exit.

CISTERN_ENV="${HOME}/.cistern/env"

# Check 1: credential file must exist.
if [[ ! -f "${CISTERN_ENV}" ]]; then
    echo "cistern: ${CISTERN_ENV} not found — run: ct init" >&2
    exit 1
fi

# Check 2: ANTHROPIC_API_KEY must be set to a non-empty value (not commented out).
if ! grep -qE '^ANTHROPIC_API_KEY=[^[:space:]]+' "${CISTERN_ENV}"; then
    echo "cistern: ANTHROPIC_API_KEY not set in ${CISTERN_ENV} — add your key: echo 'ANTHROPIC_API_KEY=sk-ant-...' >> ${CISTERN_ENV}" >&2
    exit 1
fi

# Export all KEY=VALUE pairs from the env file into the process environment.
set -a
# shellcheck source=/dev/null
. "${CISTERN_ENV}"
set +a

exec ct castellarius start
