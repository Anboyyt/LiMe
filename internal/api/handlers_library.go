package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"lime/internal/store"
)

// virtualFolders 模拟 Emby 的库列表，部分第三方服务（PanSave 等）靠它识别库。
func (h *Handler) virtualFolders(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}

	libs, err := h.db.ListLibraries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]map[string]any, 0, len(libs))
	for _, lib := range libs {
		_, primaryTag, _, backdropTag := h.libraryArtwork(lib)
		out = append(out, map[string]any{
			"Name":               lib.Name,
			"Locations":          libraryLocations(lib),
			"CollectionType":     collectionType(lib.Kind),
			"ItemId":             lib.ID,
			"PrimaryImageItemId": lib.ID,
			"PrimaryImageTag":    primaryTag,
			"BackdropImageTags":  []string{backdropTag},
			"LibraryOptions": map[string]any{
				"PathInfos":     []any{},
				"MetadataAgent": "",
			},
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// virtualFoldersQuery 是 Emby 的库列表接口（PanSave 用它做校验）。
// 与 Jellyfin 的 /Library/VirtualFolders 不同，这里返回 QueryResult 包装。
func (h *Handler) virtualFoldersQuery(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}

	libs, err := h.db.ListLibraries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	start := queryInt(r, "StartIndex")
	if start < 0 {
		start = 0
	}
	limit := queryInt(r, "Limit")
	if limit <= 0 {
		limit = len(libs)
	}
	end := start + limit
	if start > len(libs) {
		start = len(libs)
	}
	if end > len(libs) {
		end = len(libs)
	}

	items := make([]map[string]any, 0, end-start)
	for _, lib := range libs[start:end] {
		items = append(items, map[string]any{
			"Name":           lib.Name,
			"Locations":      libraryLocations(lib),
			"CollectionType": collectionType(lib.Kind),
			"ItemId":         lib.ID,
			"Id":             lib.ID,
			"Guid":           lib.ID,
			"LibraryOptions": map[string]any{
				"PathInfos":     []any{},
				"MetadataAgent": "",
			},
			"RefreshProgress": 0,
			"RefreshStatus":   "",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"Items":            items,
		"TotalRecordCount": len(libs),
	})
}

func queryInt(r *http.Request, key string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(key))
	return n
}

// selectableMediaFolders 对应 Emby 的 GET /Library/SelectableMediaFolders。
// PanSave 校验配置时会用 SubFolders 里的 Path 识别库路径。
func (h *Handler) selectableMediaFolders(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}

	libs, err := h.db.ListLibraries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]map[string]any, 0, len(libs))
	for _, lib := range libs {
		out = append(out, map[string]any{
			"Name": lib.Name,
			"Id":   lib.ID,
			"Guid": lib.ID,
			"SubFolders": []map[string]any{{
				"Name":                     lib.Name,
				"Id":                       lib.ID,
				"Path":                     lib.Path,
				"IsUserAccessConfigurable": false,
			}},
			"IsUserAccessConfigurable": false,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// refreshLibrary 触发一次异步扫描。默认全量，?mode=fast 走增量。
func (h *Handler) refreshLibrary(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	fast := strings.EqualFold(r.URL.Query().Get("mode"), "fast")

	go func() {
		var count int
		var err error
		if fast {
			count, err = h.scanner.ScanAllFast()
		} else {
			count, err = h.scanner.ScanAll()
		}
		if err != nil {
			h.log.Error("library refresh failed", "fast", fast, "err", err)
			return
		}
		h.log.Info("library refresh done", "fast", fast, "strm", count)
		h.scrapeAfterIngest("library refresh")
	}()

	w.WriteHeader(http.StatusNoContent)
}

// mediaUpdated 接收 Emby/Jellyfin 的 /Library/Media/Updated 通知。
// PanSave 整理完新片后会带具体路径回调；能匹配到库就只做该库增量，
// 匹配不到则退化为全库增量扫描。
func (h *Handler) mediaUpdated(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}

	var body struct {
		Path    string `json:"Path"`
		Updates []struct {
			Path string `json:"Path"`
		} `json:"Updates"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)

	paths := make([]string, 0, len(body.Updates)+1)
	if body.Path != "" {
		paths = append(paths, body.Path)
	}
	for _, u := range body.Updates {
		if u.Path != "" {
			paths = append(paths, u.Path)
		}
	}

	go func() {
		libs, err := h.db.ListLibraries()
		if err != nil {
			h.log.Error("media updated: list libraries", "err", err)
			return
		}
		// PanSave 的 Emby 通知语义是「入库即刮削」，解析完顺手补一次元数据。
		defer h.scrapeAfterIngest("media updated")

		// 能映射到本地具体文件时只刷新这些文件，不遍历整个库。
		localPaths := localizeNotificationPaths(libs, paths)
		if len(localPaths) > 0 {
			count, ids, err := h.scanner.RefreshFilesWithIDs(localPaths)
			if err != nil {
				h.log.Error("media updated: refresh files", "err", err)
				return
			}
			h.log.Info("media updated: files refreshed",
				"notified", len(paths), "matched", len(localPaths), "changed", count)
			h.scrapeItems(ids)
			return
		}

		matched := map[string]*store.Library{}
		for _, p := range paths {
			for _, lib := range matchLibraries(libs, p) {
				matched[lib.ID] = lib
			}
		}
		if len(matched) == 0 {
			count, err := h.scanner.ScanAllFast()
			if err != nil {
				h.log.Error("media updated: full fast scan", "err", err)
				return
			}
			h.log.Info("media updated: full fast scan done", "paths", len(paths), "changed", count)
			return
		}
		for _, lib := range matched {
			count, err := h.scanner.ScanLibraryFast(lib.ID)
			if err != nil {
				h.log.Error("media updated: fast scan", "library", lib.Name, "err", err)
				continue
			}
			h.log.Info("media updated: fast scan done",
				"library", lib.Name, "changed", count)
		}
	}()

	w.WriteHeader(http.StatusNoContent)
}

// scrapeItems 只刮削刚入库的条目，避免等大批量刮削跑完。
func (h *Handler) scrapeItems(ids []string) {
	if h.scraper == nil || !h.scraper.Enabled() || len(ids) == 0 {
		return
	}
	go func() {
		if _, err := h.scraper.ScrapeItems(ids); err != nil {
			h.log.Debug("ingest scrape skipped", "items", len(ids), "err", err)
		}
	}()
}

// scrapeAfterIngest 在入库通知处理完后异步补刮削。
// 与每日刮削共用同一把锁：正在跑就跳过，不打断已有任务。
func (h *Handler) scrapeAfterIngest(source string) {
	if h.scraper == nil || !h.scraper.Enabled() {
		return
	}
	go func() {
		n, err := h.scraper.Run()
		if err != nil {
			h.log.Debug("ingest scrape skipped", "source", source, "err", err)
			return
		}
		h.log.Info("ingest scrape done", "source", source, "items", n)
	}()
}

// libraryLocations 返回库的全部文件夹；老数据没有 library_paths 时回落单路径。
func libraryLocations(lib *store.Library) []string {
	if len(lib.Paths) > 0 {
		return lib.Paths
	}
	if lib.Path != "" {
		return []string{lib.Path}
	}
	return []string{}
}

// matchLibraries 把通知里的路径映射到本地库。PanSave 传的是它容器内
// 的路径（如 /115strm/剧集/...），LiMe 挂载的是 /strm/剧集/...，
// 所以优先做前缀匹配，再退回按目录名匹配。
func matchLibraries(libs []*store.Library, path string) []*store.Library {
	p := filepath.ToSlash(path)
	var out []*store.Library
	for _, lib := range libs {
		matched := false
		for _, location := range libraryLocations(lib) {
			lp := filepath.ToSlash(location)
			if p == lp || strings.HasPrefix(p, strings.TrimRight(lp, "/")+"/") {
				matched = true
				break
			}
			base := filepath.Base(lp)
			if base != "" && base != "/" && strings.Contains(p, "/"+base+"/") {
				matched = true
				break
			}
		}
		if matched {
			out = append(out, lib)
		}
	}
	return out
}

// itemRefresh 只重解析一个条目已有的 STRM，不再触发全量扫描。
func (h *Handler) itemRefresh(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id := r.PathValue("itemId")
	if id == "" {
		writeError(w, http.StatusBadRequest, "itemId is required")
		return
	}

	go func() {
		n, err := h.scanner.RefreshItem(id)
		if err != nil {
			h.log.Error("refresh item", "item", id, "err", err)
			return
		}
		h.log.Info("item refreshed", "item", id, "sources", n)
		h.scrapeAfterIngest("item refresh")
	}()

	w.WriteHeader(http.StatusNoContent)
}

// localizeNotificationPaths 把 PanSave 容器内的路径映射到 LiMe 的挂载路径。
// 支持两种形式：已经是 /strm/...，或带 PanSave 前缀（/115strm、/影视库 等）。
func localizeNotificationPaths(libs []*store.Library, paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, raw := range paths {
		p := filepath.ToSlash(strings.TrimSpace(raw))
		if p == "" {
			continue
		}
		local := localizeOne(libs, p)
		if local != "" && !seen[local] {
			seen[local] = true
			out = append(out, local)
		}
	}
	return out
}

func localizeOne(libs []*store.Library, p string) string {
	// 已经是本地路径
	for _, lib := range libs {
		for _, root := range libraryLocations(lib) {
			r := strings.TrimRight(filepath.ToSlash(root), "/")
			if p == r || strings.HasPrefix(p, r+"/") {
				return p
			}
		}
	}

	// 按库目录名匹配通知路径里的分类段
	segments := strings.Split(strings.Trim(p, "/"), "/")
	for _, lib := range libs {
		for _, root := range libraryLocations(lib) {
			r := strings.TrimRight(filepath.ToSlash(root), "/")
			base := filepath.Base(r)
			if base == "" || base == "." || base == "/" {
				continue
			}
			for i, seg := range segments {
				if seg != base {
					continue
				}
				rest := strings.Join(segments[i+1:], "/")
				if rest == "" {
					return r
				}
				return r + "/" + rest
			}
		}
	}
	return ""
}
