package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"lime/internal/config"
	"lime/internal/dto"
	"lime/internal/library"
	"lime/internal/notify"
	"lime/internal/probe"
	"lime/internal/scrape"
	"lime/internal/store"
	"lime/internal/webui"
)

// realtimeMonitor 是面板实时监控开关需要的最小接口。
type realtimeMonitor interface {
	Enabled() bool
	SetEnabled(bool) error
}

type Handler struct {
	cfg      config.Config
	db       *store.DB
	log      *slog.Logger
	scanner  *library.Scanner
	scraper  *scrape.Scraper
	realtime realtimeMonitor
	prober   *probe.Prober
	sessions *sessionHub
	notifier *notify.Telegram

	// 播放通知去重：同一会话同一事件短时间内只推一次。
	notifyMu   sync.Mutex
	notifySeen map[string]time.Time

	serverID  string
	startedAt time.Time

	// personDL 给演员头像下载去重：同一演员并发请求只下一次 TMDB。
	personMu   sync.Mutex
	personDL   map[string]*personDownload
	personHTTP *http.Client

	// 待探测片源计数在后台刷新，避免设置页每次请求都扫 29 万行。
	probeCountsMu sync.RWMutex
	probeCounts   [3]int // [0]=全部 [1]=收藏 [2]=收藏+在追

	backupMu sync.Mutex
	// coverMu 串行化库封面生成，避免多个客户端同时请求时重复渲染。
	coverMu sync.Mutex

	loginLimiter *loginLimiter
}

type personDownload struct {
	done chan struct{}
	err  error
}

// refreshProbeCounts 每 10 分钟刷新一次待探测数量（modernc 扫描较慢，不能放在请求里）。
func (h *Handler) refreshProbeCounts() {
	time.Sleep(20 * time.Second)
	for {
		total, fav, watching := h.db.ProbePendingCounts()
		h.probeCountsMu.Lock()
		h.probeCounts = [3]int{total, fav, watching}
		h.probeCountsMu.Unlock()
		time.Sleep(10 * time.Minute)
	}
}

func (h *Handler) probeCountsSnapshot() (total, favorites, watching int) {
	h.probeCountsMu.RLock()
	c := h.probeCounts
	h.probeCountsMu.RUnlock()
	return c[0], c[1], c[2]
}

func New(cfg config.Config, db *store.DB, log *slog.Logger, scanner *library.Scanner, scraper *scrape.Scraper, realtime realtimeMonitor) (http.Handler, error) {
	serverID, err := db.ServerID()
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 8
	if cfg.TMDBProxy != "" {
		if u, err := url.Parse(cfg.TMDBProxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}

	h := &Handler{
		cfg:        cfg,
		personHTTP: &http.Client{Timeout: 20 * time.Second, Transport: tr},
		db:         db,
		log:        log,
		scanner:    scanner,
		scraper:    scraper,
		realtime:   realtime,
		prober:     probe.New(cfg, db, log),
		sessions:   newSessionHub(),
		serverID:   serverID,
		startedAt:  time.Now(),

		loginLimiter: newLoginLimiter(),
	}
	// 面板里保存的 Telegram 设置优先于环境变量；worker 常驻，改配置即时生效。
	h.notifier = notify.New(h.tgConfig(), log)
	// 面板里保存的预热设置优先于环境变量。
	h.prober.StartWarmup(h.warmupConfig())
	go h.refreshProbeCounts()

	appFS, err := webui.AppFS()
	if err != nil {
		return nil, err
	}
	return h.routes(appFS), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	// 不要转义 & < >，保持 URL 原样，避免个别客户端解析异常。
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message))
}

func writeStatus(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
}

func (h *Handler) userDto(u *store.User) *dto.UserDto {
	policy := dto.DefaultPolicy(u.IsAdmin)
	policy.IsDisabled = u.IsDisabled
	if u.MaxParentalRating > 0 {
		v := u.MaxParentalRating
		policy.MaxParentalRating = &v
	}

	d := &dto.UserDto{
		Name:                      u.Name,
		ServerId:                  h.serverID,
		Id:                        u.ID,
		HasPassword:               u.PasswordHash != "",
		HasConfiguredPassword:     u.PasswordHash != "",
		HasConfiguredEasyPassword: false,
		EnableAutoLogin:           false,
		Configuration:             map[string]any{},
		Policy:                    policy,
	}
	if u.LastLoginAt != nil {
		d.LastLoginDate = u.LastLoginAt.UTC().Format(time.RFC3339Nano)
	}
	return d
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
