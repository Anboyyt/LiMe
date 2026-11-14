package api

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"lime/internal/dto"
	"lime/internal/store"
)

// itemCounts 对应 Emby 的 GET /Items/Counts，部分客户端首页/统计会用。
func (h *Handler) itemCounts(w http.ResponseWriter, r *http.Request) {
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

	counts, err := h.db.CountItemsByTypeIn(libIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	total := counts[store.TypeMovie] + counts[store.TypeSeries] +
		counts[store.TypeSeason] + counts[store.TypeEpisode]

	writeJSON(w, http.StatusOK, map[string]any{
		"MovieCount":      counts[store.TypeMovie],
		"SeriesCount":     counts[store.TypeSeries],
		"EpisodeCount":    counts[store.TypeEpisode],
		"ItemCount":       total,
		"ArtistCount":     0,
		"AlbumCount":      0,
		"SongCount":       0,
		"MusicVideoCount": 0,
		"BoxSetCount":     0,
		"BookCount":       0,
		"TrailerCount":    0,
		"ProgramCount":    0,
		"GameCount":       0,
	})
}

// itemFilters 给客户端的筛选面板提供类型和年份。
func (h *Handler) itemFilters(w http.ResponseWriter, r *http.Request) {
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
	genres, err := h.db.GenreNamesIn(libIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	years, err := h.db.ProductionYearsIn(libIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	ratings, err := h.db.OfficialRatingsIn(libIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Genres":          genres,
		"Tags":            []string{},
		"OfficialRatings": ratings,
		"Years":           years,
	})
}

// genres 返回类型列表，部分客户端会单独请求。
func (h *Handler) genres(w http.ResponseWriter, r *http.Request) {
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
	names, err := h.db.GenreNamesIn(libIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	items := make([]*dto.BaseItemDto, 0, len(names))
	for _, name := range names {
		sum := sha1.Sum([]byte("genre:" + name))
		items = append(items, &dto.BaseItemDto{
			Name:              name,
			ServerId:          h.serverID,
			Id:                hex.EncodeToString(sum[:]),
			Type:              "Genre",
			IsFolder:          true,
			ImageTags:         map[string]string{},
			BackdropImageTags: []string{},
			ImageBlurHashes:   map[string]any{},
			Genres:            []string{},
			People:            []dto.PersonDto{},
		})
	}
	writeJSON(w, http.StatusOK, dto.QueryResult{
		Items:            items,
		TotalRecordCount: len(items),
	})
}

// similarItems 返回同类型的相似条目（电影推电影、剧集推剧集），
// 打分见 store.SimilarItems；客户端详情页和原生 UI 的「相似推荐」都用它。
func (h *Handler) similarItems(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("itemId")
	if _, err := h.db.GetItem(id); err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	items, err := h.db.SimilarItems(id, atoiDefault(r.URL.Query().Get("Limit"), 12))
	if err != nil {
		h.log.Error("similar items", "item", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	items = filterByRating(user, items)
	writeJSON(w, http.StatusOK, dto.QueryResult{
		Items:            h.itemsToDto(user, items, parseItemFields(r)),
		TotalRecordCount: len(items),
		StartIndex:       0,
	})
}

// serverDomains 是 Emby 客户端会探测的扩展信息，返回空数组即可。
func (h *Handler) serverDomains(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []string{})
}

// userImage 没有用户头像，统一返回 LiMe logo。
// 图片请求可能不带 token（浏览器 img / 客户端图片加载），所以不强制鉴权。
func (h *Handler) userImage(w http.ResponseWriter, r *http.Request) {
	serveLogo(w, r)
}

func (h *Handler) studios(w http.ResponseWriter, r *http.Request) {
	h.emptyQuery(w, r)
}

func (h *Handler) emptyQuery(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, dto.QueryResult{
		Items:            []*dto.BaseItemDto{},
		TotalRecordCount: 0,
	})
}

// mediaSegments 返回条目的片头/片尾片段。
// Jellyfin 10.10 的 GET /MediaSegments/{itemId} 返回 QueryResult<MediaSegmentDto>。
func (h *Handler) mediaSegments(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	itemID := r.PathValue("itemId")
	types := parseSegmentTypes(r)
	segs, err := h.db.MediaSegmentsForItem(itemID, types)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load media segments failed")
		return
	}
	items := make([]dto.MediaSegmentDto, 0, len(segs))
	for _, s := range segs {
		items = append(items, dto.MediaSegmentDto{
			Id:         s.ID,
			ItemId:     s.ItemID,
			Type:       s.Type,
			StartTicks: s.StartTicks,
			EndTicks:   s.EndTicks,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Items":            items,
		"TotalRecordCount": len(items),
	})
}

// webSetSegments 手动标记某个条目的片头片尾。手动标记优先于自动章节/指纹。
func (h *Handler) webSetSegments(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var p struct {
		ItemId   string                `json:"ItemId"`
		Segments []dto.MediaSegmentDto `json:"Segments"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	itemID := strings.TrimSpace(p.ItemId)
	if itemID == "" {
		writeError(w, http.StatusBadRequest, "ItemId is required")
		return
	}
	segs := make([]store.MediaSegment, 0, len(p.Segments))
	for _, s := range p.Segments {
		typ := s.Type
		switch typ {
		case store.SegmentIntro, store.SegmentOutro, store.SegmentRecap,
			store.SegmentPreview, store.SegmentCommercial:
		default:
			writeError(w, http.StatusBadRequest, "invalid segment type: "+typ)
			return
		}
		if s.StartTicks < 0 || s.EndTicks <= s.StartTicks ||
			s.EndTicks-s.StartTicks > 6*3600*10_000_000 {
			writeError(w, http.StatusBadRequest, "invalid segment ticks")
			return
		}
		segs = append(segs, store.MediaSegment{
			ItemID:     itemID,
			Type:       typ,
			StartTicks: s.StartTicks,
			EndTicks:   s.EndTicks,
		})
	}
	if err := h.db.SetManualSegments(itemID, segs); err != nil {
		writeError(w, http.StatusInternalServerError, "save segments failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": len(segs)})
}

// webSegmentsScan 手动触发片头片尾提取：带 seriesId 补整部剧，否则按设置范围补一批。
func (h *Handler) webSegmentsScan(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)
	var n int
	if seriesID := strings.TrimSpace(r.URL.Query().Get("seriesId")); seriesID != "" {
		n = h.prober.QueueSeriesSegments(seriesID, limit)
	} else {
		n = h.prober.QueueSegmentBackfill(limit)
	}
	writeJSON(w, http.StatusOK, map[string]any{"Queued": n})
}

// parseSegmentTypes 兼容两种写法：?includeSegmentTypes=Intro&includeSegmentTypes=Outro
// 和 ?includeSegmentTypes=Intro,Outro。
func parseSegmentTypes(r *http.Request) []string {
	var out []string
	for _, v := range r.URL.Query()["includeSegmentTypes"] {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// emptyItemList 用于 LocalTrailers / SpecialFeatures 这类可选列表。
func (h *Handler) emptyItemList(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, []*dto.BaseItemDto{})
}

// updateItem 目前不支持编辑元数据；返回 204，避免客户端因 405 报错。
func (h *Handler) updateItem(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// danmuRaw 是部分第三方客户端请求的弹幕插件接口，返回空数组。
func (h *Handler) danmuRaw(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

// displayPreferences 客户端会保存排序/视图偏好；LiMe 不做持久化。
func (h *Handler) displayPreferences(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Id":          r.PathValue("displayPreferencesId"),
		"SortBy":      []string{},
		"CustomPrefs": map[string]string{},
	})
}

func (h *Handler) saveDisplayPreferences(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) systemPing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(productName(r)))
}

func (h *Handler) plugins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func (h *Handler) devices(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Items":            []any{},
		"TotalRecordCount": 0,
	})
}

// themeMedia 返回空的主题曲/主题视频结构。
func (h *Handler) themeMedia(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	empty := dto.QueryResult{Items: []*dto.BaseItemDto{}, TotalRecordCount: 0}
	writeJSON(w, http.StatusOK, map[string]any{
		"ThemeVideosResult":     empty,
		"ThemeSongsResult":      empty,
		"SoundtrackSongsResult": empty,
	})
}
