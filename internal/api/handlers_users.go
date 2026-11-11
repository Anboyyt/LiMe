package api

import (
	"net/http"
	"strings"

	"lime/internal/dto"
	"lime/internal/store"
)

func (h *Handler) getCurrentUser(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.userDto(user))
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	// 普通用户只能看到自己，避免泄露其他账户信息。
	if !user.IsAdmin {
		writeJSON(w, http.StatusOK, []*dto.UserDto{h.userDto(user)})
		return
	}

	users, err := h.db.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]*dto.UserDto, 0, len(users))
	for _, u := range users {
		out = append(out, h.userDto(u))
	}
	writeJSON(w, http.StatusOK, out)
}

// publicUsers 登录界面用来列出可选账户。
func (h *Handler) publicUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.db.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]*dto.UserDto, 0, len(users))
	for _, u := range users {
		if u.IsDisabled {
			continue
		}
		out = append(out, h.userDto(u))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	id := r.PathValue("userId")
	if caller.ID != id && !caller.IsAdmin {
		writeError(w, http.StatusForbidden, "Forbidden")
		return
	}
	user, err := h.db.GetUserByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, h.userDto(user))
}

type createUserRequest struct {
	Name     string `json:"Name"`
	Password string `json:"Password"`
	Pw       string `json:"Pw"`
}

// createUser 对应 POST /Users/New，只有管理员能建号。
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	var req createUserRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	password := req.Password
	if password == "" {
		password = req.Pw
	}
	hash, err := store.HashPassword(password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	user, err := h.db.CreateUser(name, hash, false)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "user already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info("user created", "name", user.Name)
	writeJSON(w, http.StatusOK, h.userDto(user))
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}

	id := r.PathValue("userId")
	target, err := h.db.GetUserByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if target.ID == caller.ID {
		writeError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	if target.IsAdmin {
		n, err := h.db.CountAdmins()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if n <= 1 {
			writeError(w, http.StatusBadRequest, "cannot delete the last admin")
			return
		}
	}
	if err := h.db.DeleteUser(id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.log.Info("user deleted", "name", target.Name)
	w.WriteHeader(http.StatusNoContent)
}

type updateUserRequest struct {
	Name string `json:"Name"`
}

// updateUser 对应 POST /Users/{userId}，目前只处理改名。
// 本人可以改自己的用户名，其他人必须管理员操作。
func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	id := r.PathValue("userId")
	target, err := h.db.GetUserByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if caller.ID != target.ID && !caller.IsAdmin {
		writeError(w, http.StatusForbidden, "Forbidden")
		return
	}

	var req updateUserRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name != "" && name != target.Name {
		if err := h.db.UpdateUserName(target.ID, name); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				writeError(w, http.StatusConflict, "user already exists")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.log.Info("user renamed", "from", target.Name, "to", name)
	}

	updated, err := h.db.GetUserByID(target.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, h.userDto(updated))
}

type updatePasswordRequest struct {
	CurrentPw     string `json:"CurrentPw"`
	NewPw         string `json:"NewPw"`
	ResetPassword bool   `json:"ResetPassword"`
}

func (h *Handler) updatePassword(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	id := r.PathValue("userId")
	target, err := h.db.GetUserByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if caller.ID != target.ID && !caller.IsAdmin {
		writeError(w, http.StatusForbidden, "Forbidden")
		return
	}

	var req updatePasswordRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// 管理员重置其他账号时不需要旧密码
	adminReset := caller.IsAdmin && req.ResetPassword && caller.ID != target.ID
	if !adminReset {
		if !store.VerifyPassword(target.PasswordHash, req.CurrentPw) {
			writeError(w, http.StatusBadRequest, "current password is incorrect")
			return
		}
	}

	hash, err := store.HashPassword(req.NewPw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.db.UpdatePassword(target.ID, hash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// 其他设备上的登录失效，当前会话保留
	if err := h.db.DeleteTokensForUserExcept(target.ID, parseAuth(r).Token); err != nil {
		h.log.Warn("revoke tokens after password change", "user", target.Name, "err", err)
	}
	h.log.Info("password changed", "user", target.Name)
	w.WriteHeader(http.StatusNoContent)
}

type updatePolicyRequest struct {
	IsAdministrator   *bool `json:"IsAdministrator"`
	IsDisabled        *bool `json:"IsDisabled"`
	MaxParentalRating *int  `json:"MaxParentalRating"`
}

// updateUserPolicy 只处理管理员和禁用状态，其余策略字段先忽略。
func (h *Handler) updateUserPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	id := r.PathValue("userId")
	target, err := h.db.GetUserByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	var req updatePolicyRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	demote := req.IsAdministrator != nil && !*req.IsAdministrator && target.IsAdmin
	disable := req.IsDisabled != nil && *req.IsDisabled && target.IsAdmin
	if demote || disable {
		n, err := h.db.CountAdmins()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if n <= 1 {
			writeError(w, http.StatusBadRequest, "cannot remove the last admin")
			return
		}
	}

	if req.IsAdministrator != nil {
		if err := h.db.SetUserAdmin(target.ID, *req.IsAdministrator); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if req.IsDisabled != nil {
		if err := h.db.SetUserDisabled(target.ID, *req.IsDisabled); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if req.MaxParentalRating != nil {
		v := *req.MaxParentalRating
		if v < 0 || v > 5 {
			writeError(w, http.StatusBadRequest, "MaxParentalRating must be 0-5")
			return
		}
		if err := h.db.SetUserMaxRating(target.ID, v); err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	updated, err := h.db.GetUserByID(target.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, h.userDto(updated))
}
