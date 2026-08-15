// certmgr 是 ModelRelay 跨平台证书管理器（Go/Fyne 桌面程序）。
//
// 用于在证书管理机上离线完成 CA 工作区、CSR 签发、Relay 证书、检查和部署导出。
// 可选连接 Relay 管理 API 进行证书吊销。不持久化管理员密码。
package main

import (
	"fmt"
	"os"

	"modelrelay/internal/version"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-version", "--version":
			fmt.Printf("%s certmgr %s (go %s)\n", version.Name, version.ToolVersion(), version.GoVersion())
			return
		case "help", "-h", "--help":
			fmt.Print(`ModelRelay certmgr — 跨平台证书管理器

用法:
  certmgr           打开图形界面
  certmgr version   显示版本

CA 私钥只保存在本机受保护目录，不要上传到 Relay、Agent 或 GitHub。
Agent 私钥必须在 GPU 主机用 certctl csr 生成，本程序不会代为生成。
`)
			return
		}
	}
	runUI()
}
