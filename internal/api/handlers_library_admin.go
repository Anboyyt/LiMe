package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lime/internal/store"
)

type libraryPayload struct {
	Name  string   `json:"Name"`
	Path  string   `json:"Path"`  // 兼容旧客户端：单路径
	Paths []string `json:"Paths"` // 推荐：多个文件夹
	Kind  string   `json:"Kind"`
}

// webLibraries 是面板的媒体库列表/新建接口。
func (h *Handler) webLibraries(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if _, ok := h.requireUser(w, r); !ok {
			return
		}
		stats, err := h.db.Stats()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "load libraries failed")
			return
		}
		writeJSON(w, http.StatusOK, stats.Libraries)
		return
	}

	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	var p libraryPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, paths, kind, err := h.validateLibraryPayload(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	lib, err := h.db.CreateLibrary(name, paths, kind)
	if errors.Is(err, store.ErrLibraryPathExists) {
		writeError(w, http.StatusConflict, "这个路径已经是一个媒体库")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create library failed")
		return
	}
	writeJSON(w, http.StatusCreated, lib)
}

// webLibraryByID 处理改名/改路径/改类型和删除。
func (h *Handler) webLibraryByID(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	if _, err := h.db.GetLibrary(id); err != nil {
		writeError(w, http.StatusNotFound, "library not found")
		return
	}

	switch r.Method {
	case http.MethodPut:
		var p libraryPayload
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		name, paths, kind, err := h.validateLibraryPayload(p)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.db.UpdateLibrary(id, name, paths, kind); err != nil {
			if errors.Is(err, store.ErrLibraryPathExists) {
				writeError(w, http.StatusConflict, "这个路径已经是一个媒体库")
				return
			}
			writeError(w, http.StatusInternalServerError, "update library failed")
			return
		}
		lib, _ := h.db.GetLibrary(id)
		writeJSON(w, http.StatusOK, lib)

	case http.MethodDelete:
		if h.scanner.Status().Running {
			writeError(w, http.StatusConflict, "扫描进行中，稍后再删除")
			return
		}
		if err := h.db.DeleteLibrary(id); err != nil {
			writeError(w, http.StatusInternalServerError, "delete library failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// webLibraryScan 只扫描指定库。
func (h *Handler) webLibraryScan(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	lib, err := h.db.GetLibrary(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "library not found")
		return
	}
	if h.scanner.Status().Running {
		writeError(w, http.StatusConflict, "扫描进行中")
		return
	}
	go func() {
		n, err := h.scanner.ScanLibrary(id)
		if err != nil {
			h.log.Error("library scan failed", "library", lib.Name, "err", err)
			return
		}
		h.log.Info("library scan done", "library", lib.Name, "strm", n)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"Started": true})
}

type browseEntry struct {
	Name string `json:"Name"`
	Path string `json:"Path"`
}

// webBrowseDirs 给面板的路径下拉框列出某个目录下的子目录。
func (h *Handler) webBrowseDirs(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" && len(h.cfg.LibraryRoots) > 0 {
		path = h.cfg.LibraryRoots[0]
	}
	clean, err := h.checkLibraryDir(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, err := os.ReadDir(clean)
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取目录失败："+err.Error())
		return
	}
	dirs := make([]browseEntry, 0, len(entries))
	truncated := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if len(dirs) >= 500 {
			truncated = true
			break
		}
		dirs = append(dirs, browseEntry{Name: e.Name(), Path: filepath.Join(clean, e.Name())})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })

	writeJSON(w, http.StatusOK, map[string]any{
		"Path":      clean,
		"Dirs":      dirs,
		"Truncated": truncated,
	})
}

func (h *Handler) checkLibraryDir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("路径必须是绝对路径，例如 /strm/电影")
	}
	path = filepath.Clean(path)

	roots := h.cfg.LibraryRoots
	if len(roots) == 0 {
		roots = []string{"/strm"}
	}
	allowed := false
	for _, root := range roots {
		root = filepath.Clean(root)
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("路径必须在这些目录下：%s", strings.Join(roots, ", "))
	}

	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("路径不存在或容器未挂载：%s", path)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("路径不是目录：%s", path)
	}
	return path, nil
}

func (h *Handler) validateLibraryPayload(p libraryPayload) (name string, paths []string, kind string, err error) {
	name = strings.TrimSpace(p.Name)
	if name == "" {
		return "", nil, "", fmt.Errorf("库名称不能为空")
	}
	if len([]rune(name)) > 60 {
		return "", nil, "", fmt.Errorf("库名称太长")
	}

	raw := p.Paths
	if len(raw) == 0 && strings.TrimSpace(p.Path) != "" {
		raw = []string{p.Path}
	}
	if len(raw) == 0 {
		return "", nil, "", fmt.Errorf("至少需要一个文件夹路径")
	}
	seen := make(map[string]bool, len(raw))
	for _, rp := range raw {
		cp, cerr := h.checkLibraryDir(rp)
		if cerr != nil {
			return "", nil, "", cerr
		}
		if !seen[cp] {
			seen[cp] = true
			paths = append(paths, cp)
		}
	}

	switch strings.ToLower(strings.TrimSpace(p.Kind)) {
	case "movies", "movie":
		kind = "movies"
	case "tvshows", "tv", "series":
		kind = "tvshows"
	case "mixed":
		kind = "mixed"
	case "auto", "":
		kind = "auto"
	default:
		return "", nil, "", fmt.Errorf("类型只能是 movies / tvshows / mixed / auto")
	}
	return name, paths, kind, nil
}

// webLibraryMove 上移/下移媒体库，用于自定义导航顺序。
func (h *Handler) webLibraryMove(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	dir := r.URL.Query().Get("dir")
	if dir != "up" && dir != "down" {
		writeError(w, http.StatusBadRequest, "dir must be up or down")
		return
	}
	if err := h.db.MoveLibrary(r.PathValue("id"), dir == "up"); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "move library failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// webLibraryReorder 按前端拖拽后的顺序批量重排媒体库。
func (h *Handler) webLibraryReorder(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var p struct {
		Ids []string `json:"Ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(p.Ids) == 0 {
		writeError(w, http.StatusBadRequest, "ids is required")
		return
	}
	if err := h.db.ReorderLibraries(p.Ids); err != nil {
		writeError(w, http.StatusInternalServerError, "reorder libraries failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
