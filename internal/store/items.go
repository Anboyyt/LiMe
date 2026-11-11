package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"lime/internal/pinyin"
)

const (
	TypeMovie   = "Movie"
	TypeSeries  = "Series"
	TypeSeason  = "Season"
	TypeEpisode = "Episode"
)

type Item struct {
	ID                string
	LibraryID         string
	ParentID          string
	Type              string
	Name              string
	OriginalTitle     string
	SortName          string
	SortKey           string // 拼音排序键；空时排序回退 SortName
	Pinyin            string // 拼音全拼，仅中文标题；用于拼音搜索
	PinyinAbbr        string // 拼音首字母，仅中文标题
	Overview          string
	ProductionYear    int
	IndexNumber       int
	ParentIndexNumber int
	SeriesID          string
	SeasonID          string
	TmdbID            string
	RunTimeTicks      int64
	OfficialRating    string
	RatingScore       int
	CommunityRating   float64
	PremiereDate      string
	ImagePath         string
	BackdropPath      string
	LogoPath          string // TMDB ClearLogo（透明 PNG），对应 ImageType.Logo
	Genres            string // JSON 数组，如 ["动画","奇幻"]
	People            string // JSON 数组，如 [{"Name":"...","Role":"...","Type":"Actor"}]
	ScrapedAt         int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// execer 让同一段 SQL 既能在 *sql.DB 上执行，也能在事务里执行。
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func (d *DB) UpsertItem(it *Item) error { return upsertItem(d.DB, it) }

func upsertItem(e execer, it *Item) error {
	now := time.Now().Unix()
	if it.CreatedAt.IsZero() {
		it.CreatedAt = time.Unix(now, 0)
	}

	_, err := e.Exec(`
INSERT INTO items (
  id, library_id, parent_id, type, name, original_title, sort_name,
  sort_key, pinyin, pinyin_abbr, overview,
  production_year, index_number, parent_index_number, series_id, season_id, tmdb_id,
  run_time_ticks, official_rating, community_rating, premiere_date, image_path,
  backdrop_path, logo_path, genres, people, scraped_at, created_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  library_id = excluded.library_id,
  parent_id = excluded.parent_id,
  type = excluded.type,
  -- 刮削过的单集保留 TMDB 剧集名，重扫时不回退成「第 N 集」
  name = CASE WHEN items.scraped_at > 0 AND items.type = 'Episode'
              THEN items.name ELSE excluded.name END,
  original_title = CASE WHEN excluded.original_title <> ''
                        THEN excluded.original_title ELSE items.original_title END,
  sort_name = excluded.sort_name,
  sort_key = CASE WHEN excluded.sort_key <> '' THEN excluded.sort_key ELSE items.sort_key END,
  pinyin = CASE WHEN excluded.pinyin <> '' THEN excluded.pinyin ELSE items.pinyin END,
  pinyin_abbr = CASE WHEN excluded.pinyin_abbr <> '' THEN excluded.pinyin_abbr ELSE items.pinyin_abbr END,
  overview = CASE WHEN excluded.overview <> '' THEN excluded.overview ELSE items.overview END,
  production_year = CASE WHEN excluded.production_year > 0
                         THEN excluded.production_year ELSE items.production_year END,
  index_number = excluded.index_number,
  parent_index_number = excluded.parent_index_number,
  series_id = excluded.series_id,
  season_id = excluded.season_id,
  tmdb_id = excluded.tmdb_id,
  run_time_ticks = MAX(excluded.run_time_ticks, items.run_time_ticks),
  official_rating = CASE WHEN excluded.official_rating <> ''
                         THEN excluded.official_rating ELSE items.official_rating END,
  community_rating = CASE WHEN excluded.community_rating > 0
                          THEN excluded.community_rating ELSE items.community_rating END,
  premiere_date = CASE WHEN excluded.premiere_date <> ''
                       THEN excluded.premiere_date ELSE items.premiere_date END,
  image_path = CASE WHEN excluded.image_path <> '' THEN excluded.image_path ELSE items.image_path END,
  backdrop_path = CASE WHEN excluded.backdrop_path <> ''
                       THEN excluded.backdrop_path ELSE items.backdrop_path END,
  genres = CASE WHEN excluded.genres <> '' THEN excluded.genres ELSE items.genres END,
  people = CASE WHEN excluded.people <> '' THEN excluded.people ELSE items.people END,
  scraped_at = MAX(excluded.scraped_at, items.scraped_at),
  updated_at = excluded.updated_at`,
		it.ID, it.LibraryID, it.ParentID, it.Type, it.Name, it.OriginalTitle, it.SortName,
		it.SortKey, it.Pinyin, it.PinyinAbbr, it.Overview,
		it.ProductionYear, it.IndexNumber, it.ParentIndexNumber, it.SeriesID, it.SeasonID, it.TmdbID,
		it.RunTimeTicks, it.OfficialRating, it.CommunityRating, it.PremiereDate, it.ImagePath,
		it.BackdropPath, it.LogoPath, it.Genres, it.People, it.ScrapedAt, it.CreatedAt.Unix(), now,
	)
	return err
}

// MarkTreeScraped 把季及其下属单集标记为已处理。
// 用于 TMDB 返回 404 的情况（例如第 0 季 / Specials 不存在），避免每天重试。
func (d *DB) MarkTreeScraped(seasonID string) error {
	_, err := d.Exec(`UPDATE items SET scraped_at = ? WHERE (id = ? OR parent_id = ?) AND scraped_at = 0`,
		time.Now().Unix(), seasonID, seasonID)
	return err
}

// SetItemImage 写入客户端上传的图片路径（Primary / Backdrop / Logo）。
func (d *DB) SetItemImage(id, imageType, rel string) error {
	var col string
	switch strings.ToLower(imageType) {
	case "primary":
		col = "image_path"
	case "backdrop":
		col = "backdrop_path"
	case "logo":
		col = "logo_path"
	default:
		return ErrNotFound
	}
	_, err := d.Exec(`UPDATE items SET `+col+` = ? WHERE id = ?`, rel, id)
	return err
}

// MetadataUpdate 是刮削写回条目的字段。空值表示不覆盖已有内容。
type MetadataUpdate struct {
	Overview        string
	OriginalTitle   string
	PremiereDate    string
	CommunityRating float64
	RunTimeTicks    int64
	ImagePath       string
	BackdropPath    string
	LogoPath        string
	// LogoChecked 表示这次刮削已经尝试过 logo（含 TMDB 没有 logo 的情况）。
	LogoChecked bool
	// MetadataChecked 表示这次刮削已经尝试过类型/分级/时长，
	// 即使 TMDB 返回空值也算一次，避免补齐任务每天重复请求。
	MetadataChecked bool
	Genres          string
	People          string
	OfficialRating  string
}

// UpdateItemMetadata 写回电影/剧集/季的刮削结果，并打上 scraped_at。
// 注意不要动 updated_at：扫描器靠它判断条目是否还存在。
func (d *DB) UpdateItemMetadata(id string, m MetadataUpdate) error {
	score := RatingScore(m.OfficialRating)
	now := time.Now().Unix()
	_, err := d.Exec(`
UPDATE items SET
  overview         = CASE WHEN ? <> '' THEN ? ELSE overview END,
  original_title   = CASE WHEN ? <> '' THEN ? ELSE original_title END,
  premiere_date    = CASE WHEN ? <> '' THEN ? ELSE premiere_date END,
  community_rating = CASE WHEN ? > 0 THEN ? ELSE community_rating END,
  run_time_ticks   = CASE WHEN ? > 0 THEN ? ELSE run_time_ticks END,
  image_path       = CASE WHEN ? <> '' THEN ? ELSE image_path END,
  backdrop_path    = CASE WHEN ? <> '' THEN ? ELSE backdrop_path END,
  logo_path        = CASE WHEN ? <> '' THEN ? ELSE logo_path END,
  logo_checked     = CASE WHEN ? THEN 1 ELSE logo_checked END,
  genres_checked_at  = CASE WHEN ? THEN ? ELSE genres_checked_at END,
  rating_checked_at  = CASE WHEN ? THEN ? ELSE rating_checked_at END,
  runtime_checked_at = CASE WHEN ? THEN ? ELSE runtime_checked_at END,
  genres           = CASE WHEN ? <> '' THEN ? ELSE genres END,
  people           = CASE WHEN ? <> '' THEN ? ELSE people END,
  official_rating  = CASE WHEN ? <> '' THEN ? ELSE official_rating END,
  rating_score     = CASE WHEN ? > 0 THEN ? ELSE rating_score END,
  scraped_at       = ?
WHERE id = ?`,
		m.Overview, m.Overview,
		m.OriginalTitle, m.OriginalTitle,
		m.PremiereDate, m.PremiereDate,
		m.CommunityRating, m.CommunityRating,
		m.RunTimeTicks, m.RunTimeTicks,
		m.ImagePath, m.ImagePath,
		m.BackdropPath, m.BackdropPath,
		m.LogoPath, m.LogoPath,
		m.LogoChecked,
		m.MetadataChecked, now,
		m.MetadataChecked, now,
		m.MetadataChecked, now,
		m.Genres, m.Genres,
		m.People, m.People,
		m.OfficialRating, m.OfficialRating,
		score, score,
		now, id,
	)
	return err
}

// UpdateEpisodeMetadata 写回单集的标题/简介/首播日期，并继承所属剧集的海报和分级。
func (d *DB) UpdateEpisodeMetadata(id, name, overview, premiereDate string, runTimeTicks int64, imagePath, backdropPath, officialRating string) error {
	score := RatingScore(officialRating)
	_, err := d.Exec(`
UPDATE items SET
  name            = CASE WHEN ? <> '' THEN ? ELSE name END,
  overview        = CASE WHEN ? <> '' THEN ? ELSE overview END,
  premiere_date   = CASE WHEN ? <> '' THEN ? ELSE premiere_date END,
  run_time_ticks  = CASE WHEN ? > 0 THEN ? ELSE run_time_ticks END,
  image_path      = CASE WHEN ? <> '' THEN ? ELSE image_path END,
  backdrop_path   = CASE WHEN ? <> '' THEN ? ELSE backdrop_path END,
  official_rating = CASE WHEN ? <> '' THEN ? ELSE official_rating END,
  rating_score    = CASE WHEN ? > 0 THEN ? ELSE rating_score END,
  scraped_at      = ?
WHERE id = ?`,
		name, name,
		overview, overview,
		premiereDate, premiereDate,
		runTimeTicks, runTimeTicks,
		imagePath, imagePath,
		backdropPath, backdropPath,
		officialRating, officialRating,
		score, score,
		time.Now().Unix(), id,
	)
	return err
}

// UpdateItemRating 只补分级，不动其他元数据。
// 即使 rating 为空也记一次尝试时间：TMDB 没分级的条目不用每天重试。
func (d *DB) UpdateItemRating(id, rating string) error {
	now := time.Now().Unix()
	if rating == "" {
		_, err := d.Exec(`UPDATE items SET rating_checked_at = ? WHERE id = ?`, now, id)
		return err
	}
	_, err := d.Exec(`UPDATE items SET official_rating = ?, rating_score = ?, rating_checked_at = ? WHERE id = ?`,
		rating, RatingScore(rating), now, id)
	return err
}

// ItemsMissingPeopleAvatars 返回已刮过、但演员数据还是旧格式（没有头像标记）的条目。
func (d *DB) ItemsMissingPeopleAvatars(typ string) ([]*Item, error) {
	rows, err := d.Query(`
SELECT `+itemColumns+` FROM items
WHERE type = ? AND tmdb_id <> '' AND scraped_at > 0 AND people <> ''
  AND people NOT LIKE '%"ImageTag"%'
ORDER BY sort_name`, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Item, 0, 32)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdateItemPeople 只更新演员 JSON。
func (d *DB) UpdateItemPeople(id, people string) error {
	if people == "" {
		return nil
	}
	_, err := d.Exec(`UPDATE items SET people = ? WHERE id = ?`, people, id)
	return err
}

// ItemsMissingLogo 返回已刮过、但还没尝试过 logo 的电影/剧集。
// 老库升级后靠它一次性补 ClearLogo；TMDB 没有 logo 的条目也会置 logo_checked，
// 不会每天重复请求。
func (d *DB) ItemsMissingLogo(typ string) ([]*Item, error) {
	rows, err := d.Query(`
SELECT `+itemColumns+` FROM items
WHERE type = ? AND tmdb_id <> '' AND scraped_at > 0 AND logo_checked = 0
ORDER BY sort_name`, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Item, 0, 64)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdateItemLogo 写回 logo 路径并标记已尝试；logoPath 为空表示 TMDB 没有 logo。
func (d *DB) UpdateItemLogo(id, logoPath string) error {
	_, err := d.Exec(`UPDATE items SET logo_path = ?, logo_checked = 1 WHERE id = ?`, logoPath, id)
	return err
}

// ItemsMissingOfficialRating 返回已刮过但分级仍为空、且距上次尝试已超过
// checkedBefore 的条目，用于老库补全。
func (d *DB) ItemsMissingOfficialRating(typ string, checkedBefore int64) ([]*Item, error) {
	rows, err := d.Query(`
SELECT `+itemColumns+` FROM items
WHERE type = ? AND tmdb_id <> '' AND scraped_at > 0 AND official_rating = ''
  AND rating_checked_at < ?
ORDER BY sort_name`, typ, checkedBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Item, 0, 32)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// PropagateSeriesRatings 把剧集分级继承给还没分级的季和单集。
func (d *DB) PropagateSeriesRatings() (int64, error) {
	res, err := d.Exec(`
UPDATE items SET
  official_rating = COALESCE((
    SELECT s.official_rating FROM items s
    WHERE s.id = items.series_id AND s.official_rating <> ''
  ), official_rating),
  rating_score = COALESCE((
    SELECT s.rating_score FROM items s
    WHERE s.id = items.series_id AND s.rating_score > 0
  ), rating_score)
WHERE type IN ('Season', 'Episode') AND official_rating = '' AND series_id <> ''`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// BackfillRatingScores 给老库补齐 rating_score（迁移只加了列，没法在 SQL 里做映射）。
func (d *DB) BackfillRatingScores() error {
	rows, err := d.Query(`SELECT DISTINCT official_rating FROM items WHERE official_rating <> '' AND rating_score = 0`)
	if err != nil {
		return err
	}
	var ratings []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return err
		}
		ratings = append(ratings, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range ratings {
		score := RatingScore(r)
		if score == 0 {
			continue
		}
		if _, err := d.Exec(`UPDATE items SET rating_score = ? WHERE official_rating = ? AND rating_score = 0`, score, r); err != nil {
			return err
		}
	}
	return nil
}

// UpdateItemRuntime 只补时长，不动其他元数据。
// 即使 ticks 为 0 也记一次尝试时间，避免 TMDB 无时长时每天重试。
func (d *DB) UpdateItemRuntime(id string, ticks int64) error {
	now := time.Now().Unix()
	if ticks <= 0 {
		_, err := d.Exec(`UPDATE items SET runtime_checked_at = ? WHERE id = ?`, now, id)
		return err
	}
	_, err := d.Exec(`UPDATE items SET run_time_ticks = ?, runtime_checked_at = ? WHERE id = ?`,
		ticks, now, id)
	return err
}

// MarkSeasonStillsChecked 记录某一季的单集剧照已经补过一轮。
func (d *DB) MarkSeasonStillsChecked(seasonID string) error {
	_, err := d.Exec(`UPDATE items SET episode_stills_checked_at = ? WHERE id = ?`,
		time.Now().Unix(), seasonID)
	return err
}

// MarkSeasonRuntimeChecked 记录某一季的单集时长已经补过一轮。
func (d *DB) MarkSeasonRuntimeChecked(seasonID string) error {
	_, err := d.Exec(`UPDATE items SET episode_runtime_checked_at = ? WHERE id = ?`,
		time.Now().Unix(), seasonID)
	return err
}

// ItemsForScrape 返回还没刮削过、且带 TMDB ID 的条目。
func (d *DB) ItemsForScrape(typ string) ([]*Item, error) {
	rows, err := d.Query(`
SELECT `+itemColumns+` FROM items
WHERE type = ? AND tmdb_id <> '' AND scraped_at = 0
ORDER BY sort_name`, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Item, 0, 32)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemsMissingRuntime 返回已刮过但时长仍为空、且距上次尝试已超过
// checkedBefore 的条目（电影用）。
func (d *DB) ItemsMissingRuntime(typ string, checkedBefore int64) ([]*Item, error) {
	rows, err := d.Query(`
SELECT `+itemColumns+` FROM items
WHERE type = ? AND tmdb_id <> '' AND scraped_at > 0 AND run_time_ticks = 0
  AND runtime_checked_at < ?
ORDER BY sort_name`, typ, checkedBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Item, 0, 16)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// LibraryPosterCandidates 返回库内最近的有海报条目，供自动生成库封面。
func (d *DB) LibraryPosterCandidates(libraryID string, limit int) ([]*Item, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := d.Query(`
SELECT `+itemColumns+` FROM items
WHERE library_id = ? AND image_path <> '' AND type IN ('Movie', 'Series')
ORDER BY created_at DESC LIMIT ?`, libraryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Item, 0, limit)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemsMissingGenres 返回已刮削、但还没有类型/演员、且距上次尝试已超过
// checkedBefore 的条目。
func (d *DB) ItemsMissingGenres(typ string, checkedBefore int64) ([]*Item, error) {
	rows, err := d.Query(`
SELECT `+itemColumns+` FROM items
WHERE type = ? AND tmdb_id <> '' AND scraped_at > 0 AND genres = ''
  AND genres_checked_at < ?
ORDER BY sort_name`, typ, checkedBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Item, 0, 16)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GenreNames 聚合全部库的类型名。
func (d *DB) GenreNames() ([]string, error) { return d.GenreNamesIn(nil) }

// GenreNamesIn 只聚合指定库的类型名；libraryIDs 为空表示全部。
func (d *DB) GenreNamesIn(libraryIDs []string) ([]string, error) {
	q := `SELECT genres FROM items WHERE genres <> ''`
	args := make([]any, 0, len(libraryIDs))
	if len(libraryIDs) > 0 {
		q += ` AND library_id IN (` + placeholders(len(libraryIDs)) + `)`
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var names []string
		if json.Unmarshal([]byte(raw), &names) != nil {
			continue
		}
		for _, name := range names {
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out, rows.Err()
}

// OfficialRatingsIn 返回库内实际存在的分级，供客户端筛选面板使用。
func (d *DB) OfficialRatingsIn(libraryIDs []string) ([]string, error) {
	q := `SELECT DISTINCT official_rating FROM items WHERE official_rating <> ''`
	args := make([]any, 0, len(libraryIDs))
	if len(libraryIDs) > 0 {
		q += ` AND library_id IN (` + placeholders(len(libraryIDs)) + `)`
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY official_rating`

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0, 8)
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SeriesSeason 标记某一季里还有单集缺时长。
type SeriesSeason struct {
	SeasonID string
	SeriesID string
	TmdbID   string
	Season   int
}

// SeriesSeasonsMissingEpisodeStill 找出还在共用季海报的单集，
// 按季聚合后统一去 TMDB 拉剧照。
func (d *DB) SeriesSeasonsMissingEpisodeStill(checkedBefore int64) ([]SeriesSeason, error) {
	rows, err := d.Query(`
SELECT DISTINCT se.id, e.series_id, s.tmdb_id, e.parent_index_number
FROM items e
JOIN items s ON s.id = e.series_id
JOIN items se ON se.id = e.season_id
WHERE e.type = 'Episode' AND e.scraped_at > 0 AND s.tmdb_id <> ''
  AND e.parent_index_number > 0
  AND (e.image_path = '' OR e.image_path = se.image_path)
  AND se.episode_stills_checked_at < ?
ORDER BY s.tmdb_id, e.parent_index_number`, checkedBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SeriesSeason, 0, 16)
	for rows.Next() {
		var ss SeriesSeason
		if err := rows.Scan(&ss.SeasonID, &ss.SeriesID, &ss.TmdbID, &ss.Season); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// UpdateItemImage 只更新图片，不动其他元数据。
func (d *DB) UpdateItemImage(id, imagePath string) error {
	if imagePath == "" {
		return nil
	}
	_, err := d.Exec(`UPDATE items SET image_path = ? WHERE id = ?`, imagePath, id)
	return err
}

// SeriesSeasonsMissingEpisodeRuntime 按季聚合缺时长的单集，避免逐集请求 TMDB。
func (d *DB) SeriesSeasonsMissingEpisodeRuntime(checkedBefore int64) ([]SeriesSeason, error) {
	rows, err := d.Query(`
SELECT DISTINCT COALESCE(se.id, ''), e.series_id, s.tmdb_id, e.parent_index_number
FROM items e
JOIN items s ON s.id = e.series_id
LEFT JOIN items se ON se.id = e.season_id
WHERE e.type = 'Episode' AND e.run_time_ticks = 0 AND e.scraped_at > 0
  AND s.tmdb_id <> '' AND e.parent_index_number > 0
  AND COALESCE(se.episode_runtime_checked_at, 0) < ?
ORDER BY s.tmdb_id, e.parent_index_number`, checkedBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SeriesSeason, 0, 16)
	for rows.Next() {
		var ss SeriesSeason
		if err := rows.Scan(&ss.SeasonID, &ss.SeriesID, &ss.TmdbID, &ss.Season); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// EpisodeByNumber 按剧集 + 季 + 集号找单集。
func (d *DB) EpisodeByNumber(seriesID string, season, episode int) (*Item, error) {
	row := d.QueryRow(`
SELECT `+itemColumns+` FROM items
WHERE type = 'Episode' AND series_id = ? AND parent_index_number = ? AND index_number = ?
LIMIT 1`, seriesID, season, episode)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return it, err
}

const itemColumns = `id, library_id, parent_id, type, name, original_title, sort_name,
  sort_key, pinyin, pinyin_abbr, overview,
  production_year, index_number, parent_index_number, COALESCE(series_id,''), COALESCE(season_id,''),
  COALESCE(tmdb_id,''), run_time_ticks, official_rating, rating_score, community_rating, premiere_date, image_path,
  backdrop_path, logo_path, genres, people, scraped_at, created_at, updated_at`

// LatestSeries 返回某库中「最近有新单集」的剧集，按最新单集添加时间倒序。
// 首页「最新」横排要的是剧集卡片，而不是同一部剧的每一集。
//
// 先 GROUP BY 出每个 series_id 的最新时间（34k 单集约 40ms），再按 ID 批量取
// 剧集。不要用相关子查询逐部剧求 MAX，实测 640 部剧要 2 秒，首页会卡住。
func (d *DB) LatestSeries(libraryID string, limit int) ([]*Item, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := d.Query(`
SELECT series_id, MAX(created_at) AS latest
FROM items
WHERE library_id = ? AND type = ? AND series_id <> ''
GROUP BY series_id
ORDER BY latest DESC
LIMIT ?`, libraryID, TypeEpisode, limit)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		var latest int64
		if err := rows.Scan(&id, &latest); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []*Item{}, nil
	}

	// SQLite 单连接：必须先关掉 rows 再发下一条查询。
	byID, err := d.ItemsByIDs(ids)
	if err != nil {
		return nil, err
	}
	out := make([]*Item, 0, len(ids))
	for _, id := range ids {
		if it, ok := byID[id]; ok {
			out = append(out, it)
		}
	}
	return out, nil
}

func (d *DB) GetItem(id string) (*Item, error) {
	row := d.QueryRow(`SELECT `+itemColumns+` FROM items WHERE id = ?`, id)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return it, err
}

// ItemsByIDs 批量取条目，返回 id → Item 的映射，调用方按自己的顺序排列。
func (d *DB) ItemsByIDs(ids []string) (map[string]*Item, error) {
	out := make(map[string]*Item, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	const chunk = 400
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		part := ids[start:end]

		q := `SELECT ` + itemColumns + ` FROM items WHERE id IN (` + placeholders(len(part)) + `)`
		args := make([]any, 0, len(part))
		for _, id := range part {
			args = append(args, id)
		}

		rows, err := d.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			it, err := scanItem(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[it.ID] = it
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

type ItemQuery struct {
	LibraryID       string
	LibraryIDs      []string
	ParentID        string
	Recursive       bool
	Types           []string
	ExcludeTypes    []string
	Genres          []string
	Years           []int
	OfficialRatings []string
	MaxRating       int // >0 时只返回 rating_score 0（未分级）或不高于该值的条目
	SeasonID        string
	Person          string
	UserID          string
	FavoriteOnly    bool
	SearchTerm      string
	SortBy          []string
	Descending      bool
	StartIndex      int
	Limit           int
}

func (d *DB) QueryItems(q ItemQuery) ([]*Item, int, error) {
	where, args := d.buildItemWhere(q)

	countSQL := `SELECT COUNT(*) FROM items`
	if len(where) > 0 {
		countSQL += " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := d.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := buildOrderBy(q)
	sqlText := `SELECT ` + itemColumns + ` FROM items`
	if len(where) > 0 {
		sqlText += " WHERE " + strings.Join(where, " AND ")
	}
	sqlText += " ORDER BY " + order

	if q.Limit > 0 {
		sqlText += fmt.Sprintf(" LIMIT %d OFFSET %d", q.Limit, max0(q.StartIndex))
	} else if q.StartIndex > 0 {
		sqlText += fmt.Sprintf(" LIMIT -1 OFFSET %d", q.StartIndex)
	}

	rows, err := d.Query(sqlText, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]*Item, 0, 32)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	return items, total, rows.Err()
}

func (d *DB) buildItemWhere(q ItemQuery) ([]string, []any) {
	var where []string
	var args []any

	if q.LibraryID != "" {
		where = append(where, "library_id = ?")
		args = append(args, q.LibraryID)
	}
	if len(q.LibraryIDs) > 0 {
		where = append(where, "library_id IN ("+placeholders(len(q.LibraryIDs))+")")
		for _, id := range q.LibraryIDs {
			args = append(args, id)
		}
	}

	if q.ParentID != "" {
		if parent, err := d.GetItem(q.ParentID); err == nil {
			switch parent.Type {
			case TypeSeries:
				if q.Recursive {
					where = append(where, "series_id = ?")
				} else {
					where = append(where, "parent_id = ?")
				}
			case TypeSeason:
				where = append(where, "parent_id = ?")
			default:
				if q.Recursive {
					where = append(where, "library_id = ?")
				} else {
					where = append(where, "parent_id = ?")
				}
			}
			args = append(args, parent.ID)
		} else if _, err := d.GetLibrary(q.ParentID); err == nil {
			if q.Recursive {
				where = append(where, "library_id = ?")
			} else {
				where = append(where, "parent_id = ?")
			}
			args = append(args, q.ParentID)
		} else {
			where = append(where, "parent_id = ?")
			args = append(args, q.ParentID)
		}
	}

	if len(q.Types) > 0 {
		where = append(where, "type IN ("+placeholders(len(q.Types))+")")
		for _, t := range q.Types {
			args = append(args, t)
		}
	}
	if len(q.ExcludeTypes) > 0 {
		where = append(where, "type NOT IN ("+placeholders(len(q.ExcludeTypes))+")")
		for _, t := range q.ExcludeTypes {
			args = append(args, t)
		}
	}
	if q.SeasonID != "" {
		where = append(where, "season_id = ?")
		args = append(args, q.SeasonID)
	}
	if q.FavoriteOnly {
		where = append(where,
			`EXISTS (SELECT 1 FROM playstates ps WHERE ps.item_id = items.id AND ps.is_favorite = 1 AND ps.user_id = ?)`)
		args = append(args, q.UserID)
	}
	if q.Person != "" {
		where = append(where,
			`EXISTS (SELECT 1 FROM json_each(CASE WHEN json_valid(people) THEN people ELSE '[]' END) p
			 WHERE json_extract(p.value, '$.Name') = ?)`)
		args = append(args, q.Person)
	}
	if term := strings.TrimSpace(q.SearchTerm); term != "" {
		// 3 个字符以上走 FTS5 trigram 索引；中文两字词（如「三体」）
		// trigram 无法覆盖，走 bigram。其余退回 LIKE。
		n := utf8.RuneCountInString(term)
		var cond string
		var condArgs []any
		switch {
		case d.searchReady && n >= 3:
			cond = "items.rowid IN (SELECT rowid FROM items_fts WHERE items_fts MATCH ?)"
			condArgs = []any{ftsPhrase(term)}
		case d.searchReady && n == 2:
			cond = "items.rowid IN (SELECT rowid FROM items_bigram WHERE items_bigram MATCH ?)"
			condArgs = []any{ftsPhrase(term)}
		default:
			like := "%" + term + "%"
			cond = "(name LIKE ? OR original_title LIKE ? OR sort_name LIKE ?)"
			condArgs = []any{like, like, like}
		}
		// 纯字母输入额外匹配拼音全拼和首字母前缀：st / santi 都能搜到「三体」。
		// 用范围查询而不是 LIKE，保证命中 pinyin / pinyin_abbr 的 B-tree 索引。
		if lo, hi := pinyin.PrefixRange(term); isASCIILetters(lo) {
			cond = "(" + cond + " OR (pinyin >= ? AND pinyin < ?) OR (pinyin_abbr >= ? AND pinyin_abbr < ?))"
			condArgs = append(condArgs, lo, hi, lo, hi)
		}
		where = append(where, cond)
		args = append(args, condArgs...)
	}
	if len(q.Genres) > 0 {
		// genres 是 JSON 文本；json_valid 兜底 NULL/空串，避免脏数据报错。
		where = append(where,
			"EXISTS (SELECT 1 FROM json_each(CASE WHEN json_valid(genres) THEN genres ELSE '[]' END) WHERE json_each.value IN ("+
				placeholders(len(q.Genres))+"))")
		for _, g := range q.Genres {
			args = append(args, g)
		}
	}
	if len(q.Years) > 0 {
		where = append(where, "production_year IN ("+placeholders(len(q.Years))+")")
		for _, y := range q.Years {
			args = append(args, y)
		}
	}
	if len(q.OfficialRatings) > 0 {
		where = append(where, "official_rating IN ("+placeholders(len(q.OfficialRatings))+")")
		for _, r := range q.OfficialRatings {
			args = append(args, r)
		}
	}
	if q.MaxRating > 0 {
		// rating_score = 0 是未分级/未知，默认放行。
		where = append(where, "(rating_score = 0 OR rating_score <= ?)")
		args = append(args, q.MaxRating)
	}

	return where, args
}

// sortKeyExpr 与迁移 0020 里的表达式索引必须逐字一致，否则索引不生效。
const sortKeyExpr = `COALESCE(NULLIF(sort_key, ''), sort_name)`

var sortColumns = map[string]string{
	"SortName":          sortKeyExpr,
	"Name":              "name",
	"DateCreated":       "created_at",
	"ProductionYear":    "production_year",
	"CommunityRating":   "community_rating",
	"IndexNumber":       "index_number",
	"ParentIndexNumber": "parent_index_number",
	"Random":            "RANDOM()",
}

// lastContentAddedOrder 「最近添加内容」排序：剧集取最新一集的入库时间，
// 其他类型取自身入库时间。
// Emby 系客户端（SenPlayer 等）首页横排用 SortBy=DateLastContentAdded，
// 之前不认识这个字段会退化成按剧集自身 created_at —— 整库扫描时所有剧集
// created_at 集中在同一秒，横排顺序就成了扫描顺序。
// (series_id, created_at) 索引让子查询只做一次 MAX 查找。
// 子查询带上 library_id 才能命中 idx_items_lib_type_series_created 的覆盖索引。
const lastContentAddedOrder = `CASE WHEN items.type = 'Series'
  THEN COALESCE(
    (SELECT MAX(e.created_at) FROM items e
     WHERE e.library_id = items.library_id
       AND e.type = 'Episode' AND e.series_id = items.id),
    items.created_at)
  ELSE items.created_at END`

// latestEpisodeOrder 剧集库按「最新一集」排序：优先看该剧最新一集的播出日期，
// 没有日期时退回最新一集的入库时间，再退回剧集本身的创建时间。
// NULLIF 放在 MAX 外层，才能让 (series_id, premiere_date) 索引参与 MAX 计算；
// 写成 MAX(NULLIF(...)) 会退化成逐行函数调用，索引失效。
const latestEpisodeOrder = `COALESCE(
  (SELECT NULLIF(MAX(e.premiere_date), '') FROM items e
   WHERE e.type = 'Episode' AND e.series_id = items.id),
  (SELECT MAX(e.created_at) FROM items e
   WHERE e.type = 'Episode' AND e.series_id = items.id),
  items.created_at)`

func buildOrderBy(q ItemQuery) string {
	dir := "ASC"
	if q.Descending {
		dir = "DESC"
	}

	if len(q.SortBy) == 0 {
		for _, t := range q.Types {
			if t == TypeEpisode {
				return "parent_index_number ASC, index_number ASC, " + sortKeyExpr + " ASC"
			}
		}
		return sortKeyExpr + " " + dir + ", name " + dir
	}

	var parts []string
	for _, s := range q.SortBy {
		if s == "LatestEpisode" {
			// 「最新一集」天然是倒序，不受 SortOrder 影响
			parts = append(parts, latestEpisodeOrder+" DESC")
			continue
		}
		if s == "DateLastContentAdded" || s == "DateLastMediaAdded" {
			// Emby 与 Jellyfin 两种叫法都接受
			parts = append(parts, lastContentAddedOrder+" "+dir)
			continue
		}
		col, ok := sortColumns[s]
		if !ok {
			continue
		}
		if col == "RANDOM()" {
			parts = append(parts, "RANDOM()")
			continue
		}
		parts = append(parts, col+" "+dir)
	}
	if len(parts) == 0 {
		return sortKeyExpr + " ASC"
	}
	return strings.Join(parts, ", ")
}

// isASCIILetters 判断是否纯英文字母，决定要不要追加拼音检索条件。
// 数字、空格、标点都不算，避免「1917」这类查询误走拼音索引。
func isASCIILetters(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// ItemChildren 统计剧集/季的子项数量，用于补 Jellyfin 的 ChildCount
// 和 RecursiveItemCount。seasonCounts 是季的集数，seriesSeasons 是
// 剧的季数，seriesEpisodes 是剧的总集数。
func (d *DB) ItemChildren(seriesIDs, seasonIDs []string) (map[string]int, map[string]int, map[string]int, error) {
	seasonCounts := make(map[string]int, len(seasonIDs))
	seriesSeasons := make(map[string]int, len(seriesIDs))
	seriesEpisodes := make(map[string]int, len(seriesIDs))

	if len(seasonIDs) > 0 {
		args := make([]any, 0, len(seasonIDs))
		for _, id := range seasonIDs {
			args = append(args, id)
		}
		rows, err := d.Query(`SELECT season_id, COUNT(*) FROM items
WHERE type = 'Episode' AND season_id IN (`+placeholders(len(seasonIDs))+`)
GROUP BY season_id`, args...)
		if err != nil {
			return nil, nil, nil, err
		}
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				rows.Close()
				return nil, nil, nil, err
			}
			seasonCounts[id] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, nil, nil, err
		}
	}

	if len(seriesIDs) > 0 {
		args := make([]any, 0, len(seriesIDs))
		for _, id := range seriesIDs {
			args = append(args, id)
		}
		rows, err := d.Query(`SELECT series_id, type, COUNT(*) FROM items
WHERE series_id IN (`+placeholders(len(seriesIDs))+`) AND type IN ('Episode', 'Season')
GROUP BY series_id, type`, args...)
		if err != nil {
			return nil, nil, nil, err
		}
		for rows.Next() {
			var id, typ string
			var n int
			if err := rows.Scan(&id, &typ, &n); err != nil {
				rows.Close()
				return nil, nil, nil, err
			}
			if typ == TypeSeason {
				seriesSeasons[id] = n
			} else {
				seriesEpisodes[id] = n
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return seasonCounts, seriesSeasons, seriesEpisodes, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func max0(v int) int {
	if v > 0 {
		return v
	}
	return 0
}

// DeleteItemsNotUpdated 删除本轮扫描没有碰到的条目（文件已被移走的）。
func (d *DB) DeleteItemsNotUpdated(libraryID string, since int64) (int64, error) {
	res, err := d.Exec(
		`DELETE FROM items WHERE library_id = ? AND updated_at < ?`, libraryID, since,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanItem(row interface {
	Scan(dest ...any) error
}) (*Item, error) {
	var (
		it        Item
		createdAt int64
		updatedAt int64
	)
	err := row.Scan(
		&it.ID, &it.LibraryID, &it.ParentID, &it.Type, &it.Name, &it.OriginalTitle,
		&it.SortName, &it.SortKey, &it.Pinyin, &it.PinyinAbbr,
		&it.Overview, &it.ProductionYear, &it.IndexNumber, &it.ParentIndexNumber,
		&it.SeriesID, &it.SeasonID, &it.TmdbID, &it.RunTimeTicks, &it.OfficialRating,
		&it.RatingScore, &it.CommunityRating,
		&it.PremiereDate, &it.ImagePath, &it.BackdropPath, &it.LogoPath, &it.Genres, &it.People,
		&it.ScrapedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	it.CreatedAt = time.Unix(createdAt, 0)
	it.UpdatedAt = time.Unix(updatedAt, 0)
	return &it, nil
}

// SimilarItems 返回同类型、共享类型名最多的条目（排除自己）。
// 单集/季按所属剧集计算。
func (d *DB) SimilarItems(itemID string, limit int) ([]*Item, error) {
	it, err := d.GetItem(itemID)
	if err != nil {
		return nil, err
	}
	target := it
	if (it.Type == TypeEpisode || it.Type == TypeSeason) && it.SeriesID != "" {
		if series, err := d.GetItem(it.SeriesID); err == nil {
			target = series
		}
	}
	if target.Type != TypeMovie && target.Type != TypeSeries {
		return []*Item{}, nil
	}
	if limit <= 0 {
		limit = 12
	}

	var genres []string
	if target.Genres != "" {
		_ = json.Unmarshal([]byte(target.Genres), &genres)
	}

	args := []any{target.Type, target.ID}
	where := "type = ? AND id <> ?"
	if len(genres) > 0 {
		where += ` AND EXISTS (SELECT 1 FROM json_each(CASE WHEN json_valid(genres) THEN genres ELSE '[]' END) g
			WHERE g.value IN (` + placeholders(len(genres)) + `))`
		for _, g := range genres {
			args = append(args, g)
		}
	}
	args = append(args, limit)

	rows, err := d.Query(`SELECT `+itemColumns+` FROM items WHERE `+where+
		` ORDER BY created_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Item, 0, limit)
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
