package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"lime/internal/store"
)

type fakeRealtime struct {
	on bool
	db *store.DB
}

func (f *fakeRealtime) Enabled() bool { return f.on }
func (f *fakeRealtime) SetEnabled(v bool) error {
	f.on = v
	return f.db.SetSetting("realtime_monitor", strconv.FormatBool(v))
}

func TestWebSettingsRealtimeMonitorToggle(t *testing.T) {
	m := &fakeRealtime{on: true}
	env := newTestEnvWithRealtime(t, nil, m)
	m.db = env.db

	hash, _ := store.HashPassword("pw")
	admin, err := env.db.CreateUser("admin", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(admin.ID, "test")
	if err != nil {
		t.Fatal(err)
	}

	get := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/web/api/settings", nil)
		req.Header.Set("X-Emby-Token", tok)
		rr := httptest.NewRecorder()
		env.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET settings = %d: %s", rr.Code, rr.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	if got := get()["RealtimeMonitor"]; got != true {
		t.Fatalf("initial RealtimeMonitor = %v, want true", got)
	}

	req := httptest.NewRequest(http.MethodPut, "/web/api/settings",
		bytes.NewBufferString(`{"RealtimeMonitor":false}`))
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("PUT settings = %d: %s", rr.Code, rr.Body.String())
	}
	if m.on {
		t.Fatal("monitor still enabled after toggle")
	}
	if v, err := env.db.GetSetting("realtime_monitor"); err != nil || v != "false" {
		t.Fatalf("saved setting = %q, %v", v, err)
	}
	if got := get()["RealtimeMonitor"]; got != false {
		t.Fatalf("RealtimeMonitor after toggle = %v, want false", got)
	}
}

func TestWebSettingsProbeWarmup(t *testing.T) {
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	admin, err := env.db.CreateUser("admin2", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(admin.ID, "test")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut, "/web/api/settings",
		bytes.NewBufferString(`{"ProbeWarmup":true,"ProbeScope":"all","ProbeWindow":"01:00-07:00","ProbeMaxRun":"6h"}`))
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d: %s", rr.Code, rr.Body.String())
	}

	for k, want := range map[string]string{
		"probe_scope":   "all",
		"probe_window":  "01:00-07:00",
		"probe_max_run": "6h0m0s",
		"probe_warmup":  "true",
	} {
		if got, _ := env.db.GetSetting(k); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}

	get := httptest.NewRequest(http.MethodGet, "/web/api/settings", nil)
	get.Header.Set("X-Emby-Token", tok)
	gr := httptest.NewRecorder()
	env.handler.ServeHTTP(gr, get)
	var out map[string]any
	if err := json.Unmarshal(gr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ProbeScope"] != "all" || out["ProbeWindow"] != "01:00-07:00" || out["ProbeMaxRun"] != "6h" {
		t.Fatalf("GET probe fields = %v / %v / %v", out["ProbeScope"], out["ProbeWindow"], out["ProbeMaxRun"])
	}

	// 非法范围应拒绝
	bad := httptest.NewRequest(http.MethodPut, "/web/api/settings", bytes.NewBufferString(`{"ProbeScope":"everything"}`))
	bad.Header.Set("X-Emby-Token", tok)
	br := httptest.NewRecorder()
	env.handler.ServeHTTP(br, bad)
	if br.Code != http.StatusBadRequest {
		t.Fatalf("invalid scope status = %d", br.Code)
	}
}

func TestWebBackupCreatesSnapshot(t *testing.T) {
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	admin, err := env.db.CreateUser("admin3", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(admin.ID, "test")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/web/api/backup", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("backup = %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	name, _ := out["File"].(string)
	if name == "" {
		t.Fatalf("no backup file in response: %s", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.cfg.DataDir, name)); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
}

func TestWebSettingsBackupSchedule(t *testing.T) {
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	admin, err := env.db.CreateUser("admin4", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(admin.ID, "test")
	if err != nil {
		t.Fatal(err)
	}

	put := func(body string) int {
		req := httptest.NewRequest(http.MethodPut, "/web/api/settings", bytes.NewBufferString(body))
		req.Header.Set("X-Emby-Token", tok)
		rr := httptest.NewRecorder()
		env.handler.ServeHTTP(rr, req)
		return rr.Code
	}
	if code := put(`{"BackupDailyAt":"01:40","BackupKeep":5}`); code != http.StatusNoContent {
		t.Fatalf("PUT = %d", code)
	}
	if v, _ := env.db.GetSetting("backup_daily_at"); v != "01:40" {
		t.Fatalf("backup_daily_at = %q", v)
	}
	if v, _ := env.db.GetSetting("backup_keep"); v != "5" {
		t.Fatalf("backup_keep = %q", v)
	}
	if code := put(`{"BackupDailyAt":"25:99"}`); code != http.StatusBadRequest {
		t.Fatalf("invalid time status = %d", code)
	}
	if code := put(`{"BackupKeep":0}`); code != http.StatusBadRequest {
		t.Fatalf("invalid keep status = %d", code)
	}
}
