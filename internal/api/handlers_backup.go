package api

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"lime/internal/store"
)

// webBackup 立即生成一份数据库备份（管理员）。
func (h *Handler) webBackup(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// 备份是重 IO 操作，同一时间只允许一个。
	h.backupMu.Lock()
	defer h.backupMu.Unlock()

	name := "lime.db.bak-" + time.Now().Format("20060102-150405")
	dest := filepath.Join(h.cfg.DataDir, name)
	start := time.Now()
	if err := h.db.BackupTo(dest); err != nil {
		h.log.Error("manual backup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	removed, _ := store.PruneBackups(h.cfg.DataDir, h.cfg.BackupKeep)
	var size int64
	if st, err := os.Stat(dest); err == nil {
		size = st.Size()
	}
	h.log.Info("database backup created",
		"file", name, "mb", size>>20, "dur", time.Since(start).Round(time.Second).String())
	writeJSON(w, http.StatusOK, map[string]any{
		"File":    name,
		"MB":      size >> 20,
		"Removed": removed,
	})
}
