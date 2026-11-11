package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// externalBase 返回客户端访问本服务所用的 scheme://host。
// 反代（Cloudflare Tunnel / nginx）情况下优先采用转发头。
func externalBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := firstForwarded(r.Header.Get("X-Forwarded-Proto")); p != "" {
		scheme = p
	}

	host := r.Host
	if h := firstForwarded(r.Header.Get("X-Forwarded-Host")); h != "" {
		host = h
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return scheme + "://" + host
}

func firstForwarded(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// absoluteStreamURL 生成客户端可直接请求的播放地址。
// 客户端拿到后请求 LiMe，LiMe 再 302 到网盘。
func (h *Handler) absoluteStreamURL(r *http.Request, itemID, sourceID string) string {
	return externalBase(r) + h.streamPath(itemID, sourceID)
}

func (h *Handler) streamPath(itemID, sourceID string) string {
	return "/Videos/" + itemID + "/stream?static=true&MediaSourceId=" + url.QueryEscape(sourceID)
}

// streamLocation 返回最终的 302 目标地址：STRM 里的原始 URL，
// 按配置把 PanSave 的 host 改写成客户端可达的地址。
func (h *Handler) streamLocation(r *http.Request, raw string) string {
	if !h.cfg.RewriteEnabled {
		return safeLocation(raw)
	}
	target := h.rewriteTarget(r)
	if target == "" {
		return safeLocation(raw)
	}
	from := h.cfg.RewriteFrom
	if from != "" && !strings.EqualFold(hostOnly(raw), from) {
		// STRM 里已经是别的地址（例如 CDN 直链），不再改写。
		return safeLocation(raw)
	}
	return safeLocation(rewriteHost(raw, target))
}

// safeLocation 保证 Location 头是合法的 ASCII URI。
// PanSave 的 file_name 参数偶尔带未编码中文，直接放进头会导致部分客户端解析失败。
func safeLocation(raw string) string {
	for i := 0; i < len(raw); i++ {
		if raw[i] > 127 {
			u, err := url.Parse(raw)
			if err != nil {
				return raw
			}
			if u.RawQuery != "" {
				if q, err := url.ParseQuery(u.RawQuery); err == nil {
					u.RawQuery = q.Encode()
				}
			}
			return u.String()
		}
	}
	return raw
}

// rewriteTarget 判断当前客户端应使用哪个地址：局域网用 local，公网用 public。
func (h *Handler) rewriteTarget(r *http.Request) string {
	local, public := h.cfg.RewriteLocal, h.cfg.RewritePublic

	if public == "" {
		return local
	}
	if local == "" {
		return public
	}

	if ip := clientAddrIP(r); ip != nil && !isPrivateIP(ip) {
		return public
	}
	if !isPrivateHost(r.Host) {
		return public
	}
	return local
}

// clientAddrIP 取最可信的客户端地址：CF-Connecting-IP → X-Real-IP → XFF 首项 → RemoteAddr。
func clientAddrIP(r *http.Request) net.IP {
	for _, h := range []string{"CF-Connecting-IP", "X-Real-IP"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return net.ParseIP(v)
		}
	}
	if v := firstForwarded(r.Header.Get("X-Forwarded-For")); v != "" {
		if ip := net.ParseIP(v); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// isPrivateHost 判断请求 Host 是否是局域网地址。
// 裸主机名（如 nas）也按局域网处理，公网域名按公网处理。
func isPrivateHost(host string) bool {
	host = hostOnly(host)
	if host == "" {
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return isPrivateIP(ip)
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	return false
}

// hostOnly 去掉 scheme 和路径，只留下 host[:port]。
func hostOnly(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

// rewriteHost 把 rawURL 的 host（以及可选的 scheme）替换成 target。
// target 可以是 "192.168.1.155:16868"，也可以是 "https://pan.example.com"。
func rewriteHost(rawURL, target string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if i := strings.Index(target, "://"); i >= 0 {
		u.Scheme = target[:i]
		u.Host = target[i+3:]
	} else {
		u.Host = target
	}
	if u.Host == "" {
		return rawURL
	}
	return u.String()
}
