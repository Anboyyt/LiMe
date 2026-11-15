package api

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"image"
	"image/jpeg"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"

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
	// 图片解码/缩放放到后台，不拖慢客户端的进度上报。
	go func() {
		msg := h.formatPlaybackMessage(name, user, a, it, position)
		photo, photoName := h.playbackPhoto(it)
		h.notifier.NotifyPhoto(name, msg, photo, photoName)
	}()
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

// playbackPhoto 返回通知配图：优先 16:9 背景图，没有背景图再用竖版海报。
// 剧集先取所属剧的背景图，单集的剧照只在没有背景图时兜底。
func (h *Handler) playbackPhoto(it *store.Item) ([]byte, string) {
	if it == nil {
		return nil, ""
	}
	var series *store.Item
	if it.Type == store.TypeEpisode && it.SeriesID != "" {
		if s, err := h.db.GetItem(it.SeriesID); err == nil {
			series = s
		}
	}
	rels := make([]string, 0, 4)
	seen := make(map[string]bool, 4)
	add := func(rel string) {
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		rels = append(rels, rel)
	}
	if series != nil {
		add(series.BackdropPath)
	}
	add(it.BackdropPath)
	add(it.ImagePath)
	if series != nil {
		add(series.ImagePath)
	}

	for _, rel := range rels {
		full, ok := h.resolveItemImagePath(rel)
		if !ok {
			continue
		}
		if data, name, err := telegramPhoto(full); err == nil {
			return data, name
		}
	}
	return nil, ""
}

// telegramPhoto 把本地图片压成适合 Telegram 的 JPEG：
// 最长边 1280，避免把几 MB 的原图直接上传。
func telegramPhoto(full string) ([]byte, string, error) {
	f, err := os.Open(full)
	if err != nil {
		return nil, "", err
	}
	img, _, decErr := image.Decode(f)
	_ = f.Close()
	if decErr != nil {
		// 解不了的格式（如 webp）直接发原文件，交给 Telegram 判断。
		ext := strings.ToLower(filepath.Ext(full))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
			data, rerr := os.ReadFile(full)
			if rerr == nil && len(data) > 0 && len(data) <= 5<<20 {
				return data, "cover" + ext, nil
			}
		}
		return nil, "", decErr
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, "", fmt.Errorf("empty image")
	}
	const maxSide = 1280
	scale := 1.0
	if w > maxSide || h > maxSide {
		scale = math.Min(float64(maxSide)/float64(w), float64(maxSide)/float64(h))
	}
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "cover.jpg", nil
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

// webIngestNotifyTest 用入库通知的 Bot 配置发一条样例消息。
// 用 SendNow 绕过事件开关，方便在正式启用前验证 Token / Chat ID。
func (h *Handler) webIngestNotifyTest(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if h.ingestNotifier == nil || !h.ingestNotifier.Configured() {
		writeError(w, http.StatusBadRequest, "请先填写入库通知 Bot Token 和 Chat ID")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := h.ingestNotifier.SendNow(ctx, ingestTestText()); err != nil {
		h.log.Warn("ingest telegram test failed", "err", err)
		writeError(w, http.StatusBadGateway, "发送失败："+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
