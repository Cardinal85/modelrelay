// Package version 提供 ModelRelay 各组件的统一版本信息。
package version

import "runtime/debug"

const (
	// Name 是产品名称。
	Name = "ModelRelay"
	// Version 是当前版本号。
	Version = "0.2.2"
)

// AgentVersion 返回 agent 版本字符串。
func AgentVersion() string { return Version }

// RelayVersion 返回 relay 版本字符串。
func RelayVersion() string { return Version }

// ToolVersion 返回 certctl/certmgr 版本字符串。
func ToolVersion() string { return Version }

// GoVersion 返回构建所用 Go 版本。
func GoVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		return bi.GoVersion
	}
	return "unknown"
}
