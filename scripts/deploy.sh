#!/usr/bin/env bash
#
# ModelRelay 一键部署脚本（Linux / macOS）
#
# 示例：
#   sudo ./scripts/deploy.sh --source-dir ./dist/modelrelay-0.1.0-linux-amd64 \
#     --component relay
#   sudo ./scripts/deploy.sh --source-dir ./dist/modelrelay-0.1.0-linux-amd64 \
#     --component agent --node-id gpu-001 \
#     --relay-url wss://relay.example.com:9443/agent/v1/connect
#
set -Eeuo pipefail

component="both"
source_dir=""
no_start=0
relay_id=""
relay_url=""
node_id=""
local_base_url="http://127.0.0.1:8000/v1"
relay_cert=""
relay_key=""
agent_ca=""
agent_cert=""
agent_key=""
relay_ca=""

usage() {
  cat <<'EOF'
ModelRelay deploy.sh

Options:
  --source-dir DIR       release directory containing relay/agent binaries
  --component NAME       relay, agent, or both (default: both)
  --relay-id ID          relay_id when creating a new Relay config
  --node-id ID           node_id when creating a new Agent config
  --relay-url URL        primary WSS URL when creating a new Agent config
  --local-base-url URL   local model service URL (default: http://127.0.0.1:8000/v1)
  --relay-cert FILE      Relay server certificate path
  --relay-key FILE       Relay server private key path
  --agent-ca FILE        Agent CA certificate path
  --agent-cert FILE      Agent client certificate path
  --agent-key FILE       Agent client private key path
  --relay-ca FILE        Relay CA certificate path
  --no-start             install files but do not start services
  -h, --help             show this help

The script never overwrites an existing config or private key.
It creates random initial Relay/API admin secrets only when relay.env is absent.
EOF
}

die() {
  echo "deploy.sh: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

yaml_quote() {
  local value="${1//\'/\'\'}"
  printf "'%s'" "$value"
}

random_hex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
  fi
}

while (($# > 0)); do
  case "$1" in
    --source-dir) source_dir="${2:?missing value for --source-dir}"; shift 2 ;;
    --component) component="${2:?missing value for --component}"; shift 2 ;;
    --relay-id) relay_id="${2:?missing value for --relay-id}"; shift 2 ;;
    --node-id) node_id="${2:?missing value for --node-id}"; shift 2 ;;
    --relay-url) relay_url="${2:?missing value for --relay-url}"; shift 2 ;;
    --local-base-url) local_base_url="${2:?missing value for --local-base-url}"; shift 2 ;;
    --relay-cert) relay_cert="${2:?missing value for --relay-cert}"; shift 2 ;;
    --relay-key) relay_key="${2:?missing value for --relay-key}"; shift 2 ;;
    --agent-ca) agent_ca="${2:?missing value for --agent-ca}"; shift 2 ;;
    --agent-cert) agent_cert="${2:?missing value for --agent-cert}"; shift 2 ;;
    --agent-key) agent_key="${2:?missing value for --agent-key}"; shift 2 ;;
    --relay-ca) relay_ca="${2:?missing value for --relay-ca}"; shift 2 ;;
    --no-start) no_start=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

case "$component" in
  relay|agent|both) ;;
  *) die "--component must be relay, agent, or both" ;;
esac

[[ "$(id -u)" == "0" ]] || die "run as root (sudo)"
need_cmd uname
need_cmd install

os="$(uname -s)"
case "$os" in
  Linux)
    platform="linux"
    bin_dir="/opt/modelrelay/bin"
    relay_conf_dir="/etc/modelrelay"
    agent_conf_dir="/etc/model-agent"
    data_dir="/var/lib/modelrelay"
    need_cmd systemctl
    ;;
  Darwin)
    platform="darwin"
    bin_dir="/usr/local/libexec/modelrelay"
    relay_conf_dir="/Library/Application Support/ModelRelay"
    agent_conf_dir="/Library/Application Support/ModelAgent"
    data_dir="/Library/Application Support/ModelRelay/data"
    ;;
  *) die "unsupported operating system: $os" ;;
esac

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ -z "$source_dir" ]]; then
  source_dir="$script_dir/../bin"
fi
[[ -d "$source_dir" ]] || die "source directory not found: $source_dir (download and extract the platform release ZIP first)"
source_dir="$(cd -- "$source_dir" 2>/dev/null && pwd)" || die "cannot resolve source directory: $source_dir"

check_binary() {
  local name="$1"
  [[ -f "$source_dir/$name" ]] || die "missing binary: $source_dir/$name"
}

if [[ "$component" == relay || "$component" == both ]]; then check_binary relay; fi
if [[ "$component" == agent || "$component" == both ]]; then check_binary agent; fi
if [[ -f "$source_dir/certctl" ]]; then check_binary certctl; fi

copy_binary() {
  local name="$1"
  [[ -f "$source_dir/$name" ]] || die "missing binary: $source_dir/$name"
  install -d -m 0755 "$bin_dir"
  install -m 0755 "$source_dir/$name" "$bin_dir/$name"
}

if [[ "$component" == relay || "$component" == both ]]; then copy_binary relay; fi
if [[ "$component" == agent || "$component" == both ]]; then copy_binary agent; fi
if [[ -f "$source_dir/certctl" ]]; then copy_binary certctl; fi

if [[ "$platform" == linux ]]; then
  id modelrelay >/dev/null 2>&1 || useradd --system --home-dir "$data_dir" \
    --shell /usr/sbin/nologin modelrelay
  group="$(id -gn modelrelay)"
else
  group=""
fi

install -d -m 0750 "$data_dir"
if [[ "$platform" == linux ]]; then chown modelrelay:"$group" "$data_dir"; fi

if [[ -z "$relay_id" ]]; then relay_id="$(hostname -s 2>/dev/null || hostname)"; fi
if [[ -z "$node_id" ]]; then node_id="$(hostname -s 2>/dev/null || hostname)"; fi
if [[ -z "$relay_cert" ]]; then relay_cert="$relay_conf_dir/relay.crt"; fi
if [[ -z "$relay_key" ]]; then relay_key="$relay_conf_dir/relay.key"; fi
if [[ -z "$agent_ca" ]]; then agent_ca="$relay_conf_dir/agent-ca.crt"; fi
if [[ -z "$agent_cert" ]]; then agent_cert="$agent_conf_dir/$node_id.crt"; fi
if [[ -z "$agent_key" ]]; then agent_key="$agent_conf_dir/$node_id.key"; fi
if [[ -z "$relay_ca" ]]; then relay_ca="$agent_conf_dir/relay-ca.crt"; fi

write_relay_config() {
  install -d -m 0750 "$relay_conf_dir"
  local config="$relay_conf_dir/relay.yaml"
  [[ -e "$config" ]] && return
  cat >"$config" <<EOF
relay_id: $(yaml_quote "$relay_id")
relay_name: $(yaml_quote "$relay_id")
http_listen: "127.0.0.1:9100"
wss_listen: "0.0.0.0:9443"
tls_cert: $(yaml_quote "$relay_cert")
tls_key: $(yaml_quote "$relay_key")
agent_ca: $(yaml_quote "$agent_ca")
internal_auth:
  enabled: true
  token: "\${RELAY_INTERNAL_TOKEN}"
limits:
  max_body_bytes: 67108864
  max_concurrency: 64
  queue_length: 256
  queue_timeout_sec: 30
  ttft_timeout_sec: 120
  idle_timeout_sec: 300
  request_timeout_sec: 1800
  heartbeat_timeout_sec: 60
admin:
  listen: "127.0.0.1:9200"
  session_ttl_min: 30
  users:
    - username: admin
      password: "\${RELAY_ADMIN_PASSWORD}"
      role: admin
store:
  db_path: $(yaml_quote "$data_dir/modelrelay.db")
retention:
  keep_prompt_response: false
  retention_days: 30
log_level: info
EOF
  echo "created $config; review certificate paths before starting Relay"
}

write_agent_config() {
  install -d -m 0750 "$agent_conf_dir"
  local config="$agent_conf_dir/agent.yaml"
  [[ -e "$config" ]] && return
  cat >"$config" <<EOF
node_id: $(yaml_quote "$node_id")
max_body_bytes: 16777216
relays:
  - url: $(yaml_quote "${relay_url:-wss://relay.example.com:9443/agent/v1/connect}")
    priority: 1
tls:
  cert: $(yaml_quote "$agent_cert")
  key: $(yaml_quote "$agent_key")
  ca: $(yaml_quote "$relay_ca")
  insecure_skip_verify: false
local:
  base_url: $(yaml_quote "$local_base_url")
  api_key: "\${LOCAL_MODEL_API_KEY}"
  tls_verify: true
  connect_timeout_sec: 5
  response_timeout_sec: 1800
  max_concurrency: 8
probe:
  interval_sec: 600
  enabled: [chat, chat_stream, completions, embeddings, responses, tools]
heartbeat_interval: 20
log_level: info
EOF
  echo "created $config; review Relay, certificate, and model URL settings"
}

make_env_file() {
  local file="$1"
  if [[ -e "$file" ]]; then chmod 0600 "$file"; return; fi
  install -d -m 0750 "$(dirname "$file")"
  if [[ "$file" == *relay.env ]]; then
    cat >"$file" <<EOF
RELAY_INTERNAL_TOKEN=$(random_hex)
RELAY_ADMIN_PASSWORD=$(random_hex)
EOF
    echo "created $file; save the generated admin password before sharing the host"
  else
    printf 'LOCAL_MODEL_API_KEY=\n' >"$file"
  fi
  chmod 0600 "$file"
}

if [[ "$component" == relay || "$component" == both ]]; then
  write_relay_config
  make_env_file "$relay_conf_dir/relay.env"
fi
if [[ "$component" == agent || "$component" == both ]]; then
  write_agent_config
  make_env_file "$agent_conf_dir/agent.env"
fi

if [[ "$platform" == linux ]]; then
  if [[ "$component" == relay || "$component" == both ]]; then
    chown -R modelrelay:"$group" "$data_dir" "$relay_conf_dir"
  fi
  if [[ "$component" == agent || "$component" == both ]]; then
    chown -R modelrelay:"$group" "$agent_conf_dir"
  fi
  install_linux_unit() {
    local name="$1" binary="$2" config="$3" env_file="$4" unit="/etc/systemd/system/modelrelay-$1.service" display
    display="Relay"
    [[ "$name" == agent ]] && display="Agent"
    cat >"$unit" <<EOF
[Unit]
Description=ModelRelay $display
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=modelrelay
Group=$group
WorkingDirectory=$data_dir
EnvironmentFile=-$env_file
ExecStart=$bin_dir/$binary -config $config
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$data_dir

[Install]
WantedBy=multi-user.target
EOF
  }

  if [[ "$component" == relay || "$component" == both ]]; then
    install_linux_unit relay relay "$relay_conf_dir/relay.yaml" "$relay_conf_dir/relay.env"
  fi
  if [[ "$component" == agent || "$component" == both ]]; then
    install_linux_unit agent agent "$agent_conf_dir/agent.yaml" "$agent_conf_dir/agent.env"
  fi
  systemctl daemon-reload
  if ((no_start == 0)); then
    for service in relay agent; do
      [[ "$component" == "$service" || "$component" == both ]] || continue
      if ! systemctl enable --now "modelrelay-$service"; then
        echo "warning: modelrelay-$service was installed but did not start; check journalctl" >&2
      fi
    done
  fi
else
  install_launchd_plist() {
    local name="$1" binary="$2" config="$3" env_file="$4"
    local wrapper="$bin_dir/run-$name.sh"
    local plist="/Library/LaunchDaemons/com.modelrelay.$name.plist"
    cat >"$wrapper" <<EOF
#!/usr/bin/env bash
set -a
. "$env_file"
set +a
exec "$bin_dir/$binary" -config "$config"
EOF
    chmod 0755 "$wrapper"
    cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.modelrelay.$name</string>
  <key>ProgramArguments</key><array>
    <string>$wrapper</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>WorkingDirectory</key><string>$data_dir</string>
  <key>StandardOutPath</key><string>/var/log/modelrelay-$name.log</string>
  <key>StandardErrorPath</key><string>/var/log/modelrelay-$name.err</string>
</dict></plist>
EOF
    chmod 0644 "$plist"
  }

  if [[ "$component" == relay || "$component" == both ]]; then
    install_launchd_plist relay relay "$relay_conf_dir/relay.yaml" "$relay_conf_dir/relay.env"
  fi
  if [[ "$component" == agent || "$component" == both ]]; then
    install_launchd_plist agent agent "$agent_conf_dir/agent.yaml" "$agent_conf_dir/agent.env"
  fi
  if ((no_start == 0)); then
    for name in relay agent; do
      [[ "$component" == "$name" || "$component" == both ]] || continue
      label="system/com.modelrelay.$name"
      launchctl bootout "$label" >/dev/null 2>&1 || true
      launchctl bootstrap system "/Library/LaunchDaemons/com.modelrelay.$name.plist" || \
        echo "warning: failed to start $name; check /var/log/modelrelay-$name.err" >&2
    done
  fi
fi

echo
echo "ModelRelay deployment files installed."
echo "Next steps: copy certificates, review generated YAML, then check service logs."
