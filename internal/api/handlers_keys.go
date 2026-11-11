package api

import (
	"net/http"
	"time"
)

// apiKeyDto 对应 Jellyfin 旧版 /Auth/Keys 的返回结构。
type apiKeyDto struct {
	AppName     string  `json:"AppName"`
	AccessToken string  `json:"AccessToken"`
	DateCreated string  `json:"DateCreated"`
	DateRevoked *string `json:"DateRevoked"`
	UserId      string  `json:"UserId"`
	UserName    string  `json:"UserName"`
}

func (h *Handler) apiKeys(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		keys, err := h.db.ListAPITokens()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		out := make([]apiKeyDto, 0, len(keys))
		for _, k := range keys {
			out = append(out, apiKeyDto{
				AppName:     k.Name,
				AccessToken: k.Token,
				DateCreated: k.CreatedAt.UTC().Format(time.RFC3339Nano),
				UserId:      k.UserID,
				UserName:    k.UserName,
			})
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		app := r.URL.Query().Get("App")
		if app == "" {
			app = "API Key"
		}
		if _, err := h.db.CreateAPIToken(caller.ID, app); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.log.Info("api key created", "app", app, "user", caller.Name)
		w.WriteHeader(http.StatusNoContent)

	case http.MethodDelete:
		key := r.PathValue("key")
		if key == "" {
			writeError(w, http.StatusBadRequest, "missing key")
			return
		}
		if err := h.db.DeleteToken(key); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.log.Info("api key deleted", "user", caller.Name)
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// logout 吊销当前请求携带的 token。
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if token := parseAuth(r).Token; token != "" {
		if err := h.db.DeleteToken(token); err != nil {
			h.log.Warn("delete token", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
