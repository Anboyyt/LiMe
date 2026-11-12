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

	// Jellyfin 的路径里带了 userId。客户端一般传自己，但管理员/共享设备
	// 可能替别人操作；以前忽略它，结果永远写到自己头上。
	target := user
	if id := r.PathValue("userId"); id != "" && id != user.ID {
		if !user.IsAdmin {
			writeError(w, http.StatusForbidden, "Forbidden")
			return
		}
		u, err := h.db.GetUserByID(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		target = u
	}

	itemID := r.PathValue("itemId")
	it, err := h.db.GetItem(itemID)
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if !h.canAccessLibrary(target, it.LibraryID) || !ratingAllowed(target, it) {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}

	on := r.Method == http.MethodPost
	if played {
		err = h.db.SetPlayed(target.ID, itemID, on)
	} else {
		err = h.db.SetFavorite(target.ID, itemID, on)
	}
	if err != nil {
		h.log.Error("update user data", "item", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	states, err := h.db.Playstates(target.ID, []string{itemID})
	if err != nil {
		h.log.Warn("load playstate", "item", itemID, "err", err)
	}

	var ps *store.Playstate
	if s, ok := states[itemID]; ok {
		ps = &s
	}
	writeJSON(w, http.StatusOK, buildUserData(itemID, it.RunTimeTicks, ps))
}
