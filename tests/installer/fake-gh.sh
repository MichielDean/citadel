#!/usr/bin/env bash
# fake-gh — stub for the GitHub CLI used in Cistern installer integration tests.
#
# ct doctor checks "gh CLI installed" and "gh authenticated"; this stub
# satisfies both by accepting any arguments and exiting 0 silently.
# Placed at /usr/local/bin/gh so exec.LookPath("gh") resolves it.
exit 0
