package api

import (
	"encoding/json"
	"net/http"

	"lime/internal/dto"
	"lime/internal/store"
)

// Jellyfin 的「标记已看/未看」「收藏」是同一套入口，POST 打开、DELETE 关闭。
func (h *Handler) setPlayed(w http.ResponseWriter, r *http.Request) {
	h.setUserFlag(w, r, true)
}

func (h *Handler) setFavorite(w http.ResponseWriter, r *http.Request) {
	h.setUserFlag(w, r, false)
}

// itemUserDataTarget 解析 /UserItems/{itemId}/UserData 的目标用户和条目。
// 新接口把 userId 放在 query 里，老接口放在路径里，两种都兼容。
func (h *Handler) itemUserDataTarget(w http.ResponseWriter, r *http.Request) (*store.User, *store.Item, bool) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return nil, nil, false
	}
	target := user
	id := r.PathValue("userId")
	if id == "" {
		id = r.URL.Query().Get("userId")
	}
	if id != "" && id != user.ID {
		if !user.IsAdmin {
			writeError(w, http.StatusForbidden, "Forbidden")
			return nil, nil, false
		}
		u, err := h.db.GetUserByID(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "user not found")
			return nil, nil, false
		}
		target = u
	}
	it, err := h.db.GetItem(r.PathValue("itemId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "item not found")
		return nil, nil, false
	}
	if !h.canAccessLibrary(target, it.LibraryID) || !ratingAllowed(target, it) {
		writeError(w, http.StatusNotFound, "item not found")
		return nil, nil, false
	}
	return target, it, true
}

func (h *Handler) currentItemUserData(user *store.User, it *store.Item) (*dto.UserItemDataDto, error) {
	states, err := h.db.Playstates(user.ID, []string{it.ID})
	if err != nil {
		return nil, err
	}
	var ps *store.Playstate
	if s, ok := states[it.ID]; ok {
		ps = &s
	}
	return buildUserData(it.ID, it.RunTimeTicks, ps), nil
}

func (h *Handler) getItemUserData(w http.ResponseWriter, r *http.Request) {
	user, it, ok := h.itemUserDataTarget(w, r)
	if !ok {
		return
	}
	ud, err := h.currentItemUserData(user, it)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, ud)
}

// updateItemUserData 兼容 Jellyfin 新接口中的播放状态写入。
func (h *Handler) updateItemUserData(w http.ResponseWriter, r *http.Request) {
	user, it, ok := h.itemUserDataTarget(w, r)
	if !ok {
		return
	}
	var p struct {
		Played                *bool  `json:"Played"`
		IsFavorite            *bool  `json:"IsFavorite"`
		PlaybackPositionTicks *int64 `json:"PlaybackPositionTicks"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if p.PlaybackPositionTicks != nil {
		pos := *p.PlaybackPositionTicks
		if pos < 0 {
			pos = 0
		}
		if err := h.db.UpsertPlaystate(user.ID, it.ID, pos, false); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if p.Played != nil {
		if err := h.db.SetPlayed(user.ID, it.ID, *p.Played); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if p.IsFavorite != nil {
		if err := h.db.SetFavorite(user.ID, it.ID, *p.IsFavorite); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	ud, err := h.currentItemUserData(user, it)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, ud)
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
