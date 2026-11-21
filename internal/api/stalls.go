package api

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lime/internal/store"
)

const (
	stallAlertKey    = "stall_alert_seconds"
	stallCooldownKey = "stall_cooldown_seconds"
)

// loadStallSettings 面板设置优先于环境变量。
func (h *Handler) loadStallSettings() {
	after := h.cfg.StallAlertAfter
	cooldown := h.cfg.StallAlertCooldown
	if after <= 0 {
		after = 2 * time.Minute
	}
	if cooldown <= 0 {
		cooldown = 10 * time.Minute
	}
	if v, err := h.db.GetSetting(stallAlertKey); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			after = time.Duration(n) * time.Second
		}
	}
	if v, err := h.db.GetSetting(stallCooldownKey); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			cooldown = time.Duration(n) * time.Second
		}
	}
	h.stallCfgMu.Lock()
	h.stallAfter = after
	h.stallCooldown = cooldown
	h.stallCfgMu.Unlock()
}

func (h *Handler) stallSettings() (time.Duration, time.Duration) {
	h.stallCfgMu.RLock()
	after, cooldown := h.stallAfter, h.stallCooldown
	h.stallCfgMu.RUnlock()
	if after <= 0 {
		after = h.cfg.StallAlertAfter
	}
	if cooldown <= 0 {
		cooldown = h.cfg.StallAlertCooldown
	}
	if after <= 0 {
		after = 2 * time.Minute
	}
	if cooldown <= 0 {
		cooldown = 10 * time.Minute
	}
	return after, cooldown
}

// SetStallSettings 保存卡顿阈值，从下一次进度上报开始生效。
func (h *Handler) SetStallSettings(after, cooldown time.Duration) error {
	if after < 10*time.Second || after > time.Hour {
		return fmt.Errorf("卡顿阈值应在 10 秒到 1 小时之间")
	}
	if cooldown < 30*time.Second || cooldown > 24*time.Hour {
		return fmt.Errorf("告警冷却应在 30 秒到 24 小时之间")
	}
	if err := h.db.SetSetting(stallAlertKey, strconv.Itoa(int(after.Seconds()))); err != nil {
		return err
	}
	if err := h.db.SetSetting(stallCooldownKey, strconv.Itoa(int(cooldown.Seconds()))); err != nil {
		return err
	}
	h.stallCfgMu.Lock()
	h.stallAfter = after
	h.stallCooldown = cooldown
	h.stallCfgMu.Unlock()
	return nil
}

// stallState 是单个播放会话的卡顿监控状态。
type stallState struct {
	lastPos     int64
	lastAdvance time.Time
	lastSeen    time.Time
	paused      bool
	lastAlert   time.Time
	alerting    bool
	alertAt     time.Time
	userID      string
	userName    string
	device      string
	client      string
	itemID      string
	sourceID    string
	sessionID   string
	deviceID    string
}

// stallSilenceReset 是「这中间没收到进度上报」的静默阈值。
// 暂停、切后台、系统挂起时客户端会停止心跳；恢复后的第一条上报
// 位置没变，不能把整段静默算成卡顿，否则暂停超过 2 分钟就会误报。
const stallSilenceReset = 60 * time.Second

// trackStall 在进度上报时判断「未暂停但进度不前进」。
// 只依赖客户端自己的进度心跳：服务端只发 302，看不到 CDN 字节，
// 但客户端卡住时上报的位置会停住，足够定位问题。
func (h *Handler) trackStall(user *store.User, a authInfo, sessionID string, it *store.Item, sourceID string, position int64, paused bool) {
	after, cooldown := h.stallSettings()
	if after <= 0 || sessionID == "" || it == nil {
		return
	}
	if cooldown <= 0 {
		cooldown = 10 * time.Minute
	}

	now := time.Now()
	h.stallMu.Lock()
	if h.stallWatch == nil {
		h.stallWatch = make(map[string]*stallState)
	}
	st, ok := h.stallWatch[sessionID]
	if !ok {
		st = &stallState{lastAdvance: now, lastSeen: now, lastPos: position}
		h.stallWatch[sessionID] = st
		if len(h.stallWatch) > 1024 {
			cutoff := now.Add(-2 * time.Hour)
			for k, v := range h.stallWatch {
				if v.lastAdvance.Before(cutoff) {
					delete(h.stallWatch, k)
				}
			}
		}
	}
	st.userID = user.ID
	st.userName = user.Name
	st.device = a.Device
	st.client = a.Client
	st.deviceID = a.DeviceID
	st.itemID = it.ID
	st.sessionID = sessionID
	if sourceID != "" {
		st.sourceID = sourceID
	}

	gap := now.Sub(st.lastSeen)
	if st.lastSeen.IsZero() {
		gap = 0
	}
	st.lastSeen = now

	delta := position - st.lastPos
	moved := delta >= 10_000_000 || delta <= -10_000_000 // 前进或拖动超过 1 秒

	if paused {
		st.paused = true
		st.lastPos = position
		st.lastAdvance = now
		recovered, alertAt := st.alerting, st.alertAt
		st.alerting = false
		h.stallMu.Unlock()
		if recovered {
			go h.recoverStall(sessionID, alertAt)
		}
		return
	}

	// 长时间没有进度上报：暂停、切后台或系统挂起都会这样。恢复后的
	// 第一条上报位置通常没动，不能把整段静默算成卡顿。
	if gap >= stallSilenceReset {
		st.lastPos = position
		st.lastAdvance = now
		recovered, alertAt := st.alerting, st.alertAt
		st.alerting = false
		h.stallMu.Unlock()
		if recovered {
			go h.recoverStall(sessionID, alertAt)
		}
		return
	}

	// 之前明确暂停过，但后续心跳没带 IsPaused：位置一直没动就继续
	// 按暂停处理，避免客户端只在暂停那一刻上报一次状态时误报。
	if st.paused {
		if !moved {
			st.lastAdvance = now
			h.stallMu.Unlock()
			return
		}
		st.paused = false
	}

	if moved {
		st.lastPos = position
		st.lastAdvance = now
		recovered, alertAt := st.alerting, st.alertAt
		st.alerting = false
		h.stallMu.Unlock()
		if recovered {
			go h.recoverStall(sessionID, alertAt)
		}
		return
	}

	if st.alerting || now.Sub(st.lastAdvance) < after {
		h.stallMu.Unlock()
		return
	}
	if !st.lastAlert.IsZero() && now.Sub(st.lastAlert) < cooldown {
		h.stallMu.Unlock()
		return
	}
	st.alerting = true
	st.alertAt = now
	st.lastAlert = now
	stalled := now.Sub(st.lastAdvance)

	snapshot := *st
	h.stallMu.Unlock()

	go h.sendStallAlert(user, snapshot, it, position, stalled)
}

// stopStall 在播放停止时清掉状态；如果还在告警中，把记录标记为已结束。
func (h *Handler) stopStall(sessionID string) {
	if sessionID == "" {
		return
	}
	h.stallMu.Lock()
	st := h.stallWatch[sessionID]
	alerting, alertAt := false, time.Time{}
	if st != nil {
		alerting, alertAt = st.alerting, st.alertAt
		delete(h.stallWatch, sessionID)
	}
	h.stallMu.Unlock()
	if alerting {
		go h.recoverStall(sessionID, alertAt)
	}
}

func (h *Handler) recoverStall(sessionID string, alertAt time.Time) {
	sec := int64(0)
	if !alertAt.IsZero() {
		sec = int64(time.Since(alertAt).Seconds())
	}
	if err := h.db.RecoverPlaybackStall(sessionID, sec, time.Now().Unix()); err != nil {
		h.log.Warn("recover playback stall", "session", sessionID, "err", err)
	}
}

func (h *Handler) sendStallAlert(user *store.User, st stallState, it *store.Item, position int64, stalled time.Duration) {
	if user == nil || it == nil {
		return
	}
	h.log.Warn("playback stall", "user", user.Name, "item", it.ID,
		"session", st.sessionID, "stalled", stalled.Round(time.Second).String())

	row := &store.PlaybackStall{
		UserID:        user.ID,
		UserName:      user.Name,
		DeviceID:      st.deviceID,
		DeviceName:    st.device,
		Client:        st.client,
		SessionID:     st.sessionID,
		ItemID:        it.ID,
		ItemName:      it.Name,
		ItemType:      it.Type,
		SourceID:      st.sourceID,
		PositionTicks: position,
		CreatedAt:     st.alertAt.Unix(),
	}
	if err := h.db.InsertPlaybackStall(row); err != nil {
		h.log.Warn("save playback stall", "err", err)
	}

	if h.notifier == nil || !h.notifier.EventEnabled("stall") {
		return
	}
	msg := h.formatStallMessage(user, st, it, position, stalled)
	photo, name := h.playbackPhoto(it)
	h.notifier.NotifyPhoto("stall", msg, photo, name)
}

func (h *Handler) formatStallMessage(user *store.User, st stallState, it *store.Item, position int64, stalled time.Duration) string {
	var b strings.Builder
	b.WriteString("<b>⚠️ 播放卡顿</b>\n")

	who := user.Name
	if st.device != "" {
		who += " · " + st.device
	}
	if st.client != "" {
		who += " (" + st.client + ")"
	}
	b.WriteString("👤 ")
	b.WriteString(html.EscapeString(who))
	b.WriteString("\n")
	b.WriteString(h.playbackTitleLine(it))
	b.WriteString("\n")

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

	b.WriteString("🐢 进度 ")
	b.WriteString(fmtDur(stalled))
	b.WriteString(" 没有前进\n")

	if label := h.sourceLabel(it.ID, st.sourceID); label != "" {
		b.WriteString("📡 ")
		b.WriteString(html.EscapeString(label))
		b.WriteString("\n")
	}
	b.WriteString("🕐 ")
	b.WriteString(time.Now().Format("01-02 15:04"))
	return b.String()
}

func (h *Handler) sourceLabel(itemID, sourceID string) string {
	sources, err := h.db.MediaSourcesByItem(itemID)
	if err != nil || len(sources) == 0 {
		return ""
	}
	var s *store.MediaSource
	for _, src := range sources {
		if src.ID == sourceID {
			s = src
			break
		}
	}
	if s == nil {
		s = sources[0]
	}
	parts := make([]string, 0, 3)
	if s.Cloud != "" {
		parts = append(parts, strings.ToUpper(s.Cloud))
	}
	if s.VersionName != "" {
		parts = append(parts, s.VersionName)
	} else if s.FileName != "" {
		parts = append(parts, s.FileName)
	}
	if s.Container != "" {
		parts = append(parts, strings.ToUpper(s.Container))
	}
	return strings.Join(parts, " · ")
}

func fmtDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0f 秒", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m < 60 {
		if s == 0 {
			return fmt.Sprintf("%d 分钟", m)
		}
		return fmt.Sprintf("%d 分 %d 秒", m, s)
	}
	return fmt.Sprintf("%d 小时 %d 分", m/60, m%60)
}

// webStalls 返回最近的卡顿记录，供面板/后续分析使用。
func (h *Handler) webStalls(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	days := atoiDefault(r.URL.Query().Get("days"), 7)
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 200)
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	rows, err := h.db.ListPlaybackStalls(since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Days":  days,
		"Total": len(rows),
		"Items": rows,
	})
}
