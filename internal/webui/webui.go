// Package webui 托管两套静态界面：
//   - /       原生媒体界面（无框架，轻量）
//   - /web/   运维面板
package webui

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.js style.css
var ops embed.FS

//go:embed app
var app embed.FS

// OpsFS 是运维面板的静态文件。
var OpsFS fs.FS = ops

// AppFS 是媒体界面的静态文件。
func AppFS() (fs.FS, error) {
	return fs.Sub(app, "app")
}
