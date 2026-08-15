# ModelRelay 部署指南

本文档只有一条主流程：**Relay → 证书 → Agent → New API → 验收**。
请从第 0 步开始执行。平台差异和手工安装放在文档末尾。

## 0. 先确认部署信息

填写下面的实际值，后文命令中的占位符都要替换：

```text
RELAY_HOST=relay.example.com
RELAY_IP=203.0.113.10
RELAY_PORT=9443
NODE_ID=gpu-001
MODEL_URL=http://127.0.0.1:8000/v1
```

需要三类机器：

1. **Relay 主机**：Agent 可以访问 TCP `9443`。
2. **GPU 主机**：已经有本地 OpenAI-compatible 模型服务。
3. **证书管理机**：保存 CA 私钥，不与生产服务共用。

网络只需要允许：

| 端口 | 用途 | 建议 |
|---|---|---|
| `9443/tcp` | Agent → Relay WSS | 只允许 GPU 网段 |
| `9100/tcp` | New API → Relay API | 只允许 New API 网段 |
| `9200/tcp` | 管理 WebUI | 只监听本机或管理网 |
| `8000/tcp` | Agent → 本地模型 | 只监听模型机本机 |

## 1. 安装 Relay

### Linux

在 Relay 主机执行。安装器会自动检测架构、下载最新包、解压并检查二进制：

```bash
curl -fsSL \
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
  | sudo bash -s -- --component relay
```

CentOS 缺少依赖时：

```bash
sudo yum install -y curl unzip
```

安装结果：

```text
服务：/etc/systemd/system/modelrelay-relay.service
配置：/etc/modelrelay/relay.yaml
环境：/etc/modelrelay/relay.env
数据：/var/lib/modelrelay/modelrelay.db
```

### Windows 或 macOS

Windows 使用管理员 PowerShell：

```powershell
$p = Join-Path $env:TEMP "modelrelay-install.ps1"
Invoke-WebRequest -UseBasicParsing `
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.ps1 `
  -OutFile $p
powershell -ExecutionPolicy Bypass -File $p -Component Relay
```

macOS 使用：

```bash
curl -fsSL \
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
  | sudo bash -s -- --component relay
```

## 2. 创建并签发证书

生产环境使用两套 CA：

```text
Agent CA → Agent 客户端证书 → Relay 校验
Relay CA  → Relay 服务端证书 → Agent 校验
```

以下命令使用发布包中的 `certctl`。CA 私钥只留在证书管理机。

### 2.1 创建 CA（证书管理机）

```bash
mkdir -p ./ca/agent ./ca/relay

certctl init-ca \
  -out ./ca/agent \
  -cn "ModelRelay Agent CA"

certctl init-ca \
  -out ./ca/relay \
  -cn "ModelRelay Relay CA"
```

`certctl init-ca` 会在每个目录生成 `agent-ca.crt` 和 `agent-ca.key`。

### 2.2 生成 Agent CSR（GPU 主机）

在 GPU 主机本地生成私钥，私钥不要上传到证书管理机：

```bash
sudo /opt/modelrelay/bin/certctl csr \
  -cn gpu-001 \
  -out /etc/model-agent
```

生成：

```text
/etc/model-agent/gpu-001.key
/etc/model-agent/gpu-001.csr
```

只把 `gpu-001.csr` 复制到证书管理机。

### 2.3 签发 Agent 证书（证书管理机）

```bash
certctl issue \
  -ca ./ca/agent/agent-ca.crt \
  -ca-key ./ca/agent/agent-ca.key \
  -csr ./gpu-001.csr \
  -cn gpu-001 \
  -days 365 \
  -out ./issued
```

生成 `./issued/gpu-001.crt`，把它复制回 GPU 主机的
`/etc/model-agent/gpu-001.crt`。

### 2.4 签发 Relay 服务端证书（证书管理机）

证书的 DNS/IP SAN 必须包含 Agent 实际连接时使用的地址：

```bash
certctl server-cert \
  -ca ./ca/relay/agent-ca.crt \
  -ca-key ./ca/relay/agent-ca.key \
  -cn relay.example.com \
  -dns relay.example.com \
  -ip 203.0.113.10 \
  -days 365 \
  -out ./issued
```

生成 `relay.example.com.crt` 和 `relay.example.com.key`。
后文将它们分别复制为 Relay 的 `relay.crt` 和 `relay.key`。

## 3. 配置并验证 Relay

### 3.1 复制 Relay 证书

```bash
sudo install -m 0644 relay.example.com.crt /etc/modelrelay/relay.crt
sudo install -m 0600 relay.example.com.key /etc/modelrelay/relay.key
sudo install -m 0644 ./ca/agent/agent-ca.crt /etc/modelrelay/agent-ca.crt
sudo chown modelrelay:modelrelay \
  /etc/modelrelay/relay.crt \
  /etc/modelrelay/relay.key \
  /etc/modelrelay/agent-ca.crt
```

### 3.2 检查配置

确认 `/etc/modelrelay/relay.yaml` 至少包含：

```yaml
http_listen: "127.0.0.1:9100"
wss_listen: "0.0.0.0:9443"
tls_cert: "/etc/modelrelay/relay.crt"
tls_key: "/etc/modelrelay/relay.key"
agent_ca: "/etc/modelrelay/agent-ca.crt"
admin:
  listen: "127.0.0.1:9200"
```

不要把 `9100` 或 `9200` 直接暴露到公网。

### 3.3 启动并检查

```bash
sudo systemctl restart modelrelay-relay
sudo systemctl status modelrelay-relay --no-pager
sudo journalctl -u modelrelay-relay -n 100 --no-pager
```

服务必须是 `active (running)`。如果失败，先修复日志中的证书或配置问题，
不要继续安装 Agent。

### 3.4 验证 Relay API 和 WebUI

查看管理员密码：

```bash
sudo sed -n 's/^RELAY_ADMIN_PASSWORD=//p' /etc/modelrelay/relay.env
```

验证模型目录：

```bash
TOKEN="$(sudo sed -n 's/^RELAY_INTERNAL_TOKEN=//p' /etc/modelrelay/relay.env)"
curl -i http://127.0.0.1:9100/v1/models \
  -H "Authorization: Bearer $TOKEN"
unset TOKEN
```

WebUI 默认只监听 `127.0.0.1:9200`。远程查看：

```bash
ssh -L 9200:127.0.0.1:9200 root@relay.example.com
```

浏览器打开 `http://127.0.0.1:9200/`，使用 `admin` 登录。

## 4. 安装并配置 Agent

### 4.1 安装 Agent

在 GPU 主机执行：

```bash
curl -fsSL \
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
  | sudo bash -s -- --component agent \
    --node-id gpu-001 \
    --relay-url wss://relay.example.com:9443/agent/v1/connect
```

### 4.2 复制 Agent 证书

```bash
sudo install -m 0644 gpu-001.crt /etc/model-agent/gpu-001.crt
sudo install -m 0600 gpu-001.key /etc/model-agent/gpu-001.key
sudo install -m 0644 ./ca/relay/agent-ca.crt /etc/model-agent/relay-ca.crt
sudo chown modelrelay:modelrelay \
  /etc/model-agent/gpu-001.crt \
  /etc/model-agent/gpu-001.key \
  /etc/model-agent/relay-ca.crt
```

### 4.3 检查 Agent 配置

确认 `/etc/model-agent/agent.yaml`：

```yaml
node_id: gpu-001
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
  tls_verify: true
```

先确认本地模型服务：

```bash
curl -fsS http://127.0.0.1:8000/v1/models
```

再启动 Agent：

```bash
sudo systemctl restart modelrelay-agent
sudo systemctl status modelrelay-agent --no-pager
sudo journalctl -u modelrelay-agent -n 100 --no-pager
```

## 5. 验证节点并接入 New API

### 5.1 验证节点

回到 Relay WebUI，确认：

- `gpu-001` 状态为 `online`
- 模型目录已经出现
- 心跳持续更新
- 能力探测完成

Agent 连不上时，按顺序检查：Agent 日志、Relay `9443` 防火墙、
Relay 地址 DNS、两套 CA、证书 SAN、证书身份和 `node_id`。

### 5.2 配置 New API

新增 OpenAI-compatible 渠道：

```text
Base URL: http://relay.example.com:9100/v1
密钥:     /etc/modelrelay/relay.env 中的 RELAY_INTERNAL_TOKEN
```

先同步 `GET /v1/models`，再测试 Chat 非流式和 SSE 流式请求。

## 6. 上线验收

- [ ] Relay、Agent 均为 `active (running)`。
- [ ] Agent 在 WebUI 中为 `online`。
- [ ] `/v1/models` 返回模型。
- [ ] Chat 非流式请求成功。
- [ ] Chat SSE 流式请求成功。
- [ ] 客户端断开后本地请求能够取消。
- [ ] `9443` 只允许 Agent 网络访问。
- [ ] `9100`、`9200` 没有暴露公网。
- [ ] 数据库、配置和证书公钥已备份。
- [ ] CA 私钥、Agent 私钥、Relay 私钥和 Token 没有进入仓库。

## 7. 其他部署方式

### 手工 Linux systemd

不使用一键脚本时，安装二进制到 `/opt/modelrelay/bin/`，
配置放到 `/etc/modelrelay/` 和 `/etc/model-agent/`，
再参考 `scripts/deploy.sh` 生成的 systemd 服务文件。

### Windows 服务

使用管理员 PowerShell 执行 `scripts/install.ps1`。
生产环境优先使用 NSSM/WinSW；没有包装器时使用 Windows 任务计划。

### macOS launchd

使用 `scripts/install.sh` 安装 Agent，再将其注册为
`/Library/LaunchDaemons/com.modelrelay.agent.plist`。

### 主备 Relay

在 Agent 配置两个 Relay 地址：

```yaml
relays:
  - url: "wss://relay-a.example.com:9443/agent/v1/connect"
    priority: 1
  - url: "wss://relay-b.example.com:9443/agent/v1/connect"
    priority: 2
prefer_primary_interval: 60
```

进行中的请求不会迁移，故障恢复后 Agent 会重新注册。

## 8. 日常运维

### 备份

```bash
sudo sqlite3 /var/lib/modelrelay/modelrelay.db \
  ".backup '/backup/modelrelay-$(date +%F).db'"
```

同时备份配置和证书公钥。不要把 CA 私钥、Agent 私钥和 Token 放入共享备份。

### 升级和回滚

1. 备份数据库。
2. 下载并校验新的 Release 包。
3. 停止服务并替换二进制。
4. 启动服务，检查日志和 WebUI。
5. 异常时恢复旧二进制并重新启动。

### 常用排障

| 现象 | 优先检查 |
|---|---|
| `source directory not found` | 使用 `scripts/install.sh`，不要手写旧版本目录 |
| Relay 启动失败 | `journalctl -u modelrelay-relay`、证书路径和权限 |
| Agent TLS 失败 | 两套 CA、证书 SAN、`node_id` 和系统时间 |
| 节点 offline | Relay `9443` 防火墙、DNS 和 Agent 日志 |
| `model_not_found` | 本地模型服务 `/v1/models` 和能力探测 |
| `capability_not_supported` | 等待探测完成或检查接口能力 |
| WebUI 401 | 管理员密码和会话是否过期 |

