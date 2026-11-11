package api

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"lime/internal/notify"
	"lime/internal/store"
)

// tgConfig 合并环境变量与面板设置（面板优先）。
func (h *Handler) tgConfig() notify.Config {
	c := notify.Config{
		Token:   h.cfg.TelegramToken,
		ChatID:  h.cfg.TelegramChatID,
		Proxy:   h.cfg.TelegramProxy,
		Enabled: h.cfg.TelegramNotify,
		Events:  notify.ParseEvents(h.cfg.TelegramEvents),
	}
	if v, err := h.db.GetSetting("tg_bot_token"); err == nil && v != "" {
		c.Token = v
	}
	if v, err := h.db.GetSetting("tg_chat_id"); err == nil && v != "" {
		c.ChatID = v
	}
	if v, err := h.db.GetSetting("tg_proxy"); err == nil && v != "" {
		c.Proxy = v
	}
	if v, err := h.db.GetSetting("tg_enabled"); err == nil && v != "" {
		c.Enabled = v == "true" || v == "1"
	}
	if v, err := h.db.GetSetting("tg_events"); err == nil && v != "" {
		c.Events = notify.ParseEvents(v)
	}
	return c
}

// notifyPlayback 根据播放事件推一条 Telegram 消息。
// 暂停/继续靠 IsPaused 的变化判断，避免每个进度心跳都推。
func (h *Handler) notifyPlayback(user *store.User, a authInfo, sessionID string, it *store.Item, event playbackEvent, position int64, paused, pausedBefore bool) {
	if h.notifier == nil || it == nil {
		return
	}

	name := ""
	switch event {
	case playbackStart:
		name = "start"
	case playbackStop:
		name = "stop"
	case playbackProgress:
		if paused == pausedBefore {
			return
		}
		if paused {
			name = "pause"
		} else {
			name = "resume"
		}
	}
	if name == "" || !h.notifier.EventEnabled(name) {
		return
	}
	if !h.allowNotify(sessionID, name) {
		return
	}
	h.notifier.Notify(name, h.formatPlaybackMessage(name, user, a, it, position))
}

// allowNotify 同一播放会话的同一事件 45 秒内只推一次；
// 部分客户端恢复播放时会重复上报 Playing，靠这个去重。
func (h *Handler) allowNotify(sessionID, event string) bool {
	if sessionID == "" {
		return true
	}
	key := sessionID + ":" + event
	now := time.Now()
	h.notifyMu.Lock()
	defer h.notifyMu.Unlock()
	if h.notifySeen == nil {
		h.notifySeen = make(map[string]time.Time)
	}
	if last, ok := h.notifySeen[key]; ok && now.Sub(last) < 45*time.Second {
		return false
	}
	h.notifySeen[key] = now
	if len(h.notifySeen) > 512 {
		cutoff := now.Add(-10 * time.Minute)
		for k, v := range h.notifySeen {
			if v.Before(cutoff) {
				delete(h.notifySeen, k)
			}
		}
	}
	return true
}

var playbackNotifyTitles = map[string]string{
	"start":  "▶️ 开始播放",
	"pause":  "⏸ 暂停播放",
	"resume": "▶️ 继续播放",
	"stop":   "⏹ 停止播放",
}

func (h *Handler) formatPlaybackMessage(event string, user *store.User, a authInfo, it *store.Item, position int64) string {
	var b strings.Builder
	b.WriteString("<b>")
	b.WriteString(playbackNotifyTitles[event])
	b.WriteString("</b>\n")

	who := user.Name
	if a.Device != "" {
		who += " · " + a.Device
	}
	if a.Client != "" {
		who += " (" + a.Client + ")"
	}
	b.WriteString("👤 ")
	b.WriteString(html.EscapeString(who))
	b.WriteString("\n")

	b.WriteString(h.playbackTitleLine(it))
	b.WriteString("\n")

	if position > 0 || it.RunTimeTicks > 0 {
		b.WriteString("⏱ ")
		b.WriteString(fmtTicks(position))
		if it.RunTimeTicks > 0 {
			b.WriteString(" / ")
			b.WriteString(fmtTicks(it.RunTimeTicks))
			if position > 0 && position <= it.RunTimeTicks {
				fmt.Fprintf(&b, " (%d%%)", position*100/it.RunTimeTicks)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("🕐 ")
	b.WriteString(time.Now().Format("01-02 15:04"))
	return b.String()
}

// playbackTitleLine 电影显示「🎬 名称 (年份)」，剧集显示「📺 剧名 S01E02 · 集名」。
func (h *Handler) playbackTitleLine(it *store.Item) string {
	if it.Type != store.TypeEpisode {
		line := "🎬 <b>" + html.EscapeString(it.Name) + "</b>"
		if it.ProductionYear > 0 {
			line += fmt.Sprintf(" (%d)", it.ProductionYear)
		}
		return line
	}

	seriesName := ""
	if it.SeriesID != "" {
		if series, err := h.db.GetItem(it.SeriesID); err == nil {
			seriesName = series.Name
		}
	}
	label := ""
	if it.ParentIndexNumber > 0 && it.IndexNumber > 0 {
		label = fmt.Sprintf(" S%02dE%02d", it.ParentIndexNumber, it.IndexNumber)
	}
	if seriesName == "" {
		line := "📺 <b>" + html.EscapeString(it.Name) + "</b>"
		return line + label
	}
	line := "📺 <b>" + html.EscapeString(seriesName) + "</b>" + label
	// 集名是「第 N 集」时不再重复显示。
	if it.Name != "" && it.Name != fmt.Sprintf("第 %d 集", it.IndexNumber) {
		line += " · " + html.EscapeString(it.Name)
	}
	return line
}

// fmtTicks 把 100ns ticks 格式化成 mm:ss 或 h:mm:ss。
func fmtTicks(ticks int64) string {
	if ticks <= 0 {
		return "0:00"
	}
	sec := ticks / 10_000_000
	h, m, s := sec/3600, (sec%3600)/60, sec%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// webNotifyTest 发送一条测试消息，验证 Token/ChatID/代理是否可用。
func (h *Handler) webNotifyTest(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if h.notifier == nil || !h.notifier.Configured() {
		writeError(w, http.StatusBadRequest, "请先填写 Bot Token 和 Chat ID")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	msg := "🤖 <b>LiMe</b>\nTelegram 通知测试成功\n🕐 " + time.Now().Format("01-02 15:04")
	if err := h.notifier.SendNow(ctx, msg); err != nil {
		h.log.Warn("telegram test failed", "err", err)
		writeError(w, http.StatusBadGateway, "发送失败："+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
