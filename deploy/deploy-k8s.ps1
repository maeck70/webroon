<#
.SYNOPSIS
    Deploy WebRoon to Kubernetes (MicroK8s / MetalLB on 192.168.10.10 / roon.home.arpa:80).
.EXAMPLE
    .\deploy\deploy-k8s.ps1
    .\deploy\deploy-k8s.ps1 -Tag v1 -BuildAndPush
#>
param(
    [string]$Registry = "jombieparty.azurecr.io",
    [string]$ImageName = "webroon",
    [string]$Tag = "v1",
    [string]$Namespace = "webroon",
    [ValidateSet("auto", "podman", "docker")]
    [string]$Engine = "auto",
    [switch]$BuildAndPush = $true
)

$ErrorActionPreference = "Stop"
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$ManifestPath = Join-Path $PSScriptRoot "k8s\manifest.yaml"
$FullImage = "${Registry}/${ImageName}:${Tag}"
$LatestImage = "${Registry}/${ImageName}:latest"

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

    Write-Host "==> Authenticating with $Registry using Kubernetes secret 'acr-secret'..." -ForegroundColor Cyan
    $dockerCfgB64 = kubectl get secret acr-secret -n default -o jsonpath='{.data.\.dockerconfigjson}' 2>$null
    if ($dockerCfgB64) {
        $dockerCfgJson = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String($dockerCfgB64)) | ConvertFrom-Json
        $acrAuth = $dockerCfgJson.auths.$Registry
        if ($acrAuth) {
            $acrAuth.password | & $Engine login $Registry --username $acrAuth.username --password-stdin
        }
    }

    Write-Host "==> Building linux/amd64 image $FullImage with $Engine..." -ForegroundColor Cyan
    & $Engine build --platform linux/amd64 -t $FullImage -t $LatestImage -f (Join-Path $RepoRoot "Dockerfile") $RepoRoot
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    Write-Host "==> Pushing $FullImage and $LatestImage..." -ForegroundColor Cyan
    & $Engine push $FullImage
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    & $Engine push $LatestImage
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

Write-Host "==> Applying Kubernetes manifests ($ManifestPath)..." -ForegroundColor Cyan
kubectl apply -f $ManifestPath
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# Copy acr-secret from default namespace to webroon namespace if present
$acrSecretJson = kubectl get secret acr-secret -n default -o json 2>$null
if ($acrSecretJson) {
    $secretObj = $acrSecretJson | ConvertFrom-Json
    $secretObj.metadata.PSObject.Properties.Remove("resourceVersion")
    $secretObj.metadata.PSObject.Properties.Remove("uid")
    $secretObj.metadata.PSObject.Properties.Remove("creationTimestamp")
    $secretObj.metadata.namespace = $Namespace
    ($secretObj | ConvertTo-Json -Depth 10) | kubectl apply -f - | Out-Null
}

kubectl -n $Namespace set image deployment/webroon "webroon=$FullImage" | Out-Null
kubectl -n $Namespace rollout restart deployment/webroon | Out-Null

Write-Host "==> Waiting for rollout in namespace '$Namespace'..." -ForegroundColor Cyan
kubectl -n $Namespace rollout status deployment/webroon --timeout=120s
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> WebRoon deployed to Kubernetes! Available at http://roon.home.arpa" -ForegroundColor Green
kubectl -n $Namespace get pods,svc,ingress -o wide
