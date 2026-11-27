package api

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"lime/internal/store"
)

// VidHub 等客户端会缓存 /Shows/{id}/Episodes 响应，之后直接拿
// MediaSources[].DirectStreamUrl 起播，不一定再调 PlaybackInfo。
// 列表里的直链优先从缓存带出；没命中就后台预热，不阻塞列表响应。
type directURLsKey struct{}

func withDirectURLs(ctx context.Context, m map[string]string) context.Context {
	return context.WithValue(ctx, directURLsKey{}, m)
}

func directURLFor(ctx context.Context, sourceID string) string {
	m, _ := ctx.Value(directURLsKey{}).(map[string]string)
	return m[sourceID]
}

// warmGate 保证同一时间只有一批预热在跑，避免客户端狂翻页时堆出
// 几十个后台 goroutine 同时打 PanSave。
var warmGate = make(chan struct{}, 1)

// resolveDirectURLs 只把缓存命中的直链带进列表响应，未命中的片源
// 丢给后台预热。以前这里同步等 600ms，剧集页会固定被拖慢。
func (h *Handler) resolveDirectURLs(r *http.Request, items []*store.Item) map[string]string {
	if !h.cfg.PlaybackDirectURL || !h.cfg.ResolveRedirect || len(items) == 0 ||
		usesRelativeStreamURL(r) || !h.isDirectURLClient(r.UserAgent()) {
		return nil
	}
	ua := r.UserAgent()

	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	sourcesByItem, err := h.db.MediaSourcesByItems(ids)
	if err != nil {
		return nil
	}

	out := make(map[string]string, len(items))
	missing := make([]*store.MediaSource, 0, len(items))
	for _, it := range items {
		srcs := sourcesByItem[it.ID]
		if len(srcs) == 0 {
			continue
		}
		// 只解析第一个片源：客户端默认播的就是它，其余版本走 LiMe 302。
		src := srcs[0]
		if u, ok := lookupResolvedLink(src.URL, ua); ok {
			out[src.ID] = u
			continue
		}
		missing = append(missing, src)
	}
	if len(missing) > 0 {
		go h.warmDirectURLs(missing, ua)
	}
	return out
}

// warmDirectURLs 在后台把列表片源解析进缓存。用 resolveBounded 而不是
// resolveURL：批量任务的预算超时是设计内行为，不该计入熔断，否则一次
// 翻页就能把加速关掉五分钟。
func (h *Handler) warmDirectURLs(srcs []*store.MediaSource, ua string) {
	select {
	case warmGate <- struct{}{}:
	default:
		return // 已有预热在跑，这批等下次请求再补
	}
	defer func() { <-warmGate }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sem := make(chan struct{}, 8)
	var (
		wg     sync.WaitGroup
		failed atomic.Int32
	)
	for _, src := range srcs {
		wg.Add(1)
		sem <- struct{}{}
		go func(s *store.MediaSource) {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := h.resolveBounded(ctx, s.URL, ua, autoResolveTimeout); err != nil {
				failed.Add(1)
			}
		}(src)
	}
	wg.Wait()
	if n := failed.Load(); n > 0 {
		h.log.Debug("direct url warm done", "total", len(srcs), "failed", n)
	}
}
