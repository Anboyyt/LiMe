package api

import (
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

// personByName 给客户端提供演员基础信息。
func (h *Handler) personByName(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := h.db.PersonImageByName(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	tags := map[string]string{}
	if p.ImageURL != "" {
		tags["Primary"] = personImageTag(p.ImageURL)
	}
	writeJSON(w, http.StatusOK, &dto.BaseItemDto{
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
	})
}

// personImage 提供 /Persons/{name}/Images/Primary。
func (h *Handler) personImage(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.PathValue("imageType"), "primary") {
		writeError(w, http.StatusNotFound, "image not found")
		return
	}
	p, err := h.db.PersonImageByName(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	h.servePersonImageFile(w, r, p)
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
