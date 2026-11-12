package api

import (
	"crypto/sha1"
	"encoding/hex"
	"net/http"

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

// persons / studios 暂不维护演职人员库，返回空列表让客户端正常渲染。
func (h *Handler) persons(w http.ResponseWriter, r *http.Request) {
	h.emptyQuery(w, r)
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

// mediaSegments 是 Jellyfin 10.9 的片头/片尾片段接口，LiMe 不做探测。
func (h *Handler) mediaSegments(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Items":            []any{},
		"TotalRecordCount": 0,
	})
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
