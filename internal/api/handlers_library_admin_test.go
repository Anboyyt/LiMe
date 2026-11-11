package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowseLibraryDirs(t *testing.T) {
	f := newUserFixture(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "电影"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "剧集"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "文件.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/web/api/browse?path="+root, nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("browse = %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Dirs []struct{ Name, Path string }
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Dirs) != 2 || out.Dirs[0].Name != "剧集" || out.Dirs[1].Name != "电影" {
		t.Fatalf("dirs = %+v", out.Dirs)
	}
}

func TestLibraryAdminAPI(t *testing.T) {
	f := newUserFixture(t)
	dir := t.TempDir()

	rr := f.do(http.MethodPost, "/web/api/libraries", map[string]string{
		"Name": "新库", "Path": dir, "Kind": "movies",
	}, f.adminTok)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rr.Code, rr.Body.String())
	}
	var lib struct{ ID, Name, Path, Kind string }
	if err := json.Unmarshal(rr.Body.Bytes(), &lib); err != nil {
		t.Fatal(err)
	}
	if lib.ID == "" || lib.Name != "新库" || lib.Kind != "movies" {
		t.Fatalf("created = %+v", lib)
	}

	rr = f.do(http.MethodGet, "/web/api/libraries", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", rr.Code, rr.Body.String())
	}
	var libs []struct {
		ID   string
		Path string
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &libs); err != nil {
		t.Fatal(err)
	}
	if len(libs) != 1 || libs[0].ID != lib.ID {
		t.Fatalf("list = %+v", libs)
	}

	rr = f.do(http.MethodPut, "/web/api/libraries/"+lib.ID, map[string]string{
		"Name": "改名", "Path": dir, "Kind": "mixed",
	}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", rr.Code, rr.Body.String())
	}
	var updated struct{ Name, Kind string }
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.Name != "改名" || updated.Kind != "mixed" {
		t.Fatalf("updated = %+v", updated)
	}

	// 普通用户不能改
	rr = f.do(http.MethodPost, "/web/api/libraries", map[string]string{
		"Name": "x", "Path": dir, "Kind": "movies",
	}, f.bobTok)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin create = %d", rr.Code)
	}

	// 不在允许根目录 / 不存在的路径
	rr = f.do(http.MethodPost, "/web/api/libraries", map[string]string{
		"Name": "x", "Path": "/no/such/path", "Kind": "movies",
	}, f.adminTok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad path = %d: %s", rr.Code, rr.Body.String())
	}

	rr = f.do(http.MethodDelete, "/web/api/libraries/"+lib.ID, nil, f.adminTok)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", rr.Code, rr.Body.String())
	}
}
