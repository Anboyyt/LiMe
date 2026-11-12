package store

import (
	"strings"
	"testing"
	"time"
)

func seedSourceItems(t *testing.T, db *DB, ids ...string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO libraries (id, name, path, kind, created_at) VALUES ('lib','测试','/strm','mixed',?)`,
		time.Now().Unix(),
	); err != nil {
		t.Fatalf("seed library: %v", err)
	}
	for _, id := range ids {
		if err := db.UpsertItem(&Item{
			ID: id, LibraryID: "lib", ParentID: "lib",
			Type: TypeEpisode, Name: id, SortName: id, CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("upsert item %s: %v", id, err)
		}
	}
}

// STRM 重新生成后 file_id 变了：同一路径只能保留新片源，否则客户端会看到两个版本。
func TestUpsertMediaSourceReplacesSamePath(t *testing.T) {
	db := newTestDB(t)
	seedSourceItems(t, db, "e1")

	path := "/strm/剧集/Show/Season 1/E01.strm"
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "old", ItemID: "e1", StrmPath: path, URL: "http://cdn/old",
		Cloud: "115", DriveID: "5", CloudFileID: "111",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "new", ItemID: "e1", StrmPath: path, URL: "http://cdn/new",
		Cloud: "115", DriveID: "5", CloudFileID: "222",
	}); err != nil {
		t.Fatal(err)
	}

	srcs, err := db.MediaSourcesByItem("e1")
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 || srcs[0].ID != "new" {
		t.Fatalf("sources = %+v, want only new", srcs)
	}
}

// 条目 ID 变化（目录改名 / NFO 标题变化）时，新 source ID 会撞
// (drive_id, cloud_file_id) 唯一索引。应清掉旧行后正常写入，而不是报错。
func TestUpsertMediaSourceMovesAcrossItemIDs(t *testing.T) {
	db := newTestDB(t)
	seedSourceItems(t, db, "old-item", "new-item")

	if err := db.UpsertMediaSource(&MediaSource{
		ID: "src-old", ItemID: "old-item", StrmPath: "/strm/a.strm",
		URL: "http://cdn/a", Cloud: "115", DriveID: "5", CloudFileID: "777",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMediaSource(&MediaSource{
		ID: "src-new", ItemID: "new-item", StrmPath: "/strm/a.strm",
		URL: "http://cdn/a", Cloud: "115", DriveID: "5", CloudFileID: "777",
	}); err != nil {
		t.Fatalf("upsert should move source, got: %v", err)
	}

	srcs, err := db.MediaSourcesByItem("new-item")
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 || srcs[0].ID != "src-new" {
		t.Fatalf("new item sources = %+v", srcs)
	}
	old, err := db.MediaSourcesByItem("old-item")
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != 0 {
		t.Fatalf("old item should have no sources, got %d", len(old))
	}
}

// 同名文件但不同 item 且路径不同时（合法多版本），不能误删。
func TestUpsertMediaSourceKeepsDifferentCloudFiles(t *testing.T) {
	db := newTestDB(t)
	seedSourceItems(t, db, "e1")

	for _, s := range []*MediaSource{
		{ID: "a", ItemID: "e1", StrmPath: "/strm/a.strm", URL: "http://cdn/a", Cloud: "115", DriveID: "5", CloudFileID: "1"},
		{ID: "b", ItemID: "e1", StrmPath: "/strm/b.strm", URL: "http://cdn/b", Cloud: "115", DriveID: "5", CloudFileID: "2"},
	} {
		if err := db.UpsertMediaSource(s); err != nil {
			t.Fatal(err)
		}
	}
	srcs, _ := db.MediaSourcesByItem("e1")
	if len(srcs) != 2 {
		t.Fatalf("sources = %d, want 2", len(srcs))
	}
}

// idx_media_sources_cloud 是部分索引；清理旧片源的 DELETE 必须带上
// cloud_file_id <> ”，否则会退化成全表扫描，全量扫描直接 O(n²)。
func TestMediaSourceCloudCleanupUsesIndex(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN DELETE FROM media_sources
WHERE drive_id = ? AND cloud_file_id = ? AND cloud_file_id <> '' AND id <> ?`, "5", "1", "x")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if !strings.Contains(plan.String(), "idx_media_sources_cloud") {
		t.Fatalf("delete should use partial index, plan = %s", plan.String())
	}
}
