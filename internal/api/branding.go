package api

import (
	_ "embed"
	"net/http"
)

//go:embed assets/logo.png
var logoPNG []byte

// brandingSplash 返回 LiMe 的 logo。
// Jellyfin 的部分客户端会在启动/登录时请求这个地址。
func (h *Handler) brandingSplash(w http.ResponseWriter, r *http.Request) {
	serveLogo(w, r)
}

// brandingLogo 给自建 Web UI / 第三方客户端使用的固定地址。
func (h *Handler) brandingLogo(w http.ResponseWriter, r *http.Request) {
	serveLogo(w, r)
}

func serveLogo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(logoPNG)
}
