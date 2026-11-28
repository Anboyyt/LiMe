package api

import (
	"net/http"
	"time"

	"lime/internal/store"
)

// webUsers 是运维面板的用户列表，带并发/下载/播放开关和设备数。
// 与 Jellyfin 的 /Users 分开：那边是给客户端看的 UserDto，这里只给面板用。
func (h *Handler) webUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	users, err := h.db.UserSummaries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	type userDto struct {
		Id                string  `json:"Id"`
		Name              string  `json:"Name"`
		IsAdmin           bool    `json:"IsAdmin"`
		IsDisabled        bool    `json:"IsDisabled"`
		MaxParentalRating int     `json:"MaxParentalRating"`
		MaxActiveSessions int     `json:"MaxActiveSessions"`
		AllowDownload     bool    `json:"AllowDownload"`
		AllowPlayback     bool    `json:"AllowPlayback"`
		LastLoginAt       *string `json:"LastLoginAt"`
		DeviceCount       int     `json:"DeviceCount"`
	}
	out := make([]userDto, 0, len(users))
	for _, u := range users {
		d := userDto{
			Id:                u.ID,
			Name:              u.Name,
			IsAdmin:           u.IsAdmin,
			IsDisabled:        u.IsDisabled,
			MaxParentalRating: u.MaxParentalRating,
			MaxActiveSessions: u.MaxActiveSessions,
			AllowDownload:     u.AllowDownload,
			AllowPlayback:     u.AllowPlayback,
			DeviceCount:       u.DeviceCount,
		}
		if u.LastLoginAt != nil {
			s := u.LastLoginAt.UTC().Format(time.RFC3339)
			d.LastLoginAt = &s
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

// webUserDevices 列出某个用户登录过的设备。
func (h *Handler) webUserDevices(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	userID := r.PathValue("userId")
	if _, err := h.db.GetUserByID(userID); err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	devices, err := h.db.ListDevices(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	type deviceDto struct {
		DeviceId   string `json:"DeviceId"`
		Client     string `json:"Client"`
		Name       string `json:"Name"`
		LastIP     string `json:"LastIP"`
		LastSeenAt string `json:"LastSeenAt"`
		TokenCount int    `json:"TokenCount"`
	}
	out := make([]deviceDto, 0, len(devices))
	for _, d := range devices {
		seen := ""
		if !d.LastSeenAt.IsZero() {
			seen = d.LastSeenAt.UTC().Format(time.RFC3339)
		}
		out = append(out, deviceDto{
			DeviceId:   d.DeviceID,
			Client:     d.Client,
			Name:       d.Name,
			LastIP:     d.LastIP,
			LastSeenAt: seen,
			TokenCount: d.TokenCount,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Devices": out})
}

// webUserDeviceDelete 吊销设备登录态。这就是「踢下线」。
func (h *Handler) webUserDeviceDelete(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	userID := r.PathValue("userId")
	// ServeMux 已经把路径段解码过了，这里不能再 Unescape 一次。
	deviceID := r.PathValue("deviceId")
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, "invalid device id")
		return
	}

	user, err := h.db.GetUserByID(userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err := h.db.DeleteDevice(userID, deviceID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// 顺手清掉该设备正在上报的播放会话，否则并发计数的窗口里还占着名额。
	h.dropDeviceSessions(userID, deviceID)

	h.log.Info("device signed out", "user", user.Name, "device", deviceID, "by", caller.Name)
	w.WriteHeader(http.StatusNoContent)
}

// dropDeviceSessions 删除某个设备遗留的播放会话。
func (h *Handler) dropDeviceSessions(userID, deviceID string) {
	sessions, err := h.db.ListPlaybackSessions()
	if err != nil {
		return
	}
	for _, s := range sessions {
		if s.UserID == userID && s.DeviceID == deviceID {
			if err := h.db.DeletePlaybackSession(s.ID); err != nil {
				h.log.Warn("drop session after sign out", "id", s.ID, "err", err)
			}
		}
	}
}

// activeSessionCount 统计用户当前「正在播放」的会话数，排除同一台设备。
// 同一设备切集时旧会话还没过期，不排除会把用户自己算成两个人。
func (h *Handler) activeSessionCount(userID, deviceID string) int {
	n, err := h.db.ActiveSessionCountExcluding(userID, deviceID,
		time.Now().Add(-store.PlaybackSessionTTL))
	if err != nil {
		return 0
	}
	return n
}
