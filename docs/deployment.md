# ModelRelay 部署与运维指南

> 本文档按部署方式分类。先选择一种方式，再阅读对应平台章节。

## 快速选择部署方式

| 部署方式 | 适用场景 | 入口 |
|---|---|---|
| 一键部署脚本 | 新机器快速安装，优先推荐 | [方式一：一键部署](#方式一一键部署推荐) |
| Linux systemd | Linux 生产环境，需要精细控制权限和服务参数 | [方式二：Linux systemd](#方式二linux-systemd) |
| Windows NSSM/任务计划 | Windows 生产环境 | [方式三：Windows 服务](#方式三windows-服务) |
| macOS launchd | macOS Agent 常驻运行 | [方式四：macOS launchd](#方式四macos-launchd) |
| 主备 Relay | Relay 高可用部署 | [方式五：主备 Relay](#方式五主备-relay) |

所有方式都需要先完成[通用准备](#通用准备发布包)和
[拓扑与证书准备](#通用准备部署拓扑与证书)，部署完成后统一按照
[链路验证](#公共运维链路验证)检查。

## 文档结构

1. [通用准备：发布包](#通用准备发布包)。
2. [方式一：一键部署](#方式一一键部署推荐)：Linux、macOS、Windows。
3. [通用准备：拓扑与证书](#通用准备部署拓扑与证书)。
4. [方式二：Linux systemd](#方式二linux-systemd)：手工安装和服务托管。
5. [方式三：Windows 服务](#方式三windows-服务)：NSSM、WinSW 或任务计划。
6. [方式四：macOS launchd](#方式四macos-launchd)：Agent 常驻服务。
7. [方式五：主备 Relay](#方式五主备-relay)：双 Relay 和故障切换。
8. [公共运维](#公共运维管理与日常操作)：New API、WebUI、备份、升级、排障和验收。

## 通用准备：发布包

在构建机执行：

```powershell
# 当前平台构建到 bin/
powershell -File scripts/build.ps1

# 运行 vet、单元测试和端到端测试
powershell -File scripts/build.ps1 -Test

# 生成 Linux/Windows/macOS 的 amd64、arm64、386/arm 产物
powershell -File scripts/build.ps1 -All
```

发布前校验对应目录下的 `SHA256SUMS`：

```bash
sha256sum -c SHA256SUMS
```

Windows 可使用：

```powershell
Get-FileHash .\relay.exe -Algorithm SHA256
```

`dist/` 下的目录名包含操作系统和架构，例如
`modelrelay-0.1.0-linux-amd64`、`modelrelay-0.1.0-windows-arm64`。

## 方式一：一键部署（推荐）

Linux 和 macOS 使用 `deploy.sh`；脚本会安装二进制、生成服务文件和配置模板，
不会覆盖已有配置、证书或私钥：

```bash
# Linux Relay
sudo bash scripts/deploy.sh \
  --source-dir dist/modelrelay-0.1.0-linux-amd64 \
  --component relay

# Linux Agent
sudo bash scripts/deploy.sh \
  --source-dir dist/modelrelay-0.1.0-linux-amd64 \
  --component agent \
  --node-id gpu-001 \
  --relay-url wss://relay.example.com:9443/agent/v1/connect \
  --local-base-url http://127.0.0.1:8000/v1

# macOS 使用 darwin-amd64 或 darwin-arm64 发布目录，参数相同
sudo bash scripts/deploy.sh \
  --source-dir dist/modelrelay-0.1.0-darwin-arm64 \
  --component agent --node-id mac-001
```

Windows 使用管理员 PowerShell：

```powershell
.\scripts\deploy.ps1 `
  -SourceDir .\dist\modelrelay-0.1.0-windows-amd64 `
  -Component Relay

.\scripts\deploy.ps1 `
  -SourceDir .\dist\modelrelay-0.1.0-windows-amd64 `
  -Component Agent `
  -NodeId gpu-001 `
  -RelayUrl wss://relay.example.com:9443/agent/v1/connect
```

Windows 优先使用已安装的 NSSM；未安装时自动使用 Task Scheduler。首次运行后，
必须把证书复制到脚本生成的路径，并检查生成的 YAML；如需只安装不启动服务，追加
`--no-start` 或 `-NoStart`。

## 通用准备：部署拓扑与证书

### 部署拓扑

#### 单 Relay（默认）

```text
公网服务器:
  Nginx(443) → New API(3000)
                  │ http://127.0.0.1:9100/v1 + 内部认证
                  ▼
              relay (HTTP :9100, WSS :9443, WebUI :9200)
                  ▲ WSS + mTLS
              agent-01 / agent-02 / agent-03 (内网 GPU 机)
```

- GPU 节点不需要任何公网入站端口，Agent 只主动出站连接 Relay。
- Relay HTTP 与 WebUI 只监听本机/受保护内网。

#### 主备 Relay

```text
agent → Relay-A (priority 1)   ← 主
      → Relay-B (priority 2)   ← 备（New API 需能访问 B 的 HTTP 地址）
```

- 同一 Agent 同时只保持一个有效连接。
- 主故障 → 退避重连备并重新注册；主恢复 → 自动回切（`prefer_primary_interval`）。
- 进行中的请求不迁移。

### 证书准备（certctl）

```bash
# 证书管理机（离线保存 agent-ca.key）
certctl init-ca -out /etc/modelrelay/ca

# Agent 机器（私钥不离开本机）
certctl csr -cn gpu-001 -out /etc/model-agent/

# 证书管理机签发
certctl issue -ca ca/agent-ca.crt -ca-key ca/agent-ca.key \
    -csr /etc/model-agent/gpu-001.csr -cn gpu-001 -out /etc/model-agent/

# Relay 服务端证书（公网部署建议 Let's Encrypt；私有部署可自签）
certctl server-cert -ca ca/agent-ca.crt -ca-key ca/agent-ca.key \
    -cn relay.example.com -dns relay.example.com -ip 1.2.3.4 -out /etc/modelrelay/
```

## 公共运维：管理与日常操作

### 接入 New API

新增 OpenAI-compatible 渠道：

```text
Base URL: http://127.0.0.1:9100/v1
密钥:     relay.yaml 的 internal_auth.token
```

- Relay 的 `GET /v1/models` 返回当前可用模型目录，New API 可配置定时同步。
- 不支持的能力接口不会被路由（返回 422），模型不存在返回 404。

### WebUI / 管理 API 运维操作

| 操作 | 说明 | 审计 |
|---|---|---|
| 立即探测 | 触发 Agent 重新执行能力探测 | 记录 |
| Drain | 停止分配新请求，等待存量完成 | 记录 |
| 踢出节点 | 强制断开并注销 | 记录 |
| 修改并发 | 调整节点最大并发 | 记录 |
| 吊销证书 | 证书吊销后立即断开当前连接，并拒绝后续重连 | 记录 |
| 数据保留 | Prompt/Response 保存开关与保留天数 | 记录 |

默认账号在 `relay.yaml` 的 `admin.users` 中配置（首次启动写入 SQLite）。

### 备份与恢复

需要备份：

- SQLite 数据库（`store.db_path`）。
- 证书元数据、吊销记录（在 SQLite 内）。
- Relay / Agent 配置（不含私钥）。
- WebUI 管理配置（SQLite 内）。

恢复：停 Relay → 替换数据库文件 → 启动。Agent 私钥与敏感 Token 不应放入普通备份包。

SQLite 备份（在线安全）：

```bash
sqlite3 modelrelay.db ".backup modelrelay.backup.db"
```

### 升级与回滚

- 升级：停服务 → 替换二进制 → 启动 → 检查日志与 `/api/overview`。
- 回滚：保留旧版本二进制，直接替换回去；数据库 schema 向后兼容（迁移只增不改）。
- 发布包带 `SHA256SUMS` 校验文件，升级前校验：

```bash
cd dist/modelrelay-0.1.0-linux-amd64 && sha256sum -c SHA256SUMS
```

### 故障排查

| 现象 | 排查 |
|---|---|
| Agent 连不上 | `agent -config agent.yaml` 日志；证书 CN 与 node_id 是否一致；CA 是否正确 |
| 节点状态 suspect/offline | 网络是否通；心跳是否被墙；`heartbeat_timeout_sec` 配置 |
| 请求 422 capability_not_supported | 该模型已完成能力探测且明确不支持；触发“立即探测”或检查探测结果 |
| 请求 404 model_not_found | 模型未被任何在线节点注册 |
| 请求 429 queue_full | 并发/队列过小或节点全部繁忙 |
| 请求 504 ttft_timeout | 本地模型响应慢；调大 `ttft_timeout_sec` |
| 流式中断 | `idle_timeout_sec` 过小；本地服务卡死；背压 |
| WebUI 401 | 会话过期；重新登录 |

### 监控

- 管理 API `GET /api/overview` 返回节点数、活动/排队请求、临期证书、错误统计。
- 日志字段：时间、`request_id`、节点、模型、路径、状态码、TTFT、总耗时、错误码（默认脱敏）。
- 建议告警：节点连续掉线、Agent 连续重连、证书临期、队列持续增长、错误率升高、TTFT 异常。

### 部署前规划

#### 端口与网络

| 端口 | 组件 | 访问方 | 建议暴露范围 |
|---|---|---|---|
| `9100/tcp` | Relay HTTP 上游 | New API | 仅本机或业务内网 |
| `9443/tcp` | Relay WSS | Agent | Agent 所在网络可访问 |
| `9200/tcp` | WebUI/管理 API | 运维人员 | 仅本机或管理网 |
| `8000/tcp` | 本地模型服务 | Agent | 仅模型机本地 |

不要把 `9100` 或 `9200` 直接暴露到公网。公网只需要提供 Agent 到 Relay 的
WSS 连接；New API 与 Relay 分机时，应通过专用内网、VPN 或防火墙白名单访问
`9100`。

#### 资源建议

- Relay：至少 2 vCPU、2 GiB 内存；高并发时按 `max_concurrency` 和 `queue_length`
  增加内存。
- Agent：至少 1 vCPU、256 MiB 内存；GPU、CPU 和显存由本地推理服务负责。
- SQLite 数据库必须放在持久化磁盘，不要放在容器临时层。

#### 证书体系与签发顺序

生产环境建议使用两套 CA，避免 Relay 服务端身份与 Agent 客户端身份共用信任根：

```text
Agent CA
  └── Agent 客户端证书（Relay 校验）

Relay CA 或公共 CA
  └── Relay 服务端证书（Agent 校验）
```

`certctl` 不会把 CA 私钥放入 Relay 或 Agent。推荐在离线证书管理机执行：

```bash
mkdir -p /secure/modelrelay/agent-ca /secure/modelrelay/relay-ca

# Agent CA；agent-ca.key 只留在证书管理机
certctl init-ca -out /secure/modelrelay/agent-ca -cn "ModelRelay Agent CA"

# Relay CA；使用公共 CA 时可跳过
certctl init-ca -out /secure/modelrelay/relay-ca -cn "ModelRelay Relay CA"

# 在每台 Agent 本地执行，私钥不上传
certctl csr -cn gpu-001 -out /etc/model-agent

# 回到证书管理机，用 Agent CA 签发 Agent 证书
certctl issue \
  -ca /secure/modelrelay/agent-ca/agent-ca.crt \
  -ca-key /secure/modelrelay/agent-ca/agent-ca.key \
  -csr /path/to/gpu-001.csr \
  -cn gpu-001 -days 365 -out /secure/modelrelay/issued

# 用 Relay CA 签发服务端证书；SAN 必须包含 Agent 使用的域名/IP
certctl server-cert \
  -ca /secure/modelrelay/relay-ca/agent-ca.crt \
  -ca-key /secure/modelrelay/relay-ca/agent-ca.key \
  -cn relay-a.example.com -dns relay-a.example.com -ip 203.0.113.10 \
  -days 365 -out /secure/modelrelay/relay-server
```

上例中 `server-cert` 按输入 CA 生成服务端证书；实际文件名以命令输出为准。
如果使用公共 CA，Agent 的 `tls.ca` 应填写对应的公共 CA 链。

检查证书：

```bash
certctl inspect -cert /etc/model-agent/gpu-001.crt
certctl inspect -cert /etc/modelrelay/relay-a.crt
```

Agent 证书的 CN 必须等于 `node_id`，并包含对应的 ModelRelay URI 身份。证书过期、
泄露或人员离职时，应在 WebUI 的“证书”页面吊销，并确认节点已离线。

## 方式二：Linux systemd

### 安装目录与权限

```bash
sudo install -d -m 0750 /opt/modelrelay/bin /etc/modelrelay /var/lib/modelrelay
sudo useradd --system --home /var/lib/modelrelay --shell /usr/sbin/nologin modelrelay
sudo install -m 0755 dist/modelrelay-0.1.0-linux-amd64/relay /opt/modelrelay/bin/relay
sudo install -m 0755 dist/modelrelay-0.1.0-linux-amd64/agent /opt/modelrelay/bin/agent
sudo install -m 0755 dist/modelrelay-0.1.0-linux-amd64/certctl /opt/modelrelay/bin/certctl
sudo chown -R modelrelay:modelrelay /var/lib/modelrelay
```

将配置、Relay 证书、Agent CA 公钥复制到 `/etc/modelrelay`，私钥权限设为
`0600`，所有权设为 `modelrelay:modelrelay`。

### Relay 配置与 systemd

以 `configs/relay.example.yaml` 为基础创建 `/etc/modelrelay/relay.yaml`：

```yaml
relay_id: relay-a
http_listen: "127.0.0.1:9100"
wss_listen: "0.0.0.0:9443"
tls_cert: "/etc/modelrelay/relay-a.crt"
tls_key: "/etc/modelrelay/relay-a.key"
agent_ca: "/etc/modelrelay/agent-ca.crt"
internal_auth:
  enabled: true
  token: "${RELAY_INTERNAL_TOKEN}"
admin:
  listen: "127.0.0.1:9200"
store:
  db_path: "/var/lib/modelrelay/modelrelay.db"
retention:
  keep_prompt_response: false
  retention_days: 30
```

创建 `/etc/modelrelay/relay.env`：

```bash
RELAY_INTERNAL_TOKEN=请替换为高强度随机值
RELAY_ADMIN_PASSWORD=请替换为首次管理员密码
```

`admin.users` 中的密码只用于首次初始化；初始化完成后应移除明文密码配置。

`/etc/systemd/system/modelrelay-relay.service`：

```ini
[Unit]
Description=ModelRelay Relay
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=modelrelay
Group=modelrelay
WorkingDirectory=/var/lib/modelrelay
EnvironmentFile=/etc/modelrelay/relay.env
ExecStart=/opt/modelrelay/bin/relay -config /etc/modelrelay/relay.yaml
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/modelrelay

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now modelrelay-relay
sudo systemctl status modelrelay-relay --no-pager
sudo journalctl -u modelrelay-relay -n 100 --no-pager
```

### Agent 配置与 systemd

在模型机创建 `/etc/model-agent/agent.yaml`：

```yaml
node_id: gpu-001
max_body_bytes: 16777216
relays:
  - url: "wss://relay.example.com:9443/agent/v1/connect"
    priority: 1
tls:
  cert: "/etc/model-agent/gpu-001.crt"
  key: "/etc/model-agent/gpu-001.key"
  ca: "/etc/model-agent/relay-ca.crt"
  insecure_skip_verify: false
local:
  base_url: "http://127.0.0.1:8000/v1"
  api_key: "${LOCAL_MODEL_API_KEY}"
  tls_verify: true
  connect_timeout_sec: 5
  response_timeout_sec: 1800
  max_concurrency: 8
probe:
  interval_sec: 600
  enabled: [chat, chat_stream, completions, embeddings, responses, tools]
heartbeat_interval: 20
```

先确认本地模型服务可用：

```bash
curl -fsS http://127.0.0.1:8000/v1/models
```

`/etc/systemd/system/modelrelay-agent.service`：

```ini
[Unit]
Description=ModelRelay Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=modelrelay
Group=modelrelay
EnvironmentFile=/etc/model-agent/agent.env
ExecStart=/opt/modelrelay/bin/agent -config /etc/model-agent/agent.yaml
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now modelrelay-agent
sudo journalctl -u modelrelay-agent -f
```

## 方式三：Windows 服务

Windows 版本不依赖 CGO。将对应目录中的 `relay.exe`、`agent.exe` 和证书复制到
`C:\ModelRelay\bin`，配置放到 `C:\ModelRelay\etc`，数据库放到
`C:\ModelRelay\data`。

生产环境建议使用 NSSM 或 WinSW 包装为 Windows Service，因为两个程序是控制台
程序。以 NSSM 为例：

```powershell
nssm install ModelRelayRelay C:\ModelRelay\bin\relay.exe
nssm set ModelRelayRelay AppParameters "-config C:\ModelRelay\etc\relay.yaml"
nssm set ModelRelayRelay AppDirectory C:\ModelRelay\data
nssm set ModelRelayRelay AppEnvironmentExtra RELAY_INTERNAL_TOKEN=替换为随机值
nssm set ModelRelayRelay Start SERVICE_AUTO_START
nssm start ModelRelayRelay

nssm install ModelRelayAgent C:\ModelRelay\bin\agent.exe
nssm set ModelRelayAgent AppParameters "-config C:\ModelRelay\etc\agent.yaml"
nssm set ModelRelayAgent AppDirectory C:\ModelRelay\data
nssm set ModelRelayAgent Start SERVICE_AUTO_START
nssm start ModelRelayAgent
```

防火墙只放行 Relay 的 `9443`，并限制来源网段；`9100`、`9200` 只允许 New API
或管理机访问。

## 方式四：macOS launchd

将 `darwin-amd64` 或 `darwin-arm64` 目录中的 Agent 放到模型机，例如
`/usr/local/libexec/modelrelay-agent`，配置和证书放在
`/Library/Application Support/ModelRelay`。

`/Library/LaunchDaemons/com.modelrelay.agent.plist` 示例：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.modelrelay.agent</string>
  <key>ProgramArguments</key><array>
    <string>/usr/local/libexec/modelrelay-agent</string>
    <string>-config</string>
    <string>/Library/Application Support/ModelRelay/agent.yaml</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/modelrelay-agent.log</string>
  <key>StandardErrorPath</key><string>/var/log/modelrelay-agent.err</string>
</dict></plist>
```

```bash
sudo launchctl bootstrap system /Library/LaunchDaemons/com.modelrelay.agent.plist
sudo launchctl kickstart -k system/com.modelrelay.agent
sudo launchctl print system/com.modelrelay.agent
```

## 公共运维：链路验证

Relay 与 New API 同机时，在 New API 中配置：

```text
类型：OpenAI-compatible
Base URL：http://127.0.0.1:9100/v1
密钥：relay.yaml 的 internal_auth.token
```

Relay 与 New API 分机时，将 Base URL 改为 Relay 内网地址，并确保 Relay 的
`http_listen` 不是公网监听地址。

```bash
# 模型目录
curl -i http://127.0.0.1:9100/v1/models \
  -H "Authorization: Bearer ${RELAY_INTERNAL_TOKEN}"

# 非流式 Chat
curl -i http://127.0.0.1:9100/v1/chat/completions \
  -H "Authorization: Bearer ${RELAY_INTERNAL_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen-local","messages":[{"role":"user","content":"hello"}]}'

# SSE 流式 Chat
curl -N http://127.0.0.1:9100/v1/chat/completions \
  -H "Authorization: Bearer ${RELAY_INTERNAL_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen-local","stream":true,"messages":[{"role":"user","content":"hello"}]}'
```

随后打开 `http://127.0.0.1:9200/` 登录 WebUI，确认节点为 `online`，模型、
能力、心跳和请求记录均正常。首次探测结束前，未完成探测的接口按“能力未知”处理；
只有明确完成能力目录后，Relay 才会返回 `capability_not_supported`。

## 方式五：主备 Relay

主备模式需要两个独立 Relay：

1. 两个 Relay 使用不同的 `relay_id`、监听地址和数据库文件。
2. 两个 Relay 都配置同一套 Agent CA，以接受同一批 Agent。
3. 两个 Relay 的服务端证书 SAN 分别覆盖各自域名。
4. New API 为两个 Relay 分别配置渠道，或通过上层负载均衡访问。
5. Agent 配置两个 WSS 地址：

```yaml
relays:
  - url: "wss://relay-a.example.com:9443/agent/v1/connect"
    priority: 1
  - url: "wss://relay-b.example.com:9443/agent/v1/connect"
    priority: 2
prefer_primary_interval: 60
```

主 Relay 故障后，Agent 会退避重连并切换到备 Relay；已有请求不会迁移，需要由
New API 或调用方重新发起。主 Relay 恢复后，Agent 按
`prefer_primary_interval` 探测并回切。

## 公共运维：证书轮换、备份与回滚

### Agent 证书轮换

1. 在 Agent 本地生成新 CSR 和私钥。
2. 用 Agent CA 签发新证书并复制到 Agent 目录。
3. 停止 Agent，替换证书文件，启动 Agent。
4. 在 WebUI 确认新证书已登记并上线。
5. 确认旧证书不再使用后吊销旧序列号。

不要先吊销唯一正在使用的证书再准备新证书，否则 Agent 会暂时无法上线。

### 备份

```bash
sudo systemctl stop modelrelay-relay
sudo cp /var/lib/modelrelay/modelrelay.db /backup/modelrelay-$(date +%F).db
sudo systemctl start modelrelay-relay

# 在线备份
sqlite3 /var/lib/modelrelay/modelrelay.db \
  ".backup '/backup/modelrelay-online.db'"
```

备份范围包括 SQLite、配置和证书公钥；不要把 Agent 私钥、CA 私钥和内部 Token
放入普通共享备份。

### 升级与回滚

```bash
sha256sum -c SHA256SUMS
sudo systemctl stop modelrelay-agent
sudo install -m 0755 agent.new /opt/modelrelay/bin/agent
sudo systemctl start modelrelay-agent
sudo journalctl -u modelrelay-agent -n 50 --no-pager
```

Relay 升级前先备份数据库。数据库迁移只增不改；若启动失败，恢复旧二进制并保留
数据库，确认版本兼容后再处理迁移。主备环境建议先升级备 Relay，验证后再升级主 Relay。

## 上线验收清单

- [ ] Relay、Agent、模型服务分别能独立启动和停止。
- [ ] Agent 证书 CN/URI 身份与 `node_id` 匹配，Relay CA 校验有效。
- [ ] Relay `9443` 可达，`9100` 和 `9200` 未暴露公网。
- [ ] WebUI 能登录，节点状态为 `online`，模型目录正确。
- [ ] `/v1/models`、Chat 非流式、Chat SSE 流式请求成功。
- [ ] Embeddings、Responses、音频或图片接口按实际模型能力验证。
- [ ] 客户端断开后，本地请求可取消并释放并发。
- [ ] Drain 不再接收新请求，存量请求完成后返回 `drain_ack`。
- [ ] 吊销证书后当前连接断开，旧证书不能重新注册。
- [ ] SQLite 备份、恢复和旧版本回滚演练完成。
- [ ] 至少完成目标操作系统上的启动级验证；真实 GPU 模型联调不能只使用 mock 服务。
