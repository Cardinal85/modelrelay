# ModelRelay 0.2.1

## 本版本重点

面板可经可信反代公网访问；安装器首次打印随机 WebUI 账号；WSS 全网开放时补上协议绑定与限额；升级改为停进程后再换二进制。

## 更新记录

- WebUI：可信反代 IP、Secure Cookie、登录 body 限制、空 Origin 拒绝 CSRF；可选 Cloudflare Turnstile。
- 首次安装生成随机 WebUI 用户名和密码，只打印一次，保存在 `relay.env`。改 env 不会覆盖 SQLite 里已有管理员。
- WSS：响应绑定到对应 `nodeID`；控制消息 1MiB、WebSocket 200MiB；`internal_auth.enabled` 时 Token 不能为空。
- Agent 使用固定 Origin；转发路径与 Relay 白名单一致。
- 安装器升级前先可选 Drain、再停进程、覆盖二进制、拉起。证书和 yaml 仍不覆盖。
- 部署文档改为：动态 GPU 时可对公网开放 9443；9100/9200/22 用云防火墙关掉公网入站；面板可反代，建议 Cloudflare Access。不要把 9100 和面板一起反出去。

## 升级

备份 `etc` 和 `data` 后再跑安装器。Windows 安装器会先停任务再拷文件。Agent 随后自己重连；进行中的 HTTP/SSE 会断。

## 发布范围

发布包仅包含公开部署所需的配置、部署和 New API 文档。
项目规划、项目任务书、架构设计、协议、WebUI、验收、安全设计和测试报告
等内部文档不进入发布包。CA 私钥、Agent 私钥、数据库和日志不得进入发布包。
