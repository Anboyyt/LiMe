package api

import (
	"time"

	"lime/internal/store"
)

// playSessionTTL 内同一设备 + 同一视频复用同一个 PlaySessionId。
// 时间太短解决不了「退出马上重进」，太长会让真正的重新播放继承旧会话。
const playSessionTTL = 10 * time.Minute

type playSessionEntry struct {
	id string
	at time.Time
}

// playbackSessionID 返回可复用的播放会话 ID。
//
// PanSave 的 115 复制同播按播放会话分配副本：客户端退出再进时如果拿到
// 新的 PlaySessionId，它就会再复制一份文件（实测连续触发 4 次，每次
// 1 秒左右），副本创建期间客户端往往已经超时报错。同一设备 10 分钟内
// 重进同一视频复用旧会话，直接命中已有副本。
func (h *Handler) playbackSessionID(deviceID, itemID string) string {
	if deviceID == "" || itemID == "" {
		return store.MustRandomHex(16)
	}
	key := deviceID + "|" + itemID

	h.playSessionMu.Lock()
	defer h.playSessionMu.Unlock()
	if h.playSessions == nil {
		h.playSessions = make(map[string]playSessionEntry, 16)
	}
	now := time.Now()
	if e, ok := h.playSessions[key]; ok && now.Sub(e.at) < playSessionTTL {
		e.at = now
		h.playSessions[key] = e
		return e.id
	}
	// 容量兜底：顺手清掉过期项，避免长期运行越积越多。
	if len(h.playSessions) >= 256 {
		for k, v := range h.playSessions {
			if now.Sub(v.at) >= playSessionTTL {
				delete(h.playSessions, k)
			}
		}
	}
	id := store.MustRandomHex(16)
	h.playSessions[key] = playSessionEntry{id: id, at: now}
	return id
}
