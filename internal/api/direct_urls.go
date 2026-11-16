package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"lime/internal/store"
)

// VidHub 等客户端会缓存 /Shows/{id}/Episodes 响应，之后直接拿
// MediaSources[].DirectStreamUrl 起播，不一定再调 PlaybackInfo。
// 所以列表里的直链也要在服务端预解析，否则每次探测还要经 CF 回 LiMe 跳一次。
type directURLsKey struct{}

func withDirectURLs(ctx context.Context, m map[string]string) context.Context {
	return context.WithValue(ctx, directURLsKey{}, m)
}

func directURLFor(ctx context.Context, sourceID string) string {
	m, _ := ctx.Value(directURLsKey{}).(map[string]string)
	return m[sourceID]
}

// resolveDirectURLs 预解析一页条目每个片源的 CDN 直链。
//
// 只取每个条目的第一个片源（客户端默认播这个），最多等 600ms；
// 超时/失败的条目保持 LiMe 地址，不影响列表可用性。解析结果按
// (URL, UA) 缓存十分钟，重复打开同一部剧几乎不再增加耗时。
func (h *Handler) resolveDirectURLs(r *http.Request, items []*store.Item) map[string]string {
	if !h.cfg.PlaybackDirectURL || !h.cfg.ResolveRedirect || len(items) == 0 ||
		usesRelativeStreamURL(r) {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	sourcesByItem, err := h.db.MediaSourcesByItems(ids)
	if err != nil {
		return nil
	}

	jobs := make(chan *store.MediaSource, len(items))
	for _, it := range items {
		if srcs := sourcesByItem[it.ID]; len(srcs) > 0 {
			jobs <- srcs[0]
		}
	}
	close(jobs)
	if len(jobs) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(r.Context(), 600*time.Millisecond)
	defer cancel()

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = make(map[string]string, len(items))
		sem = make(chan struct{}, 8)
	)
	for src := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(s *store.MediaSource) {
			defer wg.Done()
			defer func() { <-sem }()
			final, err := cachedResolveRedirect(ctx, s.URL, r.UserAgent())
			if err == nil && final != "" {
				mu.Lock()
				out[s.ID] = safeLocation(final)
				mu.Unlock()
			}
		}(src)
	}
	wg.Wait()
	return out
}
