package api

import (
	"time"
)

// PlaybackInfo 响应缓存时间。太长会让换片源/换版本后客户端拿旧地址，
// 30 秒足够覆盖客户端起播前的连续探测。
const playbackInfoCacheTTL = 30 * time.Second

const playbackInfoCacheMax = 256

type pbCacheEntry struct {
	body []byte
	at   time.Time
}

func (h *Handler) playbackInfoCacheGet(key string) ([]byte, bool) {
	h.pbCacheMu.Lock()
	defer h.pbCacheMu.Unlock()
	e, ok := h.pbCache[key]
	if !ok {
		return nil, false
	}
	if time.Since(e.at) > playbackInfoCacheTTL {
		delete(h.pbCache, key)
		return nil, false
	}
	return e.body, true
}

func (h *Handler) playbackInfoCachePut(key string, body []byte) {
	h.pbCacheMu.Lock()
	defer h.pbCacheMu.Unlock()
	if h.pbCache == nil {
		h.pbCache = make(map[string]pbCacheEntry, 64)
	}
	now := time.Now()
	if len(h.pbCache) >= playbackInfoCacheMax {
		for k, v := range h.pbCache {
			if now.Sub(v.at) > playbackInfoCacheTTL {
				delete(h.pbCache, k)
			}
		}
		if len(h.pbCache) >= playbackInfoCacheMax {
			// 极端情况下直接清空，比无限增长安全。
			h.pbCache = make(map[string]pbCacheEntry, 64)
		}
	}
	h.pbCache[key] = pbCacheEntry{body: body, at: now}
}
