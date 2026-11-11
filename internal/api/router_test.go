package api

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lime/internal/config"
	"lime/internal/library"
	"lime/internal/store"
)

type testEnv struct {
	handler http.Handler
	db      *store.DB
	cfg     config.Config
}

func newTestEnv(t *testing.T) *testEnv {
	return newTestEnvWithConfig(t, nil)
}

func newTestEnvWithConfig(t *testing.T, mutate func(*config.Config)) *testEnv {
	return newTestEnvWithRealtime(t, mutate, nil)
}

func newTestEnvWithRealtime(t *testing.T, mutate func(*config.Config), realtime realtimeMonitor) *testEnv {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := config.Config{
		ServerName:   "LiMe",
		Version:      config.DefaultVersion,
		DataDir:      t.TempDir(),
		LibraryRoots: []string{os.TempDir()},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	h, err := New(cfg, db, log, library.New(db, log), nil, realtime)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return &testEnv{handler: h, db: db, cfg: cfg}
}

func newTestHandler(t *testing.T) http.Handler {
	return newTestEnv(t).handler
}

func TestWebUIRoutes(t *testing.T) {
	h := newTestHandler(t)

	cases := []struct {
		path     string
		status   int
		contains string
	}{
		// 媒体界面
		{"/", http.StatusOK, "LiMe"},
		{"/app/app.js", http.StatusOK, "renderHome"},
		// 运维面板
		{"/web/", http.StatusOK, "LiMe"},
		{"/web/app.js", http.StatusOK, "handleAction"},
		{"/web/style.css", http.StatusOK, ".item-card"},
		// 运维接口必须登录
		{"/web/api/stats", http.StatusUnauthorized, ""},
		{"/web/api/scrape", http.StatusUnauthorized, ""},
		{"/Users/u1/Items", http.StatusUnauthorized, ""},
		{"/Library/VirtualFolders", http.StatusUnauthorized, ""},
		{"/Library/VirtualFolders/Query", http.StatusUnauthorized, ""},
		{"/emby/Library/VirtualFolders/Query", http.StatusUnauthorized, ""},
		{"/Library/SelectableMediaFolders", http.StatusUnauthorized, ""},
		{"/emby/Library/SelectableMediaFolders", http.StatusUnauthorized, ""},
		{"/Library/MediaFolders", http.StatusUnauthorized, ""},
		{"/Items/Counts", http.StatusUnauthorized, ""},
		{"/Items/x/Similar", http.StatusUnauthorized, ""},
		{"/Persons", http.StatusUnauthorized, ""},
		{"/Studios", http.StatusUnauthorized, ""},
		{"/MediaSegments/x", http.StatusUnauthorized, ""},
		{"/Items/x/LocalTrailers", http.StatusUnauthorized, ""},
		{"/api/danmu/x/raw", http.StatusOK, "[]"},
		{"/Users/u1/Images/Primary", http.StatusOK, ""},
		{"/nope", http.StatusNotFound, ""},
		// Emby 前缀兼容
		{"/emby/System/Info/Public", http.StatusOK, "Emby Server"},
		// 客户端地址里已带 /emby，再拼一次也要能命中
		{"/emby/emby/System/Info/Public", http.StatusOK, "Emby Server"},
		{"/emby/System/Ext/ServerDomains", http.StatusOK, "[]"},
		{"/emby/Items/Counts", http.StatusUnauthorized, ""},
		{"/emby/items/counts", http.StatusUnauthorized, ""},
		{"/emby/Items/x/Similar", http.StatusUnauthorized, ""},
	}

	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != c.status {
			t.Errorf("%s: status = %d, want %d", c.path, rr.Code, c.status)
			continue
		}
		if c.contains != "" && !strings.Contains(rr.Body.String(), c.contains) {
			t.Errorf("%s: body missing %q", c.path, c.contains)
		}
	}
}

func TestGzipStaticAndJSON(t *testing.T) {
	h := newTestHandler(t)

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	rr := get("/app/app.js")
	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("app.js encoding = %q", rr.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "renderHome") {
		t.Fatal("gzipped app.js content mismatch")
	}

	rr = get("/health")
	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("json encoding = %q", rr.Header().Get("Content-Encoding"))
	}
}

func TestRootServesApp(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "app/app.js") {
		t.Fatalf("root body is not the app shell: %q", rr.Body.String()[:80])
	}
}

func TestProductNameByClient(t *testing.T) {
	h := newTestHandler(t)

	cases := []struct {
		ua   string
		auth string
		want string
	}{
		{"", "", "Jellyfin Server"},
		{"Emby/4.8.0 (iOS)", "", "Emby Server"},
		{"PanSave/0.9.27", "", "Emby Server"},
		{"SenPlayer/1.0", `MediaBrowser Client="Emby", Device="iPad", DeviceId="x", Version="1"`, "Emby Server"},
		{"SenPlayer/1.0", `MediaBrowser Client="SenPlayer", Device="iPad", DeviceId="x", Version="1"`, "Jellyfin Server"},
	}

	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/System/Info/Public", nil)
		if c.ua != "" {
			req.Header.Set("User-Agent", c.ua)
		}
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if !strings.Contains(rr.Body.String(), c.want) {
			t.Errorf("ua=%q auth=%q: body = %s, want %q", c.ua, c.auth, rr.Body.String(), c.want)
		}
	}
}
