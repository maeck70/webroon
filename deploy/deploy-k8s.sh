#!/usr/bin/env bash
set -euo pipefail

# Deploy WebRoon to Kubernetes (MicroK8s / MetalLB on 192.168.10.10 / roon.home.arpa:80)
# Usage:
#   ./deploy/deploy-k8s.sh [tag]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
MANIFEST="${SCRIPT_DIR}/k8s/manifest.yaml"

REGISTRY="${REGISTRY:-jombieparty.azurecr.io}"
IMAGE_NAME="${IMAGE_NAME:-webroon}"
TAG="${1:-v1}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${TAG}"
LATEST_IMAGE="${REGISTRY}/${IMAGE_NAME}:latest"
NAMESPACE="${NAMESPACE:-webroon}"
BUILD_AND_PUSH="${BUILD_AND_PUSH:-1}"

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

    echo "==> Authenticating with ${REGISTRY} via Kubernetes secret 'acr-secret'..."
    DOCKER_CONFIG=$(kubectl get secret acr-secret -n default -o jsonpath='{.data.\.dockerconfigjson}' 2>/dev/null | base64 -d 2>/dev/null || true)
    if [[ -n "${DOCKER_CONFIG}" ]] && command -v jq >/dev/null 2>&1; then
        ACR_USER=$(echo "${DOCKER_CONFIG}" | jq -r --arg reg "${REGISTRY}" '.auths[$reg].username // empty')
        ACR_PASS=$(echo "${DOCKER_CONFIG}" | jq -r --arg reg "${REGISTRY}" '.auths[$reg].password // empty')
        if [[ -n "${ACR_USER}" && -n "${ACR_PASS}" ]]; then
            echo "${ACR_PASS}" | "${ENGINE}" login "${REGISTRY}" --username "${ACR_USER}" --password-stdin
        fi
    fi

    echo "==> Building and pushing linux/amd64 ${FULL_IMAGE} using ${ENGINE}..."
    "${ENGINE}" build --platform linux/amd64 -t "${FULL_IMAGE}" -t "${LATEST_IMAGE}" -f "${REPO_ROOT}/Dockerfile" "${REPO_ROOT}"
    "${ENGINE}" push "${FULL_IMAGE}"
    "${ENGINE}" push "${LATEST_IMAGE}"
fi

echo "==> Applying Kubernetes manifests (${MANIFEST})..."
kubectl apply -f "${MANIFEST}"

# Copy acr-secret into target namespace if available
if kubectl get secret acr-secret -n default >/dev/null 2>&1; then
    kubectl get secret acr-secret -n default -o json \
        | sed "s/\"namespace\": \"default\"/\"namespace\": \"${NAMESPACE}\"/" \
        | kubectl apply -n "${NAMESPACE}" -f - >/dev/null 2>&1 || true
fi

kubectl -n "${NAMESPACE}" set image deployment/webroon webroon="${FULL_IMAGE}"
kubectl -n "${NAMESPACE}" rollout restart deployment/webroon

echo "==> Waiting for rollout of deployment/webroon in namespace '${NAMESPACE}'..."
kubectl -n "${NAMESPACE}" rollout status deployment/webroon --timeout=120s

echo "==> Deployment complete! WebRoon is live at http://roon.home.arpa"
kubectl -n "${NAMESPACE}" get pods,svc,ingress -o wide
