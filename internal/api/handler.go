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
	WatchDirs() int
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
	// ingestNotifier 独立于播放通知：可以复用同一个 Bot，也可以单独配置。
	ingestNotifier *notify.Telegram

	// 播放通知去重：同一会话同一事件短时间内只推一次。
	notifyMu   sync.Mutex
	notifySeen map[string]time.Time

	// 多人同播（PanSave 115 复制）运行时配置，面板可改，热生效。
	sameMu      sync.RWMutex
	sameMode    string   // auto / on / off
	sameEntries []string // 反代入口，如 http://192.168.1.10:1886
	sameHosts   []string // PanSave 反代的公网域名
	// 最近见过的 Host / X-Forwarded-Host，面板里给用户勾选，避免手敲。
	hostSeenMu sync.Mutex
	hostSeen   map[string]time.Time

	// 播放卡顿监控：按 PlaySessionId 记录上次进度和告警时间。
	stallMu    sync.Mutex
	stallWatch map[string]*stallState
	// 卡顿阈值运行时可在面板调整。
	stallCfgMu    sync.RWMutex
	stallAfter    time.Duration
	stallCooldown time.Duration

	// auto 跟跳的熔断：连续失败几次后暂停解析，避免 PanSave 挂了时
	// 每个新地址都先等超时再回退。
	resolveMu           sync.Mutex
	resolveFails        int
	resolveBlockedUntil time.Time

	// 全库统计要扫几十万行；概览/媒体库/系统页来回切时复用 60 秒。
	statsMu      sync.Mutex
	statsCache   *store.Stats
	statsCacheAt time.Time

	// 刮削失败汇总（按类型计数 + 存量缺图）也要扫全库，缓存 60 秒。
	failureMu     sync.Mutex
	failureCache  map[string]int
	failureHealth store.ScrapeHealth
	failureAt     time.Time

	// 筛选面板缓存（Genres / Years / Ratings），按可见库集合缓存。
	filterMu       sync.Mutex
	filterCache    map[string]filterCacheEntry
	filterInflight map[string]bool

	healthMu      sync.Mutex
	healthAlertAt map[string]time.Time

	// SegmentPendingCount 会扫 26 万条目；设置页每次打开都查会卡 1-2 秒。
	// 缓存 5 分钟，过期后后台刷新，页面先拿旧值。
	segmentPendingMu       sync.Mutex
	segmentPending         int
	segmentPendingAt       time.Time
	segmentPendingInflight bool

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

func New(cfg config.Config, db *store.DB, log *slog.Logger, scanner *library.Scanner, scraper *scrape.Scraper, realtime realtimeMonitor) (http.Handler, *Handler, error) {
	serverID, err := db.ServerID()
	if err != nil {
		return nil, nil, err
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
	h.loadStallSettings()
	h.loadSameStreamSettings()
	resolveMode := playbackModeLabel(cfg.ResolveRedirect, cfg.ResolveRedirectAuto)
	directMode := playbackModeLabel(cfg.PlaybackDirectURL, cfg.PlaybackDirectURLAuto)
	if !cfg.ResolveRedirect {
		directMode = "off"
	}
	log.Info("playback acceleration", "resolve", resolveMode, "direct_stream_url", directMode)
	// 全库统计和图片目录统计都不适合等用户点开页面才算；后台先热一次。
	// 错开执行：多个全表扫描同时跑会把不同连接的 SQLite 页缓存一起填满，
	// 启动瞬间容器内存会冲到很高，其实随后都会被回收。
	go func() { _, _ = h.cachedStats() }()
	if h.scraper != nil {
		go func() {
			time.Sleep(5 * time.Second)
			_, _ = h.scraper.StorageStats()
		}()
	}
	h.notifier = notify.New(h.tgConfig(), log)
	h.ingestNotifier = notify.New(h.ingestTGConfig(), log)
	go h.runIngestNotifier()
	// 面板里保存的预热设置优先于环境变量。
	h.prober.StartWarmup(h.warmupConfig())
	go h.runHealthMonitor()
	h.prober.SetSegmentsEnabled(h.segmentExtractEnabled())
	go h.refreshProbeCounts() // 内部先等 20 秒
	go func() {
		time.Sleep(10 * time.Second)
		h.segmentPendingCached()
	}()

	appFS, err := webui.AppFS()
	if err != nil {
		return nil, nil, err
	}
	return h.routes(appFS), h, nil
}

// QueueIngestProbe 实时监控发现新条目后立即排队探测/片段分析，
// 不再等夜间预热窗口。由 cmd/lime 的 onIngest 回调调用。
func (h *Handler) QueueIngestProbe(ids []string) {
	if h == nil || h.prober == nil {
		return
	}
	h.prober.WarmIngestedItems(ids)
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
	policy.EnableContentDownloading = h.downloadEnabled()
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
