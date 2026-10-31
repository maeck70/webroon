#!/usr/bin/env bash
set -euo pipefail

# Deploy WebRoon to Podman or Docker
# Usage: ./deploy/deploy-container.sh [podman|docker]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

ENGINE="${1:-${CONTAINER_ENGINE:-}}"
if [[ -z "${ENGINE}" ]]; then
    if command -v podman >/dev/null 2>&1; then
        ENGINE="podman"
    elif command -v docker >/dev/null 2>&1; then
        ENGINE="docker"
    else
        echo "Error: Neither 'podman' nor 'docker' was found in PATH." >&2
        exit 1
    fi
fi

IMAGE_NAME="${IMAGE_NAME:-webroon:latest}"
CONTAINER_NAME="${CONTAINER_NAME:-webroon}"
PORT="${PORT:-8080}"
VOLUME_NAME="${VOLUME_NAME:-webroon_data}"

echo "==> Using container engine: ${ENGINE}"
echo "==> Building image ${IMAGE_NAME} from ${REPO_ROOT}..."
"${ENGINE}" build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/Dockerfile" "${REPO_ROOT}"

echo "==> Ensuring persistent volume ${VOLUME_NAME} exists..."
"${ENGINE}" volume inspect "${VOLUME_NAME}" >/dev/null 2>&1 || "${ENGINE}" volume create "${VOLUME_NAME}" >/dev/null

if "${ENGINE}" ps -a --format '{{.Names}}' | grep -Eq "^${CONTAINER_NAME}\$"; then
    echo "==> Removing existing container ${CONTAINER_NAME}..."
    "${ENGINE}" rm -f "${CONTAINER_NAME}" >/dev/null
fi

# On Linux, use --network host so UDP 9003 SOOD LAN multicast/broadcast reaches Roon Core directly
NET_ARGS=()
if [[ "$(uname -s)" == "Linux" ]]; then
    NET_ARGS=(--network host)
else
    NET_ARGS=(-p "${PORT}:${PORT}/tcp" -p "9003:9003/udp")
fi

echo "==> Starting container ${CONTAINER_NAME}..."
"${ENGINE}" run -d \
    --name "${CONTAINER_NAME}" \
    --restart unless-stopped \
    "${NET_ARGS[@]}" \
    -e "PORT=${PORT}" \
    -e "ROON_STATE_FILE=/data/roonstate.json" \
    -v "${VOLUME_NAME}:/data:Z" \
    "${IMAGE_NAME}"

echo "==> WebRoon deployed successfully! Open http://localhost:${PORT}"
