package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lime/internal/logbuf"
	"lime/internal/store"
)

func TestWebLogsEndpoint(t *testing.T) {
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	admin, err := env.db.CreateUser("logadmin", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(admin.ID, "test")
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(logbuf.NewHandler(slog.NewTextHandler(io.Discard, nil), logbuf.Default()))
	logger.Info("logs endpoint probe", "k", "v")

	req := httptest.NewRequest(http.MethodGet, "/web/api/logs?limit=50", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Entries []logbuf.Entry
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	var found bool
	for _, e := range out.Entries {
		if e.Message == "logs endpoint probe" && e.Attrs["k"] == "v" {
			found = true
		}
	}
	if !found {
		t.Fatalf("probe log not found: %+v", out.Entries)
	}
}

// 非管理员不能看运行日志。
func TestWebLogsRequiresAdmin(t *testing.T) {
	env, _, tok := newAuthedEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/web/api/logs", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestWebLogsExportEndpoint(t *testing.T) {
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	admin, err := env.db.CreateUser("logexport", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(admin.ID, "test")
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(logbuf.NewHandler(slog.NewTextHandler(io.Discard, nil), logbuf.Default()))
	logger.Warn("export probe", "k", "v")

	req := httptest.NewRequest(http.MethodGet, "/web/api/logs/export", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type = %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("content-disposition = %q", cd)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "LiMe 运行日志导出") || !strings.Contains(body, "export probe k=v") {
		t.Fatalf("export body missing content: %s", body)
	}
}

// 非管理员不能导出日志。
func TestWebLogsExportRequiresAdmin(t *testing.T) {
	env, _, tok := newAuthedEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/web/api/logs/export", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}
