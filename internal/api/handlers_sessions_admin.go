package api

import (
	"net/http"
	"strconv"
	"time"

	"lime/internal/store"
)

// webSessionDelete 结束一条正在播放的会话。
// 和「踢设备」不同：设备凭据保留，只是让这一条播放从会话列表消失，
// 并发计数会立即释放。客户端下次起播不受影响。
func (h *Handler) webSessionDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	if err := h.db.DeletePlaybackSession(id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.log.Info("session stopped by admin", "session", id)
	w.WriteHeader(http.StatusNoContent)
}

// runLoginLogPrune 每天清一次超过保留期的登录流水。
func (h *Handler) runLoginLogPrune() {
	time.Sleep(3 * time.Minute)
	for {
		if n, err := h.db.PruneLoginLogs(time.Now().Add(-store.LoginLogRetention)); err != nil {
			h.log.Warn("prune login log", "err", err)
		} else if n > 0 {
			h.log.Info("pruned login log", "rows", n)
		}
		time.Sleep(24 * time.Hour)
	}
}

// recordLogin 写一条登录流水。用户不存在时 userID 为空，只记用户名。
func (h *Handler) recordLogin(r *http.Request, name, userID string, ok bool, reason string) {
	a := parseAuth(r)
	if err := h.db.AddLoginLog(&store.LoginLog{
		UserID:   userID,
		UserName: name,
		IP:       h.clientIPString(r),
		Client:   a.Client,
		Device:   a.Device,
		Success:  ok,
		Reason:   reason,
	}); err != nil {
		h.log.Warn("write login log", "err", err)
	}
}

type loginLogDto struct {
	UserName  string `json:"UserName"`
	UserId    string `json:"UserId"`
	IP        string `json:"IP"`
	Client    string `json:"Client"`
	Device    string `json:"Device"`
	Success   bool   `json:"Success"`
	Reason    string `json:"Reason"`
	CreatedAt string `json:"CreatedAt"`
}

// webLoginLog 返回登录流水；带 userId 只看某个用户。
func (h *Handler) webLoginLog(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	userID := r.URL.Query().Get("userId")

	rows, err := h.db.LoginLogs(userID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]loginLogDto, 0, len(rows))
	for _, e := range rows {
		out = append(out, loginLogDto{
			UserName:  e.UserName,
			UserId:    e.UserID,
			IP:        e.IP,
			Client:    e.Client,
			Device:    e.Device,
			Success:   e.Success,
			Reason:    e.Reason,
			CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
