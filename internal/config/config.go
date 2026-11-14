package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultVersion 伪装成 Jellyfin 版本号，部分客户端会做最低版本校验。
const DefaultVersion = "10.9.11"

type Config struct {
	ServerName string
	HTTPAddr   string
	DataDir    string
	DBPath     string
	AdminUser  string
	AdminPass  string
	Version    string

	// STRM 302 地址改写（M3 使用）
	RewriteEnabled bool
	RewriteFrom    string
	RewriteLocal   string
	RewritePublic  string

	// 播放端点是否强制校验 token。默认关闭以最大兼容各类播放器；
	// 对外网暴露时建议打开。
	StreamRequireAuth bool

	// 是否把 API 请求记录到 Info 日志（排查第三方集成用，跳过图片/静态资源）。
	HTTPLog bool

	// 单次列表查询最多返回多少条。防止客户端不带 Limit 时把 26 万条
	// 一次性塞进内存/响应（实测 179MB / 15s）。TotalRecordCount 仍是全量。
	MaxItems int

	// MediaSource.Path 使用这个上游地址（例如 http://192.168.1.133:886）。
	// PanSave 反代需要探测上游的 302；如果返回公网地址会打回反代自身形成回环。
	// 留空则沿用请求的 Host（适合客户端直连 LiMe）。
	InternalURL string

	// 返回 302 前先跟随一次上游跳转（PanSave → 云盘 CDN），
	// 把最终 CDN 地址交给客户端。适用于只暴露 PanSave 反代、没有公网
	// PanSave 直链域名的部署。
	ResolveRedirect bool

	Libraries []LibrarySpec
	// 面板里新建库时允许的容器内根路径，默认 /strm。
	LibraryRoots []string

	// 启动时是否全量扫描；大库（几十万 STRM）可以关掉，改用定时或手动刷新。
	ScanOnStart bool
	// 周期性重扫间隔，0 表示不启用，例如 "6h"。
	ScanInterval time.Duration
	// 每天固定时间做增量扫描，格式 "HH:MM"（容器本地时区），空表示不启用。
	ScanDailyAt string
	// 实时监控媒体库目录（类似 Emby 的实时监控）：新 .strm 出现后增量解析。
	RealtimeMonitor bool

	// ---- 媒体信息探测（ffprobe 远程探测）----
	ProbeEnabled bool
	ProbeWorkers int
	ProbeTimeout time.Duration
	// PlaybackInfo 里最多同步等探测多久（首次播放会稍慢，0 = 完全异步）
	ProbeSyncWait time.Duration
	// 后台预热探测（让第一次起播不用现等 ffprobe）。
	ProbeWarmup      bool
	ProbeWarmupLimit int
	// 预热范围：favorites（收藏）/ recent（最近 7 天+进度）/ all（全库）
	ProbeScope string
	// 预热时间窗 "HH:MM-HH:MM"，空表示全天
	ProbeWindow string
	// 每次激活后最长连续运行时长，0 表示跑到窗口结束
	ProbeMaxRun time.Duration
	// 片头片尾提取：章节随探测免费获取，回填补齐存量。
	SegmentExtract bool
	// 片段回填范围：favorites / watching / recent / all
	SegmentScope string
	// 单集累计播放多久后自动补整部剧的片段，0 表示关闭。
	SegmentAutoAfter time.Duration
	// 背景图原图回填的并发数
	BackdropWorkers int

	// 每日数据库备份时间（HH:MM，空表示关闭）与保留份数
	BackupDailyAt string
	BackupKeep    int

	// ---- TMDB 刮削（M4）----
	TMDBAPIKey    string
	TMDBAPIKeys   []string
	TMDBLanguage  string
	TMDBBaseURL   string
	TMDBImageBase string
	TMDBProxy     string

	// ---- Telegram 播放通知 ----
	// Token/ChatID 也可在面板里配置（数据库优先于环境变量）。
	TelegramToken  string
	TelegramChatID string
	TelegramProxy  string
	TelegramNotify bool
	// 需要推送的事件，逗号分隔：start,pause,resume,stop
	TelegramEvents string
}

type LibrarySpec struct {
	Name string
	Path string
	Kind string // movies | tvshows | mixed | auto
}

func Load() Config {
	cfg := Config{
		ServerName:        getenv("LIME_SERVER_NAME", "LiMe"),
		HTTPAddr:          getenv("LIME_HTTP_ADDR", ":886"),
		DataDir:           getenv("LIME_DATA_DIR", "./data"),
		DBPath:            os.Getenv("LIME_DB_PATH"),
		AdminUser:         getenv("LIME_ADMIN_USER", "admin"),
		AdminPass:         getenv("LIME_ADMIN_PASSWORD", "password"),
		Version:           getenv("LIME_VERSION", DefaultVersion),
		RewriteEnabled:    getbool("LIME_REWRITE_ENABLED", false),
		RewriteFrom:       os.Getenv("LIME_REWRITE_FROM"),
		RewriteLocal:      os.Getenv("LIME_REWRITE_LOCAL"),
		RewritePublic:     os.Getenv("LIME_REWRITE_PUBLIC"),
		StreamRequireAuth: getbool("LIME_STREAM_REQUIRE_AUTH", false),
		HTTPLog:           getbool("LIME_HTTP_LOG", false),
		MaxItems:          getint("LIME_MAX_ITEMS", 20000),
		InternalURL:       strings.TrimRight(os.Getenv("LIME_INTERNAL_URL"), "/"),
		ResolveRedirect:   getbool("LIME_RESOLVE_REDIRECT", false),
		Libraries:         parseLibraries(os.Getenv("LIME_LIBRARIES")),
		LibraryRoots:      parseRoots(os.Getenv("LIME_LIBRARY_ROOTS")),
		ScanOnStart:       getbool("LIME_SCAN_ON_START", true),
		ScanInterval:      getduration("LIME_SCAN_INTERVAL", 0),
		ScanDailyAt:       strings.TrimSpace(os.Getenv("LIME_SCAN_DAILY_AT")),
		RealtimeMonitor:   getbool("LIME_REALTIME_MONITOR", true),
		ProbeEnabled:      getbool("LIME_PROBE_ENABLED", true),
		ProbeWorkers:      getint("LIME_PROBE_WORKERS", 2),
		ProbeTimeout:      getduration("LIME_PROBE_TIMEOUT", 30*time.Second),
		ProbeSyncWait:     getduration("LIME_PROBE_SYNC_WAIT", 3*time.Second),
		ProbeWarmup:       getbool("LIME_PROBE_WARMUP", true),
		ProbeWarmupLimit:  getint("LIME_PROBE_WARMUP_LIMIT", 50),
		ProbeScope:        getenv("LIME_PROBE_SCOPE", "favorites"),
		ProbeWindow:       getenv("LIME_PROBE_WINDOW", "03:00-06:00"),
		ProbeMaxRun:       getduration("LIME_PROBE_MAX_RUN", 2*time.Hour),
		SegmentExtract:    getbool("LIME_SEGMENT_EXTRACT", true),
		SegmentScope:      getenv("LIME_SEGMENT_SCOPE", "watching"),
		SegmentAutoAfter:  getduration("LIME_SEGMENT_AUTO_AFTER", 5*time.Minute),
		BackdropWorkers:   getint("LIME_BACKDROP_WORKERS", 5),
		BackupDailyAt:     getenv("LIME_BACKUP_DAILY_AT", "04:30"),
		BackupKeep:        getint("LIME_BACKUP_KEEP", 3),
		TMDBAPIKey:        strings.TrimSpace(os.Getenv("LIME_TMDB_API_KEY")),
		TMDBAPIKeys:       parseKeys(os.Getenv("LIME_TMDB_API_KEYS")),
		TMDBLanguage:      getenv("LIME_TMDB_LANGUAGE", "zh-CN"),
		TMDBBaseURL:       getenv("LIME_TMDB_BASE_URL", "https://api.themoviedb.org/3"),
		TMDBImageBase:     getenv("LIME_TMDB_IMAGE_BASE", "https://image.tmdb.org/t/p"),
		TMDBProxy:         strings.TrimSpace(os.Getenv("LIME_TMDB_PROXY")),
		TelegramToken:     strings.TrimSpace(os.Getenv("LIME_TG_BOT_TOKEN")),
		TelegramChatID:    strings.TrimSpace(os.Getenv("LIME_TG_CHAT_ID")),
		TelegramProxy:     strings.TrimSpace(os.Getenv("LIME_TG_PROXY")),
		TelegramEvents:    getenv("LIME_TG_EVENTS", "start,pause,resume,stop"),
	}
	if len(cfg.TMDBAPIKeys) == 0 && cfg.TMDBAPIKey != "" {
		cfg.TMDBAPIKeys = []string{cfg.TMDBAPIKey}
	}
	// 显式设置了 LIME_TG_NOTIFY 就听它的；否则 token 和 chat 都齐了就默认开启。
	cfg.TelegramNotify = getbool("LIME_TG_NOTIFY", false)
	if os.Getenv("LIME_TG_NOTIFY") == "" && cfg.TelegramToken != "" && cfg.TelegramChatID != "" {
		cfg.TelegramNotify = true
	}
	return cfg
}

// parseKeys 解析逗号或换行分隔的多个 API Key。
func parseKeys(raw string) []string {
	raw = strings.ReplaceAll(raw, "\n", ",")
	out := make([]string, 0, 4)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getduration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// parseLibraries 解析 "名称|路径|类型;名称|路径|类型"。
// 类型可省略，默认 auto；留空则自动探测 /strm 或 ./strm。
func parseLibraries(raw string) []LibrarySpec {
	if strings.TrimSpace(raw) == "" {
		for _, p := range []string{"/strm", "./strm"} {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				return []LibrarySpec{{Name: "媒体库", Path: p, Kind: "auto"}}
			}
		}
		return nil
	}

	var out []LibrarySpec
	for _, chunk := range strings.Split(raw, ";") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		fields := strings.Split(chunk, "|")
		spec := LibrarySpec{Kind: "auto"}
		if len(fields) > 0 {
			spec.Name = strings.TrimSpace(fields[0])
		}
		if len(fields) > 1 {
			spec.Path = strings.TrimSpace(fields[1])
		}
		if len(fields) > 2 {
			spec.Kind = normalizeKind(fields[2])
		}
		if spec.Path == "" {
			continue
		}
		if spec.Name == "" {
			spec.Name = filepath.Base(spec.Path)
		}
		out = append(out, spec)
	}
	return out
}

func parseRoots(raw string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, filepath.Clean(p))
		}
	}
	if len(out) == 0 {
		return []string{"/strm"}
	}
	return out
}

func normalizeKind(k string) string {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "movies", "movie":
		return "movies"
	case "tvshows", "tv", "series":
		return "tvshows"
	case "mixed":
		return "mixed"
	}
	return "auto"
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getint(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func getbool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
