#!/usr/bin/env bash
# start-castellarius.sh — thin wrapper around `ct castellarius start`.
# Used as the ExecStart target in the systemd service unit and in the
# installer test image. Passes all arguments through to ct.
#
# Pre-flight: checks that ~/.cistern/env exists and ANTHROPIC_API_KEY is set
# to a non-empty value. Exits 1 with an actionable error message so that
# systemd captures a useful log entry rather than a silent process exit.
set -euo pipefail

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

# Source the env file to load credentials into the process environment.
set -a
# shellcheck source=/dev/null
. "${CISTERN_ENV}"
set +a

exec ct castellarius start "$@"
