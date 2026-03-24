#!/usr/bin/env bash
# run-installer-tests.sh — installer test harness for Cistern
#
# Builds the installer-test Docker image (systemd + ct + fakeagent claude
# stub), starts a container, runs scaffolding tests, and reports results in
# GitHub Actions annotation format.
#
# Usage:
#   ./run-installer-tests.sh
#
# Exit codes:
#   0  all tests passed
#   1  one or more tests failed, or setup failed
#
# Requirements:
#   docker (with BuildKit support)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=test/installer/helpers.sh
source "${SCRIPT_DIR}/test/installer/helpers.sh"

# ─── Integration test helpers ─────────────────────────────────────────────────

# install_system_service writes a cistern-castellarius system service unit file
# pointing at the given home directory, then enables and (re)starts the service.
#
# Uses a HERE-document piped via docker exec -i so that HOME_DIR is expanded
# inside the container's shell rather than the host's.
#
# Usage: install_system_service <home_dir>
install_system_service() {
    local home_dir="$1"
    docker exec -i --env CT_NO_ASCII_LOGO=1 "${CONTAINER_NAME}" \
        bash -s -- "${home_dir}" << 'INSTALL_SCRIPT'
#!/bin/bash
HOME_DIR="$1"
cat > /etc/systemd/system/cistern-castellarius.service << EOF
[Unit]
Description=Cistern Castellarius — aqueduct scheduler (test)
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/ct castellarius start
Restart=on-failure
RestartSec=5
TimeoutStopSec=10
KillMode=mixed
KillSignal=SIGTERM
StandardOutput=journal
StandardError=journal
Environment=CT_NO_ASCII_LOGO=1
Environment=HOME=${HOME_DIR}
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload &&
systemctl enable cistern-castellarius &&
systemctl restart cistern-castellarius
INSTALL_SCRIPT
}

# wait_for_service_active polls systemctl is-active until the named service
# reports "active" or the timeout (in seconds) expires.
#
# Returns 0 if the service becomes active within the timeout, 1 otherwise.
#
# Usage: wait_for_service_active <service> [max_seconds]
wait_for_service_active() {
    local service="$1"
    local max_wait="${2:-10}"
    local elapsed=0
    while [[ "${elapsed}" -lt "${max_wait}" ]]; do
        if [[ "$(service_status "${service}")" == "active" ]]; then
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    return 1
}

# ─── Test cases ───────────────────────────────────────────────────────────────

# test_systemd_boots verifies that systemd reaches a stable operating state
# inside the container.
# Acceptable states: running (all units OK), degraded (some non-essential
# units failed — still functional for installer tests).
test_systemd_boots() {
    local status
    status=$(exec_in_container \
        systemctl is-system-running 2>/dev/null || true)
    case "${status}" in
        running|degraded) return 0 ;;
        *) return 1 ;;
    esac
}

# test_ct_available verifies that the ct binary is present and executable.
test_ct_available() {
    exec_in_container ct version >/dev/null
}

# test_fakeagent_available verifies that fakeagent is in the PATH and that
# the /usr/local/bin/claude symlink resolves correctly.
test_fakeagent_available() {
    exec_in_container which fakeagent >/dev/null &&
    exec_in_container which claude    >/dev/null
}

# test_ct_init verifies that `ct init` exits 0 and creates the Cistern config
# file at the expected location.
test_ct_init() {
    exec_in_container ct init >/dev/null 2>&1 &&
    exec_in_container test -f /root/.cistern/cistern.yaml
}

# test_ct_doctor verifies that `ct doctor` terminates without crashing.
# It is expected to exit 1 (some checks fail in the minimal container
# environment — e.g., gh CLI not installed), but it must not exit with a
# signal or an unexpected code ≥ 2.
test_ct_doctor() {
    local exit_code=0
    exec_in_container ct doctor >/dev/null 2>&1 || exit_code=$?
    [[ "${exit_code}" -le 1 ]]
}

# test_service_status_helper verifies that the service_status helper function
# returns a non-empty string when querying a systemd unit that is not
# installed.  The expected result is "inactive" (not found = inactive).
test_service_status_helper() {
    local status
    status=$(service_status "cistern-castellarius.service")
    [[ -n "${status}" ]]
}

# test_fresh_install verifies a clean first-time installation end-to-end.
#
# Given: no ~/.cistern exists in the test home directory
# When:  ct init is run
# Then:  the Castellarius service unit is loaded and active,
#        the claude agent binary is on PATH, and ct doctor exits 0.
#
# A minimal cistern.yaml (repos: []) is used so that ct castellarius start
# does not fail on missing skills or workflow paths.  A placeholder
# ANTHROPIC_API_KEY is written to the env file before ct doctor runs so
# that the credential check passes without a real API key.
test_fresh_install() {
    local home_dir="/tmp/cistern-test-fresh"

    # Given: isolated, empty home directory — no .cistern present.
    exec_in_container bash -c "rm -rf '${home_dir}' && mkdir -p '${home_dir}'" || return 1
    if exec_in_container test -d "${home_dir}/.cistern" 2>/dev/null; then
        return 1  # pre-condition violated: .cistern must not exist before init
    fi

    # When: ct init bootstraps the installation.
    exec_in_container env HOME="${home_dir}" CT_NO_ASCII_LOGO=1 ct init \
        >/dev/null 2>&1 || return 1

    # Replace the default config with a minimal one (repos: []) so that
    # ct castellarius start skips skills and workflow validation.
    exec_in_container bash -c "
        printf 'repos: []\nmax_cataractae: 2\nhandoff_token_threshold: 100000\n' \
            > '${home_dir}/.cistern/cistern.yaml'
    " || return 1

    # Add a placeholder API key so ct doctor's ANTHROPIC_API_KEY check passes.
    exec_in_container bash -c \
        "printf 'ANTHROPIC_API_KEY=sk-ant-test-placeholder\n' \
            >> '${home_dir}/.cistern/env'" || return 1

    # Use ct doctor --fix to create cistern.db before the service starts.
    exec_in_container env HOME="${home_dir}" CT_NO_ASCII_LOGO=1 ct doctor --fix \
        >/dev/null 2>&1 || true

    # Install and start the system service.
    install_system_service "${home_dir}" || return 1

    # Then: service unit is loaded and active (wait up to 10 s).
    if ! wait_for_service_active "cistern-castellarius" 10; then
        return 1
    fi

    # Then: agent binary (claude stub) is present on PATH.
    exec_in_container which claude >/dev/null || return 1

    # Then: ct doctor exits 0 (all checks pass with the configured environment).
    exec_in_container env HOME="${home_dir}" CT_NO_ASCII_LOGO=1 ct doctor \
        >/dev/null 2>&1
}

# test_upgrade verifies that re-running ct init over a prior-version layout
# leaves the Castellarius service and ct doctor in a healthy state.
#
# Given: ~/.cistern already exists with a stale config (prior-version key present)
# When:  ct init runs again (without --force)
# Then:  the service restarts cleanly (active) and ct doctor still exits 0.
#
# The stale key "stale_old_key" in the pre-populated cistern.yaml simulates a
# field that was removed or renamed in a newer version of Cistern.  Because
# ct init uses writeFileIfAbsent, the existing file is preserved; ct
# castellarius start ignores unknown YAML keys.
test_upgrade() {
    local home_dir="/tmp/cistern-test-upgrade"
    local cistern_dir="${home_dir}/.cistern"

    # Given: pre-populated ~/.cistern simulating a prior-version installation.
    exec_in_container bash -c "
        rm -rf '${home_dir}' &&
        mkdir -p '${cistern_dir}/aqueduct' '${cistern_dir}/cataractae' &&
        printf 'repos: []\nmax_cataractae: 2\nstale_old_key: removed_in_v2\n' \
            > '${cistern_dir}/cistern.yaml' &&
        printf 'ANTHROPIC_API_KEY=sk-ant-old-key\n' \
            > '${cistern_dir}/env' &&
        chmod 600 '${cistern_dir}/env'
    " || return 1

    # When: ct init runs over the existing installation.
    # writeFileIfAbsent skips cistern.yaml (already present) but creates any
    # missing files (aqueduct.yaml, start-castellarius.sh, cataractae files).
    exec_in_container env HOME="${home_dir}" CT_NO_ASCII_LOGO=1 ct init \
        >/dev/null 2>&1 || return 1

    # Create cistern.db via ct doctor --fix so the service can open it.
    exec_in_container env HOME="${home_dir}" CT_NO_ASCII_LOGO=1 ct doctor --fix \
        >/dev/null 2>&1 || true

    # (Re-)install the service unit pointing at the upgrade home and restart it.
    # This simulates "service restarts cleanly" after the upgrade.
    install_system_service "${home_dir}" || return 1

    # Then: service restarts cleanly and is active (wait up to 10 s).
    if ! wait_for_service_active "cistern-castellarius" 10; then
        return 1
    fi

    # Then: ct doctor still exits 0 after the upgrade.
    exec_in_container env HOME="${home_dir}" CT_NO_ASCII_LOGO=1 ct doctor \
        >/dev/null 2>&1
}

# ─── Runner ───────────────────────────────────────────────────────────────────

# run_test calls a test function and records pass/fail.
# Using an `if` statement means set -e does not trigger on a non-zero return.
run_test() {
    local name="$1"
    local func="$2"
    if "${func}"; then
        pass "${name}"
    else
        fail "${name}"
    fi
}

# ─── Cleanup ──────────────────────────────────────────────────────────────────

trap stop_container EXIT

# ─── Main ─────────────────────────────────────────────────────────────────────

main() {
    require_docker
    setup_container "${SCRIPT_DIR}"

    run_test "systemd boots in container"                              test_systemd_boots
    run_test "ct binary is available"                                  test_ct_available
    run_test "fakeagent available as claude stub"                      test_fakeagent_available
    run_test "ct init creates cistern config"                          test_ct_init
    run_test "ct doctor runs without crash"                            test_ct_doctor
    run_test "service_status helper queries systemd"                   test_service_status_helper
    run_test "fresh install: service active and ct doctor exits 0"     test_fresh_install
    run_test "upgrade: stale config survives ct init, service active"  test_upgrade

    printf '\nResults: %d passed, %d failed\n' "${PASS_COUNT}" "${FAIL_COUNT}"

    [[ "${FAIL_COUNT}" -eq 0 ]]
}

main "$@"
