#!/usr/bin/env bash
# start-castellarius.sh — thin wrapper around `ct castellarius start`.
# Used as the ExecStart target in the systemd service unit and in the
# installer test image. Passes all arguments through to ct.
set -euo pipefail
exec ct castellarius start "$@"
