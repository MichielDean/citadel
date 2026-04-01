#!/usr/bin/env bash
# test/installer/helpers.sh — shared helpers for the installer test harness
#
# Sourced by run-installer-tests.sh.  Do not execute directly.
#
# Provides:
#   Reporting  : pass(), fail()
#   Docker     : require_docker(), build_image(), start_container(),
#                stop_container(), wait_for_systemd()
#   Execution  : exec_in_container(), service_status()
#   Lifecycle  : setup_container()

# ─── Global state ─────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0

# Unique container name derived from the runner's PID so parallel test runs
# do not collide.
IMAGE_NAME="cistern-installer-test"
CONTAINER_NAME="cistern-installer-test-$$"

# ─── Output / GitHub Actions annotations ──────────────────────────────────────
#
# GitHub Actions interprets lines of the form ::notice::message and
# ::error::message as workflow annotations.  These prefixes are harmless in
# plain terminal output.

pass() {
    local description="$1"
    printf '::notice::PASS: %s\n' "${description}"
    PASS_COUNT=$((PASS_COUNT + 1))
}

fail() {
    local description="$1"
    local reason="${2:-}"
    if [[ -n "${reason}" ]]; then
        printf '::error::FAIL: %s — %s\n' "${description}" "${reason}"
    else
        printf '::error::FAIL: %s\n' "${description}"
    fi
    FAIL_COUNT=$((FAIL_COUNT + 1))
}

# ─── Docker helpers ───────────────────────────────────────────────────────────

# require_docker exits with an error message when Docker is not available.
require_docker() {
    if ! command -v docker &>/dev/null; then
        printf 'error: docker is not installed or not in PATH\n' >&2
        exit 1
    fi
}

# build_image builds the installer-test image from the self-contained
# tests/installer/Dockerfile.systemd (golang:1.26 builder + systemd-ubuntu runtime).
build_image() {
    local repo_root="$1"
    docker build \
        --tag  "${IMAGE_NAME}" \
        --file "${repo_root}/tests/installer/Dockerfile.systemd" \
        "${repo_root}"
}

# start_container starts the installer-test container in the background.
# Flags required for systemd to run as PID 1 on Linux 5.10+ with cgroupv2:
#   --cgroupns=host: use host cgroup namespace so systemd can manage cgroups.
#   -v /sys/fs/cgroup:/sys/fs/cgroup:rw: writable cgroup hierarchy for systemd.
#   --tmpfs /run --tmpfs /run/lock: writable tmpfs for systemd runtime.
#   --security-opt apparmor=unconfined: Docker's AppArmor profile blocks
#     syscalls systemd needs; --privileged alone does not disable it.
start_container() {
    docker run \
        --privileged \
        --cgroupns=host \
        -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
        --tmpfs /run \
        --tmpfs /run/lock \
        --security-opt apparmor=unconfined \
        --rm \
        --detach \
        --name "${CONTAINER_NAME}" \
        "${IMAGE_NAME}" \
        >/dev/null
}

# stop_container stops the running installer-test container.  Never fails so
# it is safe to call from an EXIT trap even if the container never started.
stop_container() {
    docker stop "${CONTAINER_NAME}" >/dev/null 2>&1 || true
}

# wait_for_systemd polls systemctl is-system-running until systemd reaches
# a stable state (running or degraded) or until the timeout expires.
#
# Returns 0 on success, 1 on timeout.
wait_for_systemd() {
    local max_wait="${1:-30}"
    local elapsed=0
    local status

    while [[ "${elapsed}" -lt "${max_wait}" ]]; do
        status=$(exec_in_container \
            systemctl is-system-running 2>/dev/null || true)
        case "${status}" in
            running|degraded) return 0 ;;
        esac
        sleep 1
        elapsed=$((elapsed + 1))
    done

    return 1
}

# ─── In-container execution ───────────────────────────────────────────────────

# exec_in_container runs a command inside the container with the minimal
# environment needed for ct to function without an interactive terminal.
exec_in_container() {
    docker exec \
        "${CONTAINER_NAME}" \
        "$@"
}

# service_status prints the active state of a systemd unit in the container.
# Output is one of: active, inactive, failed, activating, deactivating,
# or unknown.
service_status() {
    local service="$1"
    exec_in_container systemctl is-active "${service}" 2>/dev/null || true
}

# ─── Lifecycle ────────────────────────────────────────────────────────────────

# setup_container builds the image and starts a container, waiting for
# systemd to reach a stable state before returning.
# Exits the script (via exit 1) if any step fails.
setup_container() {
    local repo_root="$1"

    printf 'Building installer-test Docker image...\n'
    if ! build_image "${repo_root}"; then
        printf 'error: failed to build installer-test Docker image\n' >&2
        exit 1
    fi

    printf 'Starting container...\n'
    start_container

    printf 'Waiting for systemd to initialise (up to 30 s)...\n'
    if ! wait_for_systemd 30; then
        printf 'error: systemd did not reach running/degraded within 30 s\n' >&2
        exit 1
    fi
}
