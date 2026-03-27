#!/usr/bin/env bash
# start-castellarius.sh — thin wrapper around `ct castellarius start`.
# Used as the ExecStart target in the systemd service unit.
#
# Claude CLI manages its own OAuth credentials via ~/.claude/.credentials.json —
# no ANTHROPIC_API_KEY env var is needed at startup.
set -euo pipefail

exec ct castellarius start
