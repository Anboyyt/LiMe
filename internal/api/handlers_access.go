package api

import (
	"encoding/json"
	"net/http"
)

// userLibraries 读取/设置某个用户的媒体库访问权限。
// 管理员不受限制；普通用户未配置时默认可见全部库。
func (h *Handler) userLibraries(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id := r.PathValue("userId")
	if _, err := h.db.GetUserByID(id); err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		libs, err := h.db.ListLibraries()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		access, err := h.db.LibraryAccess(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		type libDTO struct {
			ID   string `json:"ID"`
			Name string `json:"Name"`
		}
		out := make([]libDTO, 0, len(libs))
		for _, l := range libs {
			out = append(out, libDTO{ID: l.ID, Name: l.Name})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"Libraries": out,
			"Access":    access,
		})

	case http.MethodPut:
		var p struct {
			LibraryIds []string `json:"LibraryIds"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := h.db.SetLibraryAccess(id, p.LibraryIds); err != nil {
			writeError(w, http.StatusInternalServerError, "save access failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
