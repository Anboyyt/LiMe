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
	autoResolveTimeout     = 500 * time.Millisecond
	autoResolveMaxFailures = 3
	autoResolveBlock       = 5 * time.Minute
)

var errAutoResolveBlocked = errors.New("auto resolve circuit open")

// resolveURL 按配置解析一跳 PanSave → CDN。
//
// 显式 true：保持原有严格行为，超时/错误交给调用方决定是否报错。
// auto：短超时 + 失败回退，并带熔断；连续失败后暂时全部走原地址。
func (h *Handler) resolveURL(ctx context.Context, raw, ua string, budget time.Duration) (string, error) {
	if !h.cfg.ResolveRedirect {
		return "", nil
	}
	if !h.cfg.ResolveRedirectAuto {
		return cachedResolveRedirect(ctx, raw, ua)
	}
	if h.resolveBlocked() {
		return "", errAutoResolveBlocked
	}
	if budget <= 0 {
		budget = autoResolveTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	final, err := cachedResolveRedirect(ctx, raw, ua)
	h.noteResolveResult(err)
	return final, err
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

// usesDirectStreamURLClient 判断 UA 是否属于会优先读 DirectStreamUrl 的客户端。
// auto 模式只给这些客户端预解析列表，避免拖慢其它客户端。
func usesDirectStreamURLClient(ua string) bool {
	ua = strings.ToLower(ua)
	for _, name := range []string{"vidhub", "senplayer", "yamby", "infuse", "fileball"} {
		if strings.Contains(ua, name) {
			return true
		}
	}
	return false
}

func (h *Handler) directURLClientAllowed(ua string) bool {
	if !h.cfg.PlaybackDirectURLAuto {
		return true
	}
	return usesDirectStreamURLClient(ua)
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
