package api

import (
	"net/http"

	"lime/internal/dto"
	"lime/internal/store"
)

// allowedLibraries 返回用户可见的媒体库 ID。
// all=true 表示不限制（管理员，或没有配置过权限）。
func (h *Handler) allowedLibraries(user *store.User) (ids []string, all bool, err error) {
	if user.IsAdmin {
		return nil, true, nil
	}
	ids, err = h.db.LibraryAccess(user.ID)
	if err != nil {
		return nil, false, err
	}
	if len(ids) == 0 {
		return nil, true, nil
	}
	return ids, false, nil
}

// canAccessLibrary 判断用户能否访问指定媒体库。
func (h *Handler) canAccessLibrary(user *store.User, libraryID string) bool {
	if user.IsAdmin {
		return true
	}
	ids, err := h.db.LibraryAccess(user.ID)
	if err != nil || len(ids) == 0 {
		return true
	}
	for _, id := range ids {
		if id == libraryID {
			return true
		}
	}
	return false
}

// filterViewsByAccess 只保留用户有权限的媒体库视图。
func filterViewsByAccess(views []*dto.BaseItemDto, allowed []string, all bool) []*dto.BaseItemDto {
	if all {
		return views
	}
	set := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		set[id] = true
	}
	out := make([]*dto.BaseItemDto, 0, len(views))
	for _, v := range views {
		if set[v.Id] {
			out = append(out, v)
		}
	}
	return out
}

// imageAccessAllowed 图片默认公开；请求带 token 时按用户的库权限校验。
func (h *Handler) imageAccessAllowed(r *http.Request, libraryID string) bool {
	tok := parseAuth(r).Token
	if tok == "" {
		return true
	}
	u, err := h.db.UserByToken(tok)
	if err != nil {
		return true
	}
	return h.canAccessLibrary(u, libraryID)
}
