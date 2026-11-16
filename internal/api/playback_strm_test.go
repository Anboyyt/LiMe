package api

import (
	"os"
	"path/filepath"
	"testing"

	"lime/internal/store"
)

// PanSave 重新生成 STRM 时只换 pickcode，播放前必须用磁盘上的新地址，
// 否则长剧批量重生成后会留下几百集旧地址。
func TestRefreshSourceFromSTRMUpdatesStaleURL(t *testing.T) {
	env := newTestEnv(t)
	lib, err := env.db.UpsertLibrary("动漫", "/strm/动漫", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "ep1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeEpisode,
		Name: "第 1 集", SeriesID: "s1", SeasonID: "se1",
		IndexNumber: 1, ParentIndexNumber: 1,
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "ep1.strm")
	newURL := "http://192.168.5.133:16868/strm/115/drive?drive_id=5&file_id=123&pickcode=new&size=100"
	if err := os.WriteFile(path, []byte(newURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := &store.MediaSource{
		ID: "src1", ItemID: "ep1", StrmPath: path,
		URL:   "http://192.168.5.133:16868/strm/115/drive?drive_id=5&file_id=123&pickcode=old&size=100",
		Cloud: "115", DriveID: "5", CloudFileID: "123", Size: 100,
		ProbeStatus: "done",
	}
	if err := env.db.UpsertMediaSource(src); err != nil {
		t.Fatal(err)
	}

	env.api.refreshSourceFromSTRM(src)
	if src.URL != newURL {
		t.Fatalf("src URL = %q, want %q", src.URL, newURL)
	}
	got, err := env.db.GetMediaSource("src1")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != newURL {
		t.Fatalf("db URL = %q, want %q", got.URL, newURL)
	}
	if got.ProbeStatus != "done" {
		t.Fatalf("probe status = %q, want done", got.ProbeStatus)
	}
}
