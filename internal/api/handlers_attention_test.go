package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 首屏只拿计数，列表按标签页请求，避免一次查四类数据把面板卡住。
func TestWebAttentionCountsAndKind(t *testing.T) {
	f := newUserFixture(t)

	rr := f.do(http.MethodGet, "/web/api/attention", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("counts status = %d: %s", rr.Code, rr.Body.String())
	}
	var counts struct {
		Counts   map[string]int `json:"Counts"`
		NoTmdb   []any          `json:"NoTmdb"`
		Failures []any          `json:"Failures"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	if counts.Counts == nil {
		t.Fatal("missing Counts")
	}
	if len(counts.NoTmdb) != 0 || len(counts.Failures) != 0 {
		t.Fatalf("counts-only response should not carry lists: %+v", counts)
	}

	rr = f.do(http.MethodGet, "/web/api/attention?kind=notmdb", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("kind status = %d: %s", rr.Code, rr.Body.String())
	}

	rr = f.do(http.MethodGet, "/web/api/attention?kind=bogus", nil, f.adminTok)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind status = %d, want 400", rr.Code)
	}
}
