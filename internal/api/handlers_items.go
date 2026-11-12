package api

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"lime/internal/dto"
	"lime/internal/store"
	"lime/internal/strm"
)

// imageTag 是图片缓存标记，客户端会拼在图片 URL 的 tag 参数里。
// 本地图片（local:）要把 mtime/size 一起算进去，否则用户替换 poster.jpg 后
// 路径没变、tag 没变，客户端会一直显示旧图。
func imageTag(path string) string {
	if path == "" {
		return ""
	}
	key := path
	if strings.HasPrefix(path, "local:") {
		full := strings.TrimPrefix(path, "local:")
		if st, err := os.Stat(full); err == nil {
			key = fmt.Sprintf("%s|%d|%d", path, st.ModTime().UnixNano(), st.Size())
		}
	}
	sum := sha1.Sum([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}

// sortNameOf 返回给客户端的排序名：中文标题用拼音键，老数据回退原排序名。
// 客户端（Sen / VidHub）本地排序也按这个字段，所以必须给拼音而不是汉字。
func sortNameOf(it *store.Item) string {
	if it.SortKey != "" {
		return it.SortKey
	}
	return it.SortName
}

func imageTags(path string) map[string]string {
	if path == "" {
		return map[string]string{}
	}
	return map[string]string{"Primary": imageTag(path)}
}

func backdropTags(path string) []string {
	if path == "" {
		return []string{}
	}
	return []string{imageTag(path)}
}

// buildUserData 把 playstates 转成客户端认的 UserData。
func buildUserData(itemID string, runTimeTicks int64, ps *store.Playstate) *dto.UserItemDataDto {
	ud := &dto.UserItemDataDto{
		Key:    itemID,
		ItemId: itemID,
	}
	if ps == nil {
		return ud
	}

	ud.PlaybackPositionTicks = ps.PositionTicks
	ud.Played = ps.Played
	ud.IsFavorite = ps.IsFavorite

	if ps.Played {
		ud.PlayCount = 1
		ud.LastPlayedDate = ps.UpdatedAt.UTC().Format(time.RFC3339Nano)
		return ud
	}
	// 有进度但没看完：给客户端画进度条。时长未知时不填，避免算出离谱百分比。
	if ps.PositionTicks > 0 && runTimeTicks > 0 {
		pct := float64(ps.PositionTicks) / float64(runTimeTicks) * 100
		if pct > 100 {
			pct = 100
		}
		ud.PlayedPercentage = &pct
	}
	return ud
}

// displayName 去掉名称末尾的外文原名，只保留中文标题。
// 例如「借东西的小人阿莉埃蒂 借りぐらしのアリエッティ」→「借东西的小人阿莉埃蒂」。
func displayName(name, original string) string {
	name = strings.TrimSpace(name)
	original = strings.TrimSpace(original)
	if name == "" || original == "" || original == name {
		return name
	}
	if !strings.HasSuffix(name, original) {
		return name
	}
	prefix := strings.TrimSpace(strings.TrimSuffix(name, original))
	prefix = strings.TrimRight(prefix, " /-·|_:：")
	if prefix == "" {
		return name
	}
	return prefix
}

// providerIds 是 Jellyfin 客户端识别外部元数据源的标准字段，
// 很多客户端读它而不是顶层的 TmdbId。
func providerIds(tmdb string) map[string]string {
	if tmdb == "" {
		return nil
	}
	return map[string]string{"Tmdb": tmdb}
}

// itemFields 是请求里的 Fields 参数（Jellyfin 语义：只返回点名要的重字段）。
// nil 表示客户端没传 Fields，保持历史行为返回全部字段。
type itemFields map[string]bool

func parseItemFields(r *http.Request) itemFields {
	raw := strings.TrimSpace(r.URL.Query().Get("Fields"))
	if raw == "" {
		return nil
	}
	set := itemFields{}
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			set[strings.ToLower(p)] = true
		}
	}
	return set
}

func (f itemFields) has(name string) bool {
	if f == nil {
		return true
	}
	return f[strings.ToLower(name)]
}

func (h *Handler) itemToDto(it *store.Item, ps *store.Playstate, fields ...itemFields) *dto.BaseItemDto {
	var f itemFields
	if len(fields) > 0 {
		f = fields[0]
	}
	tags := imageTags(it.ImagePath)
	if it.LogoPath != "" {
		tags["Logo"] = imageTag(it.LogoPath)
	}
	d := &dto.BaseItemDto{
		Name:              displayName(it.Name, it.OriginalTitle),
		ServerId:          h.serverID,
		Id:                it.ID,
		SortName:          sortNameOf(it),
		OriginalTitle:     it.OriginalTitle,
		ProductionYear:    it.ProductionYear,
		PremiereDate:      it.PremiereDate,
		TmdbId:            it.TmdbID,
		ProviderIds:       providerIds(it.TmdbID),
		RunTimeTicks:      it.RunTimeTicks,
		ImageTags:         tags,
		PrimaryImageTag:   tags["Primary"],
		BackdropImageTags: backdropTags(it.BackdropPath),
		ImageBlurHashes:   map[string]any{},
		LocationType:      "Remote",
		Type:              it.Type,
		ParentId:          it.ParentID,
		SeriesId:          it.SeriesID,
		SeasonId:          it.SeasonID,
		IsFolder:          it.Type == store.TypeSeries || it.Type == store.TypeSeason,
		OfficialRating:    it.OfficialRating,
		Genres:            parseGenres(it.Genres),
		People:            []dto.PersonDto{},
		UserData:          buildUserData(it.ID, it.RunTimeTicks, ps),
		DateCreated:       it.CreatedAt.UTC().Format(time.RFC3339Nano),
	}

	// 简介和演员表是列表响应里最占字节的两块，客户端没点名就不填。
	if f.has("Overview") {
		d.Overview = it.Overview
	}
	if f.has("People") {
		d.People = parsePeople(it.People)
	}

	if tags["Primary"] != "" {
		// 海报统一按 2:3 处理，部分客户端据此排版。
		d.PrimaryImageAspectRatio = 2.0 / 3.0
	}

	if it.Type == store.TypeMovie || it.Type == store.TypeEpisode ||
		it.Type == store.TypeSeries || it.Type == store.TypeSeason {
		d.MediaType = "Video"
	}

	if it.IndexNumber > 0 {
		v := it.IndexNumber
		d.IndexNumber = &v
	}
	if it.ParentIndexNumber > 0 {
		v := it.ParentIndexNumber
		d.ParentIndexNumber = &v
	}
	if it.CommunityRating > 0 {
		v := it.CommunityRating
		d.CommunityRating = &v
	}
	return d
}

// maxListStreamSources 限制列表响应里带轨道的片源数量。
// 大列表（>500 个片源）不带轨道，避免 payload 和 SQL 变量数失控。
const maxListStreamSources = 500

// listMediaSources 给列表响应生成片源信息。字段与详情保持一致（含轨道），
// 但不扫外挂字幕：列表一次几十上百条，没必要挨个 stat 同目录。
//
// 客户端（SenPlayer 等）打开集详情时直接拿 Episodes 列表里这条渲染「媒体信息」，
// 只给空 MediaStreams 会让剧集页永远没有视频/音频参数。
func (h *Handler) listMediaSources(it *store.Item, srcs []*store.MediaSource, streamsBy map[string][]*store.MediaStream) []dto.MediaSourceDto {
	out := make([]dto.MediaSourceDto, 0, len(srcs))
	for _, s := range srcs {
		bitrate := sourceBitrate(s, it.RunTimeTicks)
		ms, defaultAudio := h.mediaStreamDtos(nil, "", s, streamsBy[s.ID], bitrate, false)
		out = append(out, dto.MediaSourceDto{
			Protocol:                "Http",
			Id:                      s.ID,
			Path:                    s.StrmPath,
			Type:                    "Default",
			Name:                    sourceDisplayName(s, streamsBy[s.ID], bitrate),
			Container:               s.Container,
			Size:                    s.Size,
			Bitrate:                 bitrate,
			IsRemote:                true,
			SupportsDirectPlay:      true,
			SupportsDirectStream:    true,
			SupportsTranscoding:     false,
			RunTimeTicks:            it.RunTimeTicks,
			MediaStreams:            ms,
			DefaultAudioStreamIndex: defaultAudio,
		})
	}
	return out
}

// streamsForSources 一次批量取出本页所有片源的轨道。
func (h *Handler) streamsForSources(sourcesByItem map[string][]*store.MediaSource) map[string][]*store.MediaStream {
	ids := make([]string, 0, len(sourcesByItem)*2)
	for _, srcs := range sourcesByItem {
		if len(ids) >= maxListStreamSources {
			break
		}
		for _, s := range srcs {
			if len(ids) >= maxListStreamSources {
				break
			}
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	m, err := h.db.MediaStreamsBySources(ids)
	if err != nil {
		h.log.Warn("load media streams", "err", err)
		return nil
	}
	return m
}

func parseGenres(raw string) []string {
	out := []string{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func parsePeople(raw string) []dto.PersonDto {
	out := []dto.PersonDto{}
	if raw == "" {
		return out
	}
	var rows []struct {
		Name     string `json:"Name"`
		Role     string `json:"Role"`
		Type     string `json:"Type"`
		ImageTag string `json:"ImageTag"`
	}
	if json.Unmarshal([]byte(raw), &rows) != nil {
		return out
	}
	for _, p := range rows {
		if p.Name == "" {
			continue
		}
		typ := p.Type
		if typ == "" {
			typ = "Actor"
		}
		imageTags := map[string]string{}
		if p.ImageTag != "" {
			imageTags["Primary"] = p.ImageTag
		}
		out = append(out, dto.PersonDto{
			Name:            p.Name,
			Id:              store.PersonID(p.Name),
			Role:            p.Role,
			Type:            typ,
			ImageTags:       imageTags,
			PrimaryImageTag: imageTags["Primary"],
		})
	}
	return out
}

// itemsToDto 批量转换，一次查完本页所有条目的播放状态，避免 N+1。
func (h *Handler) itemsToDto(user *store.User, items []*store.Item, fields ...itemFields) []*dto.BaseItemDto {
	out := make([]*dto.BaseItemDto, 0, len(items))
	if len(items) == 0 {
		return out
	}
	var f itemFields
	if len(fields) > 0 {
		f = fields[0]
	}

	ids := make([]string, 0, len(items))
	var seriesIDs, seasonIDs, episodeParentIDs []string
	for _, it := range items {
		ids = append(ids, it.ID)
		switch it.Type {
		case store.TypeSeries:
			seriesIDs = append(seriesIDs, it.ID)
		case store.TypeSeason:
			seasonIDs = append(seasonIDs, it.ID)
		case store.TypeEpisode:
			// 部分客户端（网易爆米花等）按 SeriesName/SeasonName 分组渲染集列表。
			if it.SeriesID != "" {
				episodeParentIDs = append(episodeParentIDs, it.SeriesID)
			}
			if it.SeasonID != "" {
				episodeParentIDs = append(episodeParentIDs, it.SeasonID)
			}
		}
	}

	states, err := h.db.Playstates(user.ID, ids)
	if err != nil {
		h.log.Warn("load playstates", "user", user.ID, "err", err)
		states = nil
	}

	var seriesProgress, seasonProgress map[string]store.WatchProgress
	if len(seriesIDs) > 0 {
		seriesProgress, err = h.db.WatchProgress(user.ID, "series_id", seriesIDs)
		if err != nil {
			h.log.Warn("load series progress", "err", err)
		}
	}
	if len(seasonIDs) > 0 {
		seasonProgress, err = h.db.WatchProgress(user.ID, "season_id", seasonIDs)
		if err != nil {
			h.log.Warn("load season progress", "err", err)
		}
	}

	// ChildCount / RecursiveItemCount 是 Jellyfin 的标准字段，缺了会让
	// 部分客户端认为季是空的不渲染集列表。
	seasonCounts, seriesSeasons, seriesEpisodes, err := h.db.ItemChildren(seriesIDs, seasonIDs)
	if err != nil {
		h.log.Warn("load child counts", "err", err)
	}

	var parentNames map[string]string
	if len(episodeParentIDs) > 0 {
		parentNames = make(map[string]string, len(episodeParentIDs))
		if parents, err := h.db.ItemsByIDs(episodeParentIDs); err == nil {
			for id, p := range parents {
				parentNames[id] = p.Name
			}
		}
	}

	// 片源信息：部分客户端（网易爆米花）靠 MediaSources / MediaSourceCount
	// 判断条目能否播放，缺了会把集卡片渲染成空骨架。
	sourcesByItem, err := h.db.MediaSourcesByItems(ids)
	if err != nil {
		h.log.Warn("load media sources", "err", err)
	}
	var streamsBy map[string][]*store.MediaStream
	if len(sourcesByItem) > 0 {
		streamsBy = h.streamsForSources(sourcesByItem)
	}

	for _, it := range items {
		var ps *store.Playstate
		if s, ok := states[it.ID]; ok {
			ps = &s
		}
		d := h.itemToDto(it, ps, f)

		// 剧集/季本身没有进度，用下属集的未看数量撑起角标。
		switch it.Type {
		case store.TypeSeries:
			applyAggregate(d.UserData, seriesProgress[it.ID])
			if n, ok := seriesSeasons[it.ID]; ok {
				d.ChildCount = &n
			}
			if n, ok := seriesEpisodes[it.ID]; ok {
				d.RecursiveItemCount = &n
			}
		case store.TypeSeason:
			applyAggregate(d.UserData, seasonProgress[it.ID])
			if n, ok := seasonCounts[it.ID]; ok {
				d.ChildCount = &n
			}
		case store.TypeEpisode:
			d.SeriesName = parentNames[it.SeriesID]
			d.SeasonName = parentNames[it.SeasonID]
			if srcs := sourcesByItem[it.ID]; len(srcs) > 0 {
				d.MediaSourceCount = len(srcs)
				d.MediaSources = h.listMediaSources(it, srcs, streamsBy)
			}
		case store.TypeMovie:
			if srcs := sourcesByItem[it.ID]; len(srcs) > 0 {
				d.MediaSourceCount = len(srcs)
				d.MediaSources = h.listMediaSources(it, srcs, streamsBy)
			}
		}
		out = append(out, d)
	}
	return out
}

func applyAggregate(ud *dto.UserItemDataDto, p store.WatchProgress) {
	if p.Total == 0 {
		return
	}
	unplayed := p.Unplayed
	ud.UnplayedItemCount = &unplayed
	ud.Played = p.Unplayed == 0
	if ud.Played {
		ud.PlayCount = 1
	}
}

func (h *Handler) queryItems(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	q := parseItemQuery(r)
	// 播放状态/收藏/续播筛选都需要用户 ID。
	q.UserID = user.ID

	allowed, all, aerr := h.allowedLibraries(user)
	if aerr != nil {
		h.log.Error("load library access", "err", aerr)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !all {
		q.LibraryIDs = allowed
	}
	applyRatingLimit(user, &q)

	// 兜底上限：客户端不带 Limit 时不要把整个库塞进一次响应。
	// TotalRecordCount 仍是全量，客户端可以继续用 StartIndex 翻页。
	maxItems := h.cfg.MaxItems
	if maxItems <= 0 {
		maxItems = 20000
	}
	if q.Limit <= 0 || q.Limit > maxItems {
		if q.Limit > maxItems {
			h.log.Warn("clamp item limit", "requested", q.Limit, "max", maxItems)
		}
		q.Limit = maxItems
	}

	// 对齐 Jellyfin：没有 ParentId 时父级是用户根目录，非递归只返回它的
	// 子项（媒体库视图），而不是把整个库铺平。爆米花等客户端正是靠这个
	// 请求拿首页，旧逻辑会一次吐出 26 万条（实测 15s / 179MB）。
	if q.ParentID == "" && !q.Recursive && q.SearchTerm == "" {
		views, err := h.libraryViews()
		if err != nil {
			h.log.Error("load library views", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		views = filterByTypes(views, q.Types, q.ExcludeTypes)
		views = filterViewsByAccess(views, allowed, all)

		total := len(views)
		start := q.StartIndex
		if start > len(views) {
			start = len(views)
		}
		end := len(views)
		if q.Limit > 0 && start+q.Limit < end {
			end = start + q.Limit
		}

		writeJSON(w, http.StatusOK, dto.QueryResult{
			Items:            views[start:end],
			TotalRecordCount: total,
			StartIndex:       q.StartIndex,
		})
		return
	}

	items, total, err := h.db.QueryItems(q)
	if err != nil {
		h.log.Error("query items", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := h.itemsToDto(user, items, parseItemFields(r))

	writeJSON(w, http.StatusOK, dto.QueryResult{
		Items:            out,
		TotalRecordCount: total,
		StartIndex:       q.StartIndex,
	})
}

func filterByTypes(items []*dto.BaseItemDto, types, excludes []string) []*dto.BaseItemDto {
	if len(types) == 0 && len(excludes) == 0 {
		return items
	}
	out := make([]*dto.BaseItemDto, 0, len(items))
	for _, it := range items {
		if len(types) > 0 && !containsFold(types, it.Type) {
			continue
		}
		if containsFold(excludes, it.Type) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

func (h *Handler) getItem(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	id := r.PathValue("itemId")
	it, err := h.db.GetItem(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if !h.canAccessLibrary(user, it.LibraryID) || !ratingAllowed(user, it) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	d := h.itemsToDto(user, []*store.Item{it}, parseItemFields(r))[0]

	sources, err := h.db.MediaSourcesByItem(id)
	if err == nil {
		d.MediaSources = make([]dto.MediaSourceDto, 0, len(sources))
		for _, s := range sources {
			// 后台补全媒体信息；这次响应先用已有（文件名解析）数据。
			h.prober.Enqueue(s)
			d.MediaSources = append(d.MediaSources, h.mediaSourceDto(r, id, s, it.RunTimeTicks))
		}
	}

	writeJSON(w, http.StatusOK, d)
}

// sourceBitrate 用文件大小和总时长估算码率；优先用片源自己的时长（探测过才有）。
func sourceBitrate(s *store.MediaSource, runtimeTicks int64) int64 {
	if s.Size <= 0 {
		return 0
	}
	ticks := s.DurationTicks
	if ticks <= 0 {
		ticks = runtimeTicks
	}
	if ticks <= 0 {
		return 0
	}
	seconds := float64(ticks) / 10_000_000
	if seconds <= 0 {
		return 0
	}
	return int64(float64(s.Size) * 8 / seconds)
}

// sourceDisplayName 生成多版本片源的展示名：DV/HDR/SDR · 分辨率 · 码率。
func sourceDisplayName(s *store.MediaSource, streams []*store.MediaStream, bitrate int64) string {
	var video *store.MediaStream
	for _, st := range streams {
		if st.Type == "Video" {
			video = st
			break
		}
	}
	if video != nil {
		parts := make([]string, 0, 3)
		if r := rangeLabel(video.VideoRange); r != "" {
			parts = append(parts, r)
		}
		if res := resolutionLabel(video.Height); res != "" {
			parts = append(parts, res)
		}
		if bitrate > 0 {
			parts = append(parts, fmt.Sprintf("%.1f Mbps", float64(bitrate)/1e6))
		} else if s.Size > 0 {
			parts = append(parts, humanSize(s.Size))
		}
		if len(parts) > 0 {
			return strings.Join(parts, " · ")
		}
	}

	name := s.VersionName
	if name == "" {
		name = s.FileName
	}
	if name == "" {
		name = "Default"
	}
	return name
}

// 外挂字幕的流序号从固定基址开始：容器轨道数是 ffprobe 探测完才确定的，
// 用「容器轨道数 + i」编号的话，探测一完成序号就整体后移，客户端缓存下来的
// 序号会 404。固定基址与容器轨道数无关，探测前后都稳定。
const externalSubtitleIndexBase = 10000

func (h *Handler) subtitleURL(r *http.Request, itemID, sourceID string, index int, format string) string {
	return externalBase(r) + "/Videos/" + itemID + "/" + sourceID +
		"/Subtitles/" + strconv.Itoa(index) + "/Stream." + format
}

func rangeLabel(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "", "SDR":
		return "SDR"
	case "DOVI", "DV", "DOLBY VISION":
		return "DV"
	case "HDR10+":
		return "HDR10+"
	case "HDR10":
		return "HDR10"
	case "HDR":
		return "HDR"
	default:
		return strings.ToUpper(strings.TrimSpace(v))
	}
}

func resolutionLabel(height int) string {
	switch {
	case height >= 2000:
		return "2160p"
	case height >= 1000:
		return "1080p"
	case height >= 700:
		return "720p"
	case height > 0:
		return fmt.Sprintf("%dp", height)
	default:
		return ""
	}
}

func humanSize(size int64) string {
	if size >= 1_000_000_000 {
		return fmt.Sprintf("%.1fGB", float64(size)/1e9)
	}
	return fmt.Sprintf("%.0fMB", float64(size)/1e6)
}

func (h *Handler) mediaSourceDto(r *http.Request, itemID string, s *store.MediaSource, runtimeTicks int64) dto.MediaSourceDto {
	streams, _ := h.db.MediaStreamsBySource(s.ID)

	bitrate := sourceBitrate(s, runtimeTicks)
	ms, defaultAudio := h.mediaStreamDtos(r, itemID, s, streams, bitrate, true)

	name := sourceDisplayName(s, streams, bitrate)

	return dto.MediaSourceDto{
		Protocol:                   "Http",
		Id:                         s.ID,
		Path:                       h.absoluteStreamURL(r, itemID, s.ID),
		DirectStreamUrl:            h.absoluteStreamURL(r, itemID, s.ID),
		Type:                       "Default",
		Name:                       name,
		Container:                  s.Container,
		Size:                       s.Size,
		Bitrate:                    bitrate,
		IsRemote:                   true,
		SupportsDirectPlay:         true,
		SupportsDirectStream:       true,
		SupportsTranscoding:        false,
		RunTimeTicks:               s.DurationTicks,
		MediaStreams:               ms,
		DefaultAudioStreamIndex:    defaultAudio,
		DefaultSubtitleStreamIndex: nil,
	}
}

// mediaStreamDtos 把库里的轨道转成 Jellyfin DTO。
// externalSubs=true 时顺带扫同目录外挂字幕（要 stat 目录，列表响应传 false）。
// r 为 nil 时只可能是列表场景，此时 externalSubs 必为 false。
func (h *Handler) mediaStreamDtos(r *http.Request, itemID string, s *store.MediaSource, streams []*store.MediaStream, bitrate int64, externalSubs bool) ([]dto.MediaStreamDto, *int) {
	ms := make([]dto.MediaStreamDto, 0, len(streams))
	var defaultAudio *int

	for _, st := range streams {
		d := dto.MediaStreamDto{
			Type:                   st.Type,
			Index:                  st.Index,
			Codec:                  st.Codec,
			Width:                  st.Width,
			Height:                 st.Height,
			BitRate:                st.BitRate,
			BitDepth:               st.BitDepth,
			Channels:               st.Channels,
			VideoRange:             st.VideoRange,
			VideoRangeType:         videoRangeType(st.VideoRange),
			Profile:                st.Profile,
			AspectRatio:            st.AspectRatio,
			IsInterlaced:           st.IsInterlaced,
			ColorPrimaries:         st.ColorPrimaries,
			ColorTransfer:          st.ColorTransfer,
			ColorSpace:             st.ColorSpace,
			PixelFormat:            st.PixelFormat,
			SampleRate:             st.SampleRate,
			ChannelLayout:          st.ChannelLayout,
			IsDefault:              st.IsDefault || st.Index == 0,
			IsForced:               st.IsForced,
			IsExternal:             false,
			IsTextSubtitleStream:   st.Type == "Subtitle",
			SupportsExternalStream: st.Type == "Subtitle",
			DeliveryMethod:         "File",
			DisplayTitle:           streamDisplayTitle(st),
			Language:               st.Language,
			Title:                  st.Title,
		}
		if st.Type == "Video" {
			// 视频轨道常常没有单独码率，用文件大小/时长估算兜底。
			if d.BitRate <= 0 {
				d.BitRate = bitrate
			}
		}
		if st.FrameRate > 0 {
			v := st.FrameRate
			d.RealFrameRate = &v
			d.AverageFrameRate = &v
		}
		if st.Level > 0 {
			v := st.Level
			d.Level = &v
		}
		if st.Type == "Audio" && defaultAudio == nil {
			idx := st.Index
			defaultAudio = &idx
			d.IsDefault = true
		}
		ms = append(ms, d)
	}

	// 外挂字幕：同目录、同文件名的 .srt/.ass/.ssa/.vtt
	if externalSubs && r != nil {
		for i, sub := range strm.FindSubtitles(s.StrmPath) {
			index := externalSubtitleIndexBase + i
			ms = append(ms, dto.MediaStreamDto{
				Type:                   "Subtitle",
				Index:                  index,
				Codec:                  sub.Format,
				IsDefault:              false,
				IsExternal:             true,
				IsTextSubtitleStream:   true,
				SupportsExternalStream: true,
				DeliveryMethod:         "External",
				DeliveryUrl:            h.subtitleURL(r, itemID, s.ID, index, sub.Format),
				Language:               sub.Language,
				Title:                  sub.Title,
				DisplayTitle:           sub.Title,
			})
		}
	}

	return ms, defaultAudio
}

func (h *Handler) userViews(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	allowed, all, err := h.allowedLibraries(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	items, err := h.libraryViews()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	items = filterViewsByAccess(items, allowed, all)

	writeJSON(w, http.StatusOK, dto.QueryResult{
		Items:            items,
		TotalRecordCount: len(items),
	})
}

// libraryViews 把媒体库映射成 Jellyfin 的 CollectionFolder。
// Jellyfin 在没有 ParentId 时会把查询落在 UserRootFolder 上，非递归返回的
// 就是这些视图；不能退回「全库无过滤」，否则 26 万条会一次性返回。
func (h *Handler) libraryViews() ([]*dto.BaseItemDto, error) {
	libs, err := h.db.ListLibraries()
	if err != nil {
		return nil, err
	}

	items := make([]*dto.BaseItemDto, 0, len(libs))
	for _, lib := range libs {
		_, primaryTag, _, backdropTag := h.libraryArtwork(lib)
		imageTags := map[string]string{}
		if primaryTag != "" {
			imageTags["Primary"] = primaryTag
		}
		backdrops := []string{}
		if backdropTag != "" {
			backdrops = append(backdrops, backdropTag)
		}
		items = append(items, &dto.BaseItemDto{
			Name:              lib.Name,
			ServerId:          h.serverID,
			Id:                lib.ID,
			SortName:          lib.Name,
			ImageTags:         imageTags,
			PrimaryImageTag:   primaryTag,
			BackdropImageTags: backdrops,
			ImageBlurHashes:   map[string]any{},
			// Emby 客户端按数组解析，null 会导致部分客户端渲染中断。
			Genres:         []string{},
			People:         []dto.PersonDto{},
			LocationType:   "Remote",
			Type:           "CollectionFolder",
			IsFolder:       true,
			CollectionType: collectionType(lib.Kind),
			UserData:       &dto.UserItemDataDto{Key: lib.ID, ItemId: lib.ID},
		})
	}
	return items, nil
}

func (h *Handler) showSeasons(w http.ResponseWriter, r *http.Request) {
	h.queryByParent(w, r, r.PathValue("seriesId"), []string{store.TypeSeason}, false)
}

func (h *Handler) showEpisodes(w http.ResponseWriter, r *http.Request) {
	h.queryByParent(w, r, r.PathValue("seriesId"), []string{store.TypeEpisode}, true)
}

func (h *Handler) queryByParent(w http.ResponseWriter, r *http.Request, parentID string, types []string, recursive bool) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	parent, err := h.db.GetItem(parentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !h.canAccessLibrary(user, parent.LibraryID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	q := store.ItemQuery{
		ParentID:   parentID,
		Recursive:  recursive,
		Types:      types,
		SeasonID:   r.URL.Query().Get("SeasonId"),
		StartIndex: atoiDefault(r.URL.Query().Get("StartIndex"), 0),
		Limit:      atoiDefault(r.URL.Query().Get("Limit"), 0),
	}
	applyRatingLimit(user, &q)

	items, total, err := h.db.QueryItems(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, dto.QueryResult{
		Items:            h.itemsToDto(user, items, parseItemFields(r)),
		TotalRecordCount: total,
		StartIndex:       q.StartIndex,
	})
}

func (h *Handler) latestItems(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	v := r.URL.Query()
	limit := atoiDefault(v.Get("Limit"), 20)
	types := normalizeTypes(splitCSV(v.Get("IncludeItemTypes")))
	parentID := v.Get("ParentId")
	groupItems := !strings.EqualFold(v.Get("GroupItems"), "false")

	allowed, all, aerr := h.allowedLibraries(user)
	if aerr != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if parentID != "" && !all {
		ok := true
		if lib, err := h.db.GetLibrary(parentID); err == nil {
			ok = h.canAccessLibrary(user, lib.ID)
		} else if it, err := h.db.GetItem(parentID); err == nil {
			ok = h.canAccessLibrary(user, it.LibraryID)
		}
		if !ok {
			writeJSON(w, http.StatusOK, []*dto.BaseItemDto{})
			return
		}
	}

	// 剧集库的「最新」按剧集吐卡片：同一部剧的多个新单集只出现一次。
	// 否则首页会变成「第 180 集、第 181 集、第 182 集……」，全是同一部剧。
	if parentID != "" && groupItems {
		if lib, err := h.db.GetLibrary(parentID); err == nil && lib.Kind == "tvshows" {
			wantSeries := len(types) == 0 || containsFold(types, store.TypeSeries)
			wantEpisodes := containsFold(types, store.TypeEpisode)
			if wantSeries && !wantEpisodes {
				items, err := h.db.LatestSeries(parentID, limit)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "internal error")
					return
				}
				writeJSON(w, http.StatusOK, h.itemsToDto(user, filterByRating(user, items), parseItemFields(r)))
				return
			}
		}
	}

	if len(types) == 0 {
		// 电影/混合库默认返回电影和单集。
		types = []string{store.TypeMovie, store.TypeEpisode}
	}
	q := store.ItemQuery{
		ParentID:   parentID,
		Recursive:  parentID != "",
		Types:      types,
		SortBy:     []string{"DateCreated"},
		Descending: true,
		Limit:      limit,
	}
	if !all {
		q.LibraryIDs = allowed
	}
	applyRatingLimit(user, &q)

	items, _, err := h.db.QueryItems(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Jellyfin 的 /Items/Latest 返回裸数组，不是 QueryResult。
	// 爆米花等客户端按数组解析，返回对象会导致首页各库横排空白。
	writeJSON(w, http.StatusOK, h.latestToDto(user, items, parseItemFields(r)))
}

// latestToDto 在 itemsToDto 基础上补单集的 SeriesName / SeasonName。
// 首页单集卡片要靠这两个字段显示归属剧名，缺失时多数客户端不渲染。
func (h *Handler) latestToDto(user *store.User, items []*store.Item, fields ...itemFields) []*dto.BaseItemDto {
	out := h.itemsToDto(user, items, fields...)
	if len(items) == 0 {
		return out
	}

	ids := make([]string, 0, len(items)*2)
	for _, it := range items {
		if it.SeriesID != "" {
			ids = append(ids, it.SeriesID)
		}
		if it.SeasonID != "" {
			ids = append(ids, it.SeasonID)
		}
	}
	if len(ids) == 0 {
		return out
	}

	byID, err := h.db.ItemsByIDs(ids)
	if err != nil {
		return out
	}
	for i, it := range items {
		if s, ok := byID[it.SeriesID]; ok {
			out[i].SeriesName = s.Name
		}
		if s, ok := byID[it.SeasonID]; ok {
			out[i].SeasonName = s.Name
		}
	}
	return out
}

// resumeItems 返回继续观看列表。数据来自 playstates：有进度、没看完。
func (h *Handler) resumeItems(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	v := r.URL.Query()
	limit := atoiDefault(v.Get("Limit"), 20)
	start := atoiDefault(v.Get("StartIndex"), 0)
	types := normalizeTypes(splitCSV(v.Get("IncludeItemTypes")))

	allowed, all, aerr := h.allowedLibraries(user)
	if aerr != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var libIDs []string
	if !all {
		libIDs = allowed
	}
	// 权限/分级过滤下推到 SQL：既避免分页后过滤导致页不满，
	// 也保证 TotalRecordCount 是过滤后的真实总数。
	ids, total, err := h.db.ResumeItemIDs(user.ID, types, libIDs, maxRatingOf(user), limit, start)
	if err != nil {
		h.log.Error("resume items", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	byID, err := h.db.ItemsByIDs(ids)
	if err != nil {
		h.log.Error("load resume items", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// ResumeItemIDs 已按最近播放排好序，这里保持顺序。
	items := make([]*store.Item, 0, len(ids))
	for _, id := range ids {
		if it, ok := byID[id]; ok {
			items = append(items, it)
		}
	}

	writeJSON(w, http.StatusOK, dto.QueryResult{
		Items:            h.itemsToDto(user, items, parseItemFields(r)),
		TotalRecordCount: total,
		StartIndex:       start,
	})
}

// nextUp 返回看过的剧集接下来该看的一集，对应 Jellyfin 的「接下来看」。
func (h *Handler) nextUp(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	allowed, all, aerr := h.allowedLibraries(user)
	if aerr != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var libIDs []string
	if !all {
		libIDs = allowed
	}

	v := r.URL.Query()
	start := atoiDefault(v.Get("StartIndex"), 0)
	items, total, err := h.db.NextUp(user.ID,
		atoiDefault(v.Get("Limit"), 25), start, v.Get("SeriesId"),
		libIDs, maxRatingOf(user))
	if err != nil {
		h.log.Error("next up", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, dto.QueryResult{
		Items:            h.itemsToDto(user, items, parseItemFields(r)),
		TotalRecordCount: total,
		StartIndex:       start,
	})
}

func parseItemQuery(r *http.Request) store.ItemQuery {
	v := r.URL.Query()
	filters := splitCSV(v.Get("Filters"))
	return store.ItemQuery{
		ParentID:        v.Get("ParentId"),
		Recursive:       strings.EqualFold(v.Get("Recursive"), "true"),
		Types:           normalizeTypes(splitCSV(v.Get("IncludeItemTypes"))),
		ExcludeTypes:    normalizeTypes(splitCSV(v.Get("ExcludeItemTypes"))),
		Genres:          splitCSV(v.Get("Genres")),
		Years:           parseInts(v.Get("Years")),
		OfficialRatings: splitCSV(v.Get("OfficialRatings")),
		SeasonID:        v.Get("SeasonId"),
		Person:          v.Get("Person"),
		FavoriteOnly: strings.EqualFold(v.Get("IsFavorite"), "true") ||
			containsFold(filters, "IsFavorite"),
		PlayedOnly: strings.EqualFold(v.Get("IsPlayed"), "true") ||
			containsFold(filters, "IsPlayed"),
		// Jellyfin 的 IsPlayed=false 等价于只看未看。
		UnplayedOnly: strings.EqualFold(v.Get("IsPlayed"), "false") ||
			containsFold(filters, "IsUnplayed"),
		ResumableOnly: strings.EqualFold(v.Get("IsResumable"), "true") ||
			containsFold(filters, "IsResumable"),
		SearchTerm: v.Get("SearchTerm"),
		SortBy:     splitCSV(v.Get("SortBy")),
		Descending: strings.EqualFold(v.Get("SortOrder"), "Descending"),
		StartIndex: atoiDefault(v.Get("StartIndex"), 0),
		Limit:      atoiDefault(v.Get("Limit"), 0),
	}
}

func parseInts(s string) []int {
	parts := splitCSV(s)
	if len(parts) == 0 {
		return nil
	}
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func normalizeTypes(types []string) []string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		switch strings.ToLower(t) {
		case "movie":
			out = append(out, store.TypeMovie)
		case "series":
			out = append(out, store.TypeSeries)
		case "season":
			out = append(out, store.TypeSeason)
		case "episode":
			out = append(out, store.TypeEpisode)
		default:
			out = append(out, t)
		}
	}
	return out
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func collectionType(kind string) string {
	switch kind {
	case "tvshows":
		return "tvshows"
	case "mixed":
		return "mixed"
	default:
		return "movies"
	}
}

// streamDisplayTitle 给客户端音轨/视频轨选择器用的可读标题。
func streamDisplayTitle(st *store.MediaStream) string {
	parts := make([]string, 0, 4)
	switch st.Type {
	case "Audio":
		if st.Title != "" {
			parts = append(parts, st.Title)
		} else if st.Language != "" {
			parts = append(parts, languageLabel(st.Language))
		}
		if st.Codec != "" {
			parts = append(parts, strings.ToUpper(st.Codec))
		}
		if st.ChannelLayout != "" {
			parts = append(parts, st.ChannelLayout)
		} else if st.Channels > 0 {
			parts = append(parts, channelLabel(st.Channels))
		}
	case "Subtitle":
		if st.Title != "" {
			parts = append(parts, st.Title)
		} else if st.Language != "" {
			parts = append(parts, languageLabel(st.Language))
		}
		if st.Codec != "" {
			parts = append(parts, strings.ToUpper(st.Codec))
		}
	default:
		if st.Height >= 2000 {
			parts = append(parts, "4K")
		} else if st.Height >= 1000 {
			parts = append(parts, "1080p")
		} else if st.Height >= 700 {
			parts = append(parts, "720p")
		}
		if st.VideoRange != "" {
			parts = append(parts, st.VideoRange)
		}
		if st.Codec != "" {
			parts = append(parts, strings.ToUpper(st.Codec))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// videoRangeType 对齐 Jellyfin 的枚举值。
func videoRangeType(v string) string {
	switch strings.ToUpper(v) {
	case "DOVI":
		return "DOVI"
	case "HDR10", "HDR10+", "HLG", "SDR":
		return strings.ToUpper(v)
	case "HDR":
		return "HDR10"
	}
	return v
}

func languageLabel(code string) string {
	switch strings.ToLower(code) {
	case "chi", "zho", "zh":
		return "国语"
	case "yue":
		return "粤语"
	case "jpn", "ja":
		return "日语"
	case "eng", "en":
		return "英语"
	case "kor", "ko":
		return "韩语"
	default:
		return strings.ToUpper(code)
	}
}

func channelLabel(ch int) string {
	switch ch {
	case 2:
		return "2.0"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	default:
		return fmt.Sprintf("%dch", ch)
	}
}
