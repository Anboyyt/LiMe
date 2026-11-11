package api

import (
	"net/http"
	"strings"

	"lime/internal/store"
)

type authInfo struct {
	Client   string
	Device   string
	DeviceID string
	Version  string
	Token    string
}

func parseAuth(r *http.Request) authInfo {
	var a authInfo

	raw := r.Header.Get("X-Emby-Authorization")
	if raw == "" {
		raw = r.Header.Get("Authorization")
	}
	if raw != "" {
		a = parseAuthValue(raw)
	}

	if a.Token == "" {
		a.Token = r.Header.Get("X-Emby-Token")
	}
	if a.Token == "" {
		a.Token = r.Header.Get("X-MediaBrowser-Token")
	}
	if a.Token == "" {
		a.Token = r.URL.Query().Get("api_key")
	}
	if a.Token == "" {
		a.Token = r.URL.Query().Get("ApiKey")
	}
	return a
}

// parseAuthValue 解析形如：
//
//	MediaBrowser Client="x", Device="y", DeviceId="z", Version="1", Token="t"
//
// 以及 Emby 前缀的变体。逗号可能出现在引号内，顺序也不固定。
func parseAuthValue(v string) authInfo {
	var a authInfo

	if i := strings.Index(v, " "); i > 0 && !strings.Contains(v[:i], "=") {
		v = v[i+1:]
	}

	var parts []string
	var cur strings.Builder
	inQuote := false
	for _, r := range v {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ',' && !inQuote:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}

	for _, p := range parts {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		val := strings.Trim(strings.TrimSpace(kv[1]), `"`)

		switch key {
		case "client":
			a.Client = val
		case "device":
			a.Device = val
		case "deviceid":
			a.DeviceID = val
		case "version":
			a.Version = val
		case "token":
			a.Token = val
		}
	}
	return a
}

// requireAdmin 在 requireUser 基础上要求管理员权限。
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	u, ok := h.requireUser(w, r)
	if !ok {
		return nil, false
	}
	if !u.IsAdmin {
		writeError(w, http.StatusForbidden, "Forbidden")
		return nil, false
	}
	return u, true
}

// requireUser 校验 token 并返回用户；失败时已写好 401 响应。
func (h *Handler) requireUser(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	a := parseAuth(r)
	if a.Token == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return nil, false
	}

	u, err := h.db.UserByToken(a.Token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return nil, false
	}
	return u, true
}
