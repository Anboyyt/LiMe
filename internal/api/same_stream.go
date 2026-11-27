package api

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

// 多人同播（PanSave 115 复制）的面板配置。存 settings 表，改完即时生效；
// 环境变量仍然优先，已有部署不会因为面板默认值改变行为。
const (
	sameModeKey    = "playback_same_stream_mode"
	sameEntriesKey = "playback_same_stream_entries"
	sameHostsKey   = "playback_same_stream_hosts"
)

// loadSameStreamSettings 启动时把面板配置读进内存。
func (h *Handler) loadSameStreamSettings() {
	mode, _ := h.db.GetSetting(sameModeKey)
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on", "force":
		mode = "on"
	case "off":
		mode = "off"
	default:
		mode = "auto"
	}
	entries, _ := h.db.GetSetting(sameEntriesKey)
	hosts, _ := h.db.GetSetting(sameHostsKey)
	h.sameMu.Lock()
	h.sameMode = mode
	h.sameEntries = splitProxyEntries(entries)
	h.sameHosts = splitProxyHosts(hosts)
	h.sameMu.Unlock()

	h.hostSeenMu.Lock()
	if h.hostSeen == nil {
		h.hostSeen = make(map[string]time.Time)
	}
	h.hostSeenMu.Unlock()
}

// saveSameStreamSettings 保存面板改动并立刻应用到当前进程。
func (h *Handler) saveSameStreamSettings(mode, entries, hosts string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "on", "force":
		mode = "on"
	case "off":
		mode = "off"
	default:
		mode = "auto"
	}
	if err := h.db.SetSetting(sameModeKey, mode); err != nil {
		return err
	}
	if err := h.db.SetSetting(sameEntriesKey, strings.TrimSpace(entries)); err != nil {
		return err
	}
	if err := h.db.SetSetting(sameHostsKey, strings.TrimSpace(hosts)); err != nil {
		return err
	}
	h.sameMu.Lock()
	h.sameMode = mode
	h.sameEntries = splitProxyEntries(entries)
	h.sameHosts = splitProxyHosts(hosts)
	h.sameMu.Unlock()
	return nil
}

func (h *Handler) sameStreamConfig() (mode string, entries, hosts []string) {
	h.sameMu.RLock()
	defer h.sameMu.RUnlock()
	if h.sameMode == "" {
		return "auto", nil, nil
	}
	return h.sameMode, h.sameEntries, h.sameHosts
}

// sameStreamEntry 返回要给客户端用的反代入口：环境变量优先，其次面板。
func (h *Handler) sameStreamEntry() string {
	if h.cfg.InternalURL != "" {
		return h.cfg.InternalURL
	}
	_, entries, _ := h.sameStreamConfig()
	if len(entries) > 0 {
		return entries[0]
	}
	return ""
}

// noteRequestHosts 记住最近见过的 Host / X-Forwarded-Host，面板里给用户勾选。
// 只存 host，不存路径和查询串。
func (h *Handler) noteRequestHosts(r *http.Request) {
	now := time.Now()
	candidates := make([]string, 0, 2)
	if xfh := firstForwarded(r.Header.Get("X-Forwarded-Host")); xfh != "" {
		candidates = append(candidates, strings.ToLower(hostOnly(xfh)))
	}
	if h := strings.ToLower(hostOnly(r.Host)); h != "" {
		candidates = append(candidates, h)
	}

	h.hostSeenMu.Lock()
	defer h.hostSeenMu.Unlock()
	if h.hostSeen == nil {
		h.hostSeen = make(map[string]time.Time)
	}
	for _, c := range candidates {
		if c != "" {
			h.hostSeen[c] = now
		}
	}
	if len(h.hostSeen) <= 64 {
		return
	}
	// 超限时丢掉最旧的，避免长时间运行无限增长。
	type kv struct {
		host string
		at   time.Time
	}
	all := make([]kv, 0, len(h.hostSeen))
	for k, v := range h.hostSeen {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
	for _, item := range all[48:] {
		delete(h.hostSeen, item.host)
	}
}

// seenHosts 返回最近见过的私网入口和公网域名，按时间倒序。
func (h *Handler) seenHosts() (private, public []string) {
	h.hostSeenMu.Lock()
	defer h.hostSeenMu.Unlock()
	type kv struct {
		host string
		at   time.Time
	}
	all := make([]kv, 0, len(h.hostSeen))
	for k, v := range h.hostSeen {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
	for _, item := range all {
		if isPrivateHost(item.host) {
			private = append(private, item.host)
		} else {
			public = append(public, item.host)
		}
	}
	return private, public
}

func splitProxyEntries(raw string) []string {
	out := make([]string, 0, 4)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimRight(strings.TrimSpace(p), "/"); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitProxyHosts(raw string) []string {
	out := make([]string, 0, 4)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.ToLower(strings.TrimSpace(hostOnly(p))); p != "" {
			if i := strings.LastIndexByte(p, ':'); i >= 0 {
				p = p[:i]
			}
			out = append(out, p)
		}
	}
	return out
}

// hostInList 判断 host 是否命中域名列表（忽略端口）。
func hostInList(list []string, host string) bool {
	host = strings.ToLower(hostOnly(host))
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return false
	}
	for _, p := range list {
		if host == p {
			return true
		}
	}
	return false
}
