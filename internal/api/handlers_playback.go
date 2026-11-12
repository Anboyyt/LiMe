package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"lime/internal/config"
	"lime/internal/dto"
	"lime/internal/probe"
	"lime/internal/store"
)

// playbackInfo 返回可直连的 MediaSource。我们不转码，
// 所以客户端要么 302 直连，要么自己放弃播放。
func (h *Handler) playbackInfo(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	itemID := r.PathValue("itemId")
	it, err := h.db.GetItem(itemID)
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if !h.canAccessLibrary(user, it.LibraryID) || !ratingAllowed(user, it) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	sources, err := h.db.MediaSourcesByItem(itemID)
	if err != nil {
		h.log.Error("load media sources", "item", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if len(sources) == 0 {
		writeError(w, http.StatusNotFound, "no media source")
		return
	}

	// 首次播放时短暂等待 ffprobe，让客户端一次就拿到完整轨道信息。
	if h.prober.Enabled() {
		chans := make([]<-chan struct{}, 0, len(sources))
		for _, s := range sources {
			chans = append(chans, h.prober.Enqueue(s))
		}
		probe.WaitAll(chans, h.cfg.ProbeSyncWait)
	}

	// 客户端切换版本时会带 MediaSourceId，把指定版本放到第一位。
	var req struct {
		MediaSourceId string `json:"MediaSourceId"`
	}
	_ = decodeOptionalJSON(r, &req)
	wantSource := req.MediaSourceId
	if wantSource == "" {
		wantSource = queryAny(r, "MediaSourceId", "mediaSourceId")
	}

	out := make([]dto.MediaSourceDto, 0, len(sources))
	for _, s := range sources {
		if wantSource != "" && s.ID == wantSource {
			out = append([]dto.MediaSourceDto{h.playbackSourceDto(r, itemID, s, it.RunTimeTicks)}, out...)
			continue
		}
		out = append(out, h.playbackSourceDto(r, itemID, s, it.RunTimeTicks))
	}

	writeJSON(w, http.StatusOK, dto.PlaybackInfoResponse{
		MediaSources:  out,
		PlaySessionId: store.MustRandomHex(16),
	})
}

func (h *Handler) playbackSourceDto(r *http.Request, itemID string, s *store.MediaSource, runtimeTicks int64) dto.MediaSourceDto {
	d := h.mediaSourceDto(r, itemID, s, runtimeTicks)
	// PlaybackInfo 里 Path 指向本服务的 302 端点，客户端据此播放。
	d.Path = h.streamBase(r) + h.streamPath(itemID, s.ID)
	return d
}

func (h *Handler) videoStream(w http.ResponseWriter, r *http.Request) {
	h.serveStream(w, r)
}

func (h *Handler) audioStream(w http.ResponseWriter, r *http.Request) {
	h.serveStream(w, r)
}

// isStreamAction 接受 stream 以及 stream.mp4 / stream.mkv 这类变体。
func isStreamAction(r *http.Request) bool {
	action := r.PathValue("action")
	return action == "stream" || strings.HasPrefix(action, "stream.")
}

// serveStream 找到条目对应的网盘地址并 302。
// 客户端带上 Range 也没关系：会跟随 302 到 PanSave/CDN，由对方处理。
func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request) {
	if !isStreamAction(r) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var streamUser *store.User
	if h.cfg.StreamRequireAuth {
		u, ok := h.requireUser(w, r)
		if !ok {
			return
		}
		streamUser = u
	}

	itemID := r.PathValue("itemId")
	if streamUser != nil {
		if it, err := h.db.GetItem(itemID); err == nil &&
			(!h.canAccessLibrary(streamUser, it.LibraryID) || !ratingAllowed(streamUser, it)) {
			writeError(w, http.StatusNotFound, "item not found")
			return
		}
	}
	sources, err := h.db.MediaSourcesByItem(itemID)
	if err != nil {
		h.log.Error("load media sources", "item", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if len(sources) == 0 {
		writeError(w, http.StatusNotFound, "no media source")
		return
	}
	for _, s := range sources {
		h.prober.Enqueue(s)
	}

	src := pickMediaSource(sources, queryAny(r, "MediaSourceId", "mediaSourceId", "MediaSourceID"))
	if src == nil {
		writeError(w, http.StatusNotFound, "media source not found")
		return
	}
	if strings.TrimSpace(src.URL) == "" {
		writeError(w, http.StatusNotFound, "empty media url")
		return
	}

	location := h.streamLocation(r, src.URL)
	if h.cfg.ResolveRedirect {
		// 115 的直链签名和请求 UA 有关，必须透传客户端 UA，
		// 否则 PanSave 按 LiMe 的 UA 生成链接，播放器拿到会 403 invalid signature。
		if final, err := resolveRedirect(r.Context(), location, r.UserAgent()); err != nil {
			h.log.Warn("resolve redirect failed", "item", itemID, "err", err)
		} else if final != "" {
			location = safeLocation(final)
		}
	}
	h.log.Info("stream redirect",
		"item", itemID,
		"source", src.ID,
		"cloud", src.Cloud,
		"remote", r.RemoteAddr,
	)

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", location)
	// 不用 http.Redirect：它会为 GET 追加一段 HTML，客户端不需要。
	w.WriteHeader(http.StatusFound)
}

// redirectResolver 只跟一跳，用来把 PanSave 的 302 换成最终 CDN 地址。
// 不读取响应体，Range 只要 1 字节，避免碰视频数据。
var redirectResolver = &http.Client{
	Timeout: 8 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func resolveRedirect(ctx context.Context, raw, userAgent string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	if userAgent == "" {
		userAgent = "LiMe/" + config.DefaultVersion
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Range", "bytes=0-0")

	resp, err := redirectResolver.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", nil
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", nil
	}
	u, err := resp.Request.URL.Parse(loc)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func pickMediaSource(sources []*store.MediaSource, id string) *store.MediaSource {
	if id == "" {
		return sources[0]
	}
	for _, s := range sources {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// queryAny 按多个可能的键名取第一个非空值（客户端大小写并不统一）。
func queryAny(r *http.Request, keys ...string) string {
	q := r.URL.Query()
	for _, k := range keys {
		if v := q.Get(k); v != "" {
			return v
		}
	}
	return ""
}

type playbackEvent int

const (
	playbackStart playbackEvent = iota
	playbackProgress
	playbackStop
)

func (h *Handler) sessionPlaying(w http.ResponseWriter, r *http.Request) {
	h.saveProgress(w, r, playbackStart)
}

func (h *Handler) sessionProgress(w http.ResponseWriter, r *http.Request) {
	h.saveProgress(w, r, playbackProgress)
}

func (h *Handler) sessionStopped(w http.ResponseWriter, r *http.Request) {
	h.saveProgress(w, r, playbackStop)
}

// saveProgress 把上报的进度落库。M5 会基于 playstates 做续播。
func (h *Handler) saveProgress(w http.ResponseWriter, r *http.Request, event playbackEvent) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	var body dto.PlaybackProgressInfo
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	itemID := body.ItemId
	if itemID == "" {
		writeError(w, http.StatusBadRequest, "missing ItemId")
		return
	}
	it, err := h.db.GetItem(itemID)
	if err != nil {
		// 客户端可能缓存了已删除条目的 ID（例如删库/重扫后）。
		// 这里返回 204 而不是 404，避免播放器整页报「连接失败」。
		h.log.Warn("progress item not found", "item", itemID, "ua", r.UserAgent())
		w.WriteHeader(http.StatusNoContent)
		return
	}

	sessionID := body.PlaySessionId
	if sessionID == "" {
		sessionID = body.SessionId
	}
	if sessionID == "" {
		sessionID = store.MustRandomHex(16)
	}

	a := parseAuth(r)
	deviceID := body.DeviceId
	if deviceID == "" {
		deviceID = a.DeviceID
	}

	// 记录暂停/恢复需要先知道上一次的状态（upsert 会覆盖）。
	pausedBefore := false
	if event == playbackProgress {
		if v, found, err := h.db.PlaybackSessionPaused(sessionID); err == nil && found {
			pausedBefore = v
		}
	}

	played := false
	if event == playbackStop {
		played = h.isPlayed(itemID, body.PositionTicks)
		h.log.Info("playback stopped", "user", user.Name, "item", itemID,
			"position_ticks", body.PositionTicks, "paused", body.IsPaused,
			"play_session", sessionID)
		if err := h.db.DeletePlaybackSession(sessionID); err != nil {
			h.log.Warn("delete playback session", "id", sessionID, "err", err)
		}
	} else {
		if err := h.db.UpsertPlaybackSession(&store.PlaybackSession{
			ID:            sessionID,
			UserID:        user.ID,
			DeviceID:      deviceID,
			ItemID:        itemID,
			PositionTicks: body.PositionTicks,
			IsPaused:      body.IsPaused,
		}); err != nil {
			h.log.Warn("save playback session", "id", sessionID, "err", err)
		}
	}

	position := body.PositionTicks
	if event != playbackStart && position <= 0 {
		// 部分客户端暂停/停止上报时 PositionTicks 为 0，直接用会把已有的
		// 续播位置清零，回退到数据库里最后一次记录。
		// 开始播放时 0 是合法的（用户选择从头播放），不回退。
		if states, err := h.db.Playstates(user.ID, []string{itemID}); err == nil {
			if ps, ok := states[itemID]; ok && ps.PositionTicks > 0 {
				position = ps.PositionTicks
			}
		}
	}
	if played {
		// 看完就把进度清零，否则客户端会在「已看」的条目上画一半的进度条。
		position = 0
	}
	if err := h.db.UpsertPlaystate(user.ID, itemID, position, played); err != nil {
		h.log.Warn("save playstate", "item", itemID, "err", err)
	}

	// 开始播放某部剧的单集时，立即把该剧未探测的单集排进预热队列，
	// 下一集起播就不用再等 ffprobe。只在 start 事件触发，避免频繁重复。
	if event == playbackStart && it.Type == store.TypeEpisode && it.SeriesID != "" {
		h.prober.WarmSeries(it.SeriesID, 30)
	}

	// 播放开始/暂停/继续/停止写一条历史，保留一周。
	h.recordPlaybackHistory(user, a, deviceID, it, event, position, body.IsPaused, pausedBefore)

	// Telegram 播放通知（异步，不阻塞客户端上报）。
	h.notifyPlayback(user, a, sessionID, it, event, position, body.IsPaused, pausedBefore)

	// 播放开始/进度/暂停/停止后立刻让订阅者刷新一次，不必等下一个轮询点。
	h.sessions.broadcast()

	// Emby 风格的主动通知：客户端即使没订阅 Sessions 也能收到开始/停止。
	// 暂停没有独立事件，客户端靠 Sessions 快照里的 PlayState.IsPaused 判断。
	if event != playbackProgress {
		kind := "PlaybackStart"
		if event == playbackStop {
			kind = "PlaybackStopped"
		}
		h.sessions.broadcastMessage(wsMessage{
			MessageType: kind,
			MessageId:   store.MustRandomHex(16),
			Data: h.playbackSessionInfo(user, a, sessionID, deviceID, itemID,
				body.PositionTicks, body.IsPaused),
		}, user.ID)
	}

	w.WriteHeader(http.StatusNoContent)
}

// playbackSessionInfo 用上报内容构造单条会话信息，供 PlaybackStart/Stopped
// 主动消息使用。停止时会话记录已经删了，不能依赖 sessionSnapshot。
func (h *Handler) playbackSessionInfo(user *store.User, a authInfo, sessionID, deviceID, itemID string, ticks int64, paused bool) *dto.SessionInfoDto {
	info := &dto.SessionInfoDto{
		Id:                    sessionID,
		UserId:                user.ID,
		UserName:              user.Name,
		Client:                a.Client,
		DeviceName:            a.Device,
		DeviceId:              deviceID,
		LastActivityDate:      nowISO(),
		PlayableMediaTypes:    []string{"Video", "Audio"},
		SupportedCommands:     []string{},
		SupportsRemoteControl: false,
		IsActive:              true,
		PlayState: &dto.PlayerStateInfoDto{
			PositionTicks: ticks,
			CanSeek:       true,
			IsPaused:      paused,
			RepeatMode:    "RepeatNone",
		},
	}
	if it, err := h.db.GetItem(itemID); err == nil {
		info.NowPlayingItem = h.itemToDto(it, nil)
	}
	return info
}

// recordPlaybackHistory 只记状态变化的点，不记每 10 秒一次的进度心跳。
func (h *Handler) recordPlaybackHistory(user *store.User, a authInfo, deviceID string, it *store.Item, event playbackEvent, position int64, paused, pausedBefore bool) {
	var name string
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
			name = "unpause"
		}
	}
	if name == "" {
		return
	}

	err := h.db.AddPlaybackHistory(&store.PlaybackHistory{
		UserID:        user.ID,
		UserName:      user.Name,
		DeviceID:      deviceID,
		DeviceName:    a.Device,
		ItemID:        it.ID,
		ItemName:      it.Name,
		ItemType:      it.Type,
		Event:         name,
		PositionTicks: position,
	})
	if err != nil {
		h.log.Warn("save playback history", "err", err)
	}
}

// isPlayed 播放进度到 95% 视为看完。没有时长信息时只能靠客户端上报接近结尾。
func (h *Handler) isPlayed(itemID string, positionTicks int64) bool {
	it, err := h.db.GetItem(itemID)
	if err != nil || it.RunTimeTicks <= 0 {
		return false
	}
	return positionTicks >= it.RunTimeTicks*95/100
}

func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	out, err := h.sessionSnapshot(user)
	if err != nil {
		h.log.Error("list sessions", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// sessionSnapshot 构造会话快照，HTTP /Sessions 和 WebSocket 推送共用。
// user 为 nil 时不做过滤；非管理员只看自己的会话。
func (h *Handler) sessionSnapshot(user *store.User) ([]*dto.SessionInfoDto, error) {
	sessions, err := h.db.ListPlaybackSessions()
	if err != nil {
		return nil, err
	}
	if user != nil && !user.IsAdmin {
		mine := sessions[:0]
		for _, s := range sessions {
			if s.UserID == user.ID {
				mine = append(mine, s)
			}
		}
		sessions = mine
	}

	// 一次取完这些会话涉及的播放状态，避免每个会话查一次。
	itemsByUser := make(map[string][]string, len(sessions))
	for _, s := range sessions {
		itemsByUser[s.UserID] = append(itemsByUser[s.UserID], s.ItemID)
	}
	statesByUser := make(map[string]map[string]store.Playstate, len(itemsByUser))
	for uid, ids := range itemsByUser {
		states, err := h.db.Playstates(uid, ids)
		if err != nil {
			h.log.Warn("load playstates", "user", uid, "err", err)
			continue
		}
		statesByUser[uid] = states
	}

	out := make([]*dto.SessionInfoDto, 0, len(sessions))
	for _, s := range sessions {
		info := &dto.SessionInfoDto{
			Id:                    s.ID,
			UserId:                s.UserID,
			UserName:              s.UserName,
			Client:                s.Client,
			DeviceName:            s.DeviceName,
			DeviceId:              s.DeviceID,
			LastActivityDate:      s.UpdatedAt.UTC().Format(time.RFC3339Nano),
			PlayableMediaTypes:    []string{"Video", "Audio"},
			SupportedCommands:     []string{},
			SupportsRemoteControl: false,
			IsActive:              time.Since(s.UpdatedAt) < 30*time.Minute,
			PlayState: &dto.PlayerStateInfoDto{
				PositionTicks: s.PositionTicks,
				CanSeek:       true,
				IsPaused:      s.IsPaused,
				RepeatMode:    "RepeatNone",
			},
		}
		if it, err := h.db.GetItem(s.ItemID); err == nil {
			var ps *store.Playstate
			if state, ok := statesByUser[s.UserID][s.ItemID]; ok {
				ps = &state
			}
			info.NowPlayingItem = h.itemToDto(it, ps)
		}
		out = append(out, info)
	}
	return out, nil
}

// 客户端连接后会 POST 能力上报，我们不关心内容，返回 204 即可。
func (h *Handler) sessionCapabilities(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) sessionPing(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func decodeOptionalJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
