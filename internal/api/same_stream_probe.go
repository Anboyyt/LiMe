package api

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// webSameStreamProbe 检查 PanSave 反代地址是否可达。
// 不跟随重定向：反代本身就会 302 到 CDN，跟过去只能说明 CDN 通，说明不了反代。
func (h *Handler) webSameStreamProbe(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Url string `json:"Url"`
	}
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	raw := strings.TrimSpace(req.Url)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(w, http.StatusBadRequest, "地址需要以 http:// 或 https:// 开头")
		return
	}

	client := &http.Client{
		Timeout: 4 * time.Second,
		// 反代一般在内网，不能被 HTTP_PROXY 带走。
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(u.String())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"Ok": false, "Error": err.Error()})
		return
	}
	defer resp.Body.Close()
	// 读掉少量响应体，别为了探测把整页内容拉下来。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	ok := resp.StatusCode < 400
	out := map[string]any{"Ok": ok, "Status": resp.StatusCode}
	if !ok {
		out["Error"] = "HTTP " + resp.Status
	}
	h.log.Info("same-stream probe", "url", raw, "status", resp.StatusCode, "ok", ok)
	writeJSON(w, http.StatusOK, out)
}
