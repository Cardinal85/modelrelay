# ModelRelay optional local toolchain (PowerShell)
$here = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($here)) { $here = Split-Path -Parent $MyInvocation.MyCommand.Path }
$root = (Resolve-Path (Join-Path $here "..")).Path
$goBin = Join-Path $root ".tools\go\bin"
$mingwBin = Join-Path $root ".tools\llvm-mingw\bin"
if (Test-Path (Join-Path $mingwBin "gcc.exe")) {
    $env:PATH = "$mingwBin;$goBin;$env:PATH"
    $env:CC = Join-Path $mingwBin "gcc.exe"
} elseif (Test-Path (Join-Path $goBin "go.exe")) {
    $env:PATH = "$goBin;$env:PATH"
}
if (Test-Path (Join-Path $root ".tools\gopath")) {
    $env:GOMODCACHE = Join-Path $root ".tools\gopath\pkg\mod"
    $env:GOPATH = Join-Path $root ".tools\gopath"
    $env:GOCACHE = Join-Path $root ".tools\gocache"
}
