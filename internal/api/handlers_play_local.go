package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"lime/internal/store"
)

// localPlayer 描述一个可以唤起本地播放器的 URL 协议。
// proto 里的 %s 会替换成 LiMe 的直链；query=true 时直链先做 URL 编码
// （IINA / Infuse 把直链作为 query 参数，不编码会截断）。
type localPlayer struct {
	Key   string `json:"Key"`
	Label string `json:"Label"`
	Proto string `json:"-"`
	Query bool   `json:"-"`
	Note  string `json:"Note,omitempty"`
}

var localPlayers = []localPlayer{
	{Key: "vlc", Label: "VLC", Proto: "vlc://%s", Note: "Windows / macOS 安装后自动注册"},
	{Key: "potplayer", Label: "PotPlayer", Proto: "potplayer://%s", Note: "需在 PotPlayer 设置里允许 URL Scheme"},
	{Key: "mpv", Label: "MPV", Proto: "mpv://%s", Note: "需安装 mpv-handler 并注册协议"},
	{Key: "iina", Label: "IINA（macOS）", Proto: "iina://weblink?url=%s", Query: true},
	{Key: "infuse", Label: "Infuse（iOS / macOS）", Proto: "infuse://x-callback-url/play?url=%s", Query: true},
}

func playerByKey(key string) (localPlayer, bool) {
	for _, p := range localPlayers {
		if p.Key == key {
			return p, true
		}
	}
	return localPlayer{}, false
}

// webPlayTarget 生成本地播放器可用的协议地址。
// GET  ?itemId=&sourceId=&player=  → {HttpUrl, ProtocolUrl, M3uUrl, Player, Players}
// POST {"Player":"vlc"}           → 保存管理员默认播放器
func (h *Handler) webPlayTarget(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	if r.Method == http.MethodPost {
		if !u.IsAdmin {
			writeError(w, http.StatusForbidden, "Forbidden")
			return
		}
		var p struct {
			Player string `json:"Player"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if _, ok := playerByKey(p.Player); !ok {
			writeError(w, http.StatusBadRequest, "不支持的播放器")
			return
		}
		if err := h.db.SetSetting("local_player", p.Player); err != nil {
			writeError(w, http.StatusInternalServerError, "save setting failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"Player": p.Player})
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()
	itemID := strings.TrimSpace(q.Get("itemId"))
	if itemID == "" {
		// 只取播放器列表和默认值，详情页初始化时用。
		writeJSON(w, http.StatusOK, map[string]any{
			"DefaultPlayer": h.defaultPlayer(),
			"Players":       localPlayers,
		})
		return
	}

	it, err := h.db.GetItem(itemID)
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if !h.canAccessLibrary(u, it.LibraryID) || !ratingAllowed(u, it) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	sourceID := strings.TrimSpace(q.Get("sourceId"))
	sources, err := h.db.MediaSourcesByItem(itemID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load sources failed")
		return
	}
	var src *store.MediaSource
	for _, s := range sources {
		if sourceID == "" || s.ID == sourceID {
			src = s
			break
		}
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "source not found")
		return
	}

	playerKey := strings.TrimSpace(q.Get("player"))
	if playerKey == "" {
		playerKey = h.defaultPlayer()
	}
	spec, ok := playerByKey(playerKey)
	if !ok {
		writeError(w, http.StatusBadRequest, "不支持的播放器")
		return
	}

	httpURL := h.localStreamURL(r, itemID, src.ID)
	protoURL := buildProtocolURL(spec, httpURL)

	m3u := externalBase(r) + "/web/api/playlist.m3u?itemId=" + url.QueryEscape(itemID) +
		"&sourceId=" + url.QueryEscape(src.ID)
	if h.cfg.StreamRequireAuth {
		m3u += "&api_key=" + url.QueryEscape(parseAuth(r).Token)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"HttpUrl":       httpURL,
		"ProtocolUrl":   protoURL,
		"M3uUrl":        m3u,
		"Player":        spec.Key,
		"PlayerLabel":   spec.Label,
		"DefaultPlayer": h.defaultPlayer(),
		"Players":       localPlayers,
	})
}

// localStreamURL 返回给本地播放器用的直链。开启 StreamRequireAuth 时
// 播放器不会带 Authorization 头，必须把 token 放进 api_key。
func (h *Handler) localStreamURL(r *http.Request, itemID, sourceID string) string {
	u := h.absoluteStreamURL(r, itemID, sourceID)
	if h.cfg.StreamRequireAuth {
		u += "&api_key=" + url.QueryEscape(parseAuth(r).Token)
	}
	return u
}

// buildProtocolURL 把 LiMe 直链套进播放器的 URL 协议。
// IINA / Infuse 把直链放在 query 参数里，必须编码；VLC / PotPlayer / MPV
// 直接拼在协议后面。
func buildProtocolURL(spec localPlayer, httpURL string) string {
	if spec.Query {
		return fmt.Sprintf(spec.Proto, url.QueryEscape(httpURL))
	}
	return fmt.Sprintf(spec.Proto, httpURL)
}

// webPlaylist 输出 .m3u 播放列表，作为协议唤不起时的通用回退。
func (h *Handler) webPlaylist(w http.ResponseWriter, r *http.Request) {
	if h.cfg.StreamRequireAuth {
		if _, ok := h.requireUser(w, r); !ok {
			return
		}
	}
	q := r.URL.Query()
	itemID := strings.TrimSpace(q.Get("itemId"))
	sourceID := strings.TrimSpace(q.Get("sourceId"))
	if itemID == "" || sourceID == "" {
		writeError(w, http.StatusBadRequest, "itemId and sourceId are required")
		return
	}
	it, err := h.db.GetItem(itemID)
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	sources, err := h.db.MediaSourcesByItem(itemID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load sources failed")
		return
	}
	found := false
	for _, s := range sources {
		if s.ID == sourceID {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "source not found")
		return
	}

	httpURL := h.localStreamURL(r, itemID, sourceID)
	name := strings.TrimSpace(it.Name)
	if name == "" {
		name = it.ID
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="lime-playlist.m3u"`)
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1,%s\n%s\n", name, httpURL)
}

// defaultPlayer 读取管理员在设置里选的默认播放器，没配时用 PotPlayer。
func (h *Handler) defaultPlayer() string {
	if v, err := h.db.GetSetting("local_player"); err == nil {
		if _, ok := playerByKey(strings.TrimSpace(v)); ok {
			return strings.TrimSpace(v)
		}
	}
	return "potplayer"
}
