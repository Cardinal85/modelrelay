#requires -version 5.1
<#
ModelRelay latest 一键安装器（Windows）

示例：
  $p = Join-Path $env:TEMP "modelrelay-install.ps1"
  Invoke-WebRequest -UseBasicParsing `
    https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.ps1 `
    -OutFile $p
  powershell -ExecutionPolicy Bypass -File $p -Component Relay
#>
[CmdletBinding()]
param(
    [ValidateSet("Relay", "Agent", "Both")]
    [string]$Component = "Both",
    [string]$InstallRoot = "C:\ModelRelay",
    [string]$RelayId = $env:COMPUTERNAME,
    [string]$NodeId = $env:COMPUTERNAME,
    [string]$RelayUrl = "wss://relay.example.com:9443/agent/v1/connect",
    [string]$LocalBaseUrl = "http://127.0.0.1:8000/v1",
    [switch]$NoStart
)

$ErrorActionPreference = "Stop"

function Fail([string]$Message) {
    throw "install.ps1: $Message"
}

$architecture = $env:PROCESSOR_ARCHITEW6432
if ([string]::IsNullOrWhiteSpace($architecture)) {
    $architecture = $env:PROCESSOR_ARCHITECTURE
}
switch ($architecture.ToUpperInvariant()) {
    "AMD64" { $releaseArch = "amd64" }
    "ARM64" { $releaseArch = "arm64" }
    "X86" { $releaseArch = "386" }
    default { Fail "unsupported CPU architecture: $architecture" }
}

$packageName = "modelrelay-windows-$releaseArch.zip"
$packageUrl = "https://github.com/Cardinal85/modelrelay/releases/latest/download/$packageName"
$tempRoot = Join-Path $env:TEMP ("modelrelay-install-" + [Guid]::NewGuid().ToString("N"))
$packagePath = Join-Path $tempRoot $packageName
$sourceDir = Join-Path $tempRoot "package"

New-Item -ItemType Directory -Force -Path $tempRoot | Out-Null
try {
    Write-Host "downloading $packageUrl ..."
    Invoke-WebRequest -UseBasicParsing $packageUrl -OutFile $packagePath
    Expand-Archive -LiteralPath $packagePath -DestinationPath $sourceDir

    $deploy = Join-Path $sourceDir "scripts\deploy.ps1"
    if (-not (Test-Path $deploy -PathType Leaf)) {
        Fail "downloaded package is invalid: scripts\deploy.ps1 not found"
    }

    $parameters = @{
        SourceDir = $sourceDir
        Component = $Component
        InstallRoot = $InstallRoot
        RelayId = $RelayId
        NodeId = $NodeId
        RelayUrl = $RelayUrl
        LocalBaseUrl = $LocalBaseUrl
    }
    if ($NoStart) {
        $parameters.NoStart = $true
    }
    & $deploy @parameters
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
}
finally {
    if (Test-Path $tempRoot) {
        Remove-Item -Recurse -Force $tempRoot
    }
}
