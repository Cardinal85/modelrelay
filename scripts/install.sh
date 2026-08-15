#!/usr/bin/env bash
#
# ModelRelay latest 一键安装器（Linux / macOS）
#
# 示例：
#   curl -fsSL https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
#     | sudo bash -s -- --component relay
#   curl -fsSL https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
#     | sudo bash -s -- --component agent --node-id gpu-001 \
#       --relay-url wss://relay.example.com:9443/agent/v1/connect
#
set -Eeuo pipefail

die() {
  echo "install.sh: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

[[ "$(id -u)" == "0" ]] || die "run as root (sudo)"
need_cmd uname
need_cmd mktemp
need_cmd curl
need_cmd unzip

platform="$(uname -s)"
case "$platform" in
  Linux) release_os="linux" ;;
  Darwin) release_os="darwin" ;;
  *) die "unsupported operating system: $platform" ;;
esac

machine="$(uname -m)"
case "$machine" in
  x86_64|amd64) release_arch="amd64" ;;
  aarch64|arm64) release_arch="arm64" ;;
  i386|i686) release_arch="386" ;;
  armv6l|armv7l|armv8l) release_arch="arm" ;;
  *) die "unsupported CPU architecture: $machine" ;;
esac

source_tmp="$(mktemp -d "${TMPDIR:-/tmp}/modelrelay-install.XXXXXX")"
trap 'rm -rf "$source_tmp"' EXIT

package_name="modelrelay-${release_os}-${release_arch}.zip"
package_url="https://github.com/Cardinal85/modelrelay/releases/latest/download/$package_name"
package_zip="$source_tmp/$package_name"
source_dir="$source_tmp/package"

echo "downloading $package_url ..."
curl -fL --retry 3 --retry-delay 2 "$package_url" -o "$package_zip"
unzip -q "$package_zip" -d "$source_dir"

[[ -f "$source_dir/relay" || -f "$source_dir/agent" ]] ||
  die "downloaded package is invalid: relay/agent binary not found"
[[ -f "$source_dir/scripts/deploy.sh" ]] ||
  die "downloaded package is invalid: scripts/deploy.sh not found"

exec bash "$source_dir/scripts/deploy.sh" --source-dir "$source_dir" "$@"
