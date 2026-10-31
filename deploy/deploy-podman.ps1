<#
.SYNOPSIS
    Deploy WebRoon using Podman.
#>
param(
    [string]$ImageName = "webroon:latest",
    [string]$ContainerName = "webroon",
    [int]$Port = 8080,
    [string]$VolumeName = "webroon_data",
    [switch]$UseHostNetwork
)

& (Join-Path $PSScriptRoot "deploy-container.ps1") `
    -Engine podman `
    -ImageName $ImageName `
    -ContainerName $ContainerName `
    -Port $Port `
    -VolumeName $VolumeName `
    -UseHostNetwork:$UseHostNetwork
