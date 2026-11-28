package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lime/internal/geo"
)

// GeoIP 数据不内嵌（v4 11MB + v6 37MB），面板里点一下从这里下载。
// 只缺 v6 的用户也能用：v4 查不到 v6 地址时按规则里的 UnknownPolicy 处理。

const (
	geoSourceRepo = "lionsoul2014/ip2region/master/data"
	geoDownloadTO = 20 * time.Minute
	// 小于这个大小的文件视为没下完，重新下。
	geoMinSize = 1 << 20
)

// 依次尝试的下载源：官方源最稳，镜像用于国内网络。
var geoDownloadSources = []string{
	"https://raw.githubusercontent.com/" + geoSourceRepo + "/",
	"https://ghproxy.net/https://raw.githubusercontent.com/" + geoSourceRepo + "/",
	"https://gh-proxy.com/https://raw.githubusercontent.com/" + geoSourceRepo + "/",
}

var geoDL struct {
	mu        sync.Mutex
	running   bool
	doneAt    time.Time
	lastError string
}

type geoDownloadState struct {
	Running bool   `json:"Running"`
	DoneAt  string `json:"DoneAt"`
	Error   string `json:"Error"`
}

func geoDownloadSnapshot() geoDownloadState {
	geoDL.mu.Lock()
	defer geoDL.mu.Unlock()
	st := geoDownloadState{Running: geoDL.running, Error: geoDL.lastError}
	if !geoDL.doneAt.IsZero() {
		st.DoneAt = geoDL.doneAt.UTC().Format(time.RFC3339)
	}
	return st
}

// webGeoIPDownload 触发一次后台下载，立即返回。
func (h *Handler) webGeoIPDownload(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	geoDL.mu.Lock()
	if geoDL.running {
		geoDL.mu.Unlock()
		writeJSON(w, http.StatusOK, geoDownloadSnapshot())
		return
	}
	geoDL.running = true
	geoDL.lastError = ""
	geoDL.mu.Unlock()

	go h.runGeoDownload()
	writeJSON(w, http.StatusAccepted, geoDownloadSnapshot())
}

func (h *Handler) runGeoDownload() {
	err := h.downloadGeoFiles()

	geoDL.mu.Lock()
	geoDL.running = false
	geoDL.doneAt = time.Now()
	if err != nil {
		geoDL.lastError = err.Error()
	}
	geoDL.mu.Unlock()

	if err != nil {
		h.log.Warn("geoip download failed", "err", err)
		return
	}
	h.reloadGeo()
	h.log.Info("geoip database updated", "dir", h.geoDir())
}

func (h *Handler) downloadGeoFiles() error {
	dir := h.geoDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var firstErr error
	for _, name := range []string{geo.V4File, geo.V6File} {
		path := filepath.Join(dir, name)
		if st, err := os.Stat(path); err == nil && st.Size() >= geoMinSize {
			continue
		}
		if err := downloadFirstAvailable(name, path); err != nil {
			h.log.Warn("geoip file download", "file", name, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func downloadFirstAvailable(name, dst string) error {
	var lastErr error
	for _, base := range geoDownloadSources {
		if err := fetchFile(base+name, dst); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func fetchFile(src, dst string) error {
	client := &http.Client{Timeout: geoDownloadTO}
	resp, err := client.Get(src)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &downloadError{status: resp.StatusCode, url: src}
	}

	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if n < geoMinSize {
		_ = os.Remove(tmp)
		return &downloadError{status: 0, url: src, short: true}
	}
	return os.Rename(tmp, dst)
}

type downloadError struct {
	status int
	url    string
	short  bool
}

func (e *downloadError) Error() string {
	if e.short {
		return "下载内容不完整：" + e.url
	}
	return "下载失败（HTTP " + http.StatusText(e.status) + "）：" + e.url
}

// reloadGeo 重新加载目录里的 xdb。下载完成和启动时各调一次。
func (h *Handler) reloadGeo() {
	db, err := geo.Open(h.geoDir())
	if err != nil {
		h.log.Warn("geoip not loaded, province rules disabled", "dir", h.geoDir(), "err", err)
		return
	}
	SetGeoDB(db)
}
