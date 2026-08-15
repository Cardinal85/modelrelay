# ModelRelay 0.1.0

## 发布内容

- Relay：OpenAI-compatible 代理、节点调度、有界队列和 WebUI。
- Agent：WSS/mTLS 连接、本地模型转发、心跳和能力探测。
- certctl：CA、CSR、Agent 证书和 Relay 服务端证书工具。
- Linux、Windows、macOS 多平台发布包。

## 发布前检查

- [ ] 在目标平台校验 `SHA256SUMS`。
- [ ] 替换示例地址、证书和 Token。
- [ ] 使用真实模型服务完成联调。
- [ ] 准备数据库备份和回滚方案。

## 发布范围

发布包仅包含公开部署所需的配置、部署和 New API 文档。
项目规划、项目任务书、架构设计、协议、WebUI、验收、安全设计和测试报告
等内部文档不进入发布包。
