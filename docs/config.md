# ModelRelay 配置说明

> 依据：`ModelRelay-项目任务书.md` 第 12 节（TASK-301、TASK-306 等）。
> 示例配置：`configs/relay.example.yaml`、`configs/agent.example.yaml`。

## 1. 通用原则

- 配置文件可纳入版本控制，但不得提交密钥；敏感值用 `${ENV_VAR}` 或 `${ENV_VAR:default}` 引用。
- 配置修改需要校验（`Validate`），错误配置拒绝启动，避免无限重启。
- 枚举项用下拉框、开关项用勾选框、危险配置用弹窗确认（WebUI）。
- 保存前展示变更摘要；敏感值只显示“已配置/未配置”。

## 2. Relay 配置

| 字段 | 说明 | 默认 |
|---|---|---|
| `relay_id` | Relay 标识（用于 hello_ack 与展示） | 必填 |
| `http_listen` | New API 上游监听地址 | `127.0.0.1:9100` |
| `wss_listen` | Agent 接入监听地址 | `0.0.0.0:9443` |
| `tls_cert` / `tls_key` | Relay 服务端证书（WSS） | 必填 |
| `agent_ca` | 验证 Agent 客户端证书的 CA | 必填 |
| `internal_auth.token` | New API 调用 Relay 的内部认证 Token | 必填（启用时） |
| `limits.max_body_bytes` | 请求体上限 | 64 MiB |
| `limits.max_concurrency` | Relay 全局并发 | 64 |
| `limits.queue_length` | 有界队列长度 | 256 |
| `limits.queue_timeout_sec` | 排队超时 | 30 |
| `limits.ttft_timeout_sec` | 首 Token 超时 | 120 |
| `limits.idle_timeout_sec` | 流式空闲超时 | 300 |
| `limits.request_timeout_sec` | 整体请求超时 | 1800 |
| `limits.heartbeat_timeout_sec` | 心跳超时（suspect/offline 判定） | 60 |
| `admin.listen` | 管理 API / WebUI 监听 | `127.0.0.1:9200` |
| `admin.users` | 初始管理员（角色 admin/readonly） | 空 |
| `store.db_path` | SQLite 路径 | `modelrelay.db` |
| `retention.keep_prompt_response` | 是否保存完整 Prompt/Response | false |
| `retention.retention_days` | 摘要/审计保留天数 | 30 |
| `log_level` | debug/info/warn/error | info |

## 3. Agent 配置

| 字段 | 说明 | 默认 |
|---|---|---|
| `node_id` | 节点标识（必须与客户端证书 CN 一致） | 必填 |
| `relays[].url` | Relay 地址，形如 `wss://host:9443/agent/v1/connect` | 至少 1 个 |
| `relays[].priority` | 越小越优先（主备组网） | — |
| `max_body_bytes` | Agent 侧单请求组装缓冲上限 | 16 MiB |
| `tls.cert` / `tls.key` | Agent 客户端证书与私钥（私钥仅本机） | 必填 |
| `tls.ca` | Relay 服务端 CA | 必填 |
| `tls.insecure_skip_verify` | 跳过服务端校验 | 禁止，必须为 false |
| `local.base_url` | 本地模型服务地址，如 `http://127.0.0.1:8000/v1` | 必填 |
| `local.api_key` | 本地模型 API Key | 空 |
| `local.tls_verify` | 本地 HTTPS 校验 | true |
| `local.connect_timeout_sec` | 本地连接超时 | 5 |
| `local.response_timeout_sec` | 本地响应超时 | 300 |
| `local.max_concurrency` | 本地并发上限 | 8 |
| `probe.interval_sec` | 能力探测周期 | 600 |
| `probe.test_model` | 探测用模型（空则用第一个） | 空 |
| `probe.enabled` | 启用的探测项：chat/chat_stream/completions/embeddings/responses/tools | 见示例 |
| `heartbeat_interval` | 心跳间隔（秒） | 20 |
| `log_level` | 日志级别 | info |

## 4. 路径与目录约定

| 平台 | Relay 配置 | Agent 配置 | 二进制 |
|---|---|---|---|
| Linux | `/etc/modelrelay/` | `/etc/model-agent/` | `/opt/modelrelay/bin/` |
| Windows（安装器默认） | `C:\ModelRelay\etc\relay\` | `C:\ModelRelay\etc\agent\` | `C:\ModelRelay\bin\` |
| macOS | `/Library/Application Support/ModelRelay/` | `/Library/Application Support/ModelAgent/` | `/usr/local/libexec/modelrelay/` |

完整部署步骤、Windows 生成 CSR、域名和反代见 [部署与运维指南](deployment.md)。

## 5. 环境变量引用

```yaml
internal_auth:
  token: "${RELAY_INTERNAL_TOKEN}"      # 无默认值
local:
  api_key: "${LOCAL_MODEL_API_KEY:-}"   # 空默认
```

未设置的必填环境变量保持字面值（会被校验拒绝或按空处理），避免静默错误。
