package api

import (
	"net/http"

	"lime/internal/store"
)

// Jellyfin 的「标记已看/未看」「收藏」是同一套入口，POST 打开、DELETE 关闭。
func (h *Handler) setPlayed(w http.ResponseWriter, r *http.Request) {
	h.setUserFlag(w, r, true)
}

func (h *Handler) setFavorite(w http.ResponseWriter, r *http.Request) {
	h.setUserFlag(w, r, false)
}

func (h *Handler) setUserFlag(w http.ResponseWriter, r *http.Request, played bool) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	itemID := r.PathValue("itemId")
	it, err := h.db.GetItem(itemID)
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	on := r.Method == http.MethodPost
	if played {
		err = h.db.SetPlayed(user.ID, itemID, on)
	} else {
		err = h.db.SetFavorite(user.ID, itemID, on)
	}
	if err != nil {
		h.log.Error("update user data", "item", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	states, err := h.db.Playstates(user.ID, []string{itemID})
	if err != nil {
		h.log.Warn("load playstate", "item", itemID, "err", err)
	}

	var ps *store.Playstate
	if s, ok := states[itemID]; ok {
		ps = &s
	}
	writeJSON(w, http.StatusOK, buildUserData(itemID, it.RunTimeTicks, ps))
}
