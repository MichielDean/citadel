#!/usr/bin/env bash
# run-local.sh — build the installer test image and run the full suite locally.
# Equivalent to what the installer-integration-tests CI job does on every PR.
#
# Usage (from the repository root):
#   bash tests/installer/run-local.sh
#
# Override the image tag:
#   CISTERN_TEST_IMAGE=myrepo/cistern-test:dev bash tests/installer/run-local.sh

set -euo pipefail

CONTAINER_NAME="cistern-installer-test-local"
IMAGE_TAG="${CISTERN_TEST_IMAGE:-cistern/installer-test:latest}"

# Stop any leftover container from a previous interrupted run.
docker stop "${CONTAINER_NAME}" 2>/dev/null || true

cleanup() {
    docker stop "${CONTAINER_NAME}" 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Building installer test image ==="
CISTERN_TEST_IMAGE="${IMAGE_TAG}" bash tests/installer/build.sh

echo ""
echo "=== Starting test container ==="
docker run \
    --privileged \
    --cgroupns=host \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
    --tmpfs /run \
    --tmpfs /run/lock \
    --security-opt apparmor=unconfined \
    -d \
    --name "${CONTAINER_NAME}" \
    "${IMAGE_TAG}"

echo ""
echo "=== Running installer integration tests ==="
docker exec "${CONTAINER_NAME}" /usr/local/bin/run-tests.sh
