package store

// SeasonEpisodes 是一季里还没刮削的单集，按季聚合后一次 TMDB 请求补齐。
type SeasonEpisodes struct {
	SeasonID   string
	SeriesID   string
	TmdbID     string
	Season     int
	EpisodeIDs []string
}

// SeasonsWithUnscrapedEpisodes 找出「季本身已刮过、但里面有单集没刮」的组。
// 全局刮削只处理电影/剧集/季，不直接处理单集；新增集数需要单独补。
func (d *DB) SeasonsWithUnscrapedEpisodes() ([]SeasonEpisodes, error) {
	rows, err := d.Query(`
SELECT s.id, s.parent_id, COALESCE(s.tmdb_id, ''), s.index_number, e.id
FROM items e
JOIN items s ON s.id = e.season_id
WHERE e.type = 'Episode' AND e.scraped_at = 0 AND e.season_id <> ''
  AND s.type = 'Season' AND s.tmdb_id <> ''
ORDER BY s.id, e.parent_index_number, e.index_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SeasonEpisodes, 0, 64)
	index := make(map[string]int, 64)
	for rows.Next() {
		var (
			seasonID, seriesID, tmdbID, episodeID string
			season                                int
		)
		if err := rows.Scan(&seasonID, &seriesID, &tmdbID, &season, &episodeID); err != nil {
			return nil, err
		}
		i, ok := index[seasonID]
		if !ok {
			i = len(out)
			index[seasonID] = i
			out = append(out, SeasonEpisodes{
				SeasonID: seasonID,
				SeriesID: seriesID,
				TmdbID:   tmdbID,
				Season:   season,
			})
		}
		out[i].EpisodeIDs = append(out[i].EpisodeIDs, episodeID)
	}
	return out, rows.Err()
}
