package api

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lime/internal/dto"
	"lime/internal/store"
)

// loginLimitKeys 返回 IP 和 IP+用户名 两个限速键。
func loginLimitKeys(r *http.Request, username string) (ipKey, userKey string) {
	ipKey = "ip:" + clientAddrIP(r).String()
	userKey = ipKey + "|u:" + strings.ToLower(username)
	return ipKey, userKey
}

type authenticateRequest struct {
	Username string `json:"Username"`
	Pw       string `json:"Pw"`
	// 少数客户端会发 Password，兼容之。
	Password string `json:"Password"`
}

func (h *Handler) authenticateByName(w http.ResponseWriter, r *http.Request) {
	var req authenticateRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			// 少数客户端（旧版 Infuse / 第三方脚本）把用户名密码放在
			// x-www-form-urlencoded 请求体里，而不是 JSON。
			if vals, perr := url.ParseQuery(string(body)); perr == nil {
				req.Username = firstNonEmpty(vals.Get("Username"), vals.Get("username"))
				req.Pw = firstNonEmpty(vals.Get("Pw"), vals.Get("pw"))
				req.Password = firstNonEmpty(vals.Get("Password"), vals.Get("password"))
			}
		}
	}
	// 也有客户端直接放在 query 上，完全不发 body。
	q := r.URL.Query()
	if req.Username == "" {
		req.Username = firstNonEmpty(q.Get("Username"), q.Get("username"))
	}
	if req.Pw == "" {
		req.Pw = firstNonEmpty(q.Get("Pw"), q.Get("pw"))
	}
	if req.Password == "" {
		req.Password = firstNonEmpty(q.Get("Password"), q.Get("password"))
	}

	username := strings.TrimSpace(req.Username)
	password := req.Pw
	if password == "" {
		password = req.Password
	}
	if username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	ipKey, userKey := loginLimitKeys(r, username)
	limitKeys := []loginKey{{ipKey, loginIPLimit}, {userKey, loginUserLimit}}
	if wait, blocked := h.loginLimiter.blocked(limitKeys); blocked {
		h.log.Warn("login rate limited", "user", username, "ip", clientIP(r))
		if sec := int(math.Ceil(wait.Seconds())); sec > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(sec))
		}
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}

	user, err := h.db.GetUserByName(username)
	if err != nil {
		// 用户不存在：没有 userID 可记，只留用户名。
		h.loginLimiter.fail(limitKeys)
		h.log.Warn("login failed: unknown user", "user", username, "ip", clientIP(r))
		h.recordLogin(r, username, "", false, "user not found")
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "Invalid username or password",
		})
		return
	}
	if !store.VerifyPassword(user.PasswordHash, password) {
		h.loginLimiter.fail(limitKeys)
		h.log.Warn("login failed: bad password", "user", username, "ip", clientIP(r))
		h.recordLogin(r, username, user.ID, false, "wrong password")
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "Invalid username or password",
		})
		return
	}
	if user.IsDisabled {
		h.loginLimiter.fail(limitKeys)
		h.log.Warn("login disabled user", "user", username, "ip", clientIP(r))
		h.recordLogin(r, username, user.ID, false, "disabled")
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "User is disabled",
		})
		return
	}
	h.loginLimiter.reset(userKey)

	a := parseAuth(r)
	deviceID := a.DeviceID
	if deviceID == "" {
		deviceID = store.MustRandomHex(16)
	}
	deviceName := a.Device
	if deviceName == "" {
		deviceName = a.Client
	}

	if err := h.db.UpsertDevice(user.ID, deviceID, a.Client, deviceName, h.clientIPString(r)); err != nil {
		h.log.Warn("upsert device", "err", err)
	}

	token, err := h.db.CreateToken(user.ID, deviceID)
	if err != nil {
		h.log.Error("create token", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.db.TouchLogin(user.ID)
	h.recordLogin(r, user.Name, user.ID, true, "")

	sessionID := store.MustRandomHex(16)
	item := &dto.AuthenticationResult{
		User:        h.userDto(user),
		AccessToken: token,
		ServerId:    h.serverID,
		SessionId:   sessionID,
		SessionInfo: &dto.SessionInfoDto{
			Id:                    sessionID,
			UserId:                user.ID,
			UserName:              user.Name,
			Client:                a.Client,
			DeviceName:            deviceName,
			DeviceId:              deviceID,
			ApplicationVersion:    a.Version,
			IsActive:              true,
			PlayableMediaTypes:    []string{"Audio", "Video"},
			SupportedCommands:     []string{},
			SupportsRemoteControl: false,
			LastActivityDate:      nowISO(),
		},
	}

	h.log.Info("login ok", "user", user.Name, "client", a.Client, "device", deviceName)
	writeJSON(w, http.StatusOK, item)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return host
}
