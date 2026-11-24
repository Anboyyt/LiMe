package api

import (
	"io"
	"net/http"
	"time"

	"lime/internal/store"
)

// Yamby 对「Emby 地址 → 302 → 云盘 CDN」这条链路兼容性很差：
// 往往只发一次取流请求，跟不过去或者跟过去被 115 按 UA 拒绝，表现就是
// 点播放没反应。对它改成服务端中转：LiMe 用 Yamby 的 UA 取流再回给客户端。
// 代价是视频流量经过 NAS，外网上行不够时可能卡；可用 LIME_YAMBY_PROXY=false 关掉。
var streamProxyClient = &http.Client{
	Transport: func() *http.Transport {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = nil // 直连 115 CDN，不走容器里的环境代理
		t.ResponseHeaderTimeout = 20 * time.Second
		t.MaxIdleConnsPerHost = 8
		return t
	}(),
	// 视频流没有总超时；连接和响应头超时由 Transport 控制。
	Timeout: 0,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		// 跨域跳转时 Go 默认会丢掉 Range 等头，这里重新补上。
		if len(via) > 0 {
			if rg := via[0].Header.Get("Range"); rg != "" && req.Header.Get("Range") == "" {
				req.Header.Set("Range", rg)
			}
		}
		return nil
	},
}

func (h *Handler) proxyStream(w http.ResponseWriter, r *http.Request, src *store.MediaSource, location string) {
	// 头/尾缓存命中时直接从本地回，后面的范围再继续透传 CDN。
	if h.tryServeStreamCache(w, r, src) {
		return
	}

	final := location
	if u, err := h.resolveURL(r.Context(), location, r.UserAgent(), autoResolveTimeout); err == nil && u != "" {
		final = u
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, final, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream url")
		return
	}
	// 115 的直链和 UA 绑定，必须用播放器自己的 UA 去取。
	req.Header.Set("User-Agent", r.UserAgent())
	for _, k := range []string{"Range", "If-Range", "Accept", "Accept-Language"} {
		if v := r.Header.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := streamProxyClient.Do(req)
	if err != nil {
		// 客户端主动断开时上游请求会被 cancel，这不是错误，也不要再写 502。
		if r.Context().Err() != nil {
			return
		}
		h.log.Warn("stream proxy failed", "item", src.ItemID, "source", src.ID, "err", err)
		writeError(w, http.StatusBadGateway, "upstream error")
		return
	}
	defer resp.Body.Close()

	for _, k := range []string{
		"Content-Type", "Content-Length", "Content-Range",
		"Accept-Ranges", "Last-Modified", "ETag",
	} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	// 115 CDN 常回 application/octet-stream，部分播放器会因此不愿直接解封装。
	if ct := w.Header().Get("Content-Type"); ct == "" || ct == "application/octet-stream" {
		if v := contentTypeForContainer(src.Container); v != "" {
			w.Header().Set("Content-Type", v)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64<<10)
	var written int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				break
			}
			written += int64(n)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				h.log.Warn("stream proxy read", "item", src.ItemID, "source", src.ID, "err", rerr)
			}
			break
		}
	}
	h.log.Info("stream proxy",
		"item", src.ItemID,
		"source", src.ID,
		"status", resp.StatusCode,
		"bytes", written,
		"remote", r.RemoteAddr,
	)
}
