package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type identifyResult struct {
	TmdbId        string  `json:"TmdbId"`
	Title         string  `json:"Title"`
	OriginalTitle string  `json:"OriginalTitle,omitempty"`
	Year          int     `json:"Year"`
	Overview      string  `json:"Overview"`
	PosterUrl     string  `json:"PosterUrl,omitempty"`
	Rating        float64 `json:"Rating,omitempty"`
}

// webIdentify 提供详情页的手动识别：搜索 TMDB，选中后改 ID 并重新刮削。
//
//	GET  /web/api/identify/search?type=movie|series&query=&year=
//	POST /web/api/identify  {"ItemId":"...","TmdbId":"..."}
func (h *Handler) webIdentify(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if h.scraper == nil || !h.scraper.Enabled() {
		writeError(w, http.StatusBadRequest, "未配置 TMDB Key，无法识别")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.identifySearch(w, r)
	case http.MethodPost:
		var p struct {
			ItemId string `json:"ItemId"`
			TmdbId string `json:"TmdbId"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(p.ItemId) == "" {
			writeError(w, http.StatusBadRequest, "缺少 ItemId")
			return
		}
		if err := h.scraper.Reidentify(p.ItemId, p.TmdbId); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"Started": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) identifySearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "搜索关键词不能为空")
		return
	}
	if len([]rune(query)) > 120 {
		writeError(w, http.StatusBadRequest, "搜索关键词过长")
		return
	}
	year := strings.TrimSpace(r.URL.Query().Get("year"))
	if year != "" {
		if _, err := strconv.Atoi(year); err != nil || len(year) != 4 {
			year = ""
		}
	}

	kind := "movie"
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type"))) {
	case "movie", "电影":
	case "series", "tv", "剧集":
		kind = "tv"
	default:
		writeError(w, http.StatusBadRequest, "type 只能是 movie 或 series")
		return
	}

	results, err := h.scraper.Search(r.Context(), kind, query, year)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	out := make([]identifyResult, 0, len(results))
	for _, item := range results {
		title, original, date := item.Title, item.OriginalTitle, item.ReleaseDate
		if kind == "tv" {
			title, original, date = item.Name, item.OriginalName, item.FirstAirDate
		}
		if title == "" {
			title = original
		}
		out = append(out, identifyResult{
			TmdbId:        strconv.Itoa(item.ID),
			Title:         title,
			OriginalTitle: original,
			Year:          searchYear(date),
			Overview:      item.Overview,
			PosterUrl:     h.scraper.ImageURL(item.PosterPath, "w185"),
			Rating:        item.VoteAverage,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Results": out})
}

func searchYear(date string) int {
	if len(date) < 4 {
		return 0
	}
	n, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return n
}
