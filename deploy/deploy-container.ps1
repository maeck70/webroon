<#
.SYNOPSIS
    Build and deploy WebRoon using Podman or Docker.
.EXAMPLE
    .\deploy\deploy-container.ps1
    .\deploy\deploy-container.ps1 -Engine podman -Port 8080
#>
param(
    [ValidateSet("auto", "podman", "docker")]
    [string]$Engine = "auto",
    [string]$ImageName = "webroon:latest",
    [string]$ContainerName = "webroon",
    [int]$Port = 8080,
    [string]$VolumeName = "webroon_data",
    [switch]$UseHostNetwork
)

$ErrorActionPreference = "Stop"
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")

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

Write-Host "==> Using container engine: $Engine" -ForegroundColor Cyan
Write-Host "==> Building image $ImageName..." -ForegroundColor Cyan
& $Engine build -t $ImageName -f (Join-Path $RepoRoot "Dockerfile") $RepoRoot
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& $Engine volume inspect $VolumeName *> $null
if ($LASTEXITCODE -ne 0) {
    Write-Host "==> Creating persistent volume $VolumeName..." -ForegroundColor Cyan
    & $Engine volume create $VolumeName | Out-Null
}

$existing = & $Engine ps -a --format "{{.Names}}" | Where-Object { $_ -eq $ContainerName }
if ($existing) {
    Write-Host "==> Removing existing container $ContainerName..." -ForegroundColor Cyan
    & $Engine rm -f $ContainerName | Out-Null
}

$netArgs = @()
if ($UseHostNetwork -or $IsLinux) {
    $netArgs = @("--network", "host")
} else {
    $netArgs = @("-p", "${Port}:${Port}/tcp", "-p", "9003:9003/udp")
}

Write-Host "==> Starting container $ContainerName..." -ForegroundColor Cyan
& $Engine run -d `
    --name $ContainerName `
    --restart unless-stopped `
    @netArgs `
    -e "PORT=$Port" `
    -e "ROON_STATE_FILE=/data/roonstate.json" `
    -v "${VolumeName}:/data" `
    $ImageName
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> WebRoon is running at http://localhost:$Port" -ForegroundColor Green
