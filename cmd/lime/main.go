package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lime/internal/api"
	"lime/internal/config"
	"lime/internal/library"
	"lime/internal/scrape"
	"lime/internal/store"
)

// autoDetectLibraries 在没配 LIME_LIBRARIES 时按目录结构自动建库：
// 根目录下的一级子目录视为分类，目录名用于推断类型。若识别不出任何
// 分类名，就把根目录本身注册成一个混合库。
func autoDetectLibraries(roots []string) []config.LibrarySpec {
	if len(roots) == 0 {
		for _, d := range []string{"/media", "/strm"} {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				roots = append(roots, d)
			}
		}
	}

	out := make([]config.LibrarySpec, 0, 8)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		var names []string
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				names = append(names, e.Name())
			}
		}
		if len(names) == 0 {
			out = append(out, config.LibrarySpec{Name: filepath.Base(root), Path: root, Kind: "mixed"})
			continue
		}

		specs := make([]config.LibrarySpec, 0, len(names))
		recognized := 0
		for _, name := range names {
			kind := inferLibraryKind(name)
			if kind != "mixed" {
				recognized++
			}
			specs = append(specs, config.LibrarySpec{
				Name: name,
				Path: filepath.Join(root, name),
				Kind: kind,
			})
		}
		// 一个都认不出来，说明根目录本身就是单个媒体库。
		if recognized == 0 {
			out = append(out, config.LibrarySpec{Name: filepath.Base(root), Path: root, Kind: "mixed"})
			continue
		}
		out = append(out, specs...)
	}
	return out
}

// inferLibraryKind 按目录名猜库类型；猜不出来的留给用户配置
// LIME_LIBRARIES 或在面板里改。
func inferLibraryKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "演唱"), strings.Contains(n, "电影"),
		strings.Contains(n, "movie"), strings.Contains(n, "film"):
		return "movies"
	case strings.Contains(n, "剧"), strings.Contains(n, "动漫"), strings.Contains(n, "动画"),
		strings.Contains(n, "综艺"), strings.Contains(n, "纪录"), strings.Contains(n, "儿童"),
		strings.Contains(n, "tv"), strings.Contains(n, "show"), strings.Contains(n, "series"),
		strings.Contains(n, "anime"):
		return "tvshows"
	default:
		return "mixed"
	}
}

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		logger.Error("create data dir", "err", err)
		os.Exit(1)
	}

	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(cfg.DataDir, "lime.db")
	}

	db, err := store.Open(dbPath)
	if err != nil {
		logger.Error("open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		logger.Error("migrate", "err", err)
		os.Exit(1)
	}
	if rebuilt, err := db.EnsureSearchIndex(); err != nil {
		logger.Warn("search index unavailable, fallback to LIKE", "err", err)
	} else if rebuilt {
		logger.Info("search index built")
	}
	if err := db.BackfillRatingScores(); err != nil {
		logger.Warn("backfill rating scores", "err", err)
	}
	if n, err := db.BackfillPinyin(); err != nil {
		logger.Warn("backfill pinyin", "err", err)
	} else if n > 0 {
		logger.Info("pinyin sort keys built", "items", n)
	}

	if err := db.EnsureAdmin(cfg.AdminUser, cfg.AdminPass); err != nil {
		logger.Error("ensure admin", "err", err)
		os.Exit(1)
	}

	// 每日数据库备份，避免误删/迁移出错时无法回滚。
	if cfg.BackupDailyAt != "" {
		go runBackupScheduler(cfg, db, logger)
	}

	// 只在数据库还没有库时用环境变量初始化；之后以面板管理为准，
	// 这样面板里改名/删除的库不会在重启后被 LIME_LIBRARIES 覆盖回来。
	existing, err := db.ListLibraries()
	if err != nil {
		logger.Error("list libraries", "err", err)
		os.Exit(1)
	}
	if len(existing) == 0 {
		specs := cfg.Libraries
		if len(specs) == 0 {
			// 没配 LIME_LIBRARIES 时按挂载目录自动建库：一级子目录
			// 当作分类（电影/剧集/动漫…），目录名用于推断类型。
			specs = autoDetectLibraries(cfg.LibraryRoots)
			for _, spec := range specs {
				logger.Info("library auto-detected", "name", spec.Name, "path", spec.Path, "kind", spec.Kind)
			}
		}
		for _, spec := range specs {
			lib, err := db.UpsertLibrary(spec.Name, spec.Path, spec.Kind)
			if err != nil {
				logger.Error("register library", "name", spec.Name, "path", spec.Path, "err", err)
				continue
			}
			logger.Info("library registered", "name", lib.Name, "path", lib.Path, "kind", lib.Kind)
		}
	} else if len(cfg.Libraries) > 0 {
		logger.Info("libraries managed in database; LIME_LIBRARIES seeding skipped", "count", len(existing))
	}
	libs, err := db.ListLibraries()
	if err != nil {
		logger.Error("list libraries", "err", err)
		os.Exit(1)
	}
	if len(libs) == 0 {
		logger.Warn("no libraries configured; add one in /web/ or set LIME_LIBRARIES=\"名称|路径|类型\"")
	}

	scanner := library.New(db, logger)
	// 面板里保存的运行时配置优先于环境变量。
	if v, err := db.GetSetting("tmdb_api_keys"); err == nil && v != "" {
		keys := make([]string, 0, 4)
		for _, k := range strings.Split(v, "\n") {
			if k = strings.TrimSpace(k); k != "" {
				keys = append(keys, k)
			}
		}
		if len(keys) > 0 {
			cfg.TMDBAPIKeys = keys
		}
	} else if v, err := db.GetSetting("tmdb_api_key"); err == nil && v != "" {
		// 兼容早期只存了一个 key 的版本
		cfg.TMDBAPIKeys = []string{v}
		cfg.TMDBAPIKey = v
	}
	if v, err := db.GetSetting("tmdb_proxy"); err == nil {
		cfg.TMDBProxy = v
	}

	scraper := scrape.New(cfg, db, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	monitor := library.NewMonitor(db, scanner, logger, cfg.RealtimeMonitor, func(ids []string) {
		if !scraper.Enabled() {
			return
		}
		go func() {
			if _, err := scraper.ScrapeItems(ids); err != nil {
				logger.Debug("realtime scrape skipped", "err", err)
			}
		}()
	})

	handler, err := api.New(cfg, db, logger, scanner, scraper, monitor)
	if err != nil {
		logger.Error("init api", "err", err)
		os.Exit(1)
	}

	// 启动后异步扫描，不阻塞 HTTP 服务
	if len(libs) > 0 && cfg.ScanOnStart {
		go func() {
			start := time.Now()
			count, err := scanner.ScanAll()
			if err != nil {
				logger.Error("initial scan failed", "err", err)
				return
			}
			logger.Info("initial scan done", "strm", count, "dur", time.Since(start).String())
		}()
	} else if len(libs) > 0 {
		logger.Info("startup scan disabled; POST /Library/Refresh to scan manually")
	}

	// 周期性重扫
	if len(libs) > 0 && cfg.ScanInterval > 0 {
		go func() {
			ticker := time.NewTicker(cfg.ScanInterval)
			defer ticker.Stop()
			for range ticker.C {
				start := time.Now()
				count, err := scanner.ScanAll()
				if err != nil {
					logger.Error("periodic scan failed", "err", err)
					continue
				}
				logger.Info("periodic scan done", "strm", count, "dur", time.Since(start).String())
			}
		}()
	}

	// 每日定时增量扫描：适合 PanSave 每天入库新片的场景。
	// 时间可在面板里修改，调度器每分钟重新读取。
	if len(libs) > 0 {
		go runDailyScan(cfg, db, scanner, scraper, logger)
		go runDailyScrape(cfg, db, scraper, logger)
	}

	// 实时监控媒体库（Emby 的实时监控行为）：PanSave 写入新 .strm 后
	// 不用等定时扫描，立刻增量解析；解析完顺手补刮削。
	// 开关在面板「系统 → 实时监控」，运行中也能即时启停。
	if len(libs) > 0 {
		go monitor.Run(ctx)
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("LiMe listening", "addr", cfg.HTTPAddr, "data", cfg.DataDir, "db", dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// runDailyScan 每天在 HH:MM（容器本地时区）做一次增量扫描，
// 扫完按配置决定是否继续刮削。
func runDailyScan(cfg config.Config, db *store.DB, scanner *library.Scanner, scraper *scrape.Scraper, logger *slog.Logger) {
	lastRun := ""
	for {
		// 面板里可以随时改时间，每分钟重新读一次。
		at := normalizeDaily(cfg.ScanDailyAt)
		if v, err := db.GetSetting("scan_daily_at"); err == nil && v != "" {
			at = normalizeDaily(v)
		}
		now := time.Now()
		if at != "" && now.Format("15:04") == at {
			key := now.Format("2006-01-02") + " " + at
			if lastRun != key {
				lastRun = key
				runDailyScanOnce(cfg, scanner, scraper, logger)
			}
		}
		time.Sleep(time.Minute)
	}
}

func runDailyScanOnce(cfg config.Config, scanner *library.Scanner, scraper *scrape.Scraper, logger *slog.Logger) {
	start := time.Now()
	count, err := scanner.ScanAllFast()
	if err != nil {
		logger.Error("daily scan failed", "err", err)
		return
	}
	logger.Info("daily scan done", "strm", count, "dur", time.Since(start).String())
	cleanupImageCache(cfg.DataDir, logger)
}

// runDailyScrape 独立的每日刮削任务，和扫描互不联动。
func runDailyScrape(cfg config.Config, db *store.DB, scraper *scrape.Scraper, logger *slog.Logger) {
	lastRun := ""
	for {
		raw, _ := db.GetSetting("scrape_daily_at")
		at := normalizeDaily(raw)
		now := time.Now()
		if at != "" && now.Format("15:04") == at {
			key := now.Format("2006-01-02") + " " + at
			if lastRun != key {
				lastRun = key
				if !scraper.Enabled() {
					logger.Warn("daily scrape skipped: TMDB key not configured")
				} else if _, err := scraper.Run(); err != nil {
					logger.Warn("daily scrape failed", "err", err)
				}
			}
		}
		time.Sleep(time.Minute)
	}
}

// cleanupImageCache 清理 90 天没有访问过的缩略图缓存。
func cleanupImageCache(dataDir string, logger *slog.Logger) {
	dir := filepath.Join(dataDir, "image-cache")
	cutoff := time.Now().AddDate(0, 0, -90)
	removed := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) && os.Remove(path) == nil {
			removed++
		}
		return nil
	})
	if removed > 0 {
		logger.Info("image cache cleaned", "removed", removed)
	}
}

func durationUntilDaily(at string) (time.Duration, error) {
	parts := strings.SplitN(strings.TrimSpace(at), ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("want HH:MM")
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, err
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, err
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("time out of range")
	}
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return time.Until(next), nil
}

// normalizeDaily 把 "3:00" / "03:00" 统一成 "03:00"，非法返回空。
func normalizeDaily(s string) string {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return ""
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", h, m)
}

// parseClock 把 "04:30" 解析成当天的分钟数。
func parseClock(v string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// runBackupScheduler 每天在固定时间做一次 SQLite 快照，只保留最近 keep 份。
// 时间可以随时在面板修改（每分钟重新读一次设置）。
func runBackupScheduler(cfg config.Config, db *store.DB, log *slog.Logger) {
	lastRun := ""
	for {
		at := normalizeDaily(cfg.BackupDailyAt)
		if v, err := db.GetSetting("backup_daily_at"); err == nil && v != "" {
			if strings.EqualFold(v, "off") {
				at = ""
			} else {
				at = normalizeDaily(v)
			}
		}
		keep := cfg.BackupKeep
		if v, err := db.GetSetting("backup_keep"); err == nil && v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				keep = n
			}
		}

		now := time.Now()
		if at != "" && now.Format("15:04") == at {
			key := now.Format("2006-01-02") + " " + at
			if lastRun != key {
				lastRun = key
				runBackupOnce(cfg.DataDir, db, keep, log)
			}
		}
		time.Sleep(time.Minute)
	}
}

func runBackupOnce(dataDir string, db *store.DB, keep int, log *slog.Logger) {
	name := "lime.db.bak-" + time.Now().Format("20060102-150405")
	dest := filepath.Join(dataDir, name)
	start := time.Now()
	if err := db.BackupTo(dest); err != nil {
		log.Error("database backup failed", "err", err)
		return
	}
	if removed, err := store.PruneBackups(dataDir, keep); err != nil {
		log.Warn("prune old backups failed", "err", err)
	} else if removed > 0 {
		log.Info("old database backups pruned", "removed", removed)
	}
	var size int64
	if st, err := os.Stat(dest); err == nil {
		size = st.Size()
	}
	log.Info("database backup created",
		"file", name, "mb", size>>20, "dur", time.Since(start).Round(time.Second).String())
}
