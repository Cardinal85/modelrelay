#!/usr/bin/env bash
# Build the Fyne certmgr GUI on Linux or macOS (requires CGO and a C compiler).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
export CGO_ENABLED=1
mkdir -p bin
go build -trimpath -ldflags "-s -w" -o bin/certmgr ./cmd/certmgr
echo "built bin/certmgr"
