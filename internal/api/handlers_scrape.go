package api

import (
	"net/http"

	"lime/internal/scrape"
)

type scrapeResponse struct {
	Enabled bool `json:"Enabled"`
	scrape.Status
}

func (h *Handler) webScrape(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	if h.scraper == nil {
		writeJSON(w, http.StatusOK, scrapeResponse{Enabled: false})
		return
	}

	if r.Method == http.MethodPost {
		if !h.scraper.Enabled() {
			writeError(w, http.StatusBadRequest, "TMDB API key not configured")
			return
		}
		if h.scraper.Status().Running {
			writeJSON(w, http.StatusConflict, scrapeResponse{
				Enabled: true,
				Status:  h.scraper.Status(),
			})
			return
		}
		mode := r.URL.Query().Get("mode")
		go func() {
			var err error
			if mode == "avatars" {
				_, err = h.scraper.RunAvatars()
			} else {
				_, err = h.scraper.Run()
			}
			if err != nil {
				h.log.Error("scrape failed", "mode", mode, "err", err)
			}
		}()
		writeJSON(w, http.StatusAccepted, map[string]any{"Started": true})
		return
	}

	writeJSON(w, http.StatusOK, scrapeResponse{
		Enabled: h.scraper.Enabled(),
		Status:  h.scraper.Status(),
	})
}
