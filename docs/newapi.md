# New API 接入 ModelRelay 指南

> 对应任务书交付物 7：New API 渠道配置说明。

## 1. 配置渠道

在 New API 管理后台新增渠道（类型选 OpenAI 兼容）：

```text
渠道名称:     ModelRelay-内网
类型:         OpenAI
Base URL:     http://127.0.0.1:9100/v1
密钥:         <relay.yaml 中 internal_auth.token>
模型:         由 Relay /v1/models 同步，或手动填入节点模型
```

要点：

- Relay HTTP 默认只监听 `127.0.0.1:9100`，New API 与 Relay 同机部署时直连即可。
- 若 New API 与 Relay 分机，Relay 的 `http_listen` 需改为受保护内网地址，并确保不暴露公网。
- 密钥即内部认证 Token，Relay 校验 `Authorization: Bearer <token>`。

## 2. 模型同步

Relay 的 `GET /v1/models` 返回当前可用模型目录（含能力），New API 可通过“同步模型”功能拉取：

```json
{
  "object": "list",
  "data": [
    { "id": "qwen-local", "object": "model", "created": 0, "owned_by": "relay",
      "nodes": ["gpu-001"], "capabilities": ["chat_completions","chat_stream"] }
  ]
}
```

## 3. 行为说明

| 场景 | New API 侧表现 |
|---|---|
| 模型在所有节点下线 | `404`（model_not_found） |
| 接口能力不支持（如该模型无 embeddings） | `422`（capability_not_supported） |
| 节点全部繁忙/排队超时 | `429`（queue_full）/ `503`（queue_timeout） |
| 本地模型故障 | `502`（upstream_connection_failed） |
| 本地模型超时 | `504`（ttft_timeout / idle_timeout / upstream_timeout） |
| 客户端取消 | `499`，Agent 取消本地生成并释放并发 |

错误体为 OpenAI 兼容格式：

```json
{ "error": { "message": "...", "type": "relay_error", "param": null, "code": "..." } }
```

## 4. 透明转发说明

- 请求体、查询参数、`messages`/`tools`/多模态字段原样透传，不重写。
- SSE 事件逐条即时转发，不聚合、不重组（`[DONE]` 保留）。
- multipart（音频转写）与二进制响应（音频/图片）完整透传。
- `Authorization` 内部认证头在 Relay 侧消费，Agent 使用自身 `local.api_key` 访问本地模型。

## 5. 验证清单

1. `GET /v1/models` 返回节点模型。
2. 非流式 Chat 完成：`POST /v1/chat/completions` 200。
3. 流式 Chat：SSE 事件逐条到达并以 `data: [DONE]` 结束。
4. 错误透传：请求不存在模型 → 404 且错误体可解析。
5. 取消：客户端断开后 Agent 停止本地生成（WebUI 请求追踪可见 499）。
