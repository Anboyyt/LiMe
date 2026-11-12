package store

import (
	"fmt"
	"testing"
)

func TestMediaStreamsBySources(t *testing.T) {
	db := newTestDB(t)

	// 造 401 个片源，跨过 400 的分块边界，验证两块结果都合并回来。
	srcIDs := make([]string, 0, 401)
	for i := 0; i < 401; i++ {
		srcIDs = append(srcIDs, fmt.Sprintf("src-%03d", i))
	}

	lib, err := db.UpsertLibrary("测试", "/strm", "mixed")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertItem(&Item{ID: "item", LibraryID: lib.ID, ParentID: lib.ID, Type: TypeMovie, Name: "片", SortName: "片"}); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range srcIDs {
		if _, err := tx.Exec(`INSERT INTO media_sources (id, item_id, strm_path, url, cloud, drive_id,
			cloud_file_id, size, container, file_name, sort_rank, duration_ticks, probe_status,
			strm_mtime, strm_size, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, "item", "/strm/x.strm", "http://x", "115", "", "", 0, "mkv", "x.mkv", 0, 0, "none", 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO media_streams (source_id, stream_index, type, codec, width, height)
			VALUES (?,?,?,?,?,?)`, id, 0, "Video", "hevc", 3840, 2160); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := tx.Exec(`INSERT INTO media_streams (source_id, stream_index, type, codec, channels)
				VALUES (?,?,?,?,?)`, id, 1, "Audio", "aac", 2); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := db.MediaStreamsBySources(srcIDs)
	if err != nil {
		t.Fatalf("MediaStreamsBySources: %v", err)
	}
	if len(got) != len(srcIDs) {
		t.Fatalf("分组数 = %d, want %d", len(got), len(srcIDs))
	}
	if n := len(got["src-000"]); n != 2 {
		t.Fatalf("src-000 轨道数 = %d, want 2", n)
	}
	// 跨分块的那一条也要在。
	if n := len(got["src-400"]); n != 1 || got["src-400"][0].Height != 2160 {
		t.Fatalf("src-400 轨道 = %+v", got["src-400"])
	}

	// 没有轨道的片源不出现在 map 里，调用方按 nil 处理。
	empty, err := db.MediaStreamsBySources([]string{"nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("空结果 = %v", empty)
	}
	if _, err := db.MediaStreamsBySources(nil); err != nil {
		t.Fatalf("nil ids: %v", err)
	}
}
