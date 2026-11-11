package api

import (
	"net/http"
	"strconv"
)

// webPlaybackHistory 是运维面板「播放记录」用的响应结构。
type webPlaybackHistory struct {
	Id            int64  `json:"Id"`
	UserName      string `json:"UserName"`
	DeviceName    string `json:"DeviceName"`
	ItemName      string `json:"ItemName"`
	ItemType      string `json:"ItemType"`
	Event         string `json:"Event"`
	PositionTicks int64  `json:"PositionTicks"`
	CreatedAt     int64  `json:"CreatedAt"`
}

func (h *Handler) webPlaybackHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := h.db.PlaybackHistory(limit)
	if err != nil {
		h.log.Error("load playback history", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]webPlaybackHistory, 0, len(rows))
	for _, row := range rows {
		out = append(out, webPlaybackHistory{
			Id:            row.ID,
			UserName:      row.UserName,
			DeviceName:    row.DeviceName,
			ItemName:      row.ItemName,
			ItemType:      row.ItemType,
			Event:         row.Event,
			PositionTicks: row.PositionTicks,
			CreatedAt:     row.CreatedAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
