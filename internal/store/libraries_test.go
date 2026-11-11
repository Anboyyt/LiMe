package store

import (
	"path/filepath"
	"testing"
)

func TestLibraryCRUD(t *testing.T) {
	db := newTestDB(t)

	lib, err := db.CreateLibrary("电影", []string{"/strm/电影", "/strm/电影2"}, "movies")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateLibrary("重复", []string{"/strm/电影"}, "movies"); err != ErrLibraryPathExists {
		t.Fatalf("duplicate create err = %v", err)
	}

	got, err := db.GetLibrary(lib.ID)
	if err != nil || got.Name != "电影" || got.Path != filepath.Clean("/strm/电影") || got.Kind != "movies" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if len(got.Paths) != 2 || got.Paths[0] != filepath.Clean("/strm/电影") || got.Paths[1] != filepath.Clean("/strm/电影2") {
		t.Fatalf("paths = %+v", got.Paths)
	}

	if err := db.UpdateLibrary(lib.ID, "电影库", []string{"/strm/电影3"}, "mixed"); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetLibrary(lib.ID)
	if got.Name != "电影库" || got.Path != filepath.Clean("/strm/电影3") || got.Kind != "mixed" {
		t.Fatalf("updated = %+v", got)
	}
	if len(got.Paths) != 1 || got.Paths[0] != filepath.Clean("/strm/电影3") {
		t.Fatalf("updated paths = %+v", got.Paths)
	}

	other, _ := db.CreateLibrary("剧集", []string{"/strm/剧集"}, "tvshows")
	if err := db.UpdateLibrary(other.ID, "剧集", []string{"/strm/电影3"}, "tvshows"); err != ErrLibraryPathExists {
		t.Fatalf("conflict update err = %v", err)
	}

	if err := db.DeleteLibrary(lib.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetLibrary(lib.ID); err != ErrNotFound {
		t.Fatalf("deleted get err = %v", err)
	}
}
