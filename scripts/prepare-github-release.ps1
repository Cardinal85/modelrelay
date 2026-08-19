#requires -version 5.1
<#
从 dist/ 生成 GitHub Release 发布包。

示例：
  powershell -File scripts/prepare-github-release.ps1 -Version 0.2.0
  powershell -File scripts/prepare-github-release.ps1 -Version 0.2.0 -Clean

输出：
  github-release/v<version>/modelrelay-<version>-<os>-<arch>.zip
  github-release/v<version>/SHA256SUMS
#>
[CmdletBinding()]
param(
    [string]$Version = "0.2.1",
    [string]$DistDir = "",
    [string]$OutputDir = "",
    [switch]$Clean
)

$ErrorActionPreference = "Stop"

function Fail([string]$Message) {
    throw "prepare-github-release.ps1: $Message"
}

function Copy-CommonFiles([string]$Destination) {
    $releaseReadme = Join-Path $RepoRoot "github-release\README.md"
    if (Test-Path $releaseReadme) {
        Copy-Item -Force $releaseReadme (Join-Path $Destination "README.md")
    } else {
        Copy-Item -Force (Join-Path $RepoRoot "README.md") (Join-Path $Destination "README.md")
    }

    $docsDestination = Join-Path $Destination "docs"
    New-Item -ItemType Directory -Force -Path $docsDestination | Out-Null
    foreach ($name in @("config.md", "deployment.md", "newapi.md")) {
        $source = Join-Path $RepoRoot "docs\$name"
        if (Test-Path $source) {
            Copy-Item -Force $source $docsDestination
        }
    }

    $configsSource = Join-Path $RepoRoot "configs"
    if (Test-Path $configsSource) {
        Copy-Item -Recurse -Force $configsSource (Join-Path $Destination "configs")
    }

    $scripts = Join-Path $Destination "scripts"
    New-Item -ItemType Directory -Force -Path $scripts | Out-Null
    foreach ($name in @("deploy.sh", "deploy.ps1", "install.sh", "install.ps1")) {
        $source = Join-Path $RepoRoot "scripts\$name"
        if (Test-Path $source) {
            $dest = Join-Path $scripts $name
            if ($name -like "*.sh") {
                Copy-UnixText $source $dest
            } else {
                Copy-Item -Force $source $dest
            }
        }
    }
}

function Copy-UnixText([string]$Source, [string]$Destination) {
    $text = [System.IO.File]::ReadAllText($Source) -replace "`r`n", "`n" -replace "`r", "`n"
    $utf8 = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($Destination, $text, $utf8)
}

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
if ([string]::IsNullOrWhiteSpace($DistDir)) {
    $DistDir = Join-Path $RepoRoot "dist"
}
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path $RepoRoot "github-release"
}
$DistDir = (Resolve-Path $DistDir).Path
$VersionRoot = Join-Path $OutputDir "v$Version"

if ($Clean -and (Test-Path $VersionRoot)) {
    Remove-Item -Recurse -Force $VersionRoot
}
New-Item -ItemType Directory -Force -Path $VersionRoot | Out-Null

$pattern = "modelrelay-$Version-*"
$packages = @(Get-ChildItem -Path $DistDir -Directory -Filter $pattern | Sort-Object Name)
if ($packages.Count -eq 0) {
    Fail ("No packages found at {0}\{1}; run scripts/build.ps1 -All first." -f $DistDir, $pattern)
}

$releaseReadme = @(
    "# ModelRelay $Version",
    "",
    "GitHub Release package for ModelRelay $Version.",
    "",
    "## Files",
    "",
    "- modelrelay-$Version-<os>-<arch>.zip: platform package with binaries and docs.",
    "- SHA256SUMS: SHA256 checksums for ZIP packages.",
    "",
    "Targets:",
    "",
    "- Linux: amd64, arm64, 386, arm",
    "- Windows: amd64, arm64, 386",
    "- macOS: amd64, arm64",
    "",
    "## Usage",
    "",
    "1. Download the ZIP for the target platform.",
    "2. Verify SHA256SUMS.",
    "3. Extract and follow docs/deployment.md.",
    "4. Replace example tokens, certificates, and configuration before production.",
    "",
    "Private keys, databases, logs, and local test data are excluded."
) -join [Environment]::NewLine
Set-Content -Path (Join-Path $VersionRoot "README.md") -Value $releaseReadme -Encoding UTF8

function New-ReleaseZip([string]$Source, [string]$Destination) {
    $tar = Get-Command tar.exe -ErrorAction SilentlyContinue
    if ($null -eq $tar) {
        Fail "missing tar.exe; install bsdtar to create ZIP files with portable path separators"
    }
    & $tar.Source -a -c -f $Destination -C $Source .
    if ($LASTEXITCODE -ne 0) {
        Fail "failed to create release archive: $Destination"
    }
}

foreach ($package in $packages) {
    $name = $package.Name
    $staging = Join-Path $VersionRoot $name
    $prefix = "modelrelay-$Version-"
    $stableName = "modelrelay-" + $name.Substring($prefix.Length) + ".zip"
    $zip = Join-Path $VersionRoot $stableName
    if (Test-Path $staging) { Remove-Item -Recurse -Force $staging }
    if (Test-Path $zip) { Remove-Item -Force $zip }

    New-Item -ItemType Directory -Force -Path $staging | Out-Null
    Copy-Item -Recurse -Force (Join-Path $package.FullName "*") $staging
    Copy-CommonFiles $staging

    $packageReadme = @(
        "# ModelRelay $Version - $name",
        "",
        "## Quick start",
        "",
        "After extraction:",
        "",
        "- Linux/macOS: run bash scripts/deploy.sh",
        "- Windows: run powershell -File scripts/deploy.ps1 as Administrator",
        "",
        "See docs/deployment.md. Prepare mTLS certificates and update the configuration.",
        "Use certmgr on the CA machine for offline issue; do not put CA private keys in this package.",
        "Do not put private keys, internal tokens, or production databases in the package."
    ) -join [Environment]::NewLine
    Set-Content -Path (Join-Path $staging "PACKAGE-README.md") -Value $packageReadme -Encoding UTF8

    New-ReleaseZip $staging $zip
    Remove-Item -Recurse -Force $staging
    Write-Output "created $zip"
}

$sumLines = foreach ($zip in Get-ChildItem -Path $VersionRoot -Filter "*.zip" | Sort-Object Name) {
    $hash = (Get-FileHash $zip.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $($zip.Name)"
}
Set-Content -Path (Join-Path $VersionRoot "SHA256SUMS") -Value $sumLines -Encoding ASCII

$releaseNotes = Join-Path $RepoRoot "github-release\RELEASE_NOTES-v$Version.md"
if (-not (Test-Path $releaseNotes)) {
    $notes = @(
        "# ModelRelay $Version",
        "",
        "## Release contents",
        "",
        "- Relay: OpenAI-compatible proxy, scheduling, queues, and WebUI.",
        "- Agent: WSS/mTLS connection, local model proxy, heartbeat, and discovery.",
        "- certctl: CA, CSR, Agent certificate, and Relay server certificate CLI.",
        "- certmgr: cross-platform CA GUI for offline issue and optional online revoke.",
        "- Packages for Linux, Windows, and macOS architectures.",
        "",
        "## Release checklist",
        "",
        "- [ ] Verify SHA256 on the target platform.",
        "- [ ] Replace example addresses, certificates, and tokens.",
        "- [ ] Test with a real model service.",
        "- [ ] Prepare database backup and rollback."
    ) -join [Environment]::NewLine
    Set-Content -Path $releaseNotes -Value $notes -Encoding UTF8
}

Write-Output "GitHub release prepared: $VersionRoot"
Write-Output "Upload the ZIP files and SHA256SUMS to the GitHub Release."
