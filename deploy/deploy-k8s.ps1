<#
.SYNOPSIS
    Deploy WebRoon to a Kubernetes cluster using kubectl (optionally building & pushing with Podman/Docker).
.EXAMPLE
    .\deploy\deploy-k8s.ps1
    .\deploy\deploy-k8s.ps1 -Image ghcr.io/maeck70/webroon:latest -BuildAndPush
#>
param(
    [string]$Image = "ghcr.io/maeck70/webroon:latest",
    [string]$Namespace = "webroon",
    [ValidateSet("auto", "podman", "docker")]
    [string]$Engine = "auto",
    [switch]$BuildAndPush
)

$ErrorActionPreference = "Stop"
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$ManifestPath = Join-Path $PSScriptRoot "k8s\manifest.yaml"

if (-not (Get-Command kubectl -ErrorAction SilentlyContinue)) {
    Write-Error "'kubectl' was not found in PATH."
    exit 1
}

if ($BuildAndPush) {
    if ($Engine -eq "auto") {
        if (Get-Command podman -ErrorAction SilentlyContinue) {
            $Engine = "podman"
        } elseif (Get-Command docker -ErrorAction SilentlyContinue) {
            $Engine = "docker"
        } else {
            Write-Error "Neither 'podman' nor 'docker' was found in PATH."
            exit 1
        }
    }
    Write-Host "==> Building image $Image with $Engine..." -ForegroundColor Cyan
    & $Engine build -t $Image -f (Join-Path $RepoRoot "Dockerfile") $RepoRoot
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    Write-Host "==> Pushing image $Image..." -ForegroundColor Cyan
    & $Engine push $Image
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

Write-Host "==> Applying Kubernetes manifests ($ManifestPath)..." -ForegroundColor Cyan
kubectl apply -f $ManifestPath
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

if ($Image -ne "ghcr.io/maeck70/webroon:latest") {
    Write-Host "==> Setting deployment image to $Image..." -ForegroundColor Cyan
    kubectl -n $Namespace set image deployment/webroon "webroon=$Image"
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

Write-Host "==> Waiting for rollout in namespace '$Namespace'..." -ForegroundColor Cyan
kubectl -n $Namespace rollout status deployment/webroon --timeout=120s
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> WebRoon deployed to Kubernetes!" -ForegroundColor Green
kubectl -n $Namespace get pods,svc -o wide
