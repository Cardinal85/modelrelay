# ModelRelay 0.2.0

## 本版本重点

新增跨平台证书管理器 `certmgr`（Go/Fyne）。证书管理机无需常在线，只在签发或轮换时使用；CA 私钥不进入发布包、Relay 或 Agent。

## 发布内容

- Relay：OpenAI-compatible 代理、节点调度、有界队列和 WebUI。
- Agent：WSS/mTLS 连接、本地模型转发、心跳和能力探测。
- certctl：CA、CSR、Agent 证书和 Relay 服务端证书命令行工具。
- certmgr：Windows / Linux / macOS 图形界面，离线签发 Agent 与 Relay 证书，检查并分别导出部署文件；可选登录 Relay 管理 API 吊销证书（会话 Cookie，不保存密码）。
- Linux、Windows、macOS 多平台发布包。

## 证书管理器说明

- 从对应操作系统的发布包中运行 `certmgr`（Windows 为 `certmgr.exe`）。
- Agent 私钥必须在 GPU 主机用 `certctl csr` 本地生成，不要由证书管理器代生成。
- 请离线备份 `agent-ca.key` 和 `relay-ca.key`。
- Fyne 桌面程序需要在目标操作系统上启用 CGO 构建。Windows 请使用 `scripts/goenv.ps1`（llvm-mingw）；Linux/macOS 使用 `scripts/build-certmgr.sh`。
- Windows 上「浏览」使用系统文件对话框，避免中文系统下 Fyne 收藏夹路径（Documents/Downloads）导致 `uri is not listable`。

## 更新记录

- 2026-08-15：更新 Windows `certmgr.exe`，改为原生文件/文件夹选择框。请重新下载 `modelrelay-windows-amd64.zip`。

## 发布前检查

- [ ] 在目标平台校验 `SHA256SUMS`。
- [ ] 替换示例地址、证书和 Token。
- [ ] 使用 `certmgr` 或 `certctl` 完成签发，并验证 Relay/Agent 启动。
- [ ] 使用真实模型服务完成联调。
- [ ] 准备数据库备份和回滚方案。

## 发布范围

发布包仅包含公开部署所需的配置、部署和 New API 文档。
项目规划、项目任务书、架构设计、协议、WebUI、验收、安全设计和测试报告
等内部文档不进入发布包。CA 私钥、Agent 私钥、数据库和日志不得进入发布包。
