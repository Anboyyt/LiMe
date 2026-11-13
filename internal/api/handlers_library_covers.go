package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type libraryCoverStatus struct {
	ID     string `json:"ID"`
	Name   string `json:"Name"`
	Custom bool   `json:"Custom"`
	Tag    string `json:"Tag"`
}

// webLibraryCovers 查看/切换库封面样式。
// UseAuto 会把 lime-cover 上传的自定义封面重命名为 *.bak（不删除），
// 让客户端回落到 LiMe 自动生成的海报墙封面。
func (h *Handler) webLibraryCovers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		libs, err := h.db.ListLibraries()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list libraries failed")
			return
		}
		out := make([]libraryCoverStatus, 0, len(libs))
		custom := 0
		for _, lib := range libs {
			has := false
			if rel, _ := h.customLibraryImage(lib.ID, ""); rel != "" {
				has = true
				custom++
			}
			out = append(out, libraryCoverStatus{
				ID: lib.ID, Name: lib.Name, Custom: has, Tag: h.coverPreviewTag(lib.ID),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"Style":       h.libraryCoverStyle(),
			"Titles":      h.coverTitles(),
			"Libraries":   out,
			"CustomCount": custom,
		})

	case http.MethodPost:
		var p struct {
			Style   *string `json:"Style"`
			UseAuto *bool   `json:"UseAuto"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if p.Style != nil {
			style := strings.ToLower(strings.TrimSpace(*p.Style))
			switch style {
			case "collage", "minimal", "latest":
			default:
				writeError(w, http.StatusBadRequest, "样式只能是 collage / minimal / latest")
				return
			}
			if err := h.db.SetSetting("library_cover_style", style); err != nil {
				writeError(w, http.StatusInternalServerError, "save setting failed")
				return
			}
		}
		backed := 0
		if p.UseAuto != nil && *p.UseAuto {
			n, err := h.backupCustomCovers()
			if err != nil {
				writeError(w, http.StatusInternalServerError, "backup custom covers failed: "+err.Error())
				return
			}
			backed = n
			go h.PreGenerateCovers()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"Style":    h.libraryCoverStyle(),
			"BackedUp": backed,
		})
	}
}

// coverPreviewTag 给面板预览用的缓存标记：只查当前样式已有的文件，
// 不触发封面生成。必须按样式区分，否则切换样式后浏览器会继续用旧图。
func (h *Handler) coverPreviewTag(libID string) string {
	if _, tag := h.customLibraryImage(libID, ""); tag != "" {
		return tag
	}
	style := h.libraryCoverStyle()
	if style == "latest" {
		// 缓存文件名里的 tag 就是所选图片自身的 imageTag。
		if rel, ok := h.latestArtworkSource(libID); ok {
			return imageTag(rel)
		}
		return ""
	}
	absData, err := filepath.Abs(h.cfg.DataDir)
	if err != nil {
		return ""
	}
	patterns := []string{libID + "-" + style + "-*.jpg"}
	if style == "collage" {
		// 旧版无样式前缀的缓存也认。
		patterns = append(patterns, libID+"-[0-9a-f]*.jpg")
	}
	patterns = append(patterns, libID+"-default.jpg")
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(absData, "library-images", pattern))
		if len(matches) == 0 {
			continue
		}
		if st, err := os.Stat(matches[0]); err == nil {
			return "auto-" + strconv.FormatInt(st.ModTime().UnixNano(), 36)
		}
	}
	return ""
}

// backupCustomCovers 把 library-images 下的自定义封面改名为 .bak。
// 只改名不删除，随时可以把 .bak 改回去恢复原封面。
func (h *Handler) backupCustomCovers() (int, error) {
	absData, err := filepath.Abs(h.cfg.DataDir)
	if err != nil {
		return 0, err
	}
	dir := filepath.Join(absData, "library-images")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.Contains(name, "-custom") || strings.HasSuffix(name, ".bak") {
			continue
		}
		src := filepath.Join(dir, name)
		dst := src + ".bak"
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
