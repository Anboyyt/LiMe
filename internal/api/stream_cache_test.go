package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lime/internal/config"
	"lime/internal/store"
)

func TestParseStreamRange(t *testing.T) {
	const size = int64(1000)
	cases := []struct {
		in         string
		start, end int64
		ok         bool
	}{
		{"bytes=0-", 0, 999, true},
		{"bytes=10-19", 10, 19, true},
		{"bytes=990-", 990, 999, true},
		{"bytes=-10", 990, 999, true},
		{"bytes=0-9999", 0, 999, true},
		{"bytes=1000-", 0, 0, false},
		{"bytes=10-5", 0, 0, false},
		{"bytes=0-1,5-6", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		s, e, ok := parseStreamRange(c.in, size)
		if ok != c.ok || (ok && (s != c.start || e != c.end)) {
			t.Fatalf("%q => %d,%d,%v; want %d,%d,%v", c.in, s, e, ok, c.start, c.end, c.ok)
		}
	}
}

// 头部/尾部命中时应该直接回本地字节，不碰上游。
func TestStreamCacheServesHeadAndTail(t *testing.T) {
	f := newUserFixtureWithConfig(t, func(c *config.Config) {
		c.ProxyYamby = true
		c.StreamCacheMB = 1
		c.StreamCacheMaxMB = 64
	})
	lib, err := f.env.db.UpsertLibrary("电影", "/strm", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: "电影", SortName: "电影",
	}); err != nil {
		t.Fatal(err)
	}
	const size = int64(4 << 20)
	src := &store.MediaSource{
		ID: "src1", ItemID: "m1", StrmPath: "/strm/m1.strm",
		URL: "http://127.0.0.1:1/should-not-be-called", Container: "mkv",
		Size: size, FileName: "m1.mkv",
	}
	if err := f.env.db.UpsertMediaSource(src); err != nil {
		t.Fatal(err)
	}

	// 1MB 头部、2MB 尾部。
	head := bytes.Repeat([]byte("A"), 1<<20)
	tail := bytes.Repeat([]byte("B"), 2<<20)
	for _, p := range []struct {
		path string
		data []byte
	}{
		{f.env.api.streamCachePath(src, false), head},
		{f.env.api.streamCachePath(src, true), tail},
	} {
		if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.path, p.data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	const path = "/Videos/m1/stream?static=true&MediaSourceId=src1"
	do := func(rng string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Emby-Token", f.adminTok)
		req.Header.Set("User-Agent", "Yamby/2.1.0.11(Android 16)")
		req.Header.Set("Range", rng)
		rr := httptest.NewRecorder()
		f.env.handler.ServeHTTP(rr, req)
		return rr
	}

	rr := do("bytes=0-1023")
	if rr.Code != http.StatusPartialContent || !bytes.Equal(rr.Body.Bytes(), bytes.Repeat([]byte("A"), 1024)) {
		t.Fatalf("head status/body = %d / %d bytes", rr.Code, rr.Body.Len())
	}
	if got := rr.Header().Get("Content-Range"); got != "bytes 0-1023/4194304" {
		t.Fatalf("head Content-Range = %q", got)
	}

	rr = do("bytes=4194303-4194303")
	if rr.Code != http.StatusPartialContent || !bytes.Equal(rr.Body.Bytes(), []byte("B")) {
		t.Fatalf("tail status/body = %d / %q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Range"); got != "bytes 4194303-4194303/4194304" {
		t.Fatalf("tail Content-Range = %q", got)
	}
}

// 头部命中但请求范围更长时，先发缓存，再继续从上游透传。
func TestStreamCacheContinuesUpstream(t *testing.T) {
	var gotRange string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(bytes.Repeat([]byte("C"), 3<<20))
	}))
	defer upstream.Close()

	f := newUserFixtureWithConfig(t, func(c *config.Config) {
		c.ProxyYamby = true
		c.StreamCacheMB = 1
		c.StreamCacheMaxMB = 64
	})
	lib, _ := f.env.db.UpsertLibrary("电影", "/strm", "movies")
	_ = f.env.db.UpsertItem(&store.Item{
		ID: "m1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeMovie, Name: "电影", SortName: "电影",
	})
	const size = int64(4 << 20)
	src := &store.MediaSource{
		ID: "src1", ItemID: "m1", StrmPath: "/strm/m1.strm",
		URL: upstream.URL + "/m1.mkv", Container: "mkv", Size: size, FileName: "m1.mkv",
	}
	_ = f.env.db.UpsertMediaSource(src)

	headPath := f.env.api.streamCachePath(src, false)
	_ = os.MkdirAll(filepath.Dir(headPath), 0o755)
	_ = os.WriteFile(headPath, bytes.Repeat([]byte("A"), 1<<20), 0o644)

	req := httptest.NewRequest(http.MethodGet, "/Videos/m1/stream?static=true&MediaSourceId=src1", nil)
	req.Header.Set("X-Emby-Token", f.adminTok)
	req.Header.Set("User-Agent", "Yamby/2.1.0.11(Android 16)")
	req.Header.Set("Range", "bytes=0-")
	rr := httptest.NewRecorder()
	f.env.handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d", rr.Code)
	}
	if gotRange != "bytes=1048576-4194303" {
		t.Fatalf("upstream range = %q", gotRange)
	}
	body := rr.Body.Bytes()
	if len(body) != 4<<20 || !bytes.HasPrefix(body, bytes.Repeat([]byte("A"), 1<<20)) ||
		!strings.HasPrefix(string(body[1<<20:1<<20+8]), "CCCCCCCC") {
		t.Fatalf("body len = %d", len(body))
	}
}
