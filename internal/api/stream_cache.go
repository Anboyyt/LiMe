package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lime/internal/store"
)

// 代理播放器（目前是 Yamby）起播前会反复读文件头/尾：一个 60GB 的
// 4K MKV 实测第一轮就要读近 30MB。每次从 115 CDN 现拉都很慢，所以
// 在 PlaybackInfo 时后台预取头部/尾部到本地，代理请求优先从磁盘回。
//
// 这是「整段响应」缓存，不改变 206 语义：命中头部时先发磁盘里的字节，
// 后面的部分继续从 CDN 透传，Content-Length/Content-Range 仍是完整的。
const (
	streamCacheTailBytes = 2 << 20 // 尾部固定 2MB
)

func (h *Handler) streamCacheHeadBytes() int64 {
	if h.cfg.StreamCacheMB <= 0 {
		return 0
	}
	return int64(h.cfg.StreamCacheMB) << 20
}

func (h *Handler) streamCacheDir() string {
	return filepath.Join(h.cfg.DataDir, "stream-cache")
}

func (h *Handler) streamCachePath(src *store.MediaSource, tail bool) string {
	kind := "head"
	if tail {
		kind = "tail"
	}
	return filepath.Join(h.streamCacheDir(),
		fmt.Sprintf("%s-%d.%s", src.ID, src.Size, kind))
}

func cacheFileReady(path string, want int64) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() == want
}

// warmStreamCache 在 PlaybackInfo 时异步把头部/尾部拉到本地。
// 同一个片源同时只跑一次；失败静默，下一次播放再试。
func (h *Handler) warmStreamCache(r *http.Request, src *store.MediaSource, location string) {
	headLen := h.streamCacheHeadBytes()
	if headLen <= 0 || src.Size <= 0 {
		return
	}
	if headLen > src.Size {
		headLen = src.Size
	}
	tailLen := int64(streamCacheTailBytes)
	if tailLen > src.Size {
		tailLen = src.Size
	}
	headPath := h.streamCachePath(src, false)
	tailPath := h.streamCachePath(src, true)
	if cacheFileReady(headPath, headLen) && cacheFileReady(tailPath, tailLen) {
		return
	}

	key := fmt.Sprintf("%s|%d", src.ID, src.Size)
	cacheFillMu.Lock()
	if cacheFilling[key] {
		cacheFillMu.Unlock()
		return
	}
	cacheFilling[key] = true
	cacheFillMu.Unlock()

	ua := r.UserAgent()
	go func() {
		defer func() {
			cacheFillMu.Lock()
			delete(cacheFilling, key)
			cacheFillMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		final := location
		if h.cfg.ResolveRedirect {
			if u, err := cachedResolveRedirect(ctx, location, ua); err == nil && u != "" {
				final = u
			}
		}
		// 尾部先取：它只要 2MB，第一轮探测马上就会用上。
		if err := h.fillCacheFile(ctx, final, ua, src.Size-tailLen, src.Size-1, tailPath); err != nil {
			h.log.Debug("stream cache tail", "source", src.ID, "err", err)
		}
		if err := h.fillCacheFile(ctx, final, ua, 0, headLen-1, headPath); err != nil {
			h.log.Debug("stream cache head", "source", src.ID, "err", err)
		}
		h.pruneStreamCache()
	}()
}

var (
	cacheFillMu  sync.Mutex
	cacheFilling = map[string]bool{}
)

func (h *Handler) fillCacheFile(ctx context.Context, raw, ua string, from, to int64, path string) error {
	if cacheFileReady(path, to-from+1) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))

	resp, err := streamProxyClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 尾部请求必须拿到 206。上游忽略 Range 返回 200 时说明它要吐整个文件，
	// 绝不能整份写进缓存目录（几十 GB 的片子会直接撑爆数据盘）。
	if from > 0 && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("range not honored: status %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	// 206 的起始偏移必须是我们要的那段，跳转链/多段后端可能回错范围。
	if resp.StatusCode == http.StatusPartialContent {
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			var gotStart int64
			if _, err := fmt.Sscanf(cr, "bytes %d-", &gotStart); err == nil && gotStart != from {
				return fmt.Errorf("content-range start %d, want %d", gotStart, from)
			}
		}
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	want := to - from + 1
	// 多读 1 字节就够判定「上游给多了」，不用把整个文件读完再报错。
	n, cerr := io.Copy(f, io.LimitReader(resp.Body, want+1))
	if err := f.Close(); err != nil && cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = os.Remove(tmp)
		return cerr
	}
	if n != want {
		_ = os.Remove(tmp)
		return fmt.Errorf("short read %d/%d", n, want)
	}
	return os.Rename(tmp, path)
}

// pruneStreamCache 按总大小做简单 LRU：超过上限时删最旧的文件。
func (h *Handler) pruneStreamCache() {
	limit := int64(h.cfg.StreamCacheMaxMB) << 20
	if limit <= 0 {
		limit = 4 << 30
	}
	dir := h.streamCacheDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fileInfo struct {
		path string
		size int64
		mod  time.Time
	}
	files := make([]fileInfo, 0, len(entries))
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= limit {
		return
	}
	// 旧文件先删。
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= limit {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

// tryServeStreamCache 在 Range 请求命中头部/尾部缓存时直接回本地字节。
// 头部命中但请求范围更靠后时，先发缓存，再继续从 CDN 透传剩余部分。
func (h *Handler) tryServeStreamCache(w http.ResponseWriter, r *http.Request, src *store.MediaSource) bool {
	headLen := h.streamCacheHeadBytes()
	if headLen <= 0 || src.Size <= 0 {
		return false
	}
	start, end, ok := parseStreamRange(r.Header.Get("Range"), src.Size)
	if !ok {
		return false
	}
	if headLen > src.Size {
		headLen = src.Size
	}
	tailLen := int64(streamCacheTailBytes)
	if tailLen > src.Size {
		tailLen = src.Size
	}

	tailPath := h.streamCachePath(src, true)
	headPath := h.streamCachePath(src, false)

	// 尾部：范围完全落在 tail 缓存内。
	if start >= src.Size-tailLen && cacheFileReady(tailPath, tailLen) {
		off := start - (src.Size - tailLen)
		if end > src.Size-1 {
			end = src.Size - 1
		}
		return h.serveCacheBytes(w, r, src, tailPath, off, end-start+1, start, end)
	}

	if start >= headLen || !cacheFileReady(headPath, headLen) {
		return false
	}
	if end > src.Size-1 {
		end = src.Size - 1
	}
	if end < start {
		return false
	}

	// 先发头部缓存。文件必须在写响应头之前打开，避免 open 失败后
	// 已经写了 206 又回退到代理，响应会错乱。
	f, err := os.Open(headPath)
	if err != nil {
		return false
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		_ = f.Close()
		return false
	}
	cachedEnd := end
	if cachedEnd > headLen-1 {
		cachedEnd = headLen - 1
	}
	n := cachedEnd - start + 1
	h.setCacheHeaders(w, src, start, end)
	w.WriteHeader(http.StatusPartialContent)
	_, _ = io.CopyN(w, f, n)
	_ = f.Close()
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if end <= headLen-1 {
		return true
	}

	// 剩余部分继续从 CDN 透传，保持完整的 Content-Length。
	location := h.streamLocation(r, src.URL)
	final := location
	if h.cfg.ResolveRedirect {
		if u, err := cachedResolveRedirect(r.Context(), location, r.UserAgent()); err == nil && u != "" {
			final = u
		}
	}
	if err := h.copyUpstreamRange(r.Context(), w, final, r.UserAgent(), headLen, end); err != nil {
		h.log.Warn("stream cache continue", "source", src.ID, "err", err)
	}
	return true
}

func (h *Handler) serveCacheBytes(w http.ResponseWriter, r *http.Request, src *store.MediaSource, path string, off, n, start, end int64) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return false
	}
	h.setCacheHeaders(w, src, start, end)
	w.WriteHeader(http.StatusPartialContent)
	if _, err := io.CopyN(w, f, n); err != nil {
		h.log.Debug("stream cache send", "source", src.ID, "err", err)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return true
}

func (h *Handler) setCacheHeaders(w http.ResponseWriter, src *store.MediaSource, start, end int64) {
	if ct := contentTypeForContainer(src.Container); ct != "" && ct != "application/octet-stream" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, src.Size))
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	w.Header().Set("Cache-Control", "no-store")
}

func (h *Handler) copyUpstreamRange(ctx context.Context, w io.Writer, raw, ua string, from, to int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
	resp, err := streamProxyClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// parseStreamRange 解析单段 Range（多段直接不支持，交给 CDN）。
func parseStreamRange(v string, size int64) (start, end int64, ok bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	if !strings.HasPrefix(v, "bytes=") || strings.Contains(v, ",") {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(v, "bytes=")
	i := strings.IndexByte(spec, '-')
	if i < 0 {
		return 0, 0, false
	}
	left, right := spec[:i], spec[i+1:]
	if left == "" {
		n, err := strconv.ParseInt(right, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true
	}
	start, err := strconv.ParseInt(left, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end = size - 1
	if right != "" {
		if end, err = strconv.ParseInt(right, 10, 64); err != nil || end < start {
			return 0, 0, false
		}
		if end > size-1 {
			end = size - 1
		}
	}
	return start, end, true
}

func contentTypeForContainer(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "mkv":
		return "video/x-matroska"
	case "mp4", "m4v":
		return "video/mp4"
	case "ts", "m2ts":
		return "video/mp2t"
	case "avi":
		return "video/x-msvideo"
	case "flv":
		return "video/x-flv"
	case "webm":
		return "video/webm"
	case "mov":
		return "video/quicktime"
	default:
		return "application/octet-stream"
	}
}
