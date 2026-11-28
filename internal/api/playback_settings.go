package api

import (
	"strings"
)

// 播放开关的面板配置。三态用 auto / on / off 表示，对应原来的
// auto（失败回退）/ 强制 / 关闭；面板存过以后优先于环境变量。
const (
	resolveModeKey = "playback_resolve_mode"
	directModeKey  = "playback_direct_mode"
	streamAuthKey  = "playback_stream_auth"
)

// loadPlaybackSettings 启动时把面板值应用到运行时。
func (h *Handler) loadPlaybackSettings() {
	if v, _ := h.db.GetSetting(resolveModeKey); v != "" {
		h.applyResolveMode(v)
	}
	if v, _ := h.db.GetSetting(directModeKey); v != "" {
		h.applyDirectMode(v)
	}
	if v, _ := h.db.GetSetting(streamAuthKey); v != "" {
		h.streamAuth.Store(v == "1" || strings.EqualFold(v, "true"))
	}
}

func (h *Handler) applyResolveMode(mode string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on", "strict", "true":
		h.resolveRedirect.Store(true)
		h.resolveAuto.Store(false)
	case "off", "false":
		h.resolveRedirect.Store(false)
		h.resolveAuto.Store(false)
	default: // auto
		h.resolveRedirect.Store(true)
		h.resolveAuto.Store(true)
	}
}

func (h *Handler) applyDirectMode(mode string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on", "strict", "true":
		h.playbackDirect.Store(true)
		h.playbackDirectAt.Store(false)
	case "off", "false":
		h.playbackDirect.Store(false)
		h.playbackDirectAt.Store(false)
	default:
		h.playbackDirect.Store(true)
		h.playbackDirectAt.Store(true)
	}
}

// savePlaybackSettings 保存面板改动并立即生效。
func (h *Handler) savePlaybackSettings(resolveMode, directMode, streamAuth string) error {
	resolveMode = normalizePlaybackMode(resolveMode)
	directMode = normalizePlaybackMode(directMode)
	streamAuth = strings.ToLower(strings.TrimSpace(streamAuth))
	if streamAuth == "" {
		streamAuth = "0"
	}
	if err := h.db.SetSetting(resolveModeKey, resolveMode); err != nil {
		return err
	}
	if err := h.db.SetSetting(directModeKey, directMode); err != nil {
		return err
	}
	if err := h.db.SetSetting(streamAuthKey, streamAuth); err != nil {
		return err
	}
	h.applyResolveMode(resolveMode)
	h.applyDirectMode(directMode)
	h.streamAuth.Store(streamAuth == "1" || streamAuth == "true")
	return nil
}

func normalizePlaybackMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on", "strict", "true":
		return "on"
	case "off", "false":
		return "off"
	default:
		return "auto"
	}
}

// resolveModeLabel 返回面板/环境变量当前生效的模式，用于展示。
func (h *Handler) resolveModeLabel() string {
	if !h.resolveRedirect.Load() {
		return "off"
	}
	if h.resolveAuto.Load() {
		return "auto"
	}
	return "strict"
}

func (h *Handler) directModeLabel() string {
	if !h.resolveRedirect.Load() || !h.playbackDirect.Load() {
		return "off"
	}
	if h.playbackDirectAt.Load() {
		return "auto"
	}
	return "strict"
}
