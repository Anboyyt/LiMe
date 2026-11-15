package api

import (
	"context"
	"fmt"
	"html"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lime/internal/notify"
	"lime/internal/store"
	"lime/internal/strm"
)

const (
	ingestNotifyDefaultDelay    = 3 * time.Minute
	ingestNotifyDefaultMaxWait  = 30 * time.Minute
	ingestNotifyDefaultBulkSize = 200
)

type ingestNotifySettings struct {
	Enabled   bool
	Delay     time.Duration
	MaxWait   time.Duration
	BulkLimit int
}

// ingestEnabled 是入库通知的总开关。和播放通知完全独立。
func (h *Handler) ingestEnabled() bool {
	v, err := h.db.GetSetting("ingest_tg_enabled")
	return err == nil && (v == "true" || v == "1")
}

// ingestTGConfig 生成入库通知使用的 Telegram 配置。
// mode=playback 复用播放通知的 Bot，但保留自己的开关；
// mode=custom 使用单独填写的 Token / Chat ID / 代理。
func (h *Handler) ingestTGConfig() notify.Config {
	pc := h.tgConfig()
	c := notify.Config{
		Token:   pc.Token,
		ChatID:  pc.ChatID,
		Proxy:   pc.Proxy,
		Enabled: h.ingestEnabled(),
		// nil 表示不按事件过滤，入库通知只有一个事件。
		Events: nil,
	}
	mode, _ := h.db.GetSetting("ingest_tg_mode")
	if strings.EqualFold(strings.TrimSpace(mode), "custom") {
		c.Token, _ = h.db.GetSetting("ingest_tg_bot_token")
		c.ChatID, _ = h.db.GetSetting("ingest_tg_chat_id")
		c.Proxy, _ = h.db.GetSetting("ingest_tg_proxy")
	}
	if c.Token == "" || c.ChatID == "" {
		c.Enabled = false
	}
	return c
}

func (h *Handler) ingestPendingCount() int {
	n, err := h.db.CountPendingIngest(time.Now().Unix())
	if err != nil {
		return 0
	}
	return n
}

func (h *Handler) ingestNotifySettings() ingestNotifySettings {
	cfg := ingestNotifySettings{
		Enabled:   h.ingestEnabled(),
		Delay:     ingestNotifyDefaultDelay,
		MaxWait:   ingestNotifyDefaultMaxWait,
		BulkLimit: ingestNotifyDefaultBulkSize,
	}
	if v, err := h.db.GetSetting("ingest_tg_delay"); err == nil && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Delay = d
		}
	}
	if v, err := h.db.GetSetting("ingest_tg_max_wait"); err == nil && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.MaxWait = d
		}
	}
	if v, err := h.db.GetSetting("ingest_tg_bulk_limit"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.BulkLimit = n
		}
	}
	if cfg.Delay < 10*time.Second {
		cfg.Delay = 10 * time.Second
	}
	if cfg.Delay > time.Hour {
		cfg.Delay = time.Hour
	}
	if cfg.MaxWait < cfg.Delay {
		cfg.MaxWait = cfg.Delay
	}
	if cfg.BulkLimit < 1 {
		cfg.BulkLimit = 1
	}
	if cfg.BulkLimit > 5000 {
		cfg.BulkLimit = 5000
	}
	return cfg
}

// runIngestNotifier 是入库通知的常驻 worker。
// 事件只落库，实际发送由这里统一聚合，避免一次入库刷出几十条消息。
func (h *Handler) runIngestNotifier() {
	time.Sleep(20 * time.Second)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		h.processIngestNotifications()
		<-ticker.C
	}
}

func (h *Handler) processIngestNotifications() {
	if h.db == nil {
		return
	}
	// 全量扫描期间不处理，避免边扫边发；扫描结束后再统一按阈值处理。
	if h.scanner != nil && h.scanner.Status().Running {
		return
	}
	cfg := h.ingestNotifySettings()
	now := time.Now()

	// 关闭时清空队列，避免以后打开通知把历史积压一次性刷出来。
	if !cfg.Enabled {
		if n, err := h.db.MarkAllPendingIngestNotified(now.Unix(), now); err == nil && n > 0 {
			h.log.Debug("ingest notify disabled, drained", "sources", n)
		}
		return
	}
	if h.ingestNotifier == nil || !h.ingestNotifier.Configured() {
		return
	}

	cutoff := now.Add(-cfg.Delay).Unix()
	count, err := h.db.CountPendingIngest(cutoff)
	if err != nil {
		h.log.Warn("ingest notify count", "err", err)
		return
	}
	if count == 0 {
		return
	}

	// 初始扫描或一次新增一个库时，避免几百上千条通知刷屏。
	if count > cfg.BulkLimit {
		text := fmt.Sprintf("✅ <b>入库通知</b>\n本次批量入库 %d 个文件，已跳过逐条通知。", count)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := h.ingestNotifier.SendMessageNow(ctx, "ingest", notify.Message{Text: text})
		cancel()
		if err != nil {
			h.log.Warn("ingest bulk summary failed", "sources", count, "err", err)
			return
		}
		if _, err := h.db.MarkAllPendingIngestNotified(cutoff, now); err != nil {
			h.log.Warn("mark bulk ingest notified", "err", err)
		}
		return
	}

	pending, err := h.db.PendingIngestSources(cutoff, cfg.BulkLimit+1)
	if err != nil {
		h.log.Warn("load pending ingest", "err", err)
		return
	}
	groups, orphanIDs := h.groupIngestSources(pending)
	if len(orphanIDs) > 0 {
		if err := h.db.MarkIngestNotified(orphanIDs, now); err != nil {
			h.log.Warn("mark orphan ingest notified", "err", err)
		}
	}
	if len(groups) == 0 {
		return
	}

	// 一次性查本季总集数，消息里的「本季共 N 集」不需要每条 group 单独查。
	seasonIDs := make([]string, 0, len(groups))
	for _, g := range groups {
		if g.seasonID != "" {
			seasonIDs = append(seasonIDs, g.seasonID)
		}
	}
	seasonCounts := map[string]int{}
	if len(seasonIDs) > 0 {
		counts, _, _, err := h.db.ItemChildren(nil, seasonIDs)
		if err != nil {
			h.log.Warn("ingest season counts", "err", err)
		} else {
			seasonCounts = counts
		}
	}

	for _, g := range groups {
		if h.ingestShouldWait(g, now, cfg.MaxWait) {
			continue
		}
		text := h.formatIngestMessage(g, seasonCounts)
		photo, photoName := h.ingestPhoto(g)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := h.ingestNotifier.SendMessageNow(ctx, "ingest", notify.Message{
			Text: text, Photo: photo, PhotoName: photoName,
		})
		cancel()
		if err != nil {
			h.log.Warn("ingest notify failed", "item", g.itemName(), "sources", len(g.sources), "err", err)
			continue
		}
		if err := h.db.MarkIngestNotified(g.sourceIDs(), now); err != nil {
			h.log.Warn("mark ingest notified", "item", g.itemName(), "err", err)
		}
		h.log.Info("ingest notified", "item", g.itemName(), "sources", len(g.sources), "episodes", len(g.episodes))
		// 和播放通知一样限速，避免连续发送被 Telegram 限流。
		time.Sleep(400 * time.Millisecond)
	}
}

// ingestShouldWait 等待刮削写入海报/演员/TMDB。
// 没有刮削器、或者已经等了足够久时直接发送，避免队列永久卡住。
func (h *Handler) ingestShouldWait(g *ingestGroup, now time.Time, maxWait time.Duration) bool {
	if h.scraper == nil || !h.scraper.Enabled() {
		return false
	}
	if now.Sub(time.Unix(g.earliest, 0)) >= maxWait {
		return false
	}
	it := g.metaItem()
	if it == nil {
		return false
	}
	return it.ScrapedAt == 0
}

type ingestGroup struct {
	series   *store.Item
	movie    *store.Item
	sample   *store.Item
	library  *store.Library
	seasonID string
	season   int
	episodes map[int]bool
	sources  []store.PendingIngest
	earliest int64
}

func (g *ingestGroup) metaItem() *store.Item {
	if g.movie != nil {
		return g.movie
	}
	if g.series != nil {
		return g.series
	}
	return g.sample
}

func (g *ingestGroup) itemName() string {
	if it := g.metaItem(); it != nil {
		return it.Name
	}
	return ""
}

func (g *ingestGroup) isEpisode() bool {
	return g.season > 0 || len(g.episodes) > 0
}

func (g *ingestGroup) sourceIDs() []string {
	ids := make([]string, 0, len(g.sources))
	for _, s := range g.sources {
		ids = append(ids, s.ID)
	}
	return ids
}

// groupIngestSources 把待通知片源按「剧 + 季」聚合；电影每部单独一组。
func (h *Handler) groupIngestSources(pending []store.PendingIngest) ([]*ingestGroup, []string) {
	if len(pending) == 0 {
		return nil, nil
	}
	itemIDs := make([]string, 0, len(pending))
	for _, p := range pending {
		itemIDs = append(itemIDs, p.ItemID)
	}
	items, err := h.db.ItemsByIDs(itemIDs)
	if err != nil {
		h.log.Warn("ingest load items", "err", err)
		return nil, nil
	}
	seriesIDs := make([]string, 0, len(items))
	seenSeries := make(map[string]bool, len(items))
	for _, it := range items {
		if it.Type == store.TypeEpisode && it.SeriesID != "" && !seenSeries[it.SeriesID] {
			seenSeries[it.SeriesID] = true
			seriesIDs = append(seriesIDs, it.SeriesID)
		}
	}
	seriesByID := map[string]*store.Item{}
	if len(seriesIDs) > 0 {
		if rows, err := h.db.ItemsByIDs(seriesIDs); err == nil {
			seriesByID = rows
		} else {
			h.log.Warn("ingest load series", "err", err)
		}
	}
	libsByID := map[string]*store.Library{}
	if libs, err := h.db.ListLibraries(); err == nil {
		for _, lib := range libs {
			libsByID[lib.ID] = lib
		}
	}

	groups := make(map[string]*ingestGroup)
	orphans := make([]string, 0, 4)
	for _, p := range pending {
		it := items[p.ItemID]
		if it == nil {
			orphans = append(orphans, p.ID)
			continue
		}
		var g *ingestGroup
		switch it.Type {
		case store.TypeEpisode:
			series := seriesByID[it.SeriesID]
			key := "episode|" + it.SeriesID + "|" + strconv.Itoa(it.ParentIndexNumber)
			if series == nil {
				key = "episode-item|" + it.ID
			}
			g = groups[key]
			if g == nil {
				g = &ingestGroup{
					series:   series,
					sample:   it,
					library:  libsByID[it.LibraryID],
					seasonID: it.SeasonID,
					season:   it.ParentIndexNumber,
					episodes: make(map[int]bool, 4),
					earliest: p.IngestedAt,
				}
				groups[key] = g
			}
			if it.IndexNumber > 0 {
				g.episodes[it.IndexNumber] = true
			}
		case store.TypeMovie:
			key := "movie|" + it.ID
			g = groups[key]
			if g == nil {
				g = &ingestGroup{
					movie:    it,
					sample:   it,
					library:  libsByID[it.LibraryID],
					earliest: p.IngestedAt,
				}
				groups[key] = g
			}
		default:
			orphans = append(orphans, p.ID)
			continue
		}
		g.sources = append(g.sources, p)
		if p.IngestedAt < g.earliest {
			g.earliest = p.IngestedAt
		}
	}

	out := make([]*ingestGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		ni, nj := out[i].itemName(), out[j].itemName()
		if ni != nj {
			return ni < nj
		}
		return out[i].season < out[j].season
	})
	return out, orphans
}

func (h *Handler) formatIngestMessage(g *ingestGroup, seasonCounts map[string]int) string {
	meta := g.metaItem()
	name := g.itemName()
	year := 0
	if meta != nil {
		year = meta.ProductionYear
	}

	var b strings.Builder
	b.WriteString("✅ <b>入库通知：")
	b.WriteString(html.EscapeString(name))
	if year > 0 {
		fmt.Fprintf(&b, " (%d)", year)
	}
	if g.isEpisode() {
		b.WriteString(" ")
		b.WriteString(episodeRangeLabel(g.season, g.episodeList()))
	}
	b.WriteString("</b>\n")

	if meta != nil {
		if people := ingestCast(meta.People, 5); people != "" {
			b.WriteString("🌟 主演：")
			b.WriteString(people)
			b.WriteString("\n")
		}
		if tmdb := strings.TrimSpace(meta.TmdbID); tmdb != "" {
			tmdb = strings.TrimPrefix(strings.ToLower(tmdb), "tmdb-")
			b.WriteString("🆔 影号：TMDB-")
			b.WriteString(html.EscapeString(tmdb))
			b.WriteString("\n")
		}
	}

	clouds := uniqueSourceField(g.sources, func(s store.PendingIngest) string { return s.Cloud })
	if len(clouds) > 0 {
		b.WriteString("📋 规则：")
		b.WriteString(html.EscapeString(strings.Join(clouds, " / ")))
		b.WriteString("\n")
		b.WriteString("☁️ 来源：")
		b.WriteString(html.EscapeString(joinNonEmpty(cloudLabels(clouds), " / ")))
		b.WriteString("\n")
	}

	if category := ingestCategory(g.library, firstSourcePath(g), name); category != "" {
		b.WriteString("🧱 分类：")
		b.WriteString(html.EscapeString(category))
		b.WriteString("\n")
	}

	if versions := ingestVersions(g.sources); versions != "" {
		b.WriteString("🎞️ 版本：")
		b.WriteString(html.EscapeString(versions))
		b.WriteString("\n")
	}

	b.WriteString("🧾 文件：")
	b.WriteString(strconv.Itoa(len(g.sources)))
	b.WriteString("个\n")

	if size := ingestTotalSize(g.sources); size > 0 {
		b.WriteString("💾 大小：")
		b.WriteString(fmtMediaSize(size))
		b.WriteString("\n")
	}

	platforms := ingestPlatforms(g.sources)
	if len(platforms) > 0 {
		b.WriteString("📺 平台：")
		b.WriteString(html.EscapeString(strings.Join(platforms, " / ")))
		b.WriteString("\n")
	}
	groups := ingestReleaseGroups(g.sources)
	if len(groups) > 0 {
		b.WriteString("👥 组名：")
		b.WriteString(html.EscapeString(strings.Join(groups, " / ")))
		b.WriteString("\n")
	}

	if g.isEpisode() && g.season > 0 {
		if n := seasonCounts[g.seasonID]; n > 0 {
			fmt.Fprintf(&b, "🧮 本季：S%02d 共%d集\n", g.season, n)
		}
	}

	b.WriteString("⏱️ 时间：")
	b.WriteString(time.Now().Format("2006-01-02 15:04:05"))
	return b.String()
}

func (g *ingestGroup) episodeList() []int {
	out := make([]int, 0, len(g.episodes))
	for n := range g.episodes {
		if n > 0 {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

func episodeRangeLabel(season int, episodes []int) string {
	prefix := ""
	if season > 0 {
		prefix = fmt.Sprintf("S%02d ", season)
	}
	if len(episodes) == 0 {
		return strings.TrimSpace(prefix)
	}
	return prefix + episodeRanges(episodes)
}

func episodeRanges(episodes []int) string {
	if len(episodes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(episodes))
	start, prev := episodes[0], episodes[0]
	flush := func() {
		if start == prev {
			parts = append(parts, fmt.Sprintf("E%02d", start))
		} else {
			parts = append(parts, fmt.Sprintf("E%02d-E%02d", start, prev))
		}
	}
	for _, n := range episodes[1:] {
		if n == prev+1 {
			prev = n
			continue
		}
		flush()
		start, prev = n, n
	}
	flush()
	return strings.Join(parts, "、")
}

func ingestCast(raw string, limit int) string {
	people := parsePeople(raw)
	names := make([]string, 0, limit+1)
	for _, p := range people {
		if strings.TrimSpace(p.Name) != "" {
			names = append(names, html.EscapeString(p.Name))
		}
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) <= limit {
		return strings.Join(names, " / ")
	}
	return strings.Join(names[:limit], " / ") + " 等"
}

func firstSourcePath(g *ingestGroup) string {
	if len(g.sources) == 0 {
		return ""
	}
	return g.sources[0].StrmPath
}

func uniqueSourceField(sources []store.PendingIngest, fn func(store.PendingIngest) string) []string {
	out := make([]string, 0, 4)
	seen := make(map[string]bool, 4)
	for _, s := range sources {
		v := strings.TrimSpace(fn(s))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func cloudLabels(clouds []string) []string {
	out := make([]string, 0, len(clouds))
	for _, cloud := range clouds {
		switch strings.ToLower(cloud) {
		case "115":
			out = append(out, "115网盘")
		case "123":
			out = append(out, "123网盘")
		default:
			out = append(out, cloud)
		}
	}
	return out
}

func joinNonEmpty(parts []string, sep string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func ingestTotalSize(sources []store.PendingIngest) int64 {
	var total int64
	for _, s := range sources {
		if s.Size > 0 {
			total += s.Size
		}
	}
	return total
}

func fmtMediaSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fK", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// ingestVersions 从文件名解析「1080p WEB-DL SDR 25fps」这类版本串。
func ingestVersions(sources []store.PendingIngest) string {
	out := make([]string, 0, 4)
	seen := make(map[string]bool, 4)
	for _, s := range sources {
		v := ingestVersion(s)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) > 3 {
		return strings.Join(out[:3], " / ") + " 等"
	}
	return strings.Join(out, " / ")
}

func ingestVersion(s store.PendingIngest) string {
	name := strings.TrimSpace(s.FileName)
	if name == "" {
		name = filepath.Base(s.StrmPath)
	}
	m := strm.Parse(name)
	parts := make([]string, 0, 4)
	if m.Resolution != "" {
		parts = append(parts, m.Resolution)
	}
	if m.Source != "" {
		parts = append(parts, m.Source)
	}
	if m.VideoRange != "" {
		parts = append(parts, m.VideoRange)
	}
	if m.FrameRate > 0 {
		parts = append(parts, fmt.Sprintf("%.0ffps", m.FrameRate))
	}
	if len(parts) == 0 {
		return strings.TrimSpace(s.VersionName)
	}
	return strings.Join(parts, " ")
}

func ingestPlatforms(sources []store.PendingIngest) []string {
	out := make([]string, 0, 2)
	seen := make(map[string]bool, 2)
	for _, s := range sources {
		m := strm.Parse(sourceFileName(s))
		if m.Platform == "" || seen[m.Platform] {
			continue
		}
		seen[m.Platform] = true
		out = append(out, m.Platform)
	}
	return out
}

func ingestReleaseGroups(sources []store.PendingIngest) []string {
	out := make([]string, 0, 2)
	seen := make(map[string]bool, 2)
	for _, s := range sources {
		m := strm.Parse(sourceFileName(s))
		if m.ReleaseGroup == "" || seen[m.ReleaseGroup] {
			continue
		}
		seen[m.ReleaseGroup] = true
		out = append(out, m.ReleaseGroup)
	}
	return out
}

func sourceFileName(s store.PendingIngest) string {
	if strings.TrimSpace(s.FileName) != "" {
		return s.FileName
	}
	return filepath.Base(s.StrmPath)
}

func ingestCategory(lib *store.Library, strmPath, mediaName string) string {
	if lib == nil {
		return ""
	}
	if strings.TrimSpace(strmPath) == "" {
		return lib.Name
	}
	for _, root := range libraryLocations(lib) {
		rel, err := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(strmPath))
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || strings.HasPrefix(rel, "../") {
			continue
		}
		parts := strings.Split(rel, "/")
		if len(parts) > 1 {
			first := strings.TrimSpace(parts[0])
			if first != "" && first != mediaName {
				return lib.Name + " / " + first
			}
		}
		return lib.Name
	}
	return lib.Name
}

func (h *Handler) ingestPhoto(g *ingestGroup) ([]byte, string) {
	candidates := make([]*store.Item, 0, 3)
	if g.series != nil {
		candidates = append(candidates, g.series)
	}
	if g.movie != nil {
		candidates = append(candidates, g.movie)
	}
	if g.sample != nil {
		candidates = append(candidates, g.sample)
	}
	for _, it := range candidates {
		if data, name := h.playbackPhoto(it); len(data) > 0 {
			return data, name
		}
	}
	return nil, ""
}

// ingestTestText 是设置页「发送测试消息」用的固定样例。
func ingestTestText() string {
	return "✅ <b>入库通知：示例剧集 (2026) S02 E01-E03</b>\n" +
		"🌟 主演：演员甲 / 演员乙 / 演员丙 等\n" +
		"🆔 影号：TMDB-293060\n" +
		"📋 规则：115\n" +
		"☁️ 来源：115网盘\n" +
		"🧱 分类：综艺 / 华语综艺\n" +
		"🎞️ 版本：1080p WEB-DL SDR 25fps\n" +
		"🧾 文件：3个\n" +
		"💾 大小：3.78G\n" +
		"📺 平台：MyTVSuper\n" +
		"👥 组名：ADWeb\n" +
		"🧮 本季：S02 共10集\n" +
		"⏱️ 时间：" + time.Now().Format("2006-01-02 15:04:05")
}
