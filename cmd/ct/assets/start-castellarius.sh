#!/usr/bin/env bash
# start-castellarius.sh — Cistern Castellarius startup wrapper.
#
# Sources ~/.cistern/env (if present) to load credentials before launching ct.
# Update ~/.cistern/env with new credentials; the Castellarius picks them up
# on each restart without needing to edit the systemd service drop-in.

env_file="${HOME}/.cistern/env"
if [ -f "${env_file}" ]; then
    # Export all KEY=VALUE pairs from the env file into the process environment.
    set -a
    # shellcheck source=/dev/null
    . "${env_file}"
    set +a
fi

exec ct castellarius start
