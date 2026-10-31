<#
.SYNOPSIS
    Deploy WebRoon using Docker.
#>
param(
    [string]$ImageName = "webroon:latest",
    [string]$ContainerName = "webroon",
    [int]$Port = 8080,
    [string]$VolumeName = "webroon_data",
    [switch]$UseHostNetwork
)

& (Join-Path $PSScriptRoot "deploy-container.ps1") `
    -Engine docker `
    -ImageName $ImageName `
    -ContainerName $ContainerName `
    -Port $Port `
    -VolumeName $VolumeName `
    -UseHostNetwork:$UseHostNetwork
