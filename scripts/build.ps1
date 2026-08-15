# ModelRelay build & release script
#
# Usage:
#   powershell -File scripts/build.ps1            # build for current platform into bin/
#   powershell -File scripts/build.ps1 -All       # cross-compile all targets into dist/
#   powershell -File scripts/build.ps1 -Test      # run all tests

param(
    [switch]$All,
    [switch]$Test,
    [switch]$Race,
    [switch]$Clean
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "goenv.ps1")

# 不继承前一次交叉编译残留的目标环境。尤其是长驻 IDE/Shell 中，
# -All 设置的 GOOS/GOARCH 若未清理，会让后续 go test 尝试启动
# darwin/linux 测试二进制并报 “%1 is not a valid Win32 application”。
$nativeGOOS = (& go env GOHOSTOS).Trim()
$nativeGOARCH = (& go env GOHOSTARCH).Trim()
$env:GOOS = $nativeGOOS
$env:GOARCH = $nativeGOARCH
$env:GOARM = $null
$env:CGO_ENABLED = $null

if ($Clean) {
    Remove-Item -Recurse -Force "bin", "dist" -ErrorAction SilentlyContinue
}

if ($Race) {
    # Race detection (some sandboxed environments cannot start -race binaries; use a normal dev machine / CI)
    go test ./... -race -count=1 -timeout 900s
    if ($LASTEXITCODE -ne 0) { exit 1 }
    $env:GOOS = $nativeGOOS; $env:GOARCH = $nativeGOARCH; $env:GOARM = $null; $env:CGO_ENABLED = $null
    exit 0
}

if ($Test) {
    go vet ./...
    go test ./... -count=1 -timeout 600s
    if ($LASTEXITCODE -ne 0) { exit 1 }
    $env:GOOS = $nativeGOOS; $env:GOARCH = $nativeGOARCH; $env:GOARM = $null; $env:CGO_ENABLED = $null
    exit 0
}

$VERSION = go run ./cmd/certctl version | ForEach-Object { $_ -match "certctl (.+?) \(" | Out-Null; $Matches[1] }
if (-not $VERSION) { $VERSION = "0.1.0" }

if ($All) {
    # Targets: Linux amd64/arm64/386/arm, Windows amd64/arm64/386, macOS amd64/arm64
    $targets = @(
        @{os="linux"; arch="amd64"}, @{os="linux"; arch="arm64"},
        @{os="linux"; arch="386"},   @{os="linux"; arch="arm"},
        @{os="windows"; arch="amd64"}, @{os="windows"; arch="arm64"}, @{os="windows"; arch="386"},
        @{os="darwin"; arch="amd64"}, @{os="darwin"; arch="arm64"}
    )
    foreach ($t in $targets) {
        $ext = if ($t.os -eq "windows") { ".exe" } else { "" }
        foreach ($name in @("relay", "agent", "certctl")) {
            $out = "dist/modelrelay-$VERSION-$($t.os)-$($t.arch)/$name$ext"
            New-Item -ItemType Directory -Force -Path (Split-Path $out) | Out-Null
            Write-Output "building $($t.os)/$($t.arch) $name ..."
            $env:GOOS = $t.os; $env:GOARCH = $t.arch
            $env:CGO_ENABLED = "0"
            go build -trimpath -ldflags "-s -w" -o $out ./cmd/$name
            if ($LASTEXITCODE -ne 0) { Write-Error "build failed for $name $($t.os)/$($t.arch)"; exit 1 }
        }
        # SHA256 checksums
        $dir = "dist/modelrelay-$VERSION-$($t.os)-$($t.arch)"
        Get-ChildItem $dir | ForEach-Object {
            $hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower()
            "$hash  $($_.Name)" | Add-Content "$dir/SHA256SUMS"
        }
    }
    $env:GOOS = $nativeGOOS; $env:GOARCH = $nativeGOARCH; $env:GOARM = $null; $env:CGO_ENABLED = $null
    Write-Output "cross-build done -> dist/"
    exit 0
}

# current platform build
New-Item -ItemType Directory -Force -Path "bin" | Out-Null
foreach ($name in @("relay", "agent", "certctl", "mockmodel")) {
    Write-Output "building $name ..."
    go build -trimpath -o "bin/$name.exe" ./cmd/$name
}
Write-Output "build done -> bin/"
