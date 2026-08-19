# ModelRelay 部署指南

当前版本：**0.2.1**

本文档只有一条主流程：**Relay → 证书 → Agent → New API → 验收**。
请从第 0 步开始执行。`certmgr` 图形界面逐步操作见第 2.0 节；两套 CA 拷反见第 8.6 节；卸载见第 11 节；平台差异、反代和手工安装放在文档末尾。

安装器会下载 GitHub 上的 **latest** 发布包。0.2.0 起发布包包含
`certmgr` 图形证书管理器（需对应操作系统的包；Fyne 程序在目标系统本地构建）。

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
| `9443/tcp` | Agent → Relay WSS/mTLS | GPU 出口 IP 不固定时可对公网开放；可 TCP 透传，禁止 TLS 终止。源站 IP 会随 9443 DNS 公开 |
| `9100/tcp` | New API → Relay API | 只允许 New API 网段；可 HTTPS 反代。不要跟面板一起反到公网 |
| `9200/tcp` | 管理 WebUI | 默认听本机；可经 Cloudflare/nginx 反代。建议再加 Cloudflare Access |
| `8000/tcp` | Agent → 本地模型 | 只监听模型机本机，不要对 Relay 开放 |

云防火墙应关掉 `9100`/`9200`/`22` 的公网入站，而不是靠藏 IP。`9443` 对动态 GPU 必须可达，因此 DNS 会暴露源站地址。
`9100` 不要和 WebUI 用同一个公网反代出口。

GPU 主机通常在 NAT/防火墙后面，**不需要公网 IP**。Agent 主动连出 `9443` 即可。
需要公网或端口映射的是 **Relay 的 `9443`**（以及 New API 能访问到的 `9100`）。

### 0.1 域名怎么定

三个入口用途不同，**不要**把 Agent 的 WSS/mTLS 和 New API 的 HTTP 混在同一个 HTTPS 反代后面。

| 用途 | 建议主机名 | Agent / 客户端填写 | 谁连接 |
|---|---|---|---|
| Agent 接入 | `relay.example.com` | `wss://relay.example.com:9443/agent/v1/connect` | 各 GPU 上的 Agent |
| New API 上游 | 同机用回环；分机用 `api.example.com` | New API Base URL：`http://127.0.0.1:9100/v1` 或 `https://api.example.com/v1` | New API |
| WebUI | `relay-ui.example.com`（可选反代） | 安装打印的用户名/密码，或 `relay.env` | 管理员 |

填写时把 `example.com` 换成你的真实域名或内网名字。没有域名时可以用 IP，但证书 SAN 和 URL 必须用同一个 IP。

推荐的 DNS：

```text
relay.example.com     A/AAAA    Relay 公网或内网 IP     （Agent 用，DNS 仅解析，不要做七层代理）
api.example.com       A/AAAA    与 New API 互通的地址   （可选；反代到 Relay :9100）
```

规则：

1. Agent 配置里的主机名（或 IP）**必须**出现在 Relay 服务端证书的 DNS SAN 或 IP SAN。
2. 证书 CN 建议与主域名相同，例如 `relay.example.com`；实际校验看 SAN。
3. GPU 若写 `wss://203.0.113.10:9443/...`，签发时必须 `-ip 203.0.113.10`。
4. GPU 若写 `wss://relay.example.com:9443/...`，签发时必须 `-dns relay.example.com`，且 GPU 能解析到 Relay。
5. **不要**给 `9443` 套 Let's Encrypt 再做 TLS 终止。Agent 用 Relay CA 校验服务端，并且要出示客户端证书。`9443` 只能直连或 **TCP 透传**。
6. Cloudflare 橙色云、阿里云/腾讯云七层 HTTPS、AWS ALB 会拆掉 mTLS，**不能**用在 `9443`。
7. New API 的 Base URL 主机名可以和 Agent 入口不同。
8. 主备 Relay 用两个名字，例如 `relay-a.example.com`、`relay-b.example.com`，各签发一张 SAN 正确的服务端证书。

完整反代、防火墙和证书 SAN 对照见文末 **第 9 节**。

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

Windows 请用**管理员** PowerShell（开始菜单搜索 Windows PowerShell → 右键「以管理员身份运行」）。
若出现 `Invoke-Expression` / `C:\bin` 之类报错，来自本机 PowerShell 配置文件（例如美亚 DBus），与安装器无关；加 `-NoProfile` 即可避开：

```powershell
$p = Join-Path $env:TEMP "modelrelay-install.ps1"
Invoke-WebRequest -UseBasicParsing `
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.ps1 `
  -OutFile $p
powershell -NoProfile -ExecutionPolicy Bypass -File $p -Component Relay
```

安装器若未提权，会弹出 UAC。不要在普通用户窗口里装。

安装结果：

```text
程序：C:\ModelRelay\bin\relay.exe、certctl.exe
配置：C:\ModelRelay\etc\relay\relay.yaml
环境：C:\ModelRelay\etc\relay\relay.env
数据：C:\ModelRelay\data\modelrelay.db
服务：NSSM 的 ModelRelayRelay，或任务计划 ModelRelay-Relay
```

在 Relay 这台 Windows 上放行 Agent 访问 `9443`。GPU 出口 IP 固定时尽量收紧来源；不固定时对公网开放 `9443`，同时用云防火墙关掉 `9100`/`9200`/`22` 公网入站：

```powershell
New-NetFirewallRule -DisplayName "ModelRelay Agent WSS" `
  -Direction Inbound -Protocol TCP -LocalPort 9443 -Action Allow
```

`9100` / `9200` 默认只监听 `127.0.0.1`，一般不必对公网放行。

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

证书管理机无需常在线，只在签发或轮换时使用。CA 私钥只留在证书管理机，
不要上传到 Relay、Agent 或 GitHub。目录权限：Windows 使用 ACL，Linux/macOS
使用 `0700`/`0600`。请离线备份 `agent-ca.key` 和 `relay-ca.key`。

### 2.0 使用证书管理器（推荐，0.2.0）

从 [GitHub Release](https://github.com/Cardinal85/modelrelay/releases/latest)
下载对应操作系统的发布包，运行其中的 `certmgr`（Windows 为 `certmgr.exe`）。
覆盖 Windows、Linux 和 macOS。证书管理机无需常在线；只有吊销时才连接 Relay。

六个页签的分工：

| 页签 | 作用 |
|---|---|
| CA 工作区 | 创建或打开 Agent CA / Relay CA。私钥只留本机。 |
| 签发 Agent | 导入 GPU 上生成的 CSR，校验后签发客户端 `.crt`，不生成 Agent 私钥。 |
| 签发 Relay | 按 CN / DNS SAN / IP SAN 签发 Relay 服务端 `.crt` 与 `.key`。 |
| 证书检查 | 查看 Subject、Issuer、Serial、SAN、用途和有效期。 |
| 部署导出 | 分别打包给 Relay、给 Agent 的文件；不导出 Agent 私钥和 CA 私钥。 |
| 在线吊销（可选） | 登录 Relay 管理 API，按序列号吊销。会话 Cookie，不保存密码。 |

推荐顺序：**建两套 CA → 签发 Relay → GPU 送来 CSR 后签发 Agent → 检查 → 分别导出**。
吊销等证书泄露或节点退役时再用。

#### 2.0.1 图形界面完整流程

三台机器、一次单向交接。私钥不要对向流动：

```text
证书管理机（certmgr，可离线）          GPU 主机                    Relay 主机
  ① 建两套 CA
  ② 签发 Relay 服务端证书  ──────────► 拷 Relay 包 ──────────► relay.crt / relay.key / agent-ca.crt
  ③ 等 GPU 送来 CSR
       ◄──── 只送 gpu-001.csr ────  certctl csr（私钥留下）
  ④ 签发 Agent 证书
       ──── 只送 gpu-001.crt + relay-ca.crt ──► 与本地 .key 配对
  ⑤ 检查、导出
  ⑥ 出事才联网吊销
```

开始前定好名字，后面所有填写都围着它们：

```text
RELAY_HOST = relay.example.com
NODE_ID    = gpu-001
```

Windows 上 `certmgr` 默认工作区是当前用户目录下的
`ModelRelay\ca\`（例如 `C:\Users\<用户>\ModelRelay\ca\`）。
`certmgr` 创建的文件名比 `certctl init-ca` 更清楚：

```text
...\ca\agent\agent-ca.crt     Agent CA 公钥
...\ca\agent\agent-ca.key     Agent CA 私钥（只留本机 + 离线备份）
...\ca\relay\relay-ca.crt     Relay CA 公钥
...\ca\relay\relay-ca.key     Relay CA 私钥（只留本机 + 离线备份）
```

**第 1 步：CA 工作区，创建两套根**

打开「CA 工作区」，做两次。

1. 类型选 `Agent CA`，目录 `...\ModelRelay\ca\agent`，主题 CN `ModelRelay Agent CA`，有效期默认 3650 天，点「创建 CA 工作区」。
2. 类型改成 `Relay CA`（目录会切到 `...\ca\relay`），主题 CN `ModelRelay Relay CA`，再点「创建」。
3. 立刻把两个 `.key` 拷到 U 盘或离线盘。以后每次打开程序，用「打开 CA 工作区」分别打开这两个目录，确认底部已显示当前 Agent CA / Relay CA。

两套 CA 的目的：Agent CA 签发 GPU 客户端证书，公钥放到 Relay，用来校验节点身份；
Relay CA 签发 Relay 服务端证书，公钥放到 GPU，用来校验对面是不是真 Relay。

**第 2 步：签发 Relay 服务端证书**

打开 Relay CA 后，切到「签发 Relay」：

| 字段 | 怎么填 |
|---|---|
| CN | `relay.example.com`（或实际 IP） |
| DNS SAN | Agent 用域名连接时必填；多个用逗号 |
| IP SAN | Agent 用 IP 连接时必填；域名和 IP 都会用就两项都填 |
| 有效期 | 365 |
| 输出目录 | 例如 `D:\issued\relay` |

点「签发 Relay 服务端证书」，得到 `relay.example.com.crt` 和 `relay.example.com.key`。
DNS/IP SAN 必须覆盖 Agent 配置里 `wss://主机:9443/...` 的那个主机名或 IP。

**第 3 步：在 GPU 主机生成 CSR**

这一步不在 `certmgr` 里做。到 GPU 上执行 `certctl csr`（见 2.2 节）。只把
`gpu-001.csr` 拷到证书管理机；`gpu-001.key` 留在 GPU。`-cn`、`node_id`、证书 CN
必须相同。

**第 4 步：签发 Agent 证书**

打开 Agent CA 后，切到「签发 Agent」：

1. 选择 GPU 送来的 `gpu-001.csr`，点「校验 CSR」。
2. 确认 CN、URI SAN、`node_id` 一致；`node_id` 填 `gpu-001`。
3. 有效期 365，选择输出目录，点「签发 Agent 证书」。

得到 `gpu-001.crt`，没有 `.key`，这是正常的。把 `gpu-001.crt` 和
`...\ca\relay\relay-ca.crt` 拷回 GPU，**不要覆盖**本机的 `gpu-001.key`。

**第 5 步：证书检查**

在「证书检查」中分别打开 Relay 的 `.crt` 和 Agent 的 `.crt`：
Relay 证书 SAN 是否包含实际连接地址；Agent 证书 CN 是否为 `gpu-001`，是否由 Agent CA 签发。

**第 6 步：部署导出，分成两包**

切到「部署导出」。不要把整个 `ca` 目录拷走。

导出给 Relay 主机：

| 字段 | 选什么 |
|---|---|
| 服务端证书 | 第 2 步的 `relay.example.com.crt` |
| 服务端私钥 | 第 2 步的 `relay.example.com.key` |
| Agent CA 公钥 | `...\ca\agent\agent-ca.crt`（不要选 `.key`） |
| 导出目录 | 例如 `D:\export\relay` |

得到 `relay.crt`、`relay.key`、`agent-ca.crt`。拷到 Relay：
Linux `/etc/modelrelay/`，Windows `C:\ModelRelay\etc\relay\`。

导出给 Agent 主机：

| 字段 | 选什么 |
|---|---|
| Agent 证书 | 第 4 步的 `gpu-001.crt` |
| Relay CA 公钥 | `...\ca\relay\relay-ca.crt` |
| node_id | `gpu-001` |
| 导出目录 | 例如 `D:\export\gpu-001` |

得到 `gpu-001.crt`、`relay-ca.crt`（不含私钥）。拷到 GPU 与本地 `gpu-001.key` 放一起。

| 文件 | 留在哪 |
|---|---|
| `agent-ca.key` / `relay-ca.key` | 只留证书管理机 + 离线备份 |
| `gpu-001.key` | 只留那台 GPU |
| `gpu-001.csr` | 临时拿到管理机，签完可删 |
| `gpu-001.crt`、`relay-ca.crt` | GPU |
| `relay.crt`、`relay.key`、`agent-ca.crt` | Relay |

多一台 GPU 就重复第 3～4 步和第 6 步的 Agent 导出，换一个 `node_id`，不要共用私钥。

**第 7 步：在线吊销（平时不用）**

证书泄露、机器报废、或换证后要作废旧证时：

1. 管理 API 填 `http://127.0.0.1:9200`（远程先做 SSH 隧道，不要把 9200 放到公网）。
2. 用 Relay 的 `admin` 登录；密码只用于当前会话，不保存。
3. 刷新列表，复制序列号，确认吊销。Relay 会立即断开并拒绝该证书重连。

正确轮换：GPU 先 `csr` 出新私钥 → 签发新证 → 新证上线 → **再**吊销旧证。
不要先吊掉正在使用的唯一一张证书。

从源码构建时，Fyne 桌面程序需要在目标操作系统上启用 CGO。
Windows 请先加载 `scripts/goenv.ps1`（使用 `.tools/llvm-mingw`，不要用
CodeBlocks MinGW 8.1，否则生成的 `certmgr.exe` 无法在 Windows 11 运行）：

```powershell
powershell -File scripts/build.ps1
```

Linux 或 macOS：

```bash
bash scripts/build-certmgr.sh
```

仍可使用下面的 `certctl` 命令完成同样流程。`certctl init-ca` 在每个目录都生成名为
`agent-ca.crt` / `agent-ca.key` 的文件，用目录区分两套 CA；`certmgr` 则为 Relay CA
使用 `relay-ca.crt` / `relay-ca.key`。

### 2.1 创建 CA（证书管理机，命令行）

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
Relay 那套必须加 `-cn "ModelRelay Relay CA"`，否则两套主题都叫 `ModelRelay Agent CA`，
后面很难用文件名区分。拷到 GPU 时把 `./ca/relay/agent-ca.crt` **改名为** `relay-ca.crt`。
创建后立刻检查：

```bash
certctl inspect -cert ./ca/agent/agent-ca.crt   # Subject: ModelRelay Agent CA
certctl inspect -cert ./ca/relay/agent-ca.crt   # Subject: ModelRelay Relay CA
```

### 2.2 生成 Agent CSR（必须在 GPU 主机上）

私钥只在这台 GPU 本机生成。**不要**在证书管理机、Relay 或另一台电脑上代生成，
也**不要**把 `.key` 拷走。`node_id`、证书 CN、CSR 的 `-cn` 必须是同一个值，
例如 `gpu-001`。

先确认本机已有 `certctl`：Linux/macOS 安装 Agent 后在 `/opt/modelrelay/bin/certctl`
或 `/usr/local/libexec/modelrelay/certctl`；Windows 在 `C:\ModelRelay\bin\certctl.exe`。
也可以先解压发布包，只用其中的 `certctl`，稍后再装服务。

#### Linux GPU

```bash
sudo mkdir -p /etc/model-agent
sudo /opt/modelrelay/bin/certctl csr \
  -cn gpu-001 \
  -out /etc/model-agent
sudo chmod 600 /etc/model-agent/gpu-001.key
```

生成：

```text
/etc/model-agent/gpu-001.key    # 留下，禁止拷走
/etc/model-agent/gpu-001.csr    # 只把这个拿到证书管理机
```

#### Windows GPU

管理员 PowerShell。若尚未安装 Agent，可先只拷贝 `certctl.exe`，或带 `-NoStart` 安装：

```powershell
$p = Join-Path $env:TEMP "modelrelay-install.ps1"
Invoke-WebRequest -UseBasicParsing `
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.ps1 `
  -OutFile $p
powershell -NoProfile -ExecutionPolicy Bypass -File $p `
  -Component Agent `
  -NodeId gpu-001 `
  -RelayUrl "wss://relay.example.com:9443/agent/v1/connect" `
  -LocalBaseUrl "http://127.0.0.1:8000/v1" `
  -NoStart
```

生成 CSR（`-cn` 必须等于后面 `agent.yaml` 里的 `node_id`）：

```powershell
New-Item -ItemType Directory -Force -Path C:\ModelRelay\etc\agent | Out-Null
C:\ModelRelay\bin\certctl.exe csr -cn gpu-001 -out C:\ModelRelay\etc\agent
icacls C:\ModelRelay\etc\agent\gpu-001.key /inheritance:r /grant:r "SYSTEM:F" "Administrators:F"
```

生成：

```text
C:\ModelRelay\etc\agent\gpu-001.key
C:\ModelRelay\etc\agent\gpu-001.csr
```

把 `gpu-001.csr` 拷到证书管理机，可用 U 盘、内网 SMB，或本机已启用的 OpenSSH：

```powershell
scp C:\ModelRelay\etc\agent\gpu-001.csr user@ca-pc:D:\csr\gpu-001.csr
```

不要把 `.key` 放进邮件、网盘或聊天工具。

#### macOS GPU

```bash
sudo mkdir -p "/Library/Application Support/ModelAgent"
sudo /usr/local/libexec/modelrelay/certctl csr \
  -cn gpu-001 \
  -out "/Library/Application Support/ModelAgent"
sudo chmod 600 "/Library/Application Support/ModelAgent/gpu-001.key"
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

生成 `./issued/gpu-001.crt`。把它复制回 GPU，**不要**覆盖 GPU 上已有的 `.key`。

Linux：

```bash
sudo install -m 0644 gpu-001.crt /etc/model-agent/gpu-001.crt
```

Windows（在 GPU 上）：

```powershell
Copy-Item -Force gpu-001.crt C:\ModelRelay\etc\agent\gpu-001.crt
```

macOS：

```bash
sudo install -m 0644 gpu-001.crt \
  "/Library/Application Support/ModelAgent/gpu-001.crt"
```

### 2.4 签发 Relay 服务端证书（证书管理机）

证书的 DNS/IP SAN 必须包含 **Agent 实际连接时使用的地址**（与 `relays[].url` 一致）。
`certctl init-ca` 在每个目录都生成名为 `agent-ca.crt` / `agent-ca.key` 的文件，
用目录区分两套 CA，不要搞混：

```text
./ca/agent/agent-ca.crt     Agent CA 公钥（放到 Relay，用来校验 GPU 证书）
./ca/agent/agent-ca.key     Agent CA 私钥（只留证书管理机）
./ca/relay/agent-ca.crt     Relay CA 公钥（放到 GPU，用来校验 Relay 证书；导出时常改名为 relay-ca.crt）
./ca/relay/agent-ca.key     Relay CA 私钥（只留证书管理机）
```

Agent 用域名连接：

```bash
certctl server-cert \
  -ca ./ca/relay/agent-ca.crt \
  -ca-key ./ca/relay/agent-ca.key \
  -cn relay.example.com \
  -dns relay.example.com \
  -days 365 \
  -out ./issued
```

同时支持域名和 IP（内网 GPU 写 IP、外网 GPU 写域名时都要写上）：

```bash
certctl server-cert \
  -ca ./ca/relay/agent-ca.crt \
  -ca-key ./ca/relay/agent-ca.key \
  -cn relay.example.com \
  -dns relay.example.com,relay-a.example.com \
  -ip 203.0.113.10,10.0.0.10 \
  -days 365 \
  -out ./issued
```

`certmgr` 图形界面里同样填写 CN、DNS SAN、IP SAN。导出 Relay 目录会得到
`relay.crt`、`relay.key`、`agent-ca.crt`；导出 Agent 目录会得到
`gpu-001.crt`、`relay-ca.crt`（不含私钥）。

生成 `relay.example.com.crt` 和 `relay.example.com.key`。
后文将它们分别复制为 Relay 的 `relay.crt` 和 `relay.key`。

## 3. 配置并验证 Relay

### 3.1 复制 Relay 证书

Linux：

```bash
sudo install -m 0644 relay.example.com.crt /etc/modelrelay/relay.crt
sudo install -m 0600 relay.example.com.key /etc/modelrelay/relay.key
sudo install -m 0644 ./ca/agent/agent-ca.crt /etc/modelrelay/agent-ca.crt
sudo chown modelrelay:modelrelay \
  /etc/modelrelay/relay.crt \
  /etc/modelrelay/relay.key \
  /etc/modelrelay/agent-ca.crt
```

Windows（管理员 PowerShell，在 Relay 主机上）：

```powershell
$d = "C:\ModelRelay\etc\relay"
Copy-Item -Force relay.example.com.crt "$d\relay.crt"
Copy-Item -Force relay.example.com.key "$d\relay.key"
Copy-Item -Force agent-ca.crt "$d\agent-ca.crt"
icacls "$d\relay.key" /inheritance:r /grant:r "SYSTEM:F" "Administrators:F"
```

若用 `certmgr` 导出目录，三个文件已经叫 `relay.crt`、`relay.key`、`agent-ca.crt`，直接拷进 `$d`。

macOS：

```bash
sudo install -m 0644 relay.example.com.crt \
  "/Library/Application Support/ModelRelay/relay.crt"
sudo install -m 0600 relay.example.com.key \
  "/Library/Application Support/ModelRelay/relay.key"
sudo install -m 0644 ./ca/agent/agent-ca.crt \
  "/Library/Application Support/ModelRelay/agent-ca.crt"
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

不要把 `9100` 或 `9200` 直接暴露到公网。Windows 配置在
`C:\ModelRelay\etc\relay\relay.yaml`，证书路径改成：

```yaml
http_listen: "127.0.0.1:9100"
wss_listen: "0.0.0.0:9443"
tls_cert: "C:\\ModelRelay\\etc\\relay\\relay.crt"
tls_key: "C:\\ModelRelay\\etc\\relay\\relay.key"
agent_ca: "C:\\ModelRelay\\etc\\relay\\agent-ca.crt"
admin:
  listen: "127.0.0.1:9200"
store:
  db_path: "C:\\ModelRelay\\data\\modelrelay.db"
```

### 3.3 启动并检查

仅在 **Relay 主机**上执行。GPU / Agent 机器不要启动 `ModelRelayRelay`。

Linux：

```bash
sudo systemctl restart modelrelay-relay
sudo systemctl status modelrelay-relay --no-pager
sudo journalctl -u modelrelay-relay -n 100 --no-pager
```

Windows Relay 主机：

```powershell
Get-Service ModelRelayRelay -ErrorAction SilentlyContinue
if ($?) {
  Restart-Service ModelRelayRelay
} else {
  Start-ScheduledTask -TaskName "ModelRelay-Relay"
}
if (Test-Path C:\ModelRelay\data\ModelRelayRelay.err.log) {
  Get-Content C:\ModelRelay\data\ModelRelayRelay.err.log -Tail 80
} else {
  Get-Content C:\ModelRelay\data\relay.log -Tail 80 -ErrorAction SilentlyContinue
}
```

macOS：

```bash
sudo launchctl kickstart -k system/com.modelrelay.relay
sudo tail -n 80 /var/log/modelrelay-relay.err
```

服务必须正常运行。如果失败，先修复日志中的证书或配置问题，不要继续安装 Agent。

### 3.4 验证 Relay API 和 WebUI

首次安装时，安装器会打印一次：

```text
WebUI user:     <随机用户名>
WebUI password: <一次性明文>
```

凭据写在 `relay.env` 的 `RELAY_ADMIN_USERNAME` 与 `RELAY_ADMIN_PASSWORD`。第二次安装不会再打印，也不会覆盖已有 `relay.env`。改 env **不会**覆盖 SQLite 里已创建的管理员。

```bash
sudo sed -n 's/^RELAY_ADMIN_USERNAME=//p' /etc/modelrelay/relay.env
sudo sed -n 's/^RELAY_ADMIN_PASSWORD=//p' /etc/modelrelay/relay.env
```

Windows：

```powershell
Select-String -Path C:\ModelRelay\etc\relay\relay.env `
  -Pattern '^RELAY_ADMIN_(USERNAME|PASSWORD)='
```

验证模型目录：

```bash
TOKEN="$(sudo sed -n 's/^RELAY_INTERNAL_TOKEN=//p' /etc/modelrelay/relay.env)"
curl -i http://127.0.0.1:9100/v1/models \
  -H "Authorization: Bearer $TOKEN"
unset TOKEN
```

Windows：

```powershell
$token = (Select-String -Path C:\ModelRelay\etc\relay\relay.env -Pattern '^RELAY_INTERNAL_TOKEN=(.+)$').Matches.Groups[1].Value
curl.exe -i http://127.0.0.1:9100/v1/models -H "Authorization: Bearer $token"
```

WebUI 默认只监听 `127.0.0.1:9200`。可用 SSH 隧道，也可以用 Cloudflare/nginx 反代到该端口（不要把 `9100` 一起反出去）。反代时在 `relay.yaml` 设置 `admin.trusted_proxies`（本机 nginx 填 `127.0.0.1`），HTTPS 时设 `admin.secure_cookie: true` 或让反代传 `X-Forwarded-Proto: https`。建议再加 Cloudflare Access；可选配置 `admin.turnstile`。

本地或隧道打开：

```bash
ssh -L 9200:127.0.0.1:9200 root@relay.example.com
```

Windows 客户端可用：

```powershell
ssh -L 9200:127.0.0.1:9200 administrator@relay.example.com
```

浏览器打开反代域名，或 `http://127.0.0.1:9200/`，使用安装输出 / `relay.env` 里的用户名和密码登录。旧安装若 yaml 仍是 `username: admin`，则用户名仍为 `admin`。

## 4. 安装并配置 Agent

### 4.1 安装 Agent

先确认本机模型服务已监听（常见为 `http://127.0.0.1:8000/v1`）。vLLM / SGLang / Ollama / llama.cpp / LM Studio 均可，只要提供 OpenAI-compatible 接口。

#### Linux GPU

```bash
curl -fsSL \
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
  | sudo bash -s -- --component agent \
    --node-id gpu-001 \
    --relay-url wss://relay.example.com:9443/agent/v1/connect \
    --local-base-url http://127.0.0.1:8000/v1
```

#### Windows GPU

管理员 PowerShell。若第 2.2 节已经用 `-NoStart` 装过，可跳过安装，只补证书和配置：

```powershell
$p = Join-Path $env:TEMP "modelrelay-install.ps1"
Invoke-WebRequest -UseBasicParsing `
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.ps1 `
  -OutFile $p
powershell -NoProfile -ExecutionPolicy Bypass -File $p `
  -Component Agent `
  -NodeId gpu-001 `
  -RelayUrl "wss://relay.example.com:9443/agent/v1/connect" `
  -LocalBaseUrl "http://127.0.0.1:8000/v1"
```

只装 **Agent**。不要在 GPU 上执行 `-Component Relay`，也不要运行
`Restart-Service ModelRelayRelay` 或 `Start-ScheduledTask ModelRelay-Relay`。
那些属于 Relay 主机；GPU 上本来就没有这个服务，会报「系统找不到指定的文件」。

无 NSSM 时任务名是 `ModelRelay-Agent`，日志是 `C:\ModelRelay\data\agent.log`。
旧安装器曾误注册为 `ModelRelay-ModelRelayAgent`，找不到任务时先查：

```powershell
Get-ScheduledTask | Where-Object { $_.TaskName -like "*ModelRelay*" } | Format-Table TaskName, State
```

再重跑上面的 `install.ps1 -Component Agent`（不覆盖已有证书和 `agent.yaml`），
或前台运行 `C:\ModelRelay\bin\run-agent.ps1`。

GPU 在 NAT 后面即可，一般只需允许**出站** TCP `9443`。Windows 默认出站是放行的。

#### macOS GPU

```bash
curl -fsSL \
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
  | sudo bash -s -- --component agent \
    --node-id gpu-001 \
    --relay-url wss://relay.example.com:9443/agent/v1/connect \
    --local-base-url http://127.0.0.1:8000/v1
```

### 4.2 复制 Agent 证书

把证书管理机签发的 `gpu-001.crt` 和 Relay CA 公钥 `relay-ca.crt` 拷回 GPU。
私钥 `gpu-001.key` 必须已经在第 2.2 节生成本机，不要从别处拷过来覆盖。

Linux：

```bash
sudo install -m 0644 gpu-001.crt /etc/model-agent/gpu-001.crt
sudo install -m 0644 relay-ca.crt /etc/model-agent/relay-ca.crt
sudo chown modelrelay:modelrelay \
  /etc/model-agent/gpu-001.crt \
  /etc/model-agent/gpu-001.key \
  /etc/model-agent/relay-ca.crt
sudo /opt/modelrelay/bin/certctl inspect -cert /etc/model-agent/relay-ca.crt
# Subject 必须是 ModelRelay Relay CA
```

Windows：

```powershell
$d = "C:\ModelRelay\etc\agent"
Copy-Item -Force gpu-001.crt "$d\gpu-001.crt"
Copy-Item -Force relay-ca.crt "$d\relay-ca.crt"
# gpu-001.key 必须仍是本机 certctl csr 生成的那份
C:\ModelRelay\bin\certctl.exe inspect -cert "$d\relay-ca.crt"
C:\ModelRelay\bin\certctl.exe inspect -cert "$d\gpu-001.crt"
```

`relay-ca.crt` 的 Subject 必须是 **ModelRelay Relay CA**。
若看到 `ModelRelay Agent CA`，说明把 Agent CA 公钥拷反了，Agent 会报
`unknown authority` / `verification error`。`gpu-001.crt` 的 Issuer 才应是 Agent CA。

macOS：

```bash
sudo install -m 0644 gpu-001.crt \
  "/Library/Application Support/ModelAgent/gpu-001.crt"
sudo install -m 0644 relay-ca.crt \
  "/Library/Application Support/ModelAgent/relay-ca.crt"
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

Windows 对应 `C:\ModelRelay\etc\agent\agent.yaml`：

```yaml
node_id: gpu-001
relays:
  - url: "wss://relay.example.com:9443/agent/v1/connect"
    priority: 1
tls:
  cert: "C:\\ModelRelay\\etc\\agent\\gpu-001.crt"
  key: "C:\\ModelRelay\\etc\\agent\\gpu-001.key"
  ca: "C:\\ModelRelay\\etc\\agent\\relay-ca.crt"
  insecure_skip_verify: false
local:
  base_url: "http://127.0.0.1:8000/v1"
  tls_verify: true
```

`relays[].url` 的主机名必须能解析，且与 Relay 证书 SAN 一致。不要把 `insecure_skip_verify` 设为 true。

先确认本地模型服务：

```bash
curl -fsS http://127.0.0.1:8000/v1/models
```

Windows：`curl.exe -fsS http://127.0.0.1:8000/v1/models`

再启动 Agent：

```bash
sudo systemctl restart modelrelay-agent
sudo systemctl status modelrelay-agent --no-pager
sudo journalctl -u modelrelay-agent -n 100 --no-pager
```

Windows GPU（不要执行 `ModelRelayRelay`）：

```powershell
Get-Service ModelRelayAgent -ErrorAction SilentlyContinue
if ($?) {
  Restart-Service ModelRelayAgent
} else {
  Start-ScheduledTask -TaskName "ModelRelay-Agent"
}
if (Test-Path C:\ModelRelay\data\ModelRelayAgent.err.log) {
  Get-Content C:\ModelRelay\data\ModelRelayAgent.err.log -Tail 80
} else {
  Get-Content C:\ModelRelay\data\agent.log -Tail 80 -ErrorAction SilentlyContinue
}
```

`agent.log` 若只有 `starting ... agent.exe`、没有后续 `ModelRelay agent ... started`：
旧版 `run-agent.ps1` 会把 Go 写到 stderr 的日志当成错误并退出。重装 Agent，或把
`C:\ModelRelay\bin\run-agent.ps1` 换成安装器新生成的版本后再 `Start-ScheduledTask`。
前台运行时出现红色 `NativeCommandError` 但内容是 `agent 0.2.0 started`，表示进程已起来，
那是 PowerShell 把 stderr 当成错误，不是启动失败。

macOS：

```bash
sudo launchctl kickstart -k system/com.modelrelay.agent
sudo tail -n 80 /var/log/modelrelay-agent.err
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
Base URL: http://127.0.0.1:9100/v1          # New API 与 Relay 同机
# 或    https://api.example.com/v1          # 分机且已按第 9 节做 HTTPS 反代
# 不要用 wss://relay.example.com:9443       # 那是 Agent 入口，New API 连不上
密钥:     Relay 的 RELAY_INTERNAL_TOKEN
```

先同步 `GET /v1/models`，再测试 Chat 非流式和 SSE 流式请求。

## 6. 上线验收

- [ ] Relay、Agent 均为 `active (running)`。
- [ ] Agent 在 WebUI 中为 `online`。
- [ ] `/v1/models` 返回模型。
- [ ] Chat 非流式请求成功。
- [ ] Chat SSE 流式请求成功。
- [ ] 客户端断开后本地请求能够取消。
- [ ] `9443` 对需要接入的 GPU 可达（出口 IP 不固定时可对公网开放）。
- [ ] `9100`、`9200` 没有在云防火墙上对公网入站开放（WebUI 走反代而不是直接暴露 9200）。
- [ ] 数据库、配置和证书公钥已备份。
- [ ] CA 私钥、Agent 私钥、Relay 私钥和 Token 没有进入仓库。

## 7. 其他部署方案（保留完整手工方式）

主流程推荐一键安装；以下方案用于需要手工控制安装目录、权限或服务托管方式的场景。

### 7.1 Linux 手工 systemd

创建目录和服务用户：

```bash
sudo install -d -m 0750 /opt/modelrelay/bin /etc/modelrelay \
  /etc/model-agent /var/lib/modelrelay
sudo useradd --system --home /var/lib/modelrelay \
  --shell /usr/sbin/nologin modelrelay || true
```

从对应 Linux 发布包复制二进制：

```bash
sudo install -m 0755 relay agent certctl /opt/modelrelay/bin/
sudo chown -R modelrelay:modelrelay /var/lib/modelrelay
```

Relay 服务文件 `/etc/systemd/system/modelrelay-relay.service`：

```ini
[Unit]
Description=ModelRelay Relay
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=modelrelay
Group=modelrelay
WorkingDirectory=/var/lib/modelrelay
EnvironmentFile=/etc/modelrelay/relay.env
ExecStart=/opt/modelrelay/bin/relay -config /etc/modelrelay/relay.yaml
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/modelrelay

[Install]
WantedBy=multi-user.target
```

Agent 服务文件 `/etc/systemd/system/modelrelay-agent.service`：

```ini
[Unit]
Description=ModelRelay Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=modelrelay
Group=modelrelay
EnvironmentFile=/etc/model-agent/agent.env
ExecStart=/opt/modelrelay/bin/agent -config /etc/model-agent/agent.yaml
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true

[Install]
WantedBy=multi-user.target
```

启用服务：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now modelrelay-relay
sudo systemctl enable --now modelrelay-agent
sudo systemctl status modelrelay-relay modelrelay-agent --no-pager
```

### 7.2 Windows NSSM、WinSW 或任务计划

目录建议：

```text
C:\ModelRelay\bin\relay.exe
C:\ModelRelay\bin\agent.exe
C:\ModelRelay\etc\relay.yaml
C:\ModelRelay\etc\agent.yaml
C:\ModelRelay\data\
```

NSSM 示例：

```powershell
nssm install ModelRelayRelay C:\ModelRelay\bin\relay.exe
nssm set ModelRelayRelay AppParameters "-config C:\ModelRelay\etc\relay.yaml"
nssm set ModelRelayRelay AppDirectory C:\ModelRelay\data
nssm set ModelRelayRelay Start SERVICE_AUTO_START
nssm start ModelRelayRelay

nssm install ModelRelayAgent C:\ModelRelay\bin\agent.exe
nssm set ModelRelayAgent AppParameters "-config C:\ModelRelay\etc\agent.yaml"
nssm set ModelRelayAgent AppDirectory C:\ModelRelay\data
nssm set ModelRelayAgent Start SERVICE_AUTO_START
nssm start ModelRelayAgent
```

没有 NSSM/WinSW 时，可以用任务计划程序托管：

```powershell
$action = New-ScheduledTaskAction `
  -Execute "C:\ModelRelay\bin\relay.exe" `
  -Argument "-config C:\ModelRelay\etc\relay.yaml" `
  -WorkingDirectory "C:\ModelRelay\data"
$trigger = New-ScheduledTaskTrigger -AtStartup
$principal = New-ScheduledTaskPrincipal `
  -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName "ModelRelay-Relay" `
  -Action $action -Trigger $trigger -Principal $principal
Start-ScheduledTask -TaskName "ModelRelay-Relay"
```

Agent 使用相同方式注册 `ModelRelay-Agent` 任务。

### 7.3 macOS launchd

将 Agent 放到 `/usr/local/libexec/modelrelay-agent`，配置放到
`/Library/Application Support/ModelAgent/agent.yaml`。

创建 `/Library/LaunchDaemons/com.modelrelay.agent.plist`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.modelrelay.agent</string>
  <key>ProgramArguments</key><array>
    <string>/usr/local/libexec/modelrelay-agent</string>
    <string>-config</string>
    <string>/Library/Application Support/ModelAgent/agent.yaml</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/modelrelay-agent.log</string>
  <key>StandardErrorPath</key><string>/var/log/modelrelay-agent.err</string>
</dict></plist>
```

注册和查看：

```bash
sudo launchctl bootstrap system /Library/LaunchDaemons/com.modelrelay.agent.plist
sudo launchctl kickstart -k system/com.modelrelay.agent
sudo launchctl print system/com.modelrelay.agent
```

### 7.4 主备 Relay

部署两个独立 Relay：

1. 使用不同的 `relay_id`、监听地址和数据库文件。
2. 两个 Relay 使用同一套 Agent CA。
3. 两个 Relay 使用各自 SAN 正确的服务端证书。
4. New API 配置两个 Relay 渠道，或使用上层负载均衡。
5. Agent 配置两个 WSS 地址：

```yaml
relays:
  - url: "wss://relay-a.example.com:9443/agent/v1/connect"
    priority: 1
  - url: "wss://relay-b.example.com:9443/agent/v1/connect"
    priority: 2
prefer_primary_interval: 60
```

主 Relay 故障后 Agent 会退避重连备 Relay；进行中的请求不会迁移。

## 8. 日常运维

### 8.1 WebUI 和管理 API

WebUI 可执行立即探测、Drain、踢出节点、修改并发和证书吊销。
证书吊销会立即断开当前连接，并拒绝旧证书重连。

管理 API：

```bash
curl -s http://127.0.0.1:9200/api/overview
```

### 8.2 备份

```bash
sudo mkdir -p /backup/modelrelay
sudo sqlite3 /var/lib/modelrelay/modelrelay.db \
  ".backup '/backup/modelrelay/modelrelay-$(date +%F).db'"
sudo cp -a /etc/modelrelay /backup/modelrelay/
sudo cp -a /etc/model-agent /backup/modelrelay/
```

不要把 CA 私钥、Agent 私钥和内部 Token 放入共享备份。

### 8.3 证书轮换

1. 在 Agent 本地生成新的 CSR 和私钥。
2. 使用 Agent CA 签发新证书。
3. 替换 Agent 证书并重启 Agent。
4. 在 WebUI 确认新证书上线。
5. 确认旧证书不再使用后再吊销。

不要先吊销唯一正在使用的证书。

### 8.4 升级和回滚

升级不是热补丁：备份配置和数据，再跑安装器。安装器会先可选 Drain、停进程，再覆盖二进制并拉起。现有 yaml、证书、`relay.env` 不会被覆盖。

Windows 必须先停服务/任务，否则占用中的 `relay.exe`/`agent.exe` 无法覆盖；安装器已自动停止。

进行中的 HTTP/SSE 会断；Agent 随后自己重连 Relay。这是已接受的中断。同域名不改则证书不必重签。

**Linux Relay**

```bash
sudo tar -C /root -czf modelrelay-backup.tgz /etc/modelrelay /var/lib/modelrelay
curl -fsSL https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.sh \
  | sudo bash -s -- --component relay
sudo journalctl -u modelrelay-relay -n 40 --no-pager
```

**Windows Relay**（管理员 PowerShell）

```powershell
Copy-Item C:\ModelRelay\etc C:\ModelRelay\etc.bak -Recurse -Force
Copy-Item C:\ModelRelay\data C:\ModelRelay\data.bak -Recurse -Force
$p = Join-Path $env:TEMP "modelrelay-install.ps1"
Invoke-WebRequest -UseBasicParsing `
  https://raw.githubusercontent.com/Cardinal85/modelrelay/main/scripts/install.ps1 `
  -OutFile $p
powershell -NoProfile -ExecutionPolicy Bypass -File $p -Component Relay
if (Test-Path C:\ModelRelay\data\ModelRelayRelay.err.log) {
  Get-Content C:\ModelRelay\data\ModelRelayRelay.err.log -Tail 40
} else {
  Get-Content C:\ModelRelay\data\relay.log -Tail 40
}
```

**Linux / Windows Agent** 把 `--component agent` / `-Component Agent` 换成对应参数即可。可选：设置 `RELAY_ADMIN_URL`、`RELAY_ADMIN_USERNAME`、`RELAY_ADMIN_PASSWORD` 后安装器会先 Drain 该 `node_id`；没有凭据就跳过，直接停进程。

日志里应出现 `0.2.1 started`。失败时用备份的 `bin` 覆盖回去再启动。

### 8.5 常用排障

| 现象 | 优先检查 |
|---|---|
| `source directory not found` | 使用 `scripts/install.sh`，不要手写旧版本目录 |
| Relay 启动失败 | Relay 日志、证书路径、权限和 `relay.yaml` |
| Agent TLS 失败 | 两套 CA 是否拷反（见 8.6）、证书 SAN 是否包含 URL 主机名、`node_id` 与 CN、系统时间 |
| `unknown authority` 且提到 `ModelRelay Agent CA` | GPU 的 `tls.ca` / `relay-ca.crt` 实际是 Agent CA，应换成 Relay CA |
| `remote error: tls: unknown certificate authority` | Relay 的 `agent_ca` / `agent-ca.crt` 不是签发 `gpu-001.crt` 的那份 Agent CA |
| `x509: certificate is valid for ... not ...` | Agent URL 主机名不在 Relay 证书 SAN；重新签发并填对 `-dns`/`-ip` |
| `uri is not listable`（certmgr 浏览） | 使用含原生文件对话框的 Windows `certmgr.exe`（v0.2.0 更新后的 `modelrelay-windows-amd64.zip`） |
| 节点 offline | Relay `9443` 防火墙、GPU 出站、DNS、是否被七层 HTTPS 反代拆掉 mTLS |
| 反代后 Agent 立刻断开 | `9443` 被 nginx/Caddy/IIS/云 WAF 做了 TLS 终止；改为 TCP 透传或直连 |
| `model_not_found` | 本地模型服务 `/v1/models` 和模型名称 |
| `capability_not_supported` | 等待能力探测完成或重新探测 |
| 请求 `429` | 节点并发、队列长度和模型服务负载 |
| 请求 `504` | 本地模型响应时间和超时配置；反代 `proxy_read_timeout` 是否过短 |
| SSE 卡住或一次性刷出 | 反代开启了缓冲；见第 9 节 `proxy_buffering off` |
| WebUI `401` | 管理员密码和会话是否过期 |
| Windows `系统找不到指定的文件`（0x80070002） | GPU 上误启 `ModelRelay-Relay`；或旧任务名 `ModelRelay-ModelRelayAgent`，见 4.1 |
| Windows 服务起不来 | 任务计划/NSSM 是否管理员安装；NSSM 看 `*.err.log`，否则看 `agent.log` / `relay.log` |
| `agent.log` 只有 `starting agent.exe` | 旧 `run-agent.ps1` 被 stderr 日志杀掉；重装或换新启动脚本 |
| 前台红字 `NativeCommandError` 但已 `started` | Go 日志在 stderr，不是启动失败 |
| Windows `此应用无法运行` | `certmgr.exe` 需用 llvm-mingw CGO 构建，不要用 CodeBlocks MinGW 8.1 |

### 8.6 两套 CA 拷反（Agent 连不上 Relay）

两套 CA 不能互换。文件名像对、主题不对，一样会失败。

| 文件 | 正确主题 | 作用 | 放哪 |
|---|---|---|---|
| GPU `relay-ca.crt`（`tls.ca`） | **ModelRelay Relay CA** | Agent 校验 Relay 的 WSS 证书 | GPU `etc/agent/` |
| GPU `gpu-001.crt` | 由 Agent CA 签发 | Agent 出示的客户端证书 | 与本机 `gpu-001.key` 一对 |
| Relay `agent-ca.crt`（`agent_ca`） | **ModelRelay Agent CA** | Relay 校验 GPU 客户端证书 | Relay `etc/relay/` 或 `/etc/modelrelay/` |
| Relay `relay.crt` | 由 Relay CA 签发，SAN 含连接主机名 | Relay 的 WSS 服务端证书 | 与 `relay.key` 一对 |

证书管理机（certmgr）默认目录：

```text
%USERPROFILE%\ModelRelay\ca\agent\agent-ca.crt     → 拷到 Relay，仍叫 agent-ca.crt
%USERPROFILE%\ModelRelay\ca\relay\relay-ca.crt     → 拷到 GPU，仍叫 relay-ca.crt
```

`certctl init-ca` 在 `ca\relay\` 里生成的仍叫 `agent-ca.crt`，拷到 GPU 前要改名为 `relay-ca.crt`，并以 `inspect` 的 Subject 为准。

GPU 上核对：

```powershell
C:\ModelRelay\bin\certctl.exe inspect -cert C:\ModelRelay\etc\agent\relay-ca.crt
C:\ModelRelay\bin\certctl.exe inspect -cert C:\ModelRelay\etc\agent\gpu-001.crt
```

Linux：

```bash
certctl inspect -cert /etc/model-agent/relay-ca.crt
certctl inspect -cert /etc/model-agent/gpu-001.crt
```

典型日志：

1. `tls: failed to verify certificate: x509: certificate signed by unknown authority`
   （并写着 candidate authority certificate **"ModelRelay Agent CA"**，或 `crypto/rsa: verification error`）
   → GPU 拿 Agent CA 去校验 Relay 服务器证书。把 `ca/relay/` 下的 **Relay CA 公钥**覆盖 GPU 的 `relay-ca.crt`，重启 Agent。
2. 换成 `remote error: tls: unknown certificate authority`
   → 服务器证书已通过，但 Relay 不信任 `gpu-001.crt`。把 `ca/agent/agent-ca.crt` 拷到 Relay 的 `agent-ca.crt`，重启 **Relay**，再重启 Agent。
3. 两套 CA 曾删除重建：旧证书全部作废，必须重新签发 Relay 服务端证书和每台 GPU 的 Agent 证书，两边都换成新公钥。

不要把 `insecure_skip_verify` 设为 true。ModelRelay 不把证书写入 Windows 证书存储，换文件后重启进程即可。

## 9. 域名、反代、防火墙和证书 SAN

这一节回答：Relay 前面要不要反代、域名填什么、GPU 怎么连、各云和各系统怎么放行。

### 9.1 先分清三条连接

```text
GPU Agent  --TCP 9443 mTLS-->  Relay wss_listen     （必须原样 TLS，可 TCP 透传）
New API    --HTTP 9100------>  Relay http_listen    （可 HTTPS 反代）
管理员     --HTTP 9200------>  Relay admin.listen   （可 Cloudflare/nginx 反代；建议 Access）
```

| 入口 | 默认监听 | 能否七层 HTTPS 反代 | 证书 |
|---|---|---|---|
| Agent WSS | `0.0.0.0:9443` | **否**。只能直连或四层 TCP 透传 | Relay CA 签发的服务端证书 + Agent 客户端证书 |
| New API | `127.0.0.1:9100` | **可以**，但不要跟面板同一公网出口 | 可用 Let's Encrypt，与 Agent 证书无关 |
| WebUI | `127.0.0.1:9200` | **可以**。反代后设置 `trusted_proxies` / `secure_cookie`；建议 Cloudflare Access + Turnstile | 可用网站证书；源站仍听 9200 |

`9443` 用 DNS-only（灰云）指向源站时，源站 IP 会公开。这是 mTLS 直连的代价，用云防火墙关掉 `9100`/`9200`/`22` 公网入站即可，不必为藏 IP 再加跳板。

常见错误：把 `wss://relay.example.com:443` 指到 nginx，用网站证书。Agent 会校验 Relay CA，且必须出示客户端证书，握手失败。

### 9.2 推荐拓扑

**动态 GPU + 面板反代（常见）：Relay 自己听 9443，WebUI 经 Cloudflare 反代。**

1. 云防火墙：`9443/tcp` 对公网开放；`9100`/`9200`/`22` 不对公网入站。
2. Agent：`wss://relay.example.com:9443/agent/v1/connect`（DNS 仅解析，不要橙色云）。
3. New API 与 Relay 同机：Base URL `http://127.0.0.1:9100/v1`，不要把 9100 跟面板一起反出去。
4. WebUI：本机 `127.0.0.1:9200`，nginx/Cloudflare 反代到独立域名；`admin.trusted_proxies` 含反代地址。

**GPU 网段固定时：** 防火墙可以把 `9443` 收紧到来源网段。

**New API 与 Relay 分机：** 只给 `9100` 做内网反代或内网防火墙放行，仍然不要把 `9100` 放到公网。

**必须把 9443 放在负载均衡后面时：** 使用 **TCP/四层**（nginx `stream`、HAProxy TCP、AWS NLB、云厂商「TCP 转发」）。Relay 继续用自己的证书做 mTLS。负载均衡不要装网站证书。同域名不改则证书不必重签。

### 9.3 证书 SAN 与 URL 对照

签发 Relay 服务端证书时，把 Agent 会用到的每一种连法都写进 SAN。

| Agent 里的 `relays[].url` | 签发时至少包含 |
|---|---|
| `wss://relay.example.com:9443/agent/v1/connect` | `-dns relay.example.com` |
| `wss://relay-a.example.com:9443/...` | `-dns relay-a.example.com` |
| `wss://203.0.113.10:9443/...` | `-ip 203.0.113.10` |
| 有的 GPU 用域名、有的用内网 IP | `-dns ...` **和** `-ip ...` 都写 |

GPU 必须能解析该主机名。内网可用：

- 内网 DNS（AD、dnsmasq、CoreDNS）
- Windows `C:\Windows\System32\drivers\etc\hosts`
- Linux `/etc/hosts`

```text
203.0.113.10    relay.example.com
```

hosts 里的名字也必须出现在证书 SAN 中。

### 9.4 不要做的反代

- Cloudflare 橙色云（Proxied）、CDN、WAF 七层加速：会终止 TLS，mTLS 失效。`relay.example.com` 必须 **仅 DNS**（灰色云）。
- 阿里云 SLB/CLB HTTPS、腾讯云 CLB HTTPS、AWS ALB、Azure Application Gateway：七层，不能用于 9443。
- IIS ARR、Caddy `reverse_proxy`、nginx `http { }` 里的 `proxy_pass`：都是七层，不能用于 9443。
- 把 Let's Encrypt 证书配到 Relay 的 `tls_cert`：Agent 拿着 `relay-ca.crt` 校验会失败。Relay 的 WSS 证书必须由 **Relay CA** 签发。
- 把 New API 的 Base URL 写成 `wss://...:9443`。

### 9.5 nginx：9443 TCP 透传（可选）

同一台机器上 nginx 与 Relay 不能抢同一个 9443。让 Relay 改听本机高位端口，nginx 对外听 9443：

`relay.yaml`：

```yaml
wss_listen: "127.0.0.1:19443"
http_listen: "127.0.0.1:9100"
```

`/etc/nginx/nginx.conf` 的 `stream` 段（与 `http` 平级，不是 server 里）：

```nginx
stream {
    server {
        listen 9443;
        proxy_pass 127.0.0.1:19443;
        proxy_timeout 1d;
        proxy_connect_timeout 10s;
    }
}
```

Agent 仍然连接 `wss://relay.example.com:9443/agent/v1/connect`。证书 SAN 用 `relay.example.com`，不要写成 `127.0.0.1`。

### 9.6 nginx：只反代 New API（9100）

给 New API 用 HTTPS。流式 SSE 必须关缓冲、拉长超时：

```nginx
server {
    listen 443 ssl http2;
    server_name api.example.com;
    ssl_certificate     /etc/letsencrypt/live/api.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api.example.com/privkey.pem;

    client_max_body_size 64m;

    location / {
        proxy_pass http://127.0.0.1:9100;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Authorization $http_authorization;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

New API Base URL：`https://api.example.com/v1`。
密钥仍是 `RELAY_INTERNAL_TOKEN`。限制源 IP 为 New API 主机。

Windows 上可用官方 nginx for Windows，把 `stream` 和 `http` 同样配置；或 9443 不反代、只给 9100 做反代。

### 9.7 Caddy（仅 9100）

```caddy
api.example.com {
    reverse_proxy 127.0.0.1:9100 {
        flush_interval -1
        transport http {
            read_timeout 1h
        }
    }
}
```

不要用 Caddy 反代 9443，除非使用四层插件并且不做 TLS 终止。

### 9.8 HAProxy TCP 透传 9443

```
frontend agent_wss
    bind :9443
    mode tcp
    default_backend relay_wss

backend relay_wss
    mode tcp
    server relay1 127.0.0.1:19443 check
```

### 9.9 云负载均衡怎么选

| 产品 | 9443 Agent | 9100 New API |
|---|---|---|
| AWS NLB / GCP TCP / Azure LB（四层） | 可以，TCP 转发 | 可以，但不需要 |
| AWS ALB / 七层 SLB / CDN | 不可以 | 可以（内网） |
| Cloudflare Proxied | 不可以（会拆 mTLS） | 不建议把 9100 放到公网 CDN |
| 路由器端口映射 | 可以：外网 9443 → Relay 9443 | 仅映射给 New API 所在网段 |

WebUI 可以用 Cloudflare 橙色云反代到源站 `9200`（独立域名）。`9443` 必须 DNS-only。安全组：`9443` 对动态 GPU 可对公网开放；`9100`/`9200`/`22` 不对公网入站。源站 IP 会随 9443 DNS 公开，靠防火墙关口，而不是藏 IP。

### 9.10 Linux 防火墙

firewalld：

```bash
sudo firewall-cmd --permanent --add-rich-rule='rule family=ipv4 source address=10.0.0.0/8 port port=9443 protocol=tcp accept'
sudo firewall-cmd --reload
```

ufw：

```bash
sudo ufw allow from 10.0.0.0/8 to any port 9443 proto tcp
```

iptables 策略因发行版而异，原则相同：只放行 GPU 网段的 9443。

### 9.11 Windows 防火墙（Relay 主机）

见第 1 节。检查：

```powershell
Get-NetFirewallRule -DisplayName "*ModelRelay*" | Format-Table DisplayName, Direction, Action, Enabled
Get-NetTCPConnection -LocalPort 9443 -ErrorAction SilentlyContinue
```

GPU 主机一般不用入站规则。若出站被拦：

```powershell
New-NetFirewallRule -DisplayName "ModelRelay Agent outbound" `
  -Direction Outbound -Protocol TCP -RemotePort 9443 -Action Allow
```

### 9.12 Windows GPU 完整中间步骤（汇总）

1. 本机先跑通模型：`curl.exe http://127.0.0.1:8000/v1/models`。
2. 管理员安装 Agent（可 `-NoStart`），得到 `C:\ModelRelay\bin\certctl.exe`。
3. `certctl.exe csr -cn gpu-001 -out C:\ModelRelay\etc\agent`，生成 `.key` 和 `.csr`。
4. **只**把 `gpu-001.csr` 拷到证书管理机（U 盘 / SMB / `scp`）。
5. 证书管理机用 `certmgr` 或 `certctl issue` 签发，导出 `gpu-001.crt` 和 `relay-ca.crt`。
6. 拷回 GPU 的 `C:\ModelRelay\etc\agent\`，不要覆盖 `.key`。
7. 编辑 `agent.yaml`：`node_id`、`relays[].url`、证书路径、`local.base_url`。
8. 确认 GPU 能解析 `relay.example.com`（`nslookup` 或 `Test-NetConnection relay.example.com -Port 9443`）。
9. 启动 Agent（不要在 GPU 上执行 `ModelRelayRelay` / `ModelRelay-Relay`）：

   ```powershell
   Get-Service ModelRelayAgent -ErrorAction SilentlyContinue
   if ($?) { Restart-Service ModelRelayAgent }
   else { Start-ScheduledTask -TaskName "ModelRelay-Agent" }
   ```

   若提示找不到任务，先查旧任务名，或直接前台运行：

   ```powershell
   Get-ScheduledTask | Where-Object { $_.TaskName -like "*ModelRelay*" } | Format-Table TaskName, State
   Start-ScheduledTask -TaskName "ModelRelay-ModelRelayAgent" -ErrorAction SilentlyContinue
   powershell -NoProfile -ExecutionPolicy Bypass -File C:\ModelRelay\bin\run-agent.ps1
   ```

10. 看日志：有 NSSM 时是 `C:\ModelRelay\data\ModelRelayAgent.err.log`，否则是 `C:\ModelRelay\data\agent.log`。再到 WebUI 确认 `online`。

证书管理机可以是另一台 Windows 笔记本：解压 `modelrelay-windows-amd64.zip`，运行 `certmgr.exe`。CA 私钥不要放进 GPU 或 Relay。

### 9.13 本地模型地址

Agent 只连配置文件里的 `local.base_url`，防止 SSRF。

| 本机推理 | 典型 `local.base_url` |
|---|---|
| vLLM | `http://127.0.0.1:8000/v1` |
| SGLang | `http://127.0.0.1:30000/v1` |
| Ollama | `http://127.0.0.1:11434/v1` |
| llama.cpp server | `http://127.0.0.1:8080/v1` |
| LM Studio（Windows） | `http://127.0.0.1:1234/v1` |

若模型开了 API Key，写入 Agent 的 `agent.env`：`LOCAL_MODEL_API_KEY=...`。
模型服务必须监听本机或 Agent 能访问的内网地址，不要对公网开放。

### 9.14 多 GPU / 多节点

每台 GPU：独立 `node_id`、独立 CSR、独立证书。不要共用私钥。
多台可以连同一个 `wss://relay.example.com:9443/agent/v1/connect`。
同一台机器两个 Agent 要用不同 `node_id` 和不同证书目录。

## 10. 各平台路径与操作对照

| 项目 | Linux | Windows | macOS |
|---|---|---|---|
| 安装 | `install.sh --component relay\|agent` | 管理员 `install.ps1 -Component Relay\|Agent` | `install.sh` |
| 二进制 | `/opt/modelrelay/bin/` | `C:\ModelRelay\bin\` | `/usr/local/libexec/modelrelay/` |
| Relay 配置 | `/etc/modelrelay/relay.yaml` | `C:\ModelRelay\etc\relay\relay.yaml` | `/Library/Application Support/ModelRelay/relay.yaml` |
| Agent 配置 | `/etc/model-agent/agent.yaml` | `C:\ModelRelay\etc\agent\agent.yaml` | `/Library/Application Support/ModelAgent/agent.yaml` |
| 生成 CSR | `certctl csr -cn gpu-001 -out /etc/model-agent` | `certctl.exe csr -cn gpu-001 -out C:\ModelRelay\etc\agent` | `certctl csr -out ".../ModelAgent"` |
| 启动 Relay | `systemctl restart modelrelay-relay` | **仅 Relay 主机**：`Restart-Service ModelRelayRelay` 或 `Start-ScheduledTask ModelRelay-Relay` | `launchctl kickstart ...relay` |
| 启动 Agent | `systemctl restart modelrelay-agent` | **仅 GPU**：`Restart-Service ModelRelayAgent` 或 `Start-ScheduledTask ModelRelay-Agent` | `launchctl kickstart ...agent` |
| 日志 | `journalctl -u modelrelay-*` | NSSM：`data\ModelRelay*.err.log`；否则 `data\relay.log` / `data\agent.log` | `/var/log/modelrelay-*.err` |
| 证书管理器 | 对应系统发布包中的 `certmgr`（需该 OS 本地 CGO 构建） | `certmgr.exe`（windows-amd64 包已含） | 在 macOS 上 `build-certmgr.sh` |
| 拷 CSR | `scp gpu-001.csr ca-host:` | `scp` / U 盘 / SMB | `scp` |
| 卸载 | 见第 11 节 | 见第 11 节 | 见第 11 节 |

手工 systemd / NSSM / launchd 见第 7 节。主备 Relay 见 7.4。日常备份、轮换、升级见第 8 节。
卸载程序和证书见第 11 节。

## 11. 卸载程序和证书

卸载要分清三件事：**停程序**、**删本机文件**、**作废信任**。
只删文件不会让对端立刻拒绝旧证书。退役 GPU 应先在 Relay 上吊销该节点证书。

| 目的 | 做什么 |
|---|---|
| 暂时停用 | 只停服务，文件留下 |
| 卸掉本机程序 | 停服务 + 删安装目录（先备份数据库和配置） |
| 退役一台 GPU | 先吊销该节点证书，再删 GPU 上的程序和 `gpu-001.*` |
| 整套作废 | 销毁 CA 私钥（证书管理机 + 离线备份），再删各机器上的程序和证书 |

`certmgr` 没有安装器，关掉窗口即可。CA 工作区默认在当前用户目录
`ModelRelay\ca\`（Windows）或 `~/ModelRelay/ca/`（Linux/macOS），
**不会**随 Relay/Agent 安装目录一起删除。只卸运行节点时不要删这个目录。

本地模型（vLLM / Ollama / LM Studio 等）不是 ModelRelay 安装的，按各自方式停止。

### 11.1 Windows

管理员 PowerShell。Relay 主机只卸 Relay，GPU 主机只卸 Agent。默认安装根目录是 `C:\ModelRelay`。

```powershell
Stop-Service ModelRelayRelay, ModelRelayAgent -ErrorAction SilentlyContinue
Get-Service ModelRelayRelay, ModelRelayAgent -ErrorAction SilentlyContinue |
  ForEach-Object { sc.exe delete $_.Name }

if (Get-Command nssm.exe -ErrorAction SilentlyContinue) {
  nssm stop ModelRelayRelay 2>$null
  nssm remove ModelRelayRelay confirm 2>$null
  nssm stop ModelRelayAgent 2>$null
  nssm remove ModelRelayAgent confirm 2>$null
}

Unregister-ScheduledTask -TaskName "ModelRelay-Relay" -Confirm:$false -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName "ModelRelay-Agent" -Confirm:$false -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName "ModelRelay-ModelRelayRelay" -Confirm:$false -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName "ModelRelay-ModelRelayAgent" -Confirm:$false -ErrorAction SilentlyContinue

Get-NetFirewallRule -DisplayName "*ModelRelay*" -ErrorAction SilentlyContinue |
  Remove-NetFirewallRule

# 先备份 C:\ModelRelay\data 和 etc，再删除
Remove-Item -Recurse -Force C:\ModelRelay
```

本机证书随安装目录删除：

```text
Relay：C:\ModelRelay\etc\relay\   relay.crt / relay.key / agent-ca.crt
GPU：  C:\ModelRelay\etc\agent\   gpu-001.crt / gpu-001.key / relay-ca.crt
```

证书管理机上的 CA 是单独目录：

```text
C:\Users\<用户>\ModelRelay\ca\agent\agent-ca.key
C:\Users\<用户>\ModelRelay\ca\relay\relay-ca.key
```

整套作废时才删除上述 `ca` 目录，并同时销毁离线备份中的 `.key`。

### 11.2 Linux

```bash
sudo systemctl disable --now modelrelay-relay modelrelay-agent
sudo rm -f /etc/systemd/system/modelrelay-relay.service \
           /etc/systemd/system/modelrelay-agent.service
sudo systemctl daemon-reload

sudo rm -rf /opt/modelrelay
# 先备份配置、证书和数据库
sudo rm -rf /etc/modelrelay /etc/model-agent /var/lib/modelrelay
sudo userdel modelrelay 2>/dev/null || true

sudo firewall-cmd --permanent --remove-port=9443/tcp 2>/dev/null || true
sudo ufw delete allow 9443/tcp 2>/dev/null || true
```

### 11.3 macOS

```bash
sudo launchctl bootout system/com.modelrelay.relay 2>/dev/null || true
sudo launchctl bootout system/com.modelrelay.agent 2>/dev/null || true
sudo rm -f /Library/LaunchDaemons/com.modelrelay.relay.plist \
           /Library/LaunchDaemons/com.modelrelay.agent.plist

sudo rm -rf /usr/local/libexec/modelrelay
sudo rm -rf "/Library/Application Support/ModelRelay" \
            "/Library/Application Support/ModelAgent"
sudo rm -f /var/log/modelrelay-relay.log /var/log/modelrelay-relay.err \
           /var/log/modelrelay-agent.log /var/log/modelrelay-agent.err
```

### 11.4 证书怎样才算失效

1. **退役 GPU**：在 `certmgr`「在线吊销」中吊销该证书序列号，WebUI 确认节点断开，再删 GPU 上的程序和 `gpu-001.crt` / `.key`。只删文件、不吊销时，他人拿到旧密钥仍可能连上 Relay。
2. **卸 Relay**：停服务并删 `relay.crt` / `relay.key`。若以后重装仍使用同一套 Agent CA，原先签发的 Agent 证书仍然有效。
3. **作废整套 CA**：确认无用后销毁 `agent-ca.key` 和 `relay-ca.key`（证书管理机 + U 盘）。没有私钥就无法再签新证；已发出的证书要靠吊销或更换新 CA 才不再被信任。



