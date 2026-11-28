package api

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	// auto 模式单次解析预算。PanSave 正常时几十毫秒就返回；
	// 慢或不可达时最多多等这么久，然后回退原 302。
	autoResolveTimeout = 500 * time.Millisecond
	// strict 模式也必须有上限。以前 strict 无限等，PanSave 抖动时
	// 起播会卡到 10 秒然后 502；现在 2 秒拿不到就回退原地址。
	strictResolveTimeout   = 2 * time.Second
	autoResolveMaxFailures = 3
	autoResolveBlock       = 5 * time.Minute
)

var errAutoResolveBlocked = errors.New("auto resolve circuit open")

// resolveURL 按配置解析一跳 PanSave → CDN。
//
// 两种模式都有单次预算，解析失败由调用方回退原始 302，不阻断播放：
// strict 用 2 秒且不熔断；auto 用 500ms，连续失败后熔断 5 分钟。
func (h *Handler) resolveURL(ctx context.Context, raw, ua string, budget time.Duration) (string, error) {
	if !h.resolveRedirect.Load() {
		return "", nil
	}
	if !h.resolveAuto.Load() {
		return h.resolveBounded(ctx, raw, ua, strictResolveTimeout)
	}
	if h.resolveBlocked() {
		return "", errAutoResolveBlocked
	}
	if budget <= 0 {
		budget = autoResolveTimeout
	}
	final, err := h.resolveBounded(ctx, raw, ua, budget)
	h.noteResolveResult(err)
	return final, err
}

// resolveBounded 是带硬预算的解析，不参与熔断计数。
// 列表页的批量预热也走这里：预算超时是设计内行为，不该被当成
// PanSave 故障把熔断打开。
func (h *Handler) resolveBounded(ctx context.Context, raw, ua string, budget time.Duration) (string, error) {
	if budget <= 0 {
		budget = autoResolveTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return cachedResolveRedirect(ctx, raw, ua)
}

func (h *Handler) resolveBlocked() bool {
	h.resolveMu.Lock()
	defer h.resolveMu.Unlock()
	return time.Now().Before(h.resolveBlockedUntil)
}

func (h *Handler) noteResolveResult(err error) {
	if err == nil {
		h.resolveMu.Lock()
		h.resolveFails = 0
		h.resolveBlockedUntil = time.Time{}
		h.resolveMu.Unlock()
		return
	}
	// 客户端自己取消（关播放页、切片）不算 PanSave 故障。
	if errors.Is(err, context.Canceled) {
		return
	}

	h.resolveMu.Lock()
	h.resolveFails++
	blocked := false
	if h.resolveFails >= autoResolveMaxFailures {
		h.resolveBlockedUntil = time.Now().Add(autoResolveBlock)
		h.resolveFails = 0
		blocked = true
	}
	h.resolveMu.Unlock()
	if blocked {
		h.log.Warn("auto resolve paused after repeated failures",
			"duration", autoResolveBlock.String())
	}
}

// defaultDirectURLClients 是已知会优先读 DirectStreamUrl 的客户端。
// 可用 LIME_DIRECT_URL_CLIENTS 覆盖，不必为新客户端改代码。
var defaultDirectURLClients = []string{"vidhub", "senplayer", "yamby", "infuse", "fileball"}

// isDirectURLClient 判断 UA 是否属于会优先读 DirectStreamUrl 的客户端。
func (h *Handler) isDirectURLClient(ua string) bool {
	names := h.cfg.DirectURLClients
	if len(names) == 0 {
		names = defaultDirectURLClients
	}
	ua = strings.ToLower(ua)
	for _, name := range names {
		if name != "" && strings.Contains(ua, name) {
			return true
		}
	}
	return false
}

func (h *Handler) directURLClientAllowed(ua string) bool {
	if !h.playbackDirectAt.Load() {
		return true
	}
	return h.isDirectURLClient(ua)
}

func playbackModeLabel(enabled, auto bool) string {
	if !enabled {
		return "off"
	}
	if auto {
		return "auto"
	}
	return "strict"
}
