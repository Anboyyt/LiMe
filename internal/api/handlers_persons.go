package api

import (
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lime/internal/dto"
	"lime/internal/store"
)

// persons 返回演员列表。客户端按演员筛选/演员页会拉这个接口。
func (h *Handler) persons(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	v := r.URL.Query()
	start := atoiDefault(v.Get("StartIndex"), 0)
	limit := atoiDefault(v.Get("Limit"), 0)
	if limit <= 0 || limit > 500 {
		limit = 500
	}

	list, total, err := h.db.ListPersons(v.Get("SearchTerm"), start, limit)
	if err != nil {
		h.log.Error("list persons", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]*dto.BaseItemDto, 0, len(list))
	for i := range list {
		out = append(out, h.personToDto(&list[i]))
	}
	writeJSON(w, http.StatusOK, dto.QueryResult{
		Items:            out,
		TotalRecordCount: total,
		StartIndex:       start,
	})
}

// personByName 给客户端提供演员基础信息；路径参数可能是名字，也可能是 ID。
func (h *Handler) personByName(w http.ResponseWriter, r *http.Request) {
	p, err := h.lookupPerson(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	writeJSON(w, http.StatusOK, h.personToDto(p))
}

// personImage 提供 /Persons/{name}/Images/Primary。
func (h *Handler) personImage(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.PathValue("imageType"), "primary") {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}
	p, err := h.lookupPerson(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	h.servePersonImageFile(w, r, p)
}

// lookupPerson 同时接受演员名和 32 位 ID；Jellyfin 客户端的路由参数两种都用。
func (h *Handler) lookupPerson(key string) (*store.PersonImage, error) {
	if looksLikePersonID(key) {
		if p, err := h.db.PersonImageByID(key); err == nil {
			return p, nil
		}
	}
	return h.db.PersonImageByName(key)
}

func looksLikePersonID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// personToDto 把演员记录转成客户端认识的 BaseItemDto。
func (h *Handler) personToDto(p *store.PersonImage) *dto.BaseItemDto {
	tags := map[string]string{}
	if p.ImageURL != "" {
		tags["Primary"] = personImageTag(p.ImageURL)
	}
	d := &dto.BaseItemDto{
		Name:              p.Name,
		ServerId:          h.serverID,
		Id:                p.ID,
		SortName:          p.Name,
		Type:              "Person",
		ImageTags:         tags,
		PrimaryImageTag:   tags["Primary"],
		BackdropImageTags: []string{},
		ImageBlurHashes:   map[string]any{},
		Genres:            []string{},
		People:            []dto.PersonDto{},
		LocationType:      "Remote",
		UserData:          &dto.UserItemDataDto{Key: p.ID, ItemId: p.ID},
	}
	if tags["Primary"] != "" {
		d.PrimaryImageAspectRatio = 2.0 / 3.0
	}
	return d
}

// servePersonImageFile 优先读本地缓存，没有就从 TMDB 下载一次。
func (h *Handler) servePersonImageFile(w http.ResponseWriter, r *http.Request, p *store.PersonImage) {
	if p == nil || p.ImageURL == "" || p.ID == "" {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}

	dir := filepath.Join(h.cfg.DataDir, "persons")
	cache := filepath.Join(dir, p.ID+".jpg")
	if st, err := os.Stat(cache); err == nil && st.Size() > 0 {
		h.serveCachedImage(w, r, cache)
		return
	}
	if err := h.ensurePersonImage(p, dir, cache); err != nil {
		h.log.Warn("person image download failed", "person", p.ID, "err", err)
		writeError(w, http.StatusNotFound, "image not found")
		return
	}
	h.serveCachedImage(w, r, cache)
}

// ensurePersonImage 同一演员并发请求只下载一次，其余请求等结果。
func (h *Handler) ensurePersonImage(p *store.PersonImage, dir, cache string) error {
	h.personMu.Lock()
	if h.personDL == nil {
		h.personDL = make(map[string]*personDownload)
	}
	if dl, ok := h.personDL[p.ID]; ok {
		h.personMu.Unlock()
		<-dl.done
		return dl.err
	}
	dl := &personDownload{done: make(chan struct{})}
	h.personDL[p.ID] = dl
	h.personMu.Unlock()

	dl.err = h.downloadPersonImage(p.ImageURL, dir, cache)

	h.personMu.Lock()
	delete(h.personDL, p.ID)
	h.personMu.Unlock()
	close(dl.done)
	return dl.err
}

func (h *Handler) serveCachedImage(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}

func (h *Handler) downloadPersonImage(rawURL, dir, dest string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	client := h.personHTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb image %s", resp.Status)
	}

	tmp, err := os.CreateTemp(dir, ".person-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 10<<20)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

// personImageTag 只用于 /Persons/{name} 的缓存标记。
func personImageTag(imageURL string) string {
	if imageURL == "" {
		return ""
	}
	return imageTags(imageURL)["Primary"]
}
