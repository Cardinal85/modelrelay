# ModelRelay

ModelRelay 是一个安全的内网模型连接中间件，用于让 New API 等公网
OpenAI-compatible 网关访问没有公网 IP 的 GPU 模型服务器。

```text
New API → Relay（HTTP）→ Agent（WSS/mTLS）→ 本地 OpenAI-compatible 模型服务
                       ↘ WebUI / 管理 API
```

ModelRelay 不替代 New API，也不替代 vLLM、SGLang、Ollama 等推理服务，
只负责提供安全、可管理、可扩展的中间连接能力。

## 核心能力

- 兼容 OpenAI 协议，透明转发 JSON、SSE、multipart 和二进制数据。
- Relay 与 Agent 使用 WSS + 双向 TLS（mTLS）通信。
- 支持节点注册、心跳、调度、有界队列、Drain 和请求取消。
- 自动探测本地模型能力，并按能力进行路由。
- 内嵌 WebUI 和管理 API。
- 支持 Relay 主备切换。
- 支持 Linux、Windows、macOS 多平台构建。

## 组件

| 组件 | 作用 |
|---|---|
| `relay` | HTTP 入口、请求调度、转发、WebUI 和 Agent 接入 |
| `agent` | 安装在模型服务器上，主动连接 Relay 并转发本地请求 |
| `certctl` | 创建 CA、CSR 以及 Relay/Agent 证书 |

## 快速开始

Windows 构建和测试：

```powershell
powershell -File scripts/build.ps1 -Test
powershell -File scripts/build.ps1 -All
```

Linux/macOS 部署：

```bash
bash scripts/deploy.sh \
  --source-dir ./dist/modelrelay-0.1.0-linux-amd64 \
  --component relay
```

Windows 请使用管理员 PowerShell：

```powershell
powershell -File scripts/deploy.ps1 `
  -SourceDir .\dist\modelrelay-0.1.0-windows-amd64 `
  -Component Relay
```

生产环境启动前，请先准备 mTLS 证书并检查生成的 YAML 配置。
详细步骤见[部署与运维指南](docs/deployment.md)。

## 接入 New API

在 New API 中新增 OpenAI-compatible 渠道：

```text
Base URL: http://<relay-host>:9100/v1
密钥:     relay.yaml 中的 internal_auth.token
```

Relay 通过 `GET /v1/models` 返回当前可用模型目录。

## GitHub Release

仓库不提交构建二进制、本地数据库、日志、证书和私钥。
在项目根目录生成发布包：

```powershell
powershell -File scripts/prepare-github-release.ps1 -Version 0.1.0 -Clean
```

生成的 ZIP 和 `SHA256SUMS` 位于 `github-release/v0.1.0/`，
应作为 GitHub Release 附件上传，不要提交到源码目录。

## 从 GitHub 获取部署脚本

脚本已上传到 GitHub，可通过以下 Raw URL 下载：

Linux/macOS：

```bash
curl -fsSL \
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/deploy.sh \
  -o deploy.sh
chmod +x deploy.sh
sudo ./deploy.sh --source-dir ./modelrelay-0.1.0-linux-amd64 --component relay
```

Windows：

```powershell
$url = "https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/deploy.ps1"
$script = Join-Path $env:TEMP "modelrelay-deploy.ps1"
Invoke-WebRequest -UseBasicParsing $url -OutFile $script
powershell -ExecutionPolicy Bypass -File $script `
  -SourceDir .\modelrelay-0.1.0-windows-amd64 -Component Relay
```

远程执行前请检查脚本内容；生产环境建议使用固定版本 URL，
不要直接使用可变的 `main` 分支。

## 文档

- [配置说明](docs/config.md)
- [部署与运维指南](docs/deployment.md)
- [New API 接入指南](docs/newapi.md)

发布包只包含公开部署所需的配置、部署和 New API 文档，不包含项目规划、
项目任务书、架构设计、协议、WebUI、验收、安全设计和测试报告等内部文档。

## 安全说明

生产环境不要使用示例 Token 或证书。CA 私钥、Agent 私钥、Relay 私钥、
数据库和日志不得上传到公开仓库或发布包。
