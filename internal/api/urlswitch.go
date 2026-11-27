package api

import (
	"fmt"
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

// streamBase 决定 PlaybackInfo 里 Path 用哪个前缀。
//
// 走 PanSave 反代时请求的 Host 是内网地址，必须给内网上游地址，否则反代
// 探测 302 会回环；客户端直接从公网域名（Cloudflare 隧道 / 端口映射）访问时
// 给外部地址，否则它拿到的 http://192.168.x.x 根本连不上。
func (h *Handler) streamBase(r *http.Request) string {
	// 反代（PanSave/Cloudflare）转发时会把 Host 改成上游地址，必须优先看
	// X-Forwarded-Host 才能判断客户端是从哪个入口进来的：
	//   ps 域名 → 返回 ps 域名，播放继续走反代
	//   lime 域名 → 返回 lime 域名，LiMe 自己 302
	origHost := r.Host
	if hf := firstForwarded(r.Header.Get("X-Forwarded-Host")); hf != "" {
		origHost = hf
	}
	base := externalBase(r)
	if h.cfg.InternalURL != "" && isPrivateHost(origHost) {
		base = h.cfg.InternalURL
	}
	// 诊断日志：每个 PlaybackInfo / 详情请求都会走这里，反代下量很大
	// （20 分钟 160+ 条），降成 Debug 避免刷屏，排查时再开级别看。
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
		h.log.Debug("stream base via proxy", "host", r.Host, "xfh", xfh, "base", base)
	}
	return base
}

// streamPath 生成播放路径。playSessionID 可选：PlaybackInfo 会带上，
// 反代（PanSave）据此区分不同播放会话；列表场景没有会话就不带。
func (h *Handler) streamPath(itemID, sourceID string, playSessionID ...string) string {
	u := "/Videos/" + itemID + "/stream?static=true&MediaSourceId=" + url.QueryEscape(sourceID)
	if len(playSessionID) > 0 && playSessionID[0] != "" {
		u += "&PlaySessionId=" + url.QueryEscape(playSessionID[0])
	}
	return u
}

// mediaStreamURL 生成 MediaSources 里的播放地址。
// 播放器不会带 Authorization 头，开启流鉴权时必须把 token 放进 api_key，
// 否则拿 DirectStreamUrl 直连会 401。
func (h *Handler) mediaStreamURL(r *http.Request, itemID, sourceID string, playSessionID ...string) string {
	u := h.streamPath(itemID, sourceID, playSessionID...)
	// Yamby 会把服务器地址和 DirectStreamUrl 直接拼接，给绝对地址会变成
	// /embyhttps://cdn... 这种非法路径。对它只返回相对路径。
	if !usesRelativeStreamURL(r) {
		u = h.streamBase(r) + u
	}
	if h.cfg.StreamRequireAuth {
		if tok := parseAuth(r).Token; tok != "" {
			u += "&api_key=" + url.QueryEscape(tok)
		}
	}
	return u
}

// sourcePath 生成 MediaSources[].Path / DirectStreamUrl。
//
// 默认给本服务的 302 端点；开启 MediaPathStrm 时直接给 STRM 里的原始地址。
// PanSave 的 Emby 反代只认 /strm 形状的片源路径来启用 115 复制同播：返回
// /Videos/... 会被当成普通外部 HTTP 源，第二个设备拿到的还是同一条直链，
// 播放时就 403。地址仍过一遍 streamLocation，保证 rewrite 后的 host 可达。
func (h *Handler) sourcePath(r *http.Request, itemID, sourceID, rawURL string, playSessionID ...string) string {
	// Yamby / 爆米花会把服务器地址和这个字段直接拼接，绝对地址会拼成
	// /embyhttps://... 这种非法路径，对它们保持 LiMe 的相对地址。
	if h.cfg.MediaPathStrm && strings.TrimSpace(rawURL) != "" && !usesRelativeStreamURL(r) {
		return h.streamLocation(r, rawURL)
	}
	return h.mediaStreamURL(r, itemID, sourceID, playSessionID...)
}

// usesRelativeStreamURL 判断客户端是否要求 MediaSources 里的播放地址是相对路径。
// Yamby 和网易爆米花（Filmly）都会把服务器地址和拿到的地址直接拼接：
// 给绝对地址会变成 "/embyhttps://cdn..." 这种非法路径，直接播不了。
func usesRelativeStreamURL(r *http.Request) bool {
	ua := strings.ToLower(strings.TrimSpace(r.UserAgent()))
	return strings.HasPrefix(ua, "yamby/") || strings.HasPrefix(ua, "filmly/")
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
//
// 只做逐字节百分号转义，绝不 url.Parse + ParseQuery/Encode：
//   - Encode 会把参数按字母排序、把 %20 改成 +，带签名的地址会直接失效；
//   - ParseQuery 会按 & 拆分，未编码的 file_name=A&B.mkv 会被切成两个参数。
func safeLocation(raw string) string {
	needEscape := false
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c <= 0x20 || c >= 0x7f {
			needEscape = true
			break
		}
	}
	if !needEscape {
		return raw
	}
	var b strings.Builder
	b.Grow(len(raw) + 32)
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		// 保留已有的 %XX 编码：% 本身不转义，只转非 ASCII 和非法可见字符。
		if c <= 0x20 || c >= 0x7f || strings.IndexByte("\"<>\\^`{|}", c) >= 0 {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
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
