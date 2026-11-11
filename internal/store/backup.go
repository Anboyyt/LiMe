package store

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BackupTo 用 VACUUM INTO 写一份一致的数据库快照。
// 先写 .tmp 再改名，避免中途失败留下半个备份。
func (d *DB) BackupTo(path string) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if _, err := d.Exec(`VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// PruneBackups 只保留 dir 下最新的 keep 份 lime.db.bak-*，返回删除数量。
func PruneBackups(dir string, keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "lime.db.bak-") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names))) // 文件名带时间戳，倒序即最新在前
	if len(names) <= keep {
		return 0, nil
	}
	removed := 0
	for _, name := range names[keep:] {
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed++
		}
	}
	return removed, nil
}
