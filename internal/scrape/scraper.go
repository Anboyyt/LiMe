// Package scrape 用 TMDB 补全条目元数据和海报。
// 只按目录名里的 TMDB ID 精确匹配，不做标题搜索，避免刮错片。
package scrape

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"lime/internal/config"
	"lime/internal/store"
	"lime/internal/tmdb"
)

type Status struct {
	Running      bool   `json:"Running"`
	Processed    int    `json:"Processed"`
	Total        int    `json:"Total"`
	LastFinished int64  `json:"LastFinished"`
	LastError    string `json:"LastError"`

	// Partial/LastNote 表示上次运行撞到了单次时长上限：已处理的条目都已落库，
	// 剩下的等下次继续。这不是错误，面板不应该标红。
	Partial  bool   `json:"Partial"`
	LastNote string `json:"LastNote"`
}

type Scraper struct {
	cfg    config.Config
	db     *store.DB
	client *tmdb.Client
	log    *slog.Logger

	mu           sync.Mutex
	running      bool
	quick        int // 正在跑的新入库精准刮削数量
	processed    int
	total        int
	lastFinished int64
	lastError    string
	partial      bool
	lastNote     string
	failStreak   int // 连续失败条数，用于区分单个坏条目和 TMDB 整体不可用

	workers      int
	paceInterval time.Duration
	timeout      time.Duration
	startAt      time.Time
	pacMu        sync.Mutex
	paceLast     time.Time

	// 背景图/海报回填是独立任务，不占用刮削的 running 标记。
	// 两者共用一把锁，避免同时对 TMDB 发两倍的图。
	backdropMu sync.Mutex
	backdrop   BackdropStatus
	poster     BackdropStatus
}

func New(cfg config.Config, db *store.DB, log *slog.Logger) *Scraper {
	client := tmdb.New(cfg.TMDBAPIKeys, cfg.TMDBLanguage, cfg.TMDBBaseURL,
		cfg.TMDBImageBase, cfg.TMDBProxy, log)

	workers := 3
	if v := strings.TrimSpace(os.Getenv("LIME_SCRAPE_WORKERS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 8 {
			workers = n
		}
	}
	interval := 50 * time.Millisecond
	if v := strings.TrimSpace(os.Getenv("LIME_SCRAPE_RATE_MS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			interval = time.Duration(n) * time.Millisecond
		}
	}
	// 单次运行的时长预算。超过它只是「本次先停下，下次继续」，不是失败；
	// LIME_SCRAPE_TIMEOUT=0 表示不设上限。
	timeout := 6 * time.Hour
	if v := strings.TrimSpace(os.Getenv("LIME_SCRAPE_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			timeout = d
		}
	}
	s := &Scraper{
		cfg: cfg, db: db, client: client, log: log,
		workers: workers, paceInterval: interval, timeout: timeout,
	}
	// 面板里保存的时长上限优先于环境变量，单位是秒；0 表示不限。
	if v, err := db.GetSetting(scrapeTimeoutKey); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			s.timeout = time.Duration(n) * time.Second
		}
	}
	s.loadStatus()
	return s
}

const scrapeTimeoutKey = "scrape_timeout"

// SetTimeoutSeconds 把时长上限（秒）保存到 settings 并立即生效，
// 0 表示不限。保存后从下一次运行开始应用。
func (s *Scraper) SetTimeoutSeconds(n int) error {
	if n < 0 {
		n = 0
	}
	if err := s.db.SetSetting(scrapeTimeoutKey, strconv.Itoa(n)); err != nil {
		return err
	}
	s.SetTimeout(time.Duration(n) * time.Second)
	return nil
}

// SetTimeout 更新单次运行时长上限，保存后从下一次运行开始生效。
func (s *Scraper) SetTimeout(d time.Duration) {
	if d < 0 {
		d = 0
	}
	s.mu.Lock()
	s.timeout = d
	s.mu.Unlock()
}

// Timeout 返回当前生效的单次运行时长上限，0 表示不限。
func (s *Scraper) Timeout() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timeout
}

const scrapeStatusKey = "scrape_status"

// recheckInterval 是「补缺任务」的重试间隔。TMDB 本身没有评分/类型/
// 时长/剧照的条目，每天重查没有意义；隔 30 天再试一次，也能覆盖
// TMDB 后来补数据的情况。
const recheckInterval = 30 * 24 * time.Hour

// loadStatus 恢复上次运行的进度摘要；Running 在进程重启后一律视为 false。
func (s *Scraper) loadStatus() {
	raw, err := s.db.GetSetting(scrapeStatusKey)
	if err != nil || raw == "" {
		return
	}
	var st Status
	if json.Unmarshal([]byte(raw), &st) != nil {
		return
	}
	s.mu.Lock()
	s.processed = st.Processed
	s.total = st.Total
	s.lastFinished = st.LastFinished
	s.lastError = st.LastError
	s.partial = st.Partial
	s.lastNote = st.LastNote
	s.mu.Unlock()
}

// saveStatus 把当前进度写到 settings，重启后面板还能看到上次运行结果。
func (s *Scraper) saveStatus() {
	s.mu.Lock()
	st := Status{
		Processed:    s.processed,
		Total:        s.total,
		LastFinished: s.lastFinished,
		LastError:    s.lastError,
		Partial:      s.partial,
		LastNote:     s.lastNote,
	}
	s.mu.Unlock()

	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = s.db.SetSetting(scrapeStatusKey, string(raw))
}

func (s *Scraper) Enabled() bool { return s != nil && s.client.Enabled() }

func (s *Scraper) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{
		Running:      s.running,
		Processed:    s.processed,
		Total:        s.total,
		LastFinished: s.lastFinished,
		LastError:    s.lastError,
		Partial:      s.partial,
		LastNote:     s.lastNote,
	}
}

func (s *Scraper) setTotal(n int) {
	s.mu.Lock()
	s.total = n
	s.mu.Unlock()
}

func (s *Scraper) advance() {
	s.mu.Lock()
	s.processed++
	n, total, start := s.processed, s.total, s.startAt
	s.mu.Unlock()

	if n%200 != 0 {
		return
	}
	pct, rate := 0, 0.0
	if total > 0 {
		pct = n * 100 / total
	}
	if !start.IsZero() {
		if sec := time.Since(start).Seconds(); sec > 0 {
			rate = float64(n) / sec
		}
	}
	s.log.Info("scrape progress", "done", n, "total", total, "pct", pct, "items_per_sec", rate)
	s.saveStatus()
}

// Run 刮削所有缺元数据、带 TMDB ID 的条目。同一时间只允许一个任务。
func (s *Scraper) Run() (int, error) {
	return s.runGuarded("scrape", s.run)
}

// RunAvatars 只补老条目的演员头像信息，不跑全量刮削。
func (s *Scraper) RunAvatars() (int, error) {
	return s.runGuarded("avatar backfill", s.runAvatars)
}

// ScrapeItems 只刮削指定条目，用于新入库插队。
// 不占用完整刮削的运行标记，可以和大批量刮削并行；TMDB 限速共用。
func (s *Scraper) ScrapeItems(ids []string) (int, error) {
	if !s.Enabled() {
		return 0, errors.New("tmdb api key not configured")
	}
	if len(ids) == 0 {
		return 0, nil
	}

	s.mu.Lock()
	s.quick++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.quick--
		s.mu.Unlock()
	}()

	ctx, cancel := s.newRunContext()
	defer cancel()

	movies := make(map[string]struct{})
	series := make(map[string]struct{})
	seasons := make(map[string]struct{})
	// 已刮过的季只补新增单集，避免每次入库重刷整季。
	seasonEpisodes := make(map[string]map[string]struct{})

	for _, id := range ids {
		it, err := s.db.GetItem(id)
		if err != nil {
			continue
		}
		switch it.Type {
		case store.TypeMovie:
			movies[it.ID] = struct{}{}
		case store.TypeSeries:
			series[it.ID] = struct{}{}
		case store.TypeSeason:
			seasons[it.ID] = struct{}{}
		case store.TypeEpisode:
			if it.SeriesID != "" {
				if ser, err := s.db.GetItem(it.SeriesID); err == nil && ser.ScrapedAt == 0 {
					series[ser.ID] = struct{}{}
				}
			}
			if it.SeasonID == "" {
				continue
			}
			season, err := s.db.GetItem(it.SeasonID)
			if err != nil {
				continue
			}
			if season.ScrapedAt == 0 {
				// 新季：整季刮，顺便补齐这一季所有单集。
				seasons[season.ID] = struct{}{}
				continue
			}
			set := seasonEpisodes[season.ID]
			if set == nil {
				set = make(map[string]struct{})
				seasonEpisodes[season.ID] = set
			}
			set[it.ID] = struct{}{}
		}
	}

	count := 0
	for id := range series {
		if it, err := s.db.GetItem(id); err == nil {
			if err := s.scrapeSeries(ctx, it); err == nil {
				count++
			}
		}
		s.pace()
	}
	for id := range seasons {
		if it, err := s.db.GetItem(id); err == nil {
			if err := s.scrapeSeason(ctx, it); err == nil {
				count++
			}
		}
		s.pace()
	}
	for id, only := range seasonEpisodes {
		if it, err := s.db.GetItem(id); err == nil {
			if err := s.scrapeSeasonEpisodes(ctx, it, only); err == nil {
				count++
			}
		}
		s.pace()
	}
	for id := range movies {
		if it, err := s.db.GetItem(id); err == nil {
			if err := s.scrapeMovie(ctx, it); err == nil {
				count++
			}
		}
		s.pace()
	}

	s.log.Info("ingest scrape done", "items", len(ids), "scraped", count)
	return count, nil
}

// scrapeSeasonEpisodes 只更新指定单集，用于已刮过的季里新增的集数。
func (s *Scraper) scrapeSeasonEpisodes(ctx context.Context, it *store.Item, only map[string]struct{}) error {
	// 第 0 季是 TMDB 的 Specials，必须按 0 查；回退成第 1 季会拿错数据，
	// 而且 EpisodeByNumber 用 1 永远匹配不到第 0 季的单集。
	seasonNo := it.IndexNumber
	if seasonNo < 0 {
		seasonNo = 0
	}
	sd, err := s.client.SeasonDetail(ctx, it.TmdbID, seasonNo)
	if err != nil {
		return err
	}

	var poster, backdrop, rating string
	if parent, err := s.db.GetItem(it.ParentID); err == nil {
		poster = parent.ImagePath
		backdrop = parent.BackdropPath
		rating = parent.OfficialRating
	}

	for _, ep := range sd.Episodes {
		item, err := s.db.EpisodeByNumber(it.ParentID, seasonNo, ep.EpisodeNumber)
		if err != nil {
			continue
		}
		if _, ok := only[item.ID]; !ok {
			continue
		}
		image := poster
		if ep.StillPath != "" {
			if still, err := s.client.DownloadImage(ctx, ep.StillPath, tmdb.StillSize, s.cfg.DataDir); err == nil && still != "" {
				image = still
			}
		}
		if err := s.db.UpdateEpisodeMetadata(item.ID, ep.Name, ep.Overview, ep.AirDate,
			minutesToTicks(ep.Runtime), image, backdrop, rating); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scraper) runGuarded(label string, fn func() (int, error)) (int, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return 0, errors.New("scrape already running")
	}
	s.running = true
	s.processed = 0
	s.total = 0
	s.lastError = ""
	s.partial = false
	s.lastNote = ""
	s.failStreak = 0
	s.mu.Unlock()
	s.saveStatus()

	start := time.Now()
	processed, err := fn()

	// 单次时长预算到期不是失败：已处理的条目都落库了，剩余的下次继续。
	// 统一在这里兜底，run/runAvatars 内部提前 return 的情况也能被覆盖。
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		s.markPartial(processed)
		err = nil
	}

	s.mu.Lock()
	s.running = false
	s.lastFinished = time.Now().Unix()
	if err != nil {
		s.lastError = err.Error()
	}
	note, partial := s.lastNote, s.partial
	s.mu.Unlock()
	s.saveStatus()

	s.log.Info(label+" finished", "items", processed, "dur", time.Since(start).String(),
		"err", err, "partial", partial, "note", note)
	return processed, err
}

// newRunContext 创建本次运行的 ctx。timeout<=0 表示不设时长上限。
func (s *Scraper) newRunContext() (context.Context, context.CancelFunc) {
	timeout := s.Timeout()
	if timeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), timeout)
}

// markPartial 记录「撞到单次时长上限」的状态。它不进 LastError，
// 面板用中性提示展示，避免每天定时刮削都被标成一次失败。
func (s *Scraper) markPartial(processed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.partial {
		return
	}
	s.partial = true
	s.lastError = ""
	if s.timeout > 0 {
		s.lastNote = fmt.Sprintf("本次已达到单次运行上限 %s，处理 %d 条后暂停，剩余条目下次继续",
			s.timeout, processed)
	} else {
		s.lastNote = fmt.Sprintf("本次运行已中止，处理 %d 条，剩余条目下次继续", processed)
	}
}

// noteFailure 统计连续失败。单个坏条目（404 已单独处理、偶发超时）只进
// failures 表，不把整个面板标红；只有连续失败很多才认为是 TMDB/代理不可用。
func (s *Scraper) noteFailure(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStreak++
	if s.failStreak == 20 && s.lastError == "" {
		s.lastError = "TMDB 请求连续失败 20 次，最后一次：" + err.Error()
	}
}

func (s *Scraper) noteSuccess() {
	s.mu.Lock()
	s.failStreak = 0
	s.mu.Unlock()
}

// runAvatars 只处理已刮削但演员 JSON 还是旧格式的条目。
func (s *Scraper) runAvatars() (int, error) {
	if !s.Enabled() {
		return 0, errors.New("tmdb api key not configured")
	}

	ctx, cancel := s.newRunContext()
	defer cancel()

	s.mu.Lock()
	s.startAt = time.Now()
	s.mu.Unlock()

	movies, err := s.db.ItemsMissingPeopleAvatars(store.TypeMovie)
	if err != nil {
		return 0, err
	}
	series, err := s.db.ItemsMissingPeopleAvatars(store.TypeSeries)
	if err != nil {
		return 0, err
	}
	total := len(movies) + len(series)
	s.setTotal(total)
	if total == 0 {
		return 0, nil
	}
	s.log.Info("avatar backfill started", "movies", len(movies), "series", len(series))
	n := s.backfillAvatars(ctx, movies, series)
	if ctx.Err() != nil {
		s.markPartial(n)
	}
	return n, nil
}

func (s *Scraper) run() (int, error) {
	if !s.Enabled() {
		return 0, errors.New("tmdb api key not configured")
	}

	ctx, cancel := s.newRunContext()
	defer cancel()

	s.mu.Lock()
	s.startAt = time.Now()
	s.mu.Unlock()

	checkedBefore := time.Now().Add(-recheckInterval).Unix()

	movies, err := s.db.ItemsForScrape(store.TypeMovie)
	if err != nil {
		return 0, err
	}
	series, err := s.db.ItemsForScrape(store.TypeSeries)
	if err != nil {
		return 0, err
	}
	seasons, err := s.db.ItemsForScrape(store.TypeSeason)
	if err != nil {
		return 0, err
	}

	// 已刮过但没时长的条目：老库升级后补一次。
	missingMovies, err := s.db.ItemsMissingRuntime(store.TypeMovie, checkedBefore)
	if err != nil {
		return 0, err
	}
	missingSeasons, err := s.db.SeriesSeasonsMissingEpisodeRuntime(checkedBefore)
	if err != nil {
		return 0, err
	}
	missingGenresMovies, err := s.db.ItemsMissingGenres(store.TypeMovie, checkedBefore)
	if err != nil {
		return 0, err
	}
	missingGenresSeries, err := s.db.ItemsMissingGenres(store.TypeSeries, checkedBefore)
	if err != nil {
		return 0, err
	}
	missingStills, err := s.db.SeriesSeasonsMissingEpisodeStill(checkedBefore)
	if err != nil {
		return 0, err
	}
	missingRatingsMovies, err := s.db.ItemsMissingOfficialRating(store.TypeMovie, checkedBefore)
	if err != nil {
		return 0, err
	}
	missingRatingsSeries, err := s.db.ItemsMissingOfficialRating(store.TypeSeries, checkedBefore)
	if err != nil {
		return 0, err
	}
	missingAvatarsMovies, err := s.db.ItemsMissingPeopleAvatars(store.TypeMovie)
	if err != nil {
		return 0, err
	}
	missingAvatarsSeries, err := s.db.ItemsMissingPeopleAvatars(store.TypeSeries)
	if err != nil {
		return 0, err
	}
	// 老库升级后补 ClearLogo：每个电影/剧集只需再请求一次图片列表。
	missingLogosMovies, err := s.db.ItemsMissingLogo(store.TypeMovie)
	if err != nil {
		return 0, err
	}
	missingLogosSeries, err := s.db.ItemsMissingLogo(store.TypeSeries)
	if err != nil {
		return 0, err
	}

	// 季已刮过、但后来新增的单集不会被上面的查询覆盖，单独按季补。
	unscrapedEpisodes, err := s.db.SeasonsWithUnscrapedEpisodes()
	if err != nil {
		return 0, err
	}
	// 整季本来就没刮过的，交给上面的主任务；这里只补「季已刮过」的组。
	if len(seasons) > 0 {
		inMain := make(map[string]struct{}, len(seasons))
		for _, it := range seasons {
			inMain[it.ID] = struct{}{}
		}
		filtered := make([]store.SeasonEpisodes, 0, len(unscrapedEpisodes))
		for _, g := range unscrapedEpisodes {
			if _, ok := inMain[g.SeasonID]; !ok {
				filtered = append(filtered, g)
			}
		}
		unscrapedEpisodes = filtered
	}
	episodeCount := 0
	for _, g := range unscrapedEpisodes {
		episodeCount += len(g.EpisodeIDs)
	}

	total := len(movies) + len(series) + len(seasons) + len(missingMovies) +
		len(missingSeasons) + len(missingGenresMovies) + len(missingGenresSeries) +
		len(missingStills) + len(missingRatingsMovies) + len(missingRatingsSeries) +
		len(missingAvatarsMovies) + len(missingAvatarsSeries) + episodeCount +
		len(missingLogosMovies) + len(missingLogosSeries)
	s.setTotal(total)
	if total == 0 {
		return 0, nil
	}
	s.log.Info("scrape started", "movies", len(movies), "series", len(series),
		"seasons", len(seasons), "runtime_movies", len(missingMovies),
		"runtime_seasons", len(missingSeasons), "genres_movies", len(missingGenresMovies),
		"genres_series", len(missingGenresSeries), "stills_seasons", len(missingStills),
		"rating_movies", len(missingRatingsMovies), "rating_series", len(missingRatingsSeries),
		"avatar_movies", len(missingAvatarsMovies), "avatar_series", len(missingAvatarsSeries),
		"logo_movies", len(missingLogosMovies), "logo_series", len(missingLogosSeries),
		"episode_seasons", len(unscrapedEpisodes), "episodes", episodeCount,
		"workers", s.workers, "rate_ms", s.paceInterval.Milliseconds(),
		"timeout", s.timeout.String())

	// 老库的演员头像先补：请求量小，客户端的演员栏不用等主任务跑完。
	count := s.backfillAvatars(ctx, missingAvatarsMovies, missingAvatarsSeries)

	type job struct {
		kind int
		item *store.Item
	}
	jobs := make([]job, 0, len(movies)+len(series)+len(seasons)+len(missingLogosMovies)+len(missingLogosSeries))
	// 老库补 logo 排在最前：每个条目只多两次请求，先让客户端用上透明标题，
	// 也避免大库首轮刮削耗时过长时 logo 任务被 6 小时超时截掉。
	for _, it := range missingLogosMovies {
		jobs = append(jobs, job{3, it})
	}
	for _, it := range missingLogosSeries {
		jobs = append(jobs, job{3, it})
	}
	for _, it := range movies {
		jobs = append(jobs, job{0, it})
	}
	for _, it := range series {
		jobs = append(jobs, job{1, it})
	}
	for _, it := range seasons {
		jobs = append(jobs, job{2, it})
	}

	var (
		countMu sync.Mutex
		wg      sync.WaitGroup
	)
	ch := make(chan job)
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				if ctx.Err() != nil {
					// 预算已用完，剩下的条目这次不处理，也不推进进度，
					// 面板显示的 processed/total 才是真实的完成量。
					continue
				}
				var err error
				switch j.kind {
				case 0:
					err = s.scrapeMovie(ctx, j.item)
				case 1:
					err = s.scrapeSeries(ctx, j.item)
				case 2:
					err = s.scrapeSeason(ctx, j.item)
				default:
					err = s.scrapeLogoOnly(ctx, j.item)
				}
				if err != nil {
					var nf *tmdb.NotFoundError
					if errors.As(err, &nf) {
						// TMDB 上确实没有这个资源（错误 ID / 不存在的季），
						// 标记已处理避免每天重试，也不污染 LastError。
						_ = s.db.RecordScrapeFailure(j.item.ID, store.FailureNotFound,
							"TMDB 404，ID 可能已失效: "+j.item.TmdbID)
						if j.kind == 2 {
							_ = s.db.MarkTreeScraped(j.item.ID)
						} else {
							// 404 也算尝试过，避免补齐队列每天重查同一批死 ID。
							_ = s.db.UpdateItemMetadata(j.item.ID, store.MetadataUpdate{MetadataChecked: true})
						}
						s.log.Info("scrape skipped (tmdb 404)", "item", j.item.ID, "name", j.item.Name)
						// 404 说明 TMDB 是通的，重置连续失败计数。
						s.noteSuccess()
					} else if ctx.Err() != nil {
						// 预算用完后余下的请求会被立即取消，不算 TMDB 出错，
						// 也不写 failures 表，下次运行会重新处理。
					} else {
						_ = s.db.RecordScrapeFailure(j.item.ID, store.FailureAPI, err.Error())
						s.log.Warn("scrape failed", "item", j.item.ID, "name", j.item.Name, "err", err)
						s.noteFailure(err)
					}
				} else {
					s.noteSuccess()
					countMu.Lock()
					count++
					countMu.Unlock()
				}
				s.advance()
				s.pace()
			}
		}()
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		ch <- j
	}
	close(ch)
	wg.Wait()
	if ctx.Err() != nil {
		// 时长预算用完：把手上的进度当结果返回，不计为失败。
		s.markPartial(count)
		return count, nil
	}

	count += s.backfillEpisodes(ctx, unscrapedEpisodes)
	count += s.backfillRuntime(ctx, missingMovies, missingSeasons)
	count += s.backfillGenres(ctx, missingGenresMovies, missingGenresSeries)
	count += s.backfillStills(ctx, missingStills)
	count += s.backfillRatings(ctx, missingRatingsMovies, missingRatingsSeries)
	if n, err := s.db.PropagateSeriesRatings(); err != nil {
		s.log.Warn("propagate series ratings failed", "err", err)
	} else {
		s.log.Info("series ratings propagated", "items", n)
	}
	return count, nil
}

// backfillEpisodes 按季补齐「季已刮过、后来新增」的单集。
func (s *Scraper) backfillEpisodes(ctx context.Context, groups []store.SeasonEpisodes) int {
	count := 0
	for _, g := range groups {
		if ctx.Err() != nil {
			return count
		}
		it := &store.Item{
			ID:          g.SeasonID,
			ParentID:    g.SeriesID,
			Type:        store.TypeSeason,
			TmdbID:      g.TmdbID,
			IndexNumber: g.Season,
		}
		only := make(map[string]struct{}, len(g.EpisodeIDs))
		for _, id := range g.EpisodeIDs {
			only[id] = struct{}{}
		}
		if err := s.scrapeSeasonEpisodes(ctx, it, only); err != nil {
			var nf *tmdb.NotFoundError
			if errors.As(err, &nf) {
				_ = s.db.MarkTreeScraped(g.SeasonID)
				s.log.Info("episode backfill skipped (tmdb 404)", "season", g.SeasonID)
			} else {
				s.log.Warn("backfill episodes failed",
					"season", g.SeasonID, "episodes", len(g.EpisodeIDs), "err", err)
			}
		} else {
			count += len(g.EpisodeIDs)
		}
		for range g.EpisodeIDs {
			s.advance()
		}
		s.pace()
	}
	return count
}

// backfillRatings 给已刮过但没有分级的电影/剧集补一次。
func (s *Scraper) backfillRatings(ctx context.Context, movies, series []*store.Item) int {
	count := 0
	for _, it := range movies {
		if ctx.Err() != nil {
			return count
		}
		m, err := s.client.Movie(ctx, it.TmdbID)
		if err != nil {
			s.log.Warn("rating movie failed", "item", it.ID, "err", err)
		} else if rating := m.Certification(); rating != "" {
			if err := s.db.UpdateItemRating(it.ID, rating); err != nil {
				s.log.Warn("update movie rating failed", "item", it.ID, "err", err)
			} else {
				count++
			}
		}
		s.advance()
		s.pace()
	}

	for _, it := range series {
		if ctx.Err() != nil {
			return count
		}
		tv, err := s.client.TVShow(ctx, it.TmdbID)
		if err != nil {
			s.log.Warn("rating series failed", "item", it.ID, "err", err)
		} else if rating := tv.ContentRating(); rating != "" {
			if err := s.db.UpdateItemRating(it.ID, rating); err != nil {
				s.log.Warn("update series rating failed", "item", it.ID, "err", err)
			} else {
				count++
			}
		}
		s.advance()
		s.pace()
	}
	return count
}

// backfillStills 给已经刮削过的单集补 TMDB 剧照。
func (s *Scraper) backfillStills(ctx context.Context, seasons []store.SeriesSeason) int {
	count := 0
	for _, ss := range seasons {
		if ctx.Err() != nil {
			return count
		}
		sd, err := s.client.SeasonDetail(ctx, ss.TmdbID, ss.Season)
		if err != nil {
			s.log.Warn("stills season failed", "series", ss.SeriesID, "season", ss.Season, "err", err)
			s.advance()
			s.pace()
			continue
		}
		for _, ep := range sd.Episodes {
			if ep.StillPath == "" {
				continue
			}
			item, err := s.db.EpisodeByNumber(ss.SeriesID, ss.Season, ep.EpisodeNumber)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				continue
			}
			still, err := s.client.DownloadImage(ctx, ep.StillPath, tmdb.StillSize, s.cfg.DataDir)
			if err != nil {
				s.log.Warn("download episode still failed", "item", item.ID, "err", err)
				continue
			}
			if still == "" {
				continue
			}
			if err := s.db.UpdateItemImage(item.ID, still); err != nil {
				s.log.Warn("update episode still failed", "item", item.ID, "err", err)
				continue
			}
			count++
		}
		// 拿到季详情就算补过一轮；TMDB 这一季没有剧照也不会每天重拉。
		if ss.SeasonID != "" {
			_ = s.db.MarkSeasonStillsChecked(ss.SeasonID)
		}
		s.advance()
		s.pace()
	}
	return count
}

// backfillGenres 给已刮削但没有类型/演员的老条目补一轮。
func (s *Scraper) backfillGenres(ctx context.Context, movies, series []*store.Item) int {
	count := 0
	for _, it := range movies {
		if ctx.Err() != nil {
			return count
		}
		m, err := s.client.Movie(ctx, it.TmdbID)
		if err != nil {
			s.log.Warn("genres movie failed", "item", it.ID, "err", err)
		} else {
			s.savePeople(m.Credits.Cast)
			if err := s.db.UpdateItemMetadata(it.ID, store.MetadataUpdate{
				MetadataChecked: true,
				Genres:          genresJSON(m.Genres),
				People:          peopleJSON(m.Credits.Cast),
			}); err != nil {
				s.log.Warn("update movie genres failed", "item", it.ID, "err", err)
			} else {
				count++
			}
		}
		s.advance()
		s.pace()
	}

	for _, it := range series {
		if ctx.Err() != nil {
			return count
		}
		tv, err := s.client.TVShow(ctx, it.TmdbID)
		if err != nil {
			s.log.Warn("genres series failed", "item", it.ID, "err", err)
		} else {
			s.savePeople(tv.Credits.Cast)
			if err := s.db.UpdateItemMetadata(it.ID, store.MetadataUpdate{
				MetadataChecked: true,
				Genres:          genresJSON(tv.Genres),
				People:          peopleJSON(tv.Credits.Cast),
			}); err != nil {
				s.log.Warn("update series genres failed", "item", it.ID, "err", err)
			} else {
				count++
			}
		}
		s.advance()
		s.pace()
	}
	return count
}

func genresJSON(gs []tmdb.Genre) string {
	names := make([]string, 0, len(gs))
	for _, g := range gs {
		if g.Name != "" {
			names = append(names, g.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	b, _ := json.Marshal(names)
	return string(b)
}

type personJSON struct {
	Name     string `json:"Name"`
	Role     string `json:"Role,omitempty"`
	Type     string `json:"Type"`
	ImageTag string `json:"ImageTag"`
}

func peopleJSON(cast []tmdb.Credit) string {
	if len(cast) == 0 {
		return ""
	}
	if len(cast) > 15 {
		cast = cast[:15]
	}
	people := make([]personJSON, 0, len(cast))
	for _, c := range cast {
		if c.Name == "" {
			continue
		}
		people = append(people, personJSON{
			Name: c.Name, Role: c.Character, Type: "Actor",
			ImageTag: imageTag(c.ProfilePath),
		})
	}
	if len(people) == 0 {
		return ""
	}
	b, _ := json.Marshal(people)
	return string(b)
}

// imageTag 给头像生成稳定的缓存标记；路径为空表示没有头像。
func imageTag(profilePath string) string {
	if profilePath == "" {
		return ""
	}
	sum := sha1.Sum([]byte("profile:" + profilePath))
	return hex.EncodeToString(sum[:])[:12]
}

// savePeople 把所有演员写进 persons 索引（没有头像也写），供 /Persons 和
// PersonIds 反查使用；头像地址真正下载留到客户端请求时。
func (s *Scraper) savePeople(cast []tmdb.Credit) {
	if len(cast) > 15 {
		cast = cast[:15]
	}
	people := make([]store.PersonImage, 0, len(cast))
	for _, c := range cast {
		if c.Name == "" {
			continue
		}
		imageURL := ""
		if c.ProfilePath != "" {
			imageURL = s.cfg.TMDBImageBase + "/" + tmdb.ProfileSize + c.ProfilePath
		}
		people = append(people, store.PersonImage{
			ID:       store.PersonID(c.Name),
			Name:     c.Name,
			ImageURL: imageURL,
		})
	}
	if err := s.db.UpsertPersons(people); err != nil {
		s.log.Warn("save people failed", "err", err)
	}
}

// backfillAvatars 给老条目重写演员 JSON（补头像标记）并记录头像地址。
// 1.6 万条全串行要跑十几个小时，这里按并发 worker 处理，限速仍共用 s.pace()。
func (s *Scraper) backfillAvatars(ctx context.Context, movies, series []*store.Item) int {
	type job struct {
		item   *store.Item
		series bool
	}

	workers := s.workers * 2
	if workers < 2 {
		workers = 2
	}
	if workers > 8 {
		workers = 8
	}

	var (
		mu    sync.Mutex
		count int
		wg    sync.WaitGroup
	)
	jobs := make(chan job)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					s.advance()
					continue
				}
				ok := false
				if j.series {
					tv, err := s.client.TVShow(ctx, j.item.TmdbID)
					if err != nil {
						s.log.Warn("avatar series failed", "item", j.item.ID, "err", err)
					} else {
						s.savePeople(tv.Credits.Cast)
						if err := s.db.UpdateItemPeople(j.item.ID, peopleJSON(tv.Credits.Cast)); err != nil {
							s.log.Warn("update series people failed", "item", j.item.ID, "err", err)
						} else {
							ok = true
						}
					}
				} else {
					m, err := s.client.Movie(ctx, j.item.TmdbID)
					if err != nil {
						s.log.Warn("avatar movie failed", "item", j.item.ID, "err", err)
					} else {
						s.savePeople(m.Credits.Cast)
						if err := s.db.UpdateItemPeople(j.item.ID, peopleJSON(m.Credits.Cast)); err != nil {
							s.log.Warn("update movie people failed", "item", j.item.ID, "err", err)
						} else {
							ok = true
						}
					}
				}
				if ok {
					mu.Lock()
					count++
					mu.Unlock()
				}
				s.advance()
				s.pace()
			}
		}()
	}

	for _, it := range movies {
		jobs <- job{item: it}
	}
	for _, it := range series {
		jobs <- job{item: it, series: true}
	}
	close(jobs)
	wg.Wait()
	return count
}

// backfillRuntime 给已经刮过、但时长仍为 0 的电影和单集补 RunTimeTicks。
func (s *Scraper) backfillRuntime(ctx context.Context, movies []*store.Item, seasons []store.SeriesSeason) int {
	count := 0

	for _, it := range movies {
		if ctx.Err() != nil {
			return count
		}
		m, err := s.client.Movie(ctx, it.TmdbID)
		if err != nil {
			s.log.Warn("runtime movie failed", "item", it.ID, "err", err)
		} else if ticks := minutesToTicks(m.Runtime); ticks > 0 {
			if err := s.db.UpdateItemRuntime(it.ID, ticks); err != nil {
				s.log.Warn("update movie runtime failed", "item", it.ID, "err", err)
			} else {
				count++
			}
		}
		s.advance()
		s.pace()
	}

	tvRuntime := map[string]int{}
	for _, ss := range seasons {
		if ctx.Err() != nil {
			return count
		}
		fallback, ok := tvRuntime[ss.TmdbID]
		if !ok {
			if tv, err := s.client.TVShow(ctx, ss.TmdbID); err == nil && len(tv.EpisodeRunTime) > 0 {
				fallback = tv.EpisodeRunTime[0]
			}
			tvRuntime[ss.TmdbID] = fallback
			s.pace()
		}

		sd, err := s.client.SeasonDetail(ctx, ss.TmdbID, ss.Season)
		if err != nil {
			s.log.Warn("runtime season failed", "series", ss.SeriesID, "season", ss.Season, "err", err)
			s.advance()
			s.pace()
			continue
		}
		for _, ep := range sd.Episodes {
			runtime := ep.Runtime
			if runtime <= 0 {
				runtime = fallback
			}
			ticks := minutesToTicks(runtime)
			if ticks <= 0 {
				continue
			}
			item, err := s.db.EpisodeByNumber(ss.SeriesID, ss.Season, ep.EpisodeNumber)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				s.log.Warn("runtime episode lookup failed", "series", ss.SeriesID,
					"season", ss.Season, "episode", ep.EpisodeNumber, "err", err)
				continue
			}
			if err := s.db.UpdateItemRuntime(item.ID, ticks); err != nil {
				s.log.Warn("update episode runtime failed", "item", item.ID, "err", err)
				continue
			}
			count++
		}
		// 拿到季详情就算补过一轮；TMDB 没有单集时长也不会每天重拉整季。
		if ss.SeasonID != "" {
			_ = s.db.MarkSeasonRuntimeChecked(ss.SeasonID)
		}
		s.advance()
		s.pace()
	}
	return count
}

func minutesToTicks(minutes int) int64 {
	if minutes <= 0 {
		return 0
	}
	return int64(minutes) * 60 * 10_000_000
}

// downloadLogo 挑一张 ClearLogo 并下载。第二个返回值表示「已尝试」：
// TMDB 没有 logo 也算尝试成功，下载失败则留待下次重试。
func (s *Scraper) downloadLogo(ctx context.Context, logos []tmdb.Image, itemID string) (string, bool) {
	path := tmdb.PickLogo(logos, s.cfg.TMDBLanguage)
	if path == "" {
		_ = s.db.ClearScrapeFailure(itemID, store.FailureLogo)
		return "", true
	}
	rel, err := s.client.DownloadImage(ctx, path, tmdb.LogoSize, s.cfg.DataDir)
	if err != nil {
		s.log.Warn("download logo failed", "item", itemID, "err", err)
		_ = s.db.RecordScrapeFailure(itemID, store.FailureLogo, err.Error())
		return "", false
	}
	_ = s.db.ClearScrapeFailure(itemID, store.FailureLogo)
	return rel, true
}

func (s *Scraper) scrapeMovie(ctx context.Context, it *store.Item) error {
	m, err := s.client.Movie(ctx, it.TmdbID)
	if err != nil {
		return err
	}
	s.savePeople(m.Credits.Cast)

	poster, err := s.client.DownloadImage(ctx, m.PosterPath, tmdb.PosterSize, s.cfg.DataDir)
	if err != nil {
		s.log.Warn("download poster failed", "item", it.ID, "err", err)
		_ = s.db.RecordScrapeFailure(it.ID, store.FailurePoster, err.Error())
	} else {
		_ = s.db.ClearScrapeFailure(it.ID, store.FailurePoster)
	}
	backdrop, err := s.client.DownloadImage(ctx, m.BackdropPath, tmdb.BackdropSize, s.cfg.DataDir)
	if err != nil {
		s.log.Warn("download backdrop failed", "item", it.ID, "err", err)
		_ = s.db.RecordScrapeFailure(it.ID, store.FailureBackdrop, err.Error())
	} else {
		_ = s.db.ClearScrapeFailure(it.ID, store.FailureBackdrop)
	}
	logo, logoChecked := s.downloadLogo(ctx, m.Images.Logos, it.ID)

	// 详情接口成功了，之前记下的 404 / 接口错误可以清掉。
	_ = s.db.ClearScrapeFailure(it.ID, store.FailureNotFound, store.FailureAPI)

	return s.db.UpdateItemMetadata(it.ID, store.MetadataUpdate{
		Overview:        m.Overview,
		OriginalTitle:   m.OriginalTitle,
		PremiereDate:    m.ReleaseDate,
		CommunityRating: m.VoteAverage,
		RunTimeTicks:    minutesToTicks(m.Runtime),
		ImagePath:       poster,
		BackdropPath:    backdrop,
		LogoPath:        logo,
		LogoChecked:     logoChecked,
		MetadataChecked: true,
		Genres:          genresJSON(m.Genres),
		People:          peopleJSON(m.Credits.Cast),
		OfficialRating:  m.Certification(),
	})
}

// scrapeLogoOnly 给已刮过的老条目补 logo：只拉图片列表，不重复取 credits。
func (s *Scraper) scrapeLogoOnly(ctx context.Context, it *store.Item) error {
	var (
		logos []tmdb.Image
		err   error
	)
	if it.Type == store.TypeMovie {
		logos, err = s.client.MovieImages(ctx, it.TmdbID)
	} else {
		logos, err = s.client.TVImages(ctx, it.TmdbID)
	}
	if err != nil {
		return err
	}
	logo, ok := s.downloadLogo(ctx, logos, it.ID)
	if !ok {
		return errors.New("download logo failed")
	}
	return s.db.UpdateItemLogo(it.ID, logo)
}

func (s *Scraper) scrapeSeries(ctx context.Context, it *store.Item) error {
	tv, err := s.client.TVShow(ctx, it.TmdbID)
	if err != nil {
		return err
	}
	s.savePeople(tv.Credits.Cast)

	poster, err := s.client.DownloadImage(ctx, tv.PosterPath, tmdb.PosterSize, s.cfg.DataDir)
	if err != nil {
		s.log.Warn("download poster failed", "item", it.ID, "err", err)
		_ = s.db.RecordScrapeFailure(it.ID, store.FailurePoster, err.Error())
	} else {
		_ = s.db.ClearScrapeFailure(it.ID, store.FailurePoster)
	}
	backdrop, err := s.client.DownloadImage(ctx, tv.BackdropPath, tmdb.BackdropSize, s.cfg.DataDir)
	if err != nil {
		s.log.Warn("download backdrop failed", "item", it.ID, "err", err)
		_ = s.db.RecordScrapeFailure(it.ID, store.FailureBackdrop, err.Error())
	} else {
		_ = s.db.ClearScrapeFailure(it.ID, store.FailureBackdrop)
	}
	logo, logoChecked := s.downloadLogo(ctx, tv.Images.Logos, it.ID)

	// 详情接口成功了，之前记下的 404 / 接口错误可以清掉。
	_ = s.db.ClearScrapeFailure(it.ID, store.FailureNotFound, store.FailureAPI)

	return s.db.UpdateItemMetadata(it.ID, store.MetadataUpdate{
		Overview:        tv.Overview,
		OriginalTitle:   tv.OriginalName,
		PremiereDate:    tv.FirstAirDate,
		CommunityRating: tv.VoteAverage,
		ImagePath:       poster,
		BackdropPath:    backdrop,
		LogoPath:        logo,
		LogoChecked:     logoChecked,
		MetadataChecked: true,
		Genres:          genresJSON(tv.Genres),
		People:          peopleJSON(tv.Credits.Cast),
		OfficialRating:  tv.ContentRating(),
	})
}

// scrapeSeason 拿整季的分集信息，顺便把剧集海报继承给单集。
func (s *Scraper) scrapeSeason(ctx context.Context, it *store.Item) error {
	// 第 0 季是 TMDB 的 Specials，必须按 0 查；回退成第 1 季会拿错数据，
	// 而且 EpisodeByNumber 用 1 永远匹配不到第 0 季的单集。
	seasonNo := it.IndexNumber
	if seasonNo < 0 {
		seasonNo = 0
	}

	sd, err := s.client.SeasonDetail(ctx, it.TmdbID, seasonNo)
	if err != nil {
		return err
	}

	poster, err := s.client.DownloadImage(ctx, sd.PosterPath, tmdb.PosterSize, s.cfg.DataDir)
	if err != nil {
		s.log.Warn("download season poster failed", "item", it.ID, "err", err)
	}

	// 季没海报时用剧集海报，分级和 logo 也继承剧集
	var backdrop, rating, logo string
	if parent, err := s.db.GetItem(it.ParentID); err == nil {
		if poster == "" {
			poster = parent.ImagePath
		}
		backdrop = parent.BackdropPath
		logo = parent.LogoPath
		rating = parent.OfficialRating
	}

	if err := s.db.UpdateItemMetadata(it.ID, store.MetadataUpdate{
		Overview:       sd.Overview,
		PremiereDate:   sd.AirDate,
		ImagePath:      poster,
		BackdropPath:   backdrop,
		LogoPath:       logo,
		LogoChecked:    true,
		OfficialRating: rating,
	}); err != nil {
		return err
	}

	for _, ep := range sd.Episodes {
		item, err := s.db.EpisodeByNumber(it.ParentID, seasonNo, ep.EpisodeNumber)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		image := poster
		if ep.StillPath != "" {
			still, err := s.client.DownloadImage(ctx, ep.StillPath, tmdb.StillSize, s.cfg.DataDir)
			if err != nil {
				s.log.Warn("download episode still failed", "item", item.ID, "err", err)
			} else if still != "" {
				image = still
			}
		}
		if err := s.db.UpdateEpisodeMetadata(item.ID, ep.Name, ep.Overview, ep.AirDate,
			minutesToTicks(ep.Runtime), image, backdrop, rating); err != nil {
			return err
		}
	}
	return nil
}

// pace 控制请求频率，别把 TMDB 打挂。
// recordAPIError 把 TMDB 接口错误分到失败报告：404 单独一类，其余算接口错误。
func (s *Scraper) recordAPIError(itemID string, err error) {
	var nf *tmdb.NotFoundError
	if errors.As(err, &nf) {
		_ = s.db.RecordScrapeFailure(itemID, store.FailureNotFound, "TMDB 404，ID 可能已失效")
		return
	}
	_ = s.db.RecordScrapeFailure(itemID, store.FailureAPI, err.Error())
}

func (s *Scraper) pace() {
	s.pacMu.Lock()
	defer s.pacMu.Unlock()
	if wait := s.paceInterval - time.Since(s.paceLast); wait > 0 {
		time.Sleep(wait)
	}
	s.paceLast = time.Now()
}

// SetCredentials 运行时更新 TMDB Key/代理（面板修改后立即生效）。
func (s *Scraper) SetCredentials(keys []string, proxy string) error {
	s.mu.Lock()
	if s.running || s.quick > 0 {
		s.mu.Unlock()
		return errors.New("刮削进行中，稍后再修改")
	}
	s.cfg.TMDBAPIKeys = keys
	s.cfg.TMDBProxy = proxy
	s.mu.Unlock()

	s.client = tmdb.New(keys, s.cfg.TMDBLanguage, s.cfg.TMDBBaseURL,
		s.cfg.TMDBImageBase, proxy, s.log)
	return nil
}
