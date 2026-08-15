// Package webui 内嵌 Relay 运维 WebUI 静态资源。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFS embed.FS

// StaticFS 返回 WebUI 静态资源文件系统。
func StaticFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
