package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestEmbyLibraryEndpoints(t *testing.T) {
	f := newUserFixture(t)

	lib, err := f.env.db.UpsertLibrary("电影", "/strm/电影", "movies")
	if err != nil {
		t.Fatal(err)
	}

	rr := f.do(http.MethodGet, "/emby/Library/VirtualFolders/Query", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("VirtualFolders/Query = %d: %s", rr.Code, rr.Body.String())
	}
	var query struct {
		Items []struct {
			Name           string
			Locations      []string
			CollectionType string
			ItemId         string
		}
		TotalRecordCount int
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &query); err != nil {
		t.Fatal(err)
	}
	if query.TotalRecordCount != 1 || len(query.Items) != 1 {
		t.Fatalf("query result = %+v", query)
	}
	got := query.Items[0]
	if got.Name != "电影" || got.ItemId != lib.ID || got.CollectionType != "movies" ||
		len(got.Locations) != 1 || got.Locations[0] != lib.Path {
		t.Fatalf("virtual folder = %+v", got)
	}

	rr = f.do(http.MethodGet, "/emby/Library/SelectableMediaFolders", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("SelectableMediaFolders = %d: %s", rr.Code, rr.Body.String())
	}
	var folders []struct {
		Name       string
		Id         string
		SubFolders []struct {
			Path string
		}
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &folders); err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 || folders[0].Id != lib.ID ||
		len(folders[0].SubFolders) != 1 || folders[0].SubFolders[0].Path != lib.Path {
		t.Fatalf("selectable folders = %+v", folders)
	}
}
