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
    fail "systemd_boot_wait" "multi-user.target not active after ${timeout}s"
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
if type -P pass >/dev/null 2>&1; then
    fail "no_pass_installed" "pass binary found at $(type -P pass) — should not be installed"
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

# ── Integration test scenario helpers ────────────────────────────────────────
#
# Each scenario is a self-contained block that resets container state,
# sets up specific preconditions, and asserts expected outcomes.

# _reset_scenario_state — stop and clear all cistern state between scenarios.
_reset_scenario_state() {
    systemctl stop cistern-castellarius.service 2>/dev/null || true
    systemctl reset-failed cistern-castellarius.service 2>/dev/null || true
    rm -rf "${HOME}/.cistern"
    rm -rf "${HOME}/.claude"
}

# _install_skill_stubs — create stub SKILL.md files for skills referenced in
# the default aqueduct workflow. Satisfies both ct doctor and
# ct castellarius start validation without requiring network access.
_install_skill_stubs() {
    local _skills="cistern-droplet-state cistern-git cistern-github code-simplifier critical-code-reviewer adversarial-reviewer"
    for _skill in ${_skills}; do
        mkdir -p "${HOME}/.cistern/skills/${_skill}"
        printf "# Test stub for %s\n" "${_skill}" > "${HOME}/.cistern/skills/${_skill}/SKILL.md"
    done
}

# _wait_service_state — poll until the service reaches the expected state or
# the timeout (seconds) expires. Returns 0 on success, 1 on timeout.
_wait_service_state() {
    local _service="${1}" _expected="${2}" _timeout="${3:-20}"
    local _i=0 _state
    while [ "${_i}" -lt "${_timeout}" ]; do
        _state=$(systemctl is-active "${_service}" 2>/dev/null) || true
        if [ "${_state}" = "${_expected}" ]; then
            return 0
        fi
        sleep 1
        _i=$((_i + 1))
    done
    return 1
}

# ── Scenario 1: Fresh install ─────────────────────────────────────────────────
# Given: no ~/.cistern present (clean state)
# When:  ct init is run and the castellarius service is started with valid credentials
# Then:  the service reaches active (running) state; ct doctor exits 0
echo ""
echo "=== Scenario: Fresh install ==="

_reset_scenario_state

# Given: no ~/.cistern present (verified by _reset above).

# When: run ct init to bootstrap the directory structure.
if ! CT_NO_ASCII_LOGO=1 ct init >/dev/null; then
    fail "fresh_install_ct_init" "ct init failed"
else
    # Install skill stubs so ct castellarius start and ct doctor pass validation.
    _install_skill_stubs

    # Write valid credentials into the env file loaded by the service unit.
    echo "ANTHROPIC_API_KEY=sk-ant-test-fresh-install" > "${HOME}/.cistern/env"

    # Start the service (it loads ~/.cistern/env via EnvironmentFile and validates
    # credentials in start-castellarius.sh before launching ct castellarius start).
    systemctl daemon-reload
    systemctl start cistern-castellarius.service 2>/dev/null || true

    # Then: service reaches active (running) state.
    # Allow 1 second after active for ct castellarius start to initialise the DB.
    if _wait_service_state cistern-castellarius.service active 20; then
        sleep 1
        pass "fresh_install_service_active"

        # Then: ct doctor exits 0 with all checks satisfied.
        _doctor_out=$(ANTHROPIC_API_KEY=sk-ant-test-fresh-install \
                      CT_NO_ASCII_LOGO=1 ct doctor 2>&1) && _doctor_exit=0 || _doctor_exit=$?
        if [ "${_doctor_exit}" -eq 0 ]; then
            pass "fresh_install_ct_doctor_passes"
        else
            fail "fresh_install_ct_doctor_passes" "ct doctor exited non-zero: ${_doctor_out}"
        fi
    else
        _svc_state=$(systemctl is-active cistern-castellarius.service 2>/dev/null || true)
        _log=$(journalctl -u cistern-castellarius.service -n 5 --no-pager 2>/dev/null || true)
        fail "fresh_install_service_active" "service state=${_svc_state}: ${_log}"
    fi
fi

# ── Scenario 2: Missing credentials ──────────────────────────────────────────
# Given: ct init has succeeded; ~/.cistern/env is absent
# When:  the castellarius service is started; ct doctor is run
# Then:  service fails and logs a "missing credentials" error (not silent crash);
#        ct doctor exits non-zero with a message referencing missing credentials
echo ""
echo "=== Scenario: Missing credentials ==="

_reset_scenario_state

if ! CT_NO_ASCII_LOGO=1 ct init >/dev/null; then
    fail "missing_creds_ct_init" "ct init failed"
else
    _install_skill_stubs

    # Given: ~/.cistern/env is absent — no credentials file.
    # (already absent after _reset_scenario_state; not written here)

    # Record time so we can filter journal to this scenario's run.
    _since=$(date --iso-8601=seconds 2>/dev/null || date -u +"%Y-%m-%dT%H:%M:%S+00:00")

    # When: start service (EnvironmentFile absent → ANTHROPIC_API_KEY unset →
    # start-castellarius.sh exits 1 with "missing credentials" message).
    systemctl start cistern-castellarius.service 2>/dev/null || true

    # Then: service fails (hits StartLimitBurst after repeated credential errors).
    if _wait_service_state cistern-castellarius.service failed 25; then
        _log=$(journalctl -u cistern-castellarius.service --since "${_since}" \
               -n 20 --no-pager 2>/dev/null || true)
        if echo "${_log}" | grep -qi "missing credentials\|ANTHROPIC_API_KEY"; then
            pass "missing_creds_service_logged_error"
        else
            fail "missing_creds_service_logged_error" \
                "service failed but log does not mention missing credentials: ${_log}"
        fi
    else
        _svc_state=$(systemctl is-active cistern-castellarius.service 2>/dev/null || true)
        fail "missing_creds_service_logged_error" \
            "service did not enter failed state (state=${_svc_state})"
    fi

    # Then: ct doctor exits non-zero with a credential-related error.
    # Run without ANTHROPIC_API_KEY in the environment so the env-var check fires.
    _doctor_out=$(env -u ANTHROPIC_API_KEY CT_NO_ASCII_LOGO=1 ct doctor 2>&1) && _doctor_exit=0 || _doctor_exit=$?
    if echo "${_doctor_out}" | grep -qi "ANTHROPIC_API_KEY\|missing credentials"; then
        pass "missing_creds_ct_doctor_message"
    else
        fail "missing_creds_ct_doctor_message" \
            "ct doctor output does not reference missing credentials: ${_doctor_out}"
    fi
    if [ "${_doctor_exit}" -ne 0 ]; then
        pass "missing_creds_ct_doctor_exits_nonzero"
    else
        fail "missing_creds_ct_doctor_exits_nonzero" \
            "ct doctor should have exited non-zero with missing credentials"
    fi
fi

# ── Scenario 3: Wrong / expired token ────────────────────────────────────────
# Given: ~/.cistern/env has a syntactically valid but expired API key;
#        ~/.claude/.credentials.json has an expired OAuth token (expiresAt in the past)
# When:  the service starts; ct doctor runs
# Then:  service startup fails with an "invalid or expired token" message;
#        ct doctor exits non-zero surfacing the same expired-token error
echo ""
echo "=== Scenario: Wrong/expired token ==="

_reset_scenario_state

if ! CT_NO_ASCII_LOGO=1 ct init >/dev/null; then
    fail "wrong_token_ct_init" "ct init failed"
else
    _install_skill_stubs

    # Given: valid-format (but rejected) API key in the env file.
    echo "ANTHROPIC_API_KEY=sk-ant-api03-AAABBBCCC" > "${HOME}/.cistern/env"

    # Given: expired OAuth credentials (expiresAt=1000ms — 1970-01-01, well in the past).
    mkdir -p "${HOME}/.claude"
    cat > "${HOME}/.claude/.credentials.json" <<'CREDS_EOF'
{"claudeAiOauth":{"accessToken":"expired-token","refreshToken":"refresh-token","expiresAt":1000}}
CREDS_EOF

    _since=$(date --iso-8601=seconds 2>/dev/null || date -u +"%Y-%m-%dT%H:%M:%S+00:00")

    # When: start the service (start-castellarius.sh detects expired OAuth token
    # and exits 1 with "invalid or expired token").
    systemctl start cistern-castellarius.service 2>/dev/null || true

    # Then: service fails with an actionable token error in the log.
    if _wait_service_state cistern-castellarius.service failed 25; then
        _log=$(journalctl -u cistern-castellarius.service --since "${_since}" \
               -n 20 --no-pager 2>/dev/null || true)
        if echo "${_log}" | grep -qi "invalid.*expired\|expired.*token\|invalid.*token"; then
            pass "wrong_token_service_startup_error"
        else
            fail "wrong_token_service_startup_error" \
                "service failed but log does not mention invalid/expired token: ${_log}"
        fi
    else
        _svc_state=$(systemctl is-active cistern-castellarius.service 2>/dev/null || true)
        fail "wrong_token_service_startup_error" \
            "service did not enter failed state (state=${_svc_state})"
    fi

    # Then: ct doctor exits non-zero with an expired-token error.
    # Pass ANTHROPIC_API_KEY so the env-var check passes; the OAuth expiry check
    # (which reads ~/.claude/.credentials.json) should cause the failure.
    _doctor_out=$(ANTHROPIC_API_KEY=sk-ant-api03-AAABBBCCC \
                  CT_NO_ASCII_LOGO=1 ct doctor 2>&1) && _doctor_exit=0 || _doctor_exit=$?
    if echo "${_doctor_out}" | grep -qi "expired\|invalid.*token"; then
        pass "wrong_token_ct_doctor_surfaces_error"
    else
        fail "wrong_token_ct_doctor_surfaces_error" \
            "ct doctor did not surface expired token error: ${_doctor_out}"
    fi
    if [ "${_doctor_exit}" -ne 0 ]; then
        pass "wrong_token_ct_doctor_exits_nonzero"
    else
        fail "wrong_token_ct_doctor_exits_nonzero" \
            "ct doctor should have exited non-zero with expired token"
    fi
fi

# ── Scenario 4: Upgrade ───────────────────────────────────────────────────────
# Given: ~/.cistern is pre-seeded with a "prior-version" install
#        (stale config keys, old binary path) and existing credentials
# When:  ct init runs again (simulating an upgrade)
# Then:  service comes up cleanly; ct doctor passes;
#        credentials are not lost or silently overwritten
echo ""
echo "=== Scenario: Upgrade ==="

_reset_scenario_state

# Given: pre-seed ~/.cistern with a prior-version config.
# Unknown keys (old_binary_path, legacy_agent_timeout) are silently ignored by
# the YAML parser, making this a valid-but-stale config that represents an
# older installation.
mkdir -p "${HOME}/.cistern"
cat > "${HOME}/.cistern/cistern.yaml" <<'STALE_CFG_EOF'
# Prior-version config — preserved by ct init (writeFileIfAbsent).
repos:
  - name: ScaledTest
    url: https://github.com/example/ScaledTest
    workflow_path: aqueduct/aqueduct.yaml
    cataractae: 2
    names: [virgo, marcia]
    prefix: st
  - name: cistern
    url: https://github.com/example/cistern
    workflow_path: aqueduct/aqueduct.yaml
    cataractae: 2
    names: [virgo, marcia]
    prefix: ct
max_cataractae: 4
# Stale keys from a prior version — must not break parsing or startup.
old_binary_path: /opt/cistern-v0/bin/ct
legacy_agent_timeout: 300
STALE_CFG_EOF

# Given: existing credentials that must survive the upgrade.
echo "ANTHROPIC_API_KEY=sk-ant-test-upgrade-preserved" > "${HOME}/.cistern/env"

# When: run ct init again (simulates upgrading cistern).
# Since cistern.yaml already exists, writeFileIfAbsent skips it (no overwrite).
# The aqueduct/ directory and role files are created for the first time.
if ! CT_NO_ASCII_LOGO=1 ct init >/dev/null; then
    fail "upgrade_ct_init" "ct init failed during upgrade"
else
    # Then: credentials must not have been overwritten.
    if grep -q "ANTHROPIC_API_KEY=sk-ant-test-upgrade-preserved" \
            "${HOME}/.cistern/env" 2>/dev/null; then
        pass "upgrade_credentials_preserved"
    else
        fail "upgrade_credentials_preserved" \
            "credentials were lost or overwritten after ct init upgrade"
    fi

    _install_skill_stubs

    # Then: service comes up cleanly with the (stale-but-valid) config.
    systemctl daemon-reload
    systemctl start cistern-castellarius.service 2>/dev/null || true

    if _wait_service_state cistern-castellarius.service active 20; then
        sleep 1
        pass "upgrade_service_active"

        # Then: ct doctor passes with full credential environment.
        _doctor_out=$(ANTHROPIC_API_KEY=sk-ant-test-upgrade-preserved \
                      CT_NO_ASCII_LOGO=1 ct doctor 2>&1) && _doctor_exit=0 || _doctor_exit=$?
        if [ "${_doctor_exit}" -eq 0 ]; then
            pass "upgrade_ct_doctor_passes"
        else
            fail "upgrade_ct_doctor_passes" "ct doctor failed after upgrade: ${_doctor_out}"
        fi
    else
        _svc_state=$(systemctl is-active cistern-castellarius.service 2>/dev/null || true)
        _log=$(journalctl -u cistern-castellarius.service -n 5 --no-pager 2>/dev/null || true)
        fail "upgrade_service_active" \
            "service did not reach active state after upgrade (state=${_svc_state}): ${_log}"
    fi
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "=== Results: ${PASS_COUNT} passed, ${FAIL_COUNT} failed ==="

if [ "${FAIL_COUNT}" -gt 0 ]; then
    exit 1
fi
exit 0
