package api

import (
	_ "embed"
	"net/http"
)

//go:embed assets/logo.png
var logoPNG []byte

//go:embed assets/brand-icon.png
var brandIconPNG []byte

//go:embed assets/brand-wordmark.png
var brandWordmarkPNG []byte

// brandingSplash 返回 LiMe 的 logo。
// Jellyfin 的部分客户端会在启动/登录时请求这个地址。
func (h *Handler) brandingSplash(w http.ResponseWriter, r *http.Request) {
	servePNG(w, brandWordmarkPNG)
}

// brandingLogo 给自建 Web UI / 第三方客户端使用的固定地址。
func (h *Handler) brandingLogo(w http.ResponseWriter, r *http.Request) {
	servePNG(w, logoPNG)
}

// brandingIcon 是方形图标，用于侧栏、顶栏与 favicon。
func (h *Handler) brandingIcon(w http.ResponseWriter, r *http.Request) {
	servePNG(w, brandIconPNG)
}

// brandingWordmark 是横向字标，用于登录页。
func (h *Handler) brandingWordmark(w http.ResponseWriter, r *http.Request) {
	servePNG(w, brandWordmarkPNG)
}

func servePNG(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}
