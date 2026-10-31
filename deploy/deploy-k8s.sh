#!/usr/bin/env bash
set -euo pipefail

# Deploy WebRoon to Kubernetes
# Usage:
#   ./deploy/deploy-k8s.sh
#   IMAGE=ghcr.io/maeck70/webroon:latest BUILD_AND_PUSH=1 ./deploy/deploy-k8s.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
MANIFEST="${SCRIPT_DIR}/k8s/manifest.yaml"

IMAGE="${IMAGE:-ghcr.io/maeck70/webroon:latest}"
NAMESPACE="${NAMESPACE:-webroon}"
BUILD_AND_PUSH="${BUILD_AND_PUSH:-0}"

if ! command -v kubectl >/dev/null 2>&1; then
    echo "Error: 'kubectl' is required but not found in PATH." >&2
    exit 1
fi

if [[ "${BUILD_AND_PUSH}" == "1" ]]; then
    ENGINE="${CONTAINER_ENGINE:-}"
    if [[ -z "${ENGINE}" ]]; then
        if command -v podman >/dev/null 2>&1; then
            ENGINE="podman"
        elif command -v docker >/dev/null 2>&1; then
            ENGINE="docker"
        else
            echo "Error: Neither 'podman' nor 'docker' found to build image." >&2
            exit 1
        fi
    fi
    echo "==> Building and pushing ${IMAGE} using ${ENGINE}..."
    "${ENGINE}" build -t "${IMAGE}" -f "${REPO_ROOT}/Dockerfile" "${REPO_ROOT}"
    "${ENGINE}" push "${IMAGE}"
fi

echo "==> Applying Kubernetes manifests (${MANIFEST})..."
kubectl apply -f "${MANIFEST}"

if [[ "${IMAGE}" != "ghcr.io/maeck70/webroon:latest" ]]; then
    echo "==> Updating deployment image to ${IMAGE}..."
    kubectl -n "${NAMESPACE}" set image deployment/webroon webroon="${IMAGE}"
fi

echo "==> Waiting for rollout of deployment/webroon in namespace '${NAMESPACE}'..."
kubectl -n "${NAMESPACE}" rollout status deployment/webroon --timeout=120s

echo "==> Deployment complete! Pod status:"
kubectl -n "${NAMESPACE}" get pods,svc -o wide
