package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lime/internal/store"
)

// TotalRecordCount 必须是过滤后的真实总数，而不是当前页大小，
// 否则客户端看到 total=limit 就停止翻页。
func TestResumePaginationTotal(t *testing.T) {
	env, user, tok := newAuthedEnv(t)
	lib, err := env.db.UpsertLibrary("M", "/strm/m", "movies")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		if err := env.db.UpsertItem(&store.Item{
			ID: id, LibraryID: lib.ID, ParentID: lib.ID,
			Type: store.TypeMovie, Name: id, SortName: id,
		}); err != nil {
			t.Fatal(err)
		}
		if err := env.db.UpsertPlaystate(user.ID, id, 100, false); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/Users/"+user.ID+"/Items/Resume?Limit=2", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Items            []map[string]any
		TotalRecordCount int
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v body=%s", err, rr.Body.String())
	}
	if len(out.Items) != 2 || out.TotalRecordCount != 5 {
		t.Fatalf("items=%d total=%d, want 2/5", len(out.Items), out.TotalRecordCount)
	}
}
