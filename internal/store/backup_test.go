package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupAndPrune(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)

	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		name := "lime.db.bak-" + time.Now().Add(time.Duration(i)*time.Second).Format("20060102-150405")
		if err := db.BackupTo(filepath.Join(dir, name)); err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
		// 确保文件名时间戳递增（同一秒内 Format 会相同）
		time.Sleep(time.Second)
	}

	// 备份文件能被独立打开且数据完整
	entries, _ := os.ReadDir(dir)
	if len(entries) != 4 {
		t.Fatalf("backups = %d, want 4", len(entries))
	}
	backup, err := Open(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	it, err := backup.GetItem("s1")
	backup.Close()
	if err != nil || it.Name != "剧" {
		t.Fatalf("backup data = %v, %v", it, err)
	}

	// 备份数少于保留数时不能 panic
	if n, err := PruneBackups(dir, 10); err != nil || n != 0 {
		t.Fatalf("prune with keep>len = %d, %v", n, err)
	}

	removed, err := PruneBackups(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("remaining = %d, want 2", len(entries))
	}
}
