#!/usr/bin/env bash
# tests/installer/build.sh — build the Docker systemd test image.
#
# Usage (from the repository root):
#   bash tests/installer/build.sh
#
# The image tag can be overridden:
#   CISTERN_TEST_IMAGE=myrepo/cistern-test:pr-42 bash tests/installer/build.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE_TAG="${CISTERN_TEST_IMAGE:-cistern/installer-test:latest}"

echo "==> Building systemd test image: ${IMAGE_TAG}"
echo "    Context: ${REPO_ROOT}"
echo "    Dockerfile: tests/installer/Dockerfile.systemd"
echo ""

docker build \
  --file "${REPO_ROOT}/tests/installer/Dockerfile.systemd" \
  --tag "${IMAGE_TAG}" \
  "${REPO_ROOT}"

echo ""
echo "==> Image built: ${IMAGE_TAG}"
echo ""
echo "Run smoke tests:"
echo "  docker run --privileged --rm -d --name cistern-installer-test ${IMAGE_TAG}"
echo "  docker exec cistern-installer-test /usr/local/bin/run-tests.sh"
echo "  docker stop cistern-installer-test"
