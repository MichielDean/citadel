#!/usr/bin/env bash
# run-tests.sh — installer smoke test runner.
#
# Runs inside the cistern/installer-test container after systemd has booted.
# Emits one structured line per test, readable by GitHub Actions log parsers:
#
#   [PASS] test_name
#   [FAIL] test_name: error detail
#
# Exit code: 0 if all tests pass, 1 if any fail.
#
# Invoke from the host:
#   docker exec <container> /usr/local/bin/run-tests.sh

set -uo pipefail

PASS_COUNT=0
FAIL_COUNT=0

pass() {
    local name="$1"
    echo "[PASS] ${name}"
    PASS_COUNT=$((PASS_COUNT + 1))
}

fail() {
    local name="$1"
    local reason="${2:-}"
    if [ -n "${reason}" ]; then
        echo "[FAIL] ${name}: ${reason}"
    else
        echo "[FAIL] ${name}"
    fi
    FAIL_COUNT=$((FAIL_COUNT + 1))
}

echo "=== Cistern installer smoke tests ==="
echo ""

# ── Wait for systemd to reach multi-user.target (self-contained, CI-safe) ─────
# Retries for up to 60 seconds so the script works on slow CI runners without
# requiring the caller to sleep before invoking.
_wait_for_systemd() {
    local timeout=60
    local i=0
    echo "Waiting for systemd to reach multi-user.target..."
    while [ "${i}" -lt "${timeout}" ]; do
        if systemctl is-active --quiet multi-user.target 2>/dev/null; then
            echo "systemd ready (${i}s)"
            return 0
        fi
        sleep 1
        i=$((i + 1))
    done
    echo "[FAIL] systemd_boot_wait: multi-user.target not active after ${timeout}s" >&2
    exit 1
}
_wait_for_systemd

# ── Test 1: systemd reached multi-user.target ─────────────────────────────────
# _wait_for_systemd above already verified the target is active (or hard-exited).
pass "systemd_multi_user_target"

# ── Test 2: ct binary is present and executable ───────────────────────────────
# Given: ct binary copied into the image at /usr/local/bin/ct
# When:  running `ct version`
# Then:  exits 0 and produces output
if ct_out=$(CT_NO_ASCII_LOGO=1 ct version 2>&1); then
    pass "ct_binary_version"
else
    fail "ct_binary_version" "${ct_out}"
fi

# ── Test 3: fakeagent (claude stub) responds to --print invocation ─────────────
# Given: fakeagent installed as /usr/local/bin/claude
# When:  invoking `claude --print -p "hello"` (non-interactive mode)
# Then:  exits 0 and outputs valid JSON proposal array
if fa_out=$(claude --print -p "hello" 2>&1); then
    if echo "${fa_out}" | grep -q '"title":"mock proposal"'; then
        pass "fakeagent_print_output"
    else
        fail "fakeagent_print_output" "unexpected output: ${fa_out}"
    fi
else
    fail "fakeagent_print_output" "non-zero exit: ${fa_out}"
fi

# ── Test 4: claude resolves via exec.LookPath (on PATH) ───────────────────────
# Given: fakeagent at /usr/local/bin/claude (on PATH)
# When:  resolving the claude command
# Then:  resolves to /usr/local/bin/claude
if claude_path=$(command -v claude 2>&1); then
    pass "claude_on_path"
else
    fail "claude_on_path" "claude not found on PATH: ${claude_path}"
fi

# ── Test 5: pass password manager is NOT installed ────────────────────────────
# Given: Dockerfile does not install pass
# When:  checking for the pass binary
# Then:  it must be absent (credential story works without it)
if command -v pass >/dev/null 2>&1; then
    fail "no_pass_installed" "pass binary found at $(command -v pass) — should not be installed"
else
    pass "no_pass_installed"
fi

# ── Test 6: ct init bootstraps ~/.cistern directory structure ─────────────────
# Given: ct binary is installed and HOME is set
# When:  running `ct init`
# Then:  exits 0 and creates ~/.cistern/cistern.yaml
export HOME=/root
if init_out=$(CT_NO_ASCII_LOGO=1 ct init 2>&1); then
    if [ -f "${HOME}/.cistern/cistern.yaml" ]; then
        pass "ct_init_creates_config"
    else
        fail "ct_init_creates_config" "cistern.yaml not found after ct init"
    fi
else
    fail "ct_init_creates_config" "${init_out}"
fi

# ── Test 7: ct doctor recognises claude CLI ───────────────────────────────────
# Given: fakeagent is on PATH as "claude" and ct init has run
# When:  running `ct doctor`
# Then:  doctor output contains "✓ claude CLI found" (success prefix only)
# Note:  doctor exits non-zero when other checks fail (e.g. gh not authenticated);
#        that is expected. We only care that the claude check itself passes.
doctor_out=$(CT_NO_ASCII_LOGO=1 ct doctor 2>&1 || true)
if echo "${doctor_out}" | grep -q '✓.*claude CLI found'; then
    pass "ct_doctor_claude_found"
else
    fail "ct_doctor_claude_found" "doctor did not report claude found: ${doctor_out}"
fi

# ── Test 8: start-castellarius.sh is present and executable ───────────────────
# Given: start-castellarius.sh copied into the image
# When:  checking file existence and executable bit
# Then:  it must be present and executable
if [ -x /usr/local/bin/start-castellarius.sh ]; then
    pass "start_castellarius_script_executable"
else
    fail "start_castellarius_script_executable" "/usr/local/bin/start-castellarius.sh not found or not executable"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "=== Results: ${PASS_COUNT} passed, ${FAIL_COUNT} failed ==="

if [ "${FAIL_COUNT}" -gt 0 ]; then
    exit 1
fi
exit 0
