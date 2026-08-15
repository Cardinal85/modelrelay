# ModelRelay

当前版本：**0.2.0**

ModelRelay 是一个安全的内网模型连接中间件，让 New API 等
OpenAI-compatible 网关访问没有公网 IP 的 GPU 模型服务器。

```text
New API → Relay（HTTP）→ Agent（WSS/mTLS）→ 本地 OpenAI-compatible 模型服务
                       ↘ WebUI / 管理 API
```

0.2.0 增加跨平台证书管理器 `certmgr`：在证书管理机离线签发，只有吊销时才连接 Relay。
CA 私钥不要放入发布包或运行节点。Agent 私钥必须在 GPU 主机本地生成。

下面是一条完整部署时间线，请从第 1 步按顺序执行。

## 1. 准备三类机器

- **Relay 主机**：Agent 能访问其 TCP `9443`。
- **GPU 主机**：运行本地模型服务，例如 `http://127.0.0.1:8000/v1`。
- **证书管理机**：保存 CA 私钥，不与 Relay 或 GPU 主机共用。
  使用 `certmgr` 离线签发；只有吊销时才连接 Relay 管理 API。
  CA 私钥不要放入发布包或运行节点。

生产环境建议使用两套 CA：

```text
Agent CA → Agent 客户端证书 → Relay 校验
Relay CA  → Relay 服务端证书 → Agent 校验
```

## 2. 安装 Relay

在 Relay 主机执行。安装器会自动识别系统和架构，下载最新发布包、解压并检查：

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

- 服务：`modelrelay-relay`
- 配置：`/etc/modelrelay/relay.yaml`
- 密钥环境文件：`/etc/modelrelay/relay.env`
- 数据库目录：`/var/lib/modelrelay/`

## 3. 准备并复制 Relay 证书

从 [GitHub Release](https://github.com/Cardinal85/modelrelay/releases/latest)
下载对应操作系统的 `modelrelay-<os>-<arch>.zip`，在证书管理机运行 `certmgr`
（Windows 为 `certmgr.exe`）：

1. 创建 Agent CA 和 Relay CA，离线备份 `agent-ca.key` / `relay-ca.key`。
2. GPU 主机用 `certctl csr` 生成私钥和 CSR，只把 CSR 拷到证书管理机。
3. 导入 CSR 签发 Agent 证书；填写 CN/DNS/IP 签发 Relay 服务端证书。
4. 分别导出 Relay 与 Agent 部署文件。
5. 可选：登录 Relay 管理 API 吊销证书（不保存管理员密码）。

证书管理机无需常在线，只在签发或轮换时使用。命令行步骤见
[部署文档的证书章节](docs/deployment.md)。

将以下文件复制到 Relay：

```bash
sudo install -m 0644 relay.crt /etc/modelrelay/relay.crt
sudo install -m 0600 relay.key /etc/modelrelay/relay.key
sudo install -m 0644 agent-ca.crt /etc/modelrelay/agent-ca.crt
sudo chown modelrelay:modelrelay /etc/modelrelay/relay.crt \
  /etc/modelrelay/relay.key /etc/modelrelay/agent-ca.crt
```

检查 `/etc/modelrelay/relay.yaml` 中的 `tls_cert`、`tls_key`、`agent_ca`
路径后，重启并查看日志：

```bash
sudo systemctl restart modelrelay-relay
sudo systemctl status modelrelay-relay --no-pager
sudo journalctl -u modelrelay-relay -n 100 --no-pager
```

## 4. 验证 Relay 和 WebUI

获取首次生成的管理员密码：

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

WebUI 默认监听 `127.0.0.1:9200`。远程访问时建立隧道：

```bash
ssh -L 9200:127.0.0.1:9200 root@<relay-host>
```

然后打开 `http://127.0.0.1:9200/`，账号是 `admin`。

## 5. 安装 GPU Agent

在 GPU 主机执行：

```bash
curl -fsSL \
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
  | sudo bash -s -- --component agent \
    --node-id gpu-001 \
    --relay-url wss://<relay-host>:9443/agent/v1/connect
```

## 6. 配置 Agent 证书和模型地址

将 Agent 证书、私钥和 Relay CA 公钥复制到 `/etc/model-agent/`：

```bash
sudo install -m 0644 gpu-001.crt /etc/model-agent/gpu-001.crt
sudo install -m 0600 gpu-001.key /etc/model-agent/gpu-001.key
sudo install -m 0644 relay-ca.crt /etc/model-agent/relay-ca.crt
sudo chown modelrelay:modelrelay /etc/model-agent/*
```

检查 `/etc/model-agent/agent.yaml`：

```yaml
node_id: gpu-001
relays:
  - url: "wss://<relay-host>:9443/agent/v1/connect"
tls:
  cert: "/etc/model-agent/gpu-001.crt"
  key: "/etc/model-agent/gpu-001.key"
  ca: "/etc/model-agent/relay-ca.crt"
  insecure_skip_verify: false
local:
  base_url: "http://127.0.0.1:8000/v1"
  tls_verify: true
```

确认模型服务后启动 Agent：

```bash
curl -fsS http://127.0.0.1:8000/v1/models
sudo systemctl restart modelrelay-agent
sudo systemctl status modelrelay-agent --no-pager
sudo journalctl -u modelrelay-agent -n 100 --no-pager
```

## 7. 验证节点并接入 New API

在 Relay WebUI 确认 `gpu-001` 为 `online`，模型目录和能力探测正常。

New API 渠道配置：

```text
Base URL: http://<relay-host>:9100/v1
密钥:     /etc/modelrelay/relay.env 中的 RELAY_INTERNAL_TOKEN
```

先同步 `GET /v1/models`，再测试 Chat 非流式和 SSE 流式请求。

## 8. 上线验收

- Relay、Agent 均为 `active (running)`。
- Agent 在 WebUI 中为 `online`。
- `9443` 只允许 Agent 网络访问。
- `9100`、`9200` 未暴露公网。
- 已完成数据库、配置和证书公钥备份。
- 私钥、CA 私钥、Token、日志和数据库没有上传仓库。

## 其他平台和详细文档

Windows GPU 用 `certctl.exe csr` 在本机生成私钥和 CSR，只把 `.csr` 拿到证书管理机签发。
Relay 的 `9443` 是 Agent mTLS 入口，只能直连或 TCP 透传，不能套网站 HTTPS 反代。
域名、nginx/Caddy/云负载均衡、防火墙和各平台路径对照见
[部署与运维指南](docs/deployment.md) 第 0.1、2.2、4、9、10 节。

Windows 使用 `scripts/install.ps1`；macOS 使用 `install.sh` 后由 launchd 托管。
主备 Relay、备份和回滚也在同一份部署文档里。

- [配置说明](docs/config.md)
- [New API 接入指南](docs/newapi.md)

