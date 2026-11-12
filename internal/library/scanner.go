package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lime/internal/nfo"
	"lime/internal/pinyin"
	"lime/internal/store"
	"lime/internal/strm"
)

// batchSize 是一批写入的条目数。46 万个 STRM 必须合并事务，否则会慢到不可用。
const batchSize = 500

type Scanner struct {
	db      *store.DB
	log     *slog.Logger
	workers int

	mu           sync.Mutex
	running      bool
	lastStarted  int64
	lastFinished int64
	lastCount    int
	lastError    string

	curLibrary  string
	scanCurrent int
	scanTotal   int
}

// Status 是给运维面板看的扫描状态快照。
type Status struct {
	Running      bool   `json:"Running"`
	LastStarted  int64  `json:"LastStarted"`
	LastFinished int64  `json:"LastFinished"`
	LastCount    int    `json:"LastCount"`
	LastError    string `json:"LastError"`
	Library      string `json:"Library"`
	Current      int    `json:"Current"`
	Total        int    `json:"Total"`
}

func (s *Scanner) setLastError(msg string) {
	s.mu.Lock()
	s.lastError = msg
	s.mu.Unlock()
}

func (s *Scanner) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{
		Running:      s.running,
		LastStarted:  s.lastStarted,
		LastFinished: s.lastFinished,
		LastCount:    s.lastCount,
		LastError:    s.lastError,
		Library:      s.curLibrary,
		Current:      s.scanCurrent,
		Total:        s.scanTotal,
	}
}

func New(db *store.DB, log *slog.Logger) *Scanner {
	s := &Scanner{db: db, log: log, workers: 12}
	s.loadScanStatus()
	return s
}

const scanStatusKey = "scan_status"

// loadScanStatus 恢复上次扫描结果；Running 在进程重启后一律视为 false。
func (s *Scanner) loadScanStatus() {
	raw, err := s.db.GetSetting(scanStatusKey)
	if err != nil || raw == "" {
		return
	}
	var st Status
	if json.Unmarshal([]byte(raw), &st) != nil {
		return
	}
	s.mu.Lock()
	s.lastStarted = st.LastStarted
	s.lastFinished = st.LastFinished
	s.lastCount = st.LastCount
	s.lastError = st.LastError
	s.mu.Unlock()
}

// saveScanStatus 把最近一次扫描结果写到 settings，重启后面板还能看到。
func (s *Scanner) saveScanStatus() {
	s.mu.Lock()
	st := Status{
		LastStarted:  s.lastStarted,
		LastFinished: s.lastFinished,
		LastCount:    s.lastCount,
		LastError:    s.lastError,
	}
	s.mu.Unlock()

	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = s.db.SetSetting(scanStatusKey, string(raw))
}

type fileRef struct {
	Path    string
	RelPath string
	Mtime   int64
	Size    int64
}

type entry struct {
	Path    string
	RelPath string
	Mtime   int64
	Size    int64
	Info    *strm.Info
	Meta    strm.Metadata

	// 本地元数据（同目录 .nfo）与本地图片绝对路径
	Local    *nfo.Metadata
	Poster   string
	Backdrop string
}

// applier 既可以是 *store.DB，也可以是 *store.Batch。
type applier interface {
	UpsertItem(*store.Item) error
	UpsertMediaSource(*store.MediaSource) error
	ReplaceMediaStreams(string, []store.MediaStream) error
}

// ScanAll 全量扫描全部媒体库，返回处理的 STRM 数量。
func (s *Scanner) ScanAll() (int, error) { return s.scanAll(false) }

// ScanAllFast 增量扫描全部媒体库：跳过 mtime/size 未变化的 .strm。
func (s *Scanner) ScanAllFast() (int, error) { return s.scanAll(true) }

func (s *Scanner) scanAll(fast bool) (int, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return 0, fmt.Errorf("scan already running")
	}
	s.running = true
	s.lastStarted = time.Now().Unix()
	s.lastError = ""
	s.mu.Unlock()
	s.saveScanStatus()

	total := 0
	defer func() {
		s.mu.Lock()
		s.running = false
		s.lastFinished = time.Now().Unix()
		s.lastCount = total
		s.mu.Unlock()
		s.saveScanStatus()
	}()

	libs, err := s.db.ListLibraries()
	if err != nil {
		s.setLastError(err.Error())
		return 0, err
	}

	for _, lib := range libs {
		start := time.Now()
		n, err := s.scanLibrary(lib, fast)
		if err != nil {
			s.log.Error("scan library failed", "library", lib.Name, "path", lib.Path, "err", err)
			s.setLastError(lib.Name + ": " + err.Error())
			continue
		}
		s.log.Info("library scanned",
			"library", lib.Name, "strm", n, "fast", fast, "dur", time.Since(start).String())
		total += n
	}
	return total, nil
}

// ScanLibrary 全量扫描一个库，供面板新增/手动刷新使用。
func (s *Scanner) ScanLibrary(id string) (int, error) { return s.scanLibraryID(id, false) }

// ScanLibraryFast 增量扫描一个库：跳过未变化的 .strm。
func (s *Scanner) ScanLibraryFast(id string) (int, error) { return s.scanLibraryID(id, true) }

func (s *Scanner) scanLibraryID(id string, fast bool) (int, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return 0, fmt.Errorf("scan already running")
	}
	s.running = true
	s.lastStarted = time.Now().Unix()
	s.lastError = ""
	s.mu.Unlock()
	s.saveScanStatus()

	total := 0
	defer func() {
		s.mu.Lock()
		s.running = false
		s.lastFinished = time.Now().Unix()
		s.lastCount = total
		s.mu.Unlock()
		s.saveScanStatus()
	}()

	lib, err := s.db.GetLibrary(id)
	if err != nil {
		s.setLastError(err.Error())
		return 0, err
	}
	n, err := s.scanLibrary(lib, fast)
	if err != nil {
		s.setLastError(lib.Name + ": " + err.Error())
		return 0, err
	}
	total = n
	s.log.Info("library scanned", "library", lib.Name, "strm", n, "fast", fast)
	return n, nil
}

func (s *Scanner) scanLibrary(lib *store.Library, fast bool) (int, error) {
	started := time.Now().Unix()

	// 快速模式先取已有片源的 mtime/size，遍历时据此跳过未变化文件。
	var known map[string]store.SourceStub
	if fast {
		var err error
		known, err = s.db.SourceSnapshot(lib.ID)
		if err != nil {
			return 0, err
		}
	}

	jobs := make(chan fileRef)
	results := make(chan entry, 256)

	// 解析 worker
	var wg sync.WaitGroup
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				e, err := parseEntry(f)
				if err != nil {
					s.log.Debug("skip strm", "path", f.Path, "err", err)
					continue
				}
				results <- e
			}
		}()
	}

	// 边遍历边投递，避免先把几十万条路径全部读进内存。
	// 两种模式都调用 DirEntry.Info()：快速模式用它跳过未变化文件，
	// 全量模式用它把 created_at 记为 STRM 文件的真实修改时间。
	// 实测 6.1 万个 .strm 的额外 lstat 约 0.27s，29 万约 1.3s，可以接受。
	roots := lib.Paths
	if len(roots) == 0 {
		roots = []string{lib.Path}
	}

	// 进度分母直接用库内已有片源数，避免为了显示百分比再遍历一遍
	// 整个库（大库 29 万文件，等于多一次全量 I/O）。
	total := len(known)
	if !fast {
		if n, err := s.db.CountSources(lib.ID); err == nil {
			total = n
		}
	}
	s.mu.Lock()
	s.curLibrary = lib.Name
	s.scanCurrent = 0
	s.scanTotal = total
	s.mu.Unlock()

	var skipped int64
	go func() {
		defer close(jobs)
		for _, root := range roots {
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil // 单个目录不可读不影响整体
				}
				if d.IsDir() {
					name := d.Name()
					if path != root && (strings.HasPrefix(name, ".") || name == "@eaDir") {
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.EqualFold(filepath.Ext(path), ".strm") {
					return nil
				}
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = filepath.Base(path)
				}
				ref := fileRef{Path: path, RelPath: filepath.ToSlash(rel)}
				var stub store.SourceStub
				hasStub := false
				if fast {
					// 先取旧快照，再从 map 里划掉。走完剩下的就是
					// 「库里有记录、但这次没看到」的路径，用来兜底清理停机期间的删除。
					if k, ok := known[path]; ok {
						stub, hasStub = k, true
						delete(known, path)
					}
				}
				if info, ierr := d.Info(); ierr == nil {
					ref.Mtime = info.ModTime().Unix()
					ref.Size = info.Size()
					if fast && hasStub && stub.MTime == ref.Mtime && stub.Size == ref.Size {
						atomic.AddInt64(&skipped, 1)
						return nil
					}
				}
				jobs <- ref
				return nil
			})
			if err != nil {
				s.log.Warn("walk library", "library", lib.Name, "root", root, "err", err)
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	sawEpisode := false
	sawMovie := false
	count := 0
	pending := 0

	var batch *store.Batch

	// commit 提交后必须把 batch 置空，否则会一直占着唯一连接。
	commit := func() {
		if batch == nil {
			return
		}
		if pending > 0 {
			if err := batch.Commit(); err != nil {
				s.log.Error("commit batch", "err", err)
			}
		} else {
			_ = batch.Rollback()
		}
		batch = nil
		pending = 0
	}
	defer commit()

	for e := range results {
		if batch == nil {
			if nb, err := s.db.BeginBatch(); err != nil {
				s.log.Error("begin batch, fallback to per-row", "err", err)
			} else {
				batch = nb
			}
		}

		var applyErr error
		if batch != nil {
			_, applyErr = s.apply(batch, lib, e)
		} else {
			_, applyErr = s.apply(s.db, lib, e) // 事务不可用时逐条写，保证不丢数据
		}
		if applyErr != nil {
			s.log.Warn("apply item", "path", e.Path, "err", applyErr)
			continue
		}

		if batch != nil {
			pending++
			if pending >= batchSize {
				commit()
			}
		}

		if e.Meta.IsEpisode {
			sawEpisode = true
		} else {
			sawMovie = true
		}
		count++

		if count%100 == 0 {
			s.mu.Lock()
			s.scanCurrent = count
			s.mu.Unlock()
		}
		if count%20000 == 0 {
			s.log.Info("scan progress", "library", lib.Name, "done", count)
		}
	}

	commit()

	if fast {
		s.log.Info("fast scan done",
			"library", lib.Name, "changed", count, "skipped", atomic.LoadInt64(&skipped))
		// 快速模式也清理「遍历没看到 + 文件确实不存在」的片源：
		// 容器停机期间删掉的内容不会产生 fsnotify 事件，不兜底就会一直留在库里。
		s.pruneUnseenSources(lib, known)
	}

	if count == 0 {
		if !fast {
			s.log.Warn("no strm found", "library", lib.Name, "path", lib.Path)
		}
		return 0, nil
	}

	if !fast && (lib.Kind == "auto" || lib.Kind == "") {
		kind := "movies"
		if sawEpisode && !sawMovie {
			kind = "tvshows"
		}
		if err := s.db.UpdateLibraryKind(lib.ID, kind); err != nil {
			s.log.Warn("update library kind", "err", err)
		}
	}

	if fast {
		// 快速模式不清理未更新条目：删除交给全量扫描，避免误删。
		return count, nil
	}

	// 不能简单地「没被本次扫描写入就删」：文件临时读不到（挂载抖动、
	// 权限变化）时条目没被 upsert，会被误删并级联删掉播放进度。
	// 改成确认片源文件确实不存在后再删。
	s.pruneStaleItems(lib, started)

	return count, nil
}

// pruneUnseenSources 删除本次遍历没看到、且 os.Stat 确认已不存在的片源。
// 路径存在但被跳过（.hidden、@eaDir、无读权限）时一律保留。
func (s *Scanner) pruneUnseenSources(lib *store.Library, unseen map[string]store.SourceStub) {
	if len(unseen) == 0 {
		return
	}
	removed := 0
	for path := range unseen {
		if _, err := os.Stat(path); err == nil || !os.IsNotExist(err) {
			continue
		}
		removed += s.removeSourceByPath(path)
	}
	if removed > 0 {
		s.log.Info("removed missing sources", "library", lib.Name, "count", removed)
	}
}

// pruneStaleItems 清理全量扫描后 updated_at 没变的条目，但只删
// 「所有片源文件都不存在」的条目；有任一文件存在就保留。
func (s *Scanner) pruneStaleItems(lib *store.Library, since int64) {
	items, err := s.db.StaleItemsWithSources(lib.ID, since)
	if err != nil {
		s.log.Warn("list stale items", "err", err)
		return
	}
	deleted := 0
	for id, paths := range items {
		alive := false
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil || !os.IsNotExist(err) {
				alive = true
				break
			}
		}
		if alive {
			continue
		}
		if err := s.db.DeleteItem(id); err != nil {
			s.log.Warn("delete stale item", "item", id, "err", err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		s.log.Info("removed stale items", "library", lib.Name, "count", deleted)
	}
}

func parseEntry(f fileRef) (entry, error) {
	info, err := strm.ParseFile(f.Path)
	if err != nil {
		return entry{}, err
	}

	// 同时解析 .strm 文件名和 URL 里的 file_name。
	// PanSave 的 .strm 文件名通常没有扩展名，而 URL 里的有；
	// 但也有些同步工具会把 .strm 改成简化名，所以两边取信息更全的那个。
	base := filepath.Base(f.Path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	stemMeta := strm.Parse(stem)

	var urlMeta strm.Metadata
	if info.FileName != "" {
		urlMeta = strm.Parse(info.FileName)
	}
	meta := mergeMeta(stemMeta, urlMeta)

	// 目录名比文件名更可靠：标题干净、带年份和 TMDB ID。
	if pm, ok := strm.ParsePath(f.RelPath); ok {
		if pm.Title != "" {
			meta.Title = pm.Title
		}
		if pm.Year > 0 {
			meta.Year = pm.Year
		}
		if pm.TmdbID != "" {
			meta.TmdbID = pm.TmdbID
		}
		if meta.IsEpisode && pm.Season > 0 {
			meta.Season = pm.Season
		}
	}

	// 容器最终以 URL 里 file_name 的扩展名为准。
	if ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(info.FileName), ".")); ext != "" {
		meta.Container = ext
	}

	// 同目录 .nfo 优先于文件名解析：本地元数据是用户明确放进去的，最可信。
	// 单集的 NFO 标题只用于该集名称，不能覆盖剧名。
	dir := filepath.Dir(f.Path)
	local := nfo.Load(dir, stem)
	if local != nil {
		if !meta.IsEpisode && local.Title != "" {
			meta.Title = local.Title
		}
		if local.Year > 0 {
			meta.Year = local.Year
		}
		if meta.TmdbID == "" && local.TmdbID != "" {
			meta.TmdbID = local.TmdbID
		}
	}
	// 图片不依赖 NFO：有些目录只有 poster.jpg/fanart.jpg，或者 NFO 叫别的名字。
	poster := local.PosterFile(dir, stem)
	backdrop := local.BackdropFile(dir, stem)

	return entry{
		Path:     f.Path,
		RelPath:  f.RelPath,
		Mtime:    f.Mtime,
		Size:     f.Size,
		Info:     info,
		Meta:     meta,
		Local:    local,
		Poster:   poster,
		Backdrop: backdrop,
	}, nil
}

// mergeMeta 选信息更完整的一份作为主，另一份补齐空缺字段。
func mergeMeta(a, b strm.Metadata) strm.Metadata {
	if metaScore(b) > metaScore(a) {
		a, b = b, a
	}

	if a.Title == "" {
		a.Title = b.Title
	}
	if a.Year == 0 {
		a.Year = b.Year
	}
	if !a.IsEpisode && b.IsEpisode {
		a.IsEpisode = true
		a.Season = b.Season
		a.Episode = b.Episode
	}
	if a.AudioLang == "" {
		a.AudioLang = b.AudioLang
		a.AudioLangName = b.AudioLangName
	}
	if a.Season == 0 {
		a.Season = b.Season
	}
	if a.Episode == 0 {
		a.Episode = b.Episode
	}
	if a.Resolution == "" {
		a.Resolution = b.Resolution
		if a.Width == 0 {
			a.Width, a.Height = b.Width, b.Height
		}
	}
	if a.VideoCodec == "" {
		a.VideoCodec = b.VideoCodec
	}
	if a.AudioCodec == "" {
		a.AudioCodec = b.AudioCodec
		a.Channels = b.Channels
	} else if a.Channels == 0 && (b.AudioCodec == "" || strings.EqualFold(a.AudioCodec, b.AudioCodec)) {
		// .strm 文件名常常只写 AAC，真实文件名里才是 AAC2.0，声道要以后者为准
		a.Channels = b.Channels
	}
	if a.BitDepth == 0 {
		a.BitDepth = b.BitDepth
	}
	if a.FrameRate == 0 {
		a.FrameRate = b.FrameRate
	}
	if a.VideoRange == "" {
		a.VideoRange = b.VideoRange
	}
	if a.Source == "" {
		a.Source = b.Source
	}
	if a.ReleaseGroup == "" {
		a.ReleaseGroup = b.ReleaseGroup
	}
	if a.Container == "" || a.Container == "strm" {
		a.Container = b.Container
	}
	if a.TmdbID == "" {
		a.TmdbID = b.TmdbID
	}
	return a
}

func metaScore(m strm.Metadata) int {
	s := 0
	if m.IsEpisode {
		s += 4
	}
	if m.Year > 0 {
		s += 2
	}
	if m.Resolution != "" {
		s++
	}
	if m.VideoCodec != "" {
		s++
	}
	if m.AudioCodec != "" {
		s++
	}
	if m.TmdbID != "" {
		s++
	}
	return s
}

func (s *Scanner) apply(w applier, lib *store.Library, e entry) (string, error) {
	meta := e.Meta

	var item *store.Item

	// 首页「最近添加」按 created_at 排。用 STRM 的修改时间才反映真实入库时间，
	// 直接用 time.Now() 会让整库扫描的条目全部挤在同一秒，排序退化成扫描顺序。
	// 已在库里的条目由 UpsertItem 的 ON CONFLICT 分支保留原 created_at。
	createdAt := time.Time{}
	if e.Mtime > 0 {
		createdAt = time.Unix(e.Mtime, 0)
	}

	if meta.IsEpisode {
		seriesName := meta.Title
		if seriesName == "" {
			seriesName = "未知剧集"
		}
		seriesSort := normalizeSort(seriesName)
		seriesKey, seriesPY, seriesAbbr := pinyinKey(seriesName, seriesSort)
		seriesID := itemID(lib.ID, "series", identityKey(meta, seriesSort))

		if err := w.UpsertItem(&store.Item{
			ID:             seriesID,
			LibraryID:      lib.ID,
			ParentID:       lib.ID,
			Type:           store.TypeSeries,
			Name:           seriesName,
			SortName:       seriesSort,
			SortKey:        seriesKey,
			Pinyin:         seriesPY,
			PinyinAbbr:     seriesAbbr,
			ProductionYear: meta.Year,
			TmdbID:         meta.TmdbID,
			CreatedAt:      createdAt,
		}); err != nil {
			return "", err
		}

		seasonID := itemID(seriesID, "season", strconv.Itoa(meta.Season))
		if err := w.UpsertItem(&store.Item{
			ID:             seasonID,
			LibraryID:      lib.ID,
			ParentID:       seriesID,
			Type:           store.TypeSeason,
			Name:           fmt.Sprintf("第 %d 季", meta.Season),
			SortName:       fmt.Sprintf("Season %02d", meta.Season),
			SortKey:        fmt.Sprintf("Season %02d", meta.Season),
			IndexNumber:    meta.Season,
			SeriesID:       seriesID,
			ProductionYear: meta.Year,
			TmdbID:         meta.TmdbID,
			CreatedAt:      createdAt,
		}); err != nil {
			return "", err
		}

		episodeID := itemID(seriesID, "episode", strconv.Itoa(meta.Season), strconv.Itoa(meta.Episode))
		item = &store.Item{
			ID:                episodeID,
			LibraryID:         lib.ID,
			ParentID:          seasonID,
			Type:              store.TypeEpisode,
			Name:              fmt.Sprintf("第 %d 集", meta.Episode),
			SortName:          fmt.Sprintf("%04d", meta.Episode),
			SortKey:           fmt.Sprintf("%04d", meta.Episode),
			IndexNumber:       meta.Episode,
			ParentIndexNumber: meta.Season,
			SeriesID:          seriesID,
			SeasonID:          seasonID,
			ProductionYear:    meta.Year,
			TmdbID:            meta.TmdbID,
			CreatedAt:         createdAt,
		}
	} else {
		title := meta.Title
		if title == "" {
			title = e.RelPath
		}
		movieSort := normalizeSort(title)
		movieKey, moviePY, movieAbbr := pinyinKey(title, movieSort)
		movieID := itemID(lib.ID, "movie", movieIdentityKey(meta, e.RelPath, movieSort))
		item = &store.Item{
			ID:             movieID,
			LibraryID:      lib.ID,
			ParentID:       lib.ID,
			Type:           store.TypeMovie,
			Name:           title,
			SortName:       movieSort,
			SortKey:        movieKey,
			Pinyin:         moviePY,
			PinyinAbbr:     movieAbbr,
			ProductionYear: meta.Year,
			TmdbID:         meta.TmdbID,
			CreatedAt:      createdAt,
		}
	}

	applyLocalMeta(item, e)

	if err := w.UpsertItem(item); err != nil {
		return "", err
	}

	sourceID := itemID(item.ID, "src", e.Info.Cloud, e.Info.DriveID, e.Info.FileID)
	if e.Info.FileID == "" {
		sourceID = itemID(item.ID, "src", e.RelPath)
	}

	if err := w.UpsertMediaSource(&store.MediaSource{
		ID:          sourceID,
		ItemID:      item.ID,
		StrmPath:    e.Path,
		URL:         e.Info.RawURL,
		Cloud:       e.Info.Cloud,
		DriveID:     e.Info.DriveID,
		CloudFileID: e.Info.FileID,
		SHA1ETag:    e.Info.SHA1ETag,
		Size:        e.Info.Size,
		Container:   meta.Container,
		FileName:    fileNameOr(e.Info.FileName, e.Path),
		VersionName: versionName(meta, e.Info.Size),
		SortRank:    versionRank(meta),
		ProbeStatus: "none",
		StrmMtime:   e.Mtime,
		StrmSize:    e.Size,
	}); err != nil {
		return "", err
	}

	return item.ID, w.ReplaceMediaStreams(sourceID, buildStreams(meta))
}

// applyLocalMeta 用同目录 .nfo 填充条目元数据。
// 本地元数据视为权威来源：打上 scraped_at，避免定时 TMDB 刮削再覆盖本地海报/简介。
func applyLocalMeta(item *store.Item, e entry) {
	// 图片不依赖 .nfo：目录里有 poster.jpg/fanart.jpg 就挂上。
	if e.Poster != "" {
		item.ImagePath = "local:" + filepath.ToSlash(e.Poster)
	}
	if e.Backdrop != "" {
		item.BackdropPath = "local:" + filepath.ToSlash(e.Backdrop)
	}

	m := e.Local
	if m == nil {
		return
	}
	if item.Type == store.TypeEpisode && m.Title != "" {
		item.Name = m.Title
	}
	if m.OriginalTitle != "" {
		item.OriginalTitle = m.OriginalTitle
	}
	if v := m.Overview(); v != "" {
		item.Overview = v
	}
	if v := m.PremiereDate(); v != "" {
		item.PremiereDate = v
	}
	if v := m.OfficialRating(); v != "" {
		item.OfficialRating = v
	}
	if m.Rating > 0 {
		item.CommunityRating = m.Rating
	}
	if t := m.RuntimeTicks(); t > 0 {
		item.RunTimeTicks = t
	}
	if g := m.GenresJSON(); g != "" {
		item.Genres = g
	}
	if p := m.PeopleJSON(); p != "" {
		item.People = p
	}
	item.ScrapedAt = time.Now().Unix()
}

// identityKey 有 TMDB ID 时以它为准，否则用标题+年份。
// 这样同一部片的不同版本（1080p/2160p）会归并到一个条目下。
func identityKey(meta strm.Metadata, fallback string) string {
	if meta.TmdbID != "" {
		return "tmdb:" + meta.TmdbID
	}
	if meta.Year > 0 {
		return fallback + "#" + strconv.Itoa(meta.Year)
	}
	return fallback
}

// movieIdentityKey 决定同一部电影是否归并成一个条目（一个条目可以挂多个片源）。
//
// 有 TMDB ID 时以它为准；没有时按「所在目录」归并。一片一目录的结构里，同一目录
// 常有两个 .strm——-C 后缀、cd1/cd2、中日文标题不同。早先按「标题#年份」算键，
// 标题取自各自文件名，这些文件会各成一个条目；但它们又共用同目录的 poster.jpg，
// 显示出来就是两张一模一样的卡片。
func movieIdentityKey(meta strm.Metadata, relPath, fallback string) string {
	if meta.TmdbID != "" {
		return "tmdb:" + meta.TmdbID
	}
	if dir := path.Dir(filepath.ToSlash(relPath)); dir != "" && dir != "." && dir != "/" {
		return "dir:" + dir
	}
	if meta.Year > 0 {
		return fallback + "#" + strconv.Itoa(meta.Year)
	}
	return fallback
}

// versionName 给多版本片源生成人类可读的名字，例如
// "2160p · HDR10 · HEVC · 5.6GB"。
func versionName(meta strm.Metadata, size int64) string {
	var parts []string
	switch {
	case meta.Height >= 2000:
		parts = append(parts, "2160p")
	case meta.Height >= 1000:
		parts = append(parts, "1080p")
	case meta.Height >= 700:
		parts = append(parts, "720p")
	case meta.Resolution != "":
		parts = append(parts, meta.Resolution)
	}
	if meta.VideoRange != "" {
		parts = append(parts, meta.VideoRange)
	}
	switch strings.ToLower(meta.VideoCodec) {
	case "h264":
		parts = append(parts, "H.264")
	case "hevc":
		parts = append(parts, "HEVC")
	case "av1":
		parts = append(parts, "AV1")
	case "":
	default:
		parts = append(parts, strings.ToUpper(meta.VideoCodec))
	}
	if size > 0 {
		if size >= 1_000_000_000 {
			parts = append(parts, fmt.Sprintf("%.1fGB", float64(size)/1e9))
		} else {
			parts = append(parts, fmt.Sprintf("%.0fMB", float64(size)/1e6))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// versionRank 越小越先返回。服务端不解码、客户端全直连，
// 因此默认选最高画质：4K DV > 4K HDR10+ > HDR10 > HDR > 4K SDR > 1080p。
// 同一档位再按文件大小（近似码率）倒序，由 SQL 的 size DESC 决定。
func versionRank(meta strm.Metadata) int {
	rank := 9999
	switch {
	case meta.Height >= 2000:
		rank = 0
	case meta.Height >= 1000:
		rank = 1000
	case meta.Height >= 700:
		rank = 2000
	case meta.Height > 0:
		rank = 2500
	}

	r := strings.ToUpper(meta.VideoRange)
	switch {
	case strings.Contains(r, "DOVI") || r == "DV":
		// 杜比视界最高，不加分
	case r == "HDR10+":
		rank += 10
	case r == "HDR10":
		rank += 20
	case r == "HDR":
		rank += 30
	case r == "SDR" || r == "":
		rank += 100
	default:
		rank += 50
	}
	return rank
}

func buildStreams(meta strm.Metadata) []store.MediaStream {
	streams := make([]store.MediaStream, 0, 2)
	idx := 0

	if meta.VideoCodec != "" {
		s := store.MediaStream{
			Index:      idx,
			Type:       "Video",
			Codec:      meta.VideoCodec,
			Width:      meta.Width,
			Height:     meta.Height,
			BitDepth:   meta.BitDepth,
			FrameRate:  meta.FrameRate,
			VideoRange: meta.VideoRange,
		}
		if s.VideoRange == "" {
			s.VideoRange = "SDR"
		}
		streams = append(streams, s)
		idx++
	}

	if meta.AudioCodec != "" {
		streams = append(streams, store.MediaStream{
			Index:    idx,
			Type:     "Audio",
			Codec:    meta.AudioCodec,
			Channels: meta.Channels,
			Language: meta.AudioLang,
			Title:    meta.AudioLangName,
		})
	}

	return streams
}

func normalizeSort(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// pinyinKey 给中文标题生成拼音排序键；非中文标题不打拼音，排序仍用原排序名。
// 返回的 key 只影响 ORDER BY，不参与 itemID 计算。
func pinyinKey(name, fallback string) (key, full, abbr string) {
	full, abbr = pinyin.Key(name)
	if full != "" {
		return full, full, abbr
	}
	return fallback, "", ""
}

func itemID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func fileNameOr(name, path string) string {
	if name != "" {
		return name
	}
	return filepath.Base(path)
}

// RefreshItem 只重新解析一个条目已有的 STRM，供 /Items/{id}/Refresh 使用。
// 不做全库遍历，也不清理其他条目。
func (s *Scanner) RefreshItem(itemID string) (int, error) {
	item, err := s.db.GetItem(itemID)
	if err != nil {
		return 0, err
	}
	lib, err := s.db.GetLibrary(item.LibraryID)
	if err != nil {
		return 0, err
	}
	sources, err := s.db.MediaSourcesForItemTree(item.ID)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, src := range sources {
		info, err := os.Stat(src.StrmPath)
		if err != nil {
			continue
		}
		root := lib.Path
		for _, p := range lib.Paths {
			if src.StrmPath == p || strings.HasPrefix(src.StrmPath, strings.TrimRight(p, "/")+"/") {
				root = p
				break
			}
		}
		rel, relErr := filepath.Rel(root, src.StrmPath)
		if relErr != nil {
			rel = filepath.Base(src.StrmPath)
		}
		e, err := parseEntry(fileRef{
			Path:    src.StrmPath,
			RelPath: filepath.ToSlash(rel),
			Mtime:   info.ModTime().Unix(),
			Size:    info.Size(),
		})
		if err != nil {
			continue
		}
		if _, err := s.apply(s.db, lib, e); err != nil {
			s.log.Warn("refresh item source", "path", src.StrmPath, "err", err)
			continue
		}
		count++
	}
	return count, nil
}

// RefreshFiles 只解析指定的 STRM 文件/目录（PanSave 入库通知用）。
// 不遍历整个库，适合"新增了几个文件"的精准增量。
func (s *Scanner) RefreshFiles(paths []string) (int, error) {
	n, _, err := s.RefreshFilesWithIDs(paths)
	return n, err
}

// RefreshFilesWithIDs 与 RefreshFiles 相同，同时返回受影响的条目 ID，
// 供入库后精准刮削使用。
func (s *Scanner) RefreshFilesWithIDs(paths []string) (int, []string, error) {
	libs, err := s.db.ListLibraries()
	if err != nil {
		return 0, nil, err
	}

	count := 0
	ids := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			// 通知的是目录时，只扫这个目录下的 .strm。
			_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".strm") {
					return nil
				}
				if id, err := s.refreshOneFile(libs, p); err == nil && id != "" {
					count++
					add(id)
				}
				return nil
			})
			continue
		}
		if id, err := s.refreshOneFile(libs, path); err == nil && id != "" {
			count++
			add(id)
		}
	}
	return count, ids, nil
}

// RemoveMissing 清理磁盘上已经不存在的 STRM 对应片源。
// 传文件路径按精确路径清理；传目录路径会清理该目录前缀下所有缺失文件。
func (s *Scanner) RemoveMissing(paths []string) (int, error) {
	removed := 0
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			continue // 又回来了（例如原子替换或临时改名）
		}
		if strings.EqualFold(filepath.Ext(p), ".strm") {
			removed += s.removeSourceByPath(p)
			continue
		}
		srcs, err := s.db.SourcePathsUnder(p)
		if err != nil {
			s.log.Warn("remove missing: list sources", "path", p, "err", err)
			continue
		}
		for _, sp := range srcs {
			if _, err := os.Stat(sp); err == nil {
				continue
			}
			removed += s.removeSourceByPath(sp)
		}
	}
	return removed, nil
}

func (s *Scanner) removeSourceByPath(p string) int {
	itemID, err := s.db.DeleteSourceByPath(p)
	if err != nil {
		s.log.Warn("remove stale source", "path", p, "err", err)
		return 0
	}
	if itemID == "" {
		return 0
	}
	if n, err := s.db.CountSourcesForItem(itemID); err != nil || n > 0 {
		return 1
	}
	it, err := s.db.GetItem(itemID)
	if err != nil {
		return 1
	}
	if err := s.db.DeleteItem(itemID); err != nil {
		s.log.Warn("delete empty item", "item", itemID, "err", err)
		return 1
	}
	s.pruneEmptyParents(it)
	s.log.Info("removed stale source", "path", p, "item", itemID)
	return 1
}

// pruneEmptyParents 删掉没有剩余单集的季/剧集，避免界面上留下空壳。
func (s *Scanner) pruneEmptyParents(it *store.Item) {
	if it.Type != store.TypeEpisode {
		return
	}
	if it.SeasonID != "" {
		n, err := s.db.CountItemsByParent(it.SeasonID)
		if err != nil || n > 0 {
			return
		}
		_ = s.db.DeleteItem(it.SeasonID)
	}
	if it.SeriesID != "" {
		if n, err := s.db.CountEpisodesBySeries(it.SeriesID); err == nil && n == 0 {
			_ = s.db.DeleteSeriesTree(it.SeriesID)
		}
	}
}

func (s *Scanner) refreshOneFile(libs []*store.Library, path string) (string, error) {
	lib := libraryForFile(libs, path)
	if lib == nil {
		s.log.Debug("refresh file: no matching library", "path", path)
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	root := lib.Path
	for _, p := range lib.Paths {
		if path == p || strings.HasPrefix(path, strings.TrimRight(p, "/")+"/") {
			root = p
			break
		}
	}
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = filepath.Base(path)
	}
	e, err := parseEntry(fileRef{
		Path:    path,
		RelPath: filepath.ToSlash(rel),
		Mtime:   info.ModTime().Unix(),
		Size:    info.Size(),
	})
	if err != nil {
		return "", err
	}
	return s.apply(s.db, lib, e)
}

func libraryForFile(libs []*store.Library, path string) *store.Library {
	// 统一成 / 再比较，否则 Windows 下 \ 前缀匹配会失败。
	p := filepath.ToSlash(path)
	for _, lib := range libs {
		roots := lib.Paths
		if len(roots) == 0 {
			roots = []string{lib.Path}
		}
		for _, root := range roots {
			r := strings.TrimRight(filepath.ToSlash(root), "/")
			if p == r || strings.HasPrefix(p, r+"/") {
				return lib
			}
		}
	}
	return nil
}
