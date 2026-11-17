package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"lime/internal/store"
)

// webSegmentSeriesSearch 按剧名关键词搜索剧集，给面板「精准提取」用。
func (h *Handler) webSegmentSeriesSearch(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 20)
	hits, err := h.db.SearchSeries(q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search series failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"Series": hits})
}

// webSegmentTargets 管理「精准提取」队列：
// GET 返回各状态数量；POST 接收 SeriesIds，把每部剧里缺片头片尾的季排进队列。
func (h *Handler) webSegmentTargets(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		counts, err := h.db.SegmentTargetCounts()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read targets failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"Queued":  counts["queued"],
			"Running": counts["running"],
			"Done":    counts["done"],
			"Failed":  counts["failed"],
			"Total":   counts["queued"] + counts["running"] + counts["done"] + counts["failed"],
		})

	case http.MethodPost:
		var p struct {
			SeriesIds []string `json:"SeriesIds"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		seen := make(map[string]bool, len(p.SeriesIds))
		ids := make([]string, 0, len(p.SeriesIds))
		for _, id := range p.SeriesIds {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
			if len(ids) >= 200 {
				break
			}
		}
		if len(ids) == 0 {
			writeError(w, http.StatusBadRequest, "SeriesIds is required")
			return
		}
		targets := make([]store.SegmentTarget, 0, len(ids)*4)
		seriesHit := 0
		for _, id := range ids {
			seasons, err := h.db.SeasonsNeedingSegments(id)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "query seasons failed")
				return
			}
			if len(seasons) == 0 {
				continue
			}
			seriesHit++
			for _, season := range seasons {
				targets = append(targets, store.SegmentTarget{SeriesID: id, Season: season})
			}
		}
		added, err := h.db.AddSegmentTargets(targets)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "save targets failed")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"Added":   added,
			"Series":  seriesHit,
			"Enabled": h.segmentExtractEnabled(),
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
