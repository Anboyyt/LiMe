package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lime/internal/dto"
	"lime/internal/store"
)

func TestLibraryDefaultPoster(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("空库", "/strm/空库", "movies")
	if err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Users/"+f.admin.ID+"/Views", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("views = %d: %s", rr.Code, rr.Body.String())
	}
	var views dto.QueryResult
	if err := json.Unmarshal(rr.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views.Items) != 1 || views.Items[0].ImageTags["Primary"] == "" {
		t.Fatalf("default image tags = %+v", views.Items)
	}

	rr = f.do(http.MethodGet, "/Items/"+lib.ID+"/Images/Primary", nil, "")
	if rr.Code != http.StatusOK || rr.Body.Len() == 0 {
		t.Fatalf("default image = %d, %d bytes", rr.Code, rr.Body.Len())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/jpeg") {
		t.Fatalf("content type = %q", ct)
	}
}

func TestLibraryPosterGenerated(t *testing.T) {
	f := newUserFixture(t)
	lib, err := f.env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}

	// 写一张假海报到 data/images
	imgRel := "images/poster.jpg"
	imgAbs := filepath.Join(f.env.cfg.DataDir, filepath.FromSlash(imgRel))
	if err := os.MkdirAll(filepath.Dir(imgAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgAbs, []byte("\xff\xd8\xff\xe0fakejpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	it := &store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeMovie,
		Name: "电影", SortName: "电影", ImagePath: imgRel,
	}
	if err := f.env.db.UpsertItem(it); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/Users/"+f.admin.ID+"/Views", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("views = %d: %s", rr.Code, rr.Body.String())
	}
	var views dto.QueryResult
	if err := json.Unmarshal(rr.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views.Items) != 1 || len(views.Items[0].ImageTags) == 0 {
		t.Fatalf("library image tags = %+v", views.Items)
	}
	tag := views.Items[0].ImageTags["Primary"]
	if tag == "" {
		t.Fatalf("missing primary tag: %+v", views.Items[0])
	}

	rr = f.do(http.MethodGet, "/Items/"+lib.ID+"/Images/Primary?tag="+tag, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("library image = %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() == 0 {
		t.Fatal("empty library image")
	}
}
