package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameStreamProbe(t *testing.T) {
	f := newUserFixture(t)

	// 反代通常回 302，探测不应该跟到 CDN。
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://cdn.example.com/x")
		w.WriteHeader(http.StatusFound)
	}))
	defer target.Close()

	rr := f.do(http.MethodPost, "/web/api/same-stream/probe", map[string]any{"Url": target.URL}, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Ok     bool
		Status int
		Error  string
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Ok || out.Status != http.StatusFound {
		t.Fatalf("result = %+v", out)
	}

	// 5xx 视为不可用。
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()
	rr = f.do(http.MethodPost, "/web/api/same-stream/probe", map[string]any{"Url": broken.URL}, f.adminTok)
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Ok {
		t.Fatalf("5xx should not be ok: %+v", out)
	}

	// 非法 scheme
	if rr := f.do(http.MethodPost, "/web/api/same-stream/probe", map[string]any{"Url": "ftp://x"}, f.adminTok); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad scheme status = %d, want 400", rr.Code)
	}

	// 普通用户不能探测
	if rr := f.do(http.MethodPost, "/web/api/same-stream/probe", map[string]any{"Url": target.URL}, f.bobTok); rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", rr.Code)
	}
}
