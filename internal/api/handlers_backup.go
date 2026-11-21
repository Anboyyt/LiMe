package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lime/internal/store"
)

// backupDir 返回实际写备份的目录：面板设置 > LIME_BACKUP_DIR > 数据目录。
func (h *Handler) backupDir() string {
	dir := h.cfg.BackupDir
	if v, err := h.db.GetSetting("backup_dir"); err == nil && strings.TrimSpace(v) != "" {
		dir = strings.TrimSpace(v)
	}
	if dir == "" {
		dir = h.cfg.DataDir
	}
	return dir
}

// backupKeep 返回保留份数：面板设置 > LIME_BACKUP_KEEP。
func (h *Handler) backupKeep() int {
	keep := h.cfg.BackupKeep
	if v, err := h.db.GetSetting("backup_keep"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			keep = n
		}
	}
	return keep
}

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

	start := time.Now()
	dir := h.backupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		_ = h.db.RecordTaskRun("backup", start.Unix(), time.Now().Unix(), "error", "备份目录不可用："+err.Error())
		h.log.Error("manual backup failed", "dir", dir, "err", err)
		writeError(w, http.StatusInternalServerError, "backup dir unavailable: "+err.Error())
		return
	}
	name := "lime.db.bak-" + time.Now().Format("20060102-150405")
	dest := filepath.Join(dir, name)
	if err := h.db.BackupTo(dest); err != nil {
		_ = h.db.RecordTaskRun("backup", start.Unix(), time.Now().Unix(), "error", "备份失败："+err.Error())
		h.log.Error("manual backup failed", "dir", dir, "err", err)
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	keep := h.backupKeep()
	removed, _ := store.PruneBackups(dir, keep)
	var size int64
	if st, err := os.Stat(dest); err == nil {
		size = st.Size()
	}
	h.log.Info("database backup created",
		"file", name, "dir", dir, "mb", size>>20, "dur", time.Since(start).Round(time.Second).String())
	_ = h.db.RecordTaskRun("backup", start.Unix(), time.Now().Unix(), "ok",
		fmt.Sprintf("手动备份 %s（%d MB）", name, size>>20))
	writeJSON(w, http.StatusOK, map[string]any{
		"File":    name,
		"Dir":     dir,
		"MB":      size >> 20,
		"Removed": removed,
	})
}
