# ModelRelay 0.2.2

## 本版本重点

修复经 Cloudflare 等反代访问 WebUI 时，缓存旧前端脚本导致 Cloudflare Turnstile 登录失败。

## 更新记录

- `index.html` 使用 `Cache-Control: no-store`，并把 `app.js` / `style.css` 改成带 `?v=版本号` 的地址。
- `/api/login-config` 禁止缓存。
- Turnstile siteverify 不再发送 `remoteip`（反代时经常是边缘 IP，会导致合法 token 被拒）。
- 校验失败会把 Cloudflare `error-codes` 写到 Relay 日志。
- 登录页在未完成人机验证或验证码脚本加载失败时给出明确提示。

## 升级

备份 `etc` 和 `data` 后再跑安装器。升级后若面板仍走 Cloudflare，请清除该域名缓存（至少 `/` 与 `/static/app.js`），然后强制刷新登录页。

## 发布范围

发布包仅包含公开部署所需的配置、部署和 New API 文档。
项目规划、项目任务书、架构设计、协议、WebUI、验收、安全设计和测试报告
等内部文档不进入发布包。CA 私钥、Agent 私钥、数据库和日志不得进入发布包。
