# ModelRelay

当前版本：**0.2.0**

让 New API 等 OpenAI-compatible 网关访问没有公网 IP 的 GPU 模型服务器。
ModelRelay 不替代 New API，也不替代 vLLM / SGLang / Ollama，只在两者之间提供
安全的连接、调度和管理。

```text
公网用户 → New API → Relay（HTTP :9100）→ Agent（WSS/mTLS :9443）→ 本地模型
                                  ↘ WebUI（默认 127.0.0.1:9200）
```

| 组件 | 作用 |
|---|---|
| Relay | 给 New API 当上游；Agent 经 mTLS 接入；内嵌管理 WebUI |
| Agent | 装在 GPU 上，主动连 Relay，转发本机模型请求 |
| certctl | 命令行：GPU 上生成 CSR、证书管理机签发 |
| certmgr | 图形界面：离线签发、检查、导出；可选登录 Relay 吊销 |

CA 私钥只留证书管理机。Agent 私钥必须在 GPU 本机用 `certctl csr` 生成。

## 快速部署

需要三类机器：Relay、GPU（已有 OpenAI-compatible 模型服务）、证书管理机。

**Linux / macOS**

```bash
curl -fsSL https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
  | sudo bash -s -- --component relay

curl -fsSL https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
  | sudo bash -s -- --component agent \
    --node-id gpu-001 \
    --relay-url wss://relay.example.com:9443/agent/v1/connect
```

**Windows**（管理员 PowerShell）

```powershell
$p = Join-Path $env:TEMP "modelrelay-install.ps1"
Invoke-WebRequest -UseBasicParsing `
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.ps1 `
  -OutFile $p
powershell -ExecutionPolicy Bypass -File $p -Component Relay
powershell -ExecutionPolicy Bypass -File $p -Component Agent `
  -NodeId gpu-001 `
  -RelayUrl "wss://relay.example.com:9443/agent/v1/connect"
```

发布包：https://github.com/Cardinal85/modelrelay/releases/latest  
（`modelrelay-<os>-<arch>.zip`，Windows amd64 含 `certmgr.exe`）

## 证书

在证书管理机运行 `certmgr`：建两套 CA → 签发 Relay 服务端证书 → GPU 送来 CSR 后签发 Agent → 检查 → 分别导出。
逐步操作和文件交接见 [部署指南 2.0 节](docs/deployment.md)。

## 域名、端口与反代

| 入口 | 默认 | 说明 |
|---|---|---|
| Agent | `wss://relay.example.com:9443/agent/v1/connect` | mTLS，只能直连或 TCP 透传，不能套网站 HTTPS |
| New API | `http://127.0.0.1:9100/v1` | 可 HTTPS 反代；不要写成 `wss://...:9443` |
| WebUI | `http://127.0.0.1:9200/` | 用 SSH 隧道，不要暴露公网 |

证书 SAN 必须包含 Agent 实际连接的主机名或 IP。详见 [部署指南 0.1、9 节](docs/deployment.md)。

## New API

渠道类型选 OpenAI 兼容：

```text
Base URL: http://127.0.0.1:9100/v1
密钥:     Relay 的 RELAY_INTERNAL_TOKEN
```

同机直连即可。分机或 HTTPS 反代见 [New API 接入指南](docs/newapi.md)。

## 文档

- [部署与运维指南](docs/deployment.md)：安装、`certmgr` 流程、Windows 生成 CSR、反代、卸载
- [配置说明](docs/config.md)
- [New API 接入指南](docs/newapi.md)

卸载程序和证书见 [部署指南第 11 节](docs/deployment.md)。
