package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lime/internal/store"
)

func TestMediaSegmentsEndpoint(t *testing.T) {
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	user, err := env.db.CreateUser("seguser", hash, false)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(user.ID, "test")
	if err != nil {
		t.Fatal(err)
	}

	err = env.db.ReplaceMediaSegments("ep1", []store.MediaSegment{
		{ItemID: "ep1", Type: store.SegmentIntro, StartTicks: 16350_000000, EndTicks: 16710_000000},
		{ItemID: "ep1", Type: store.SegmentOutro, StartTicks: 165745_000000, EndTicks: 168627_200000},
	}, "chapter", "")
	if err != nil {
		t.Fatal(err)
	}

	get := func(url string) []map[string]any {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("X-Emby-Token", tok)
		rr := httptest.NewRecorder()
		env.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", url, rr.Code, rr.Body.String())
		}
		var out struct {
			Items            []map[string]any
			TotalRecordCount int
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("json: %v", err)
		}
		if out.TotalRecordCount != len(out.Items) {
			t.Fatalf("count mismatch: %d vs %d", out.TotalRecordCount, len(out.Items))
		}
		return out.Items
	}

	items := get("/MediaSegments/ep1")
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2: %+v", len(items), items)
	}
	if items[0]["Type"] != store.SegmentIntro || items[1]["Type"] != store.SegmentOutro {
		t.Fatalf("unexpected segments: %+v", items)
	}

	filtered := get("/MediaSegments/ep1?includeSegmentTypes=Intro")
	if len(filtered) != 1 || filtered[0]["Type"] != store.SegmentIntro {
		t.Fatalf("filter failed: %+v", filtered)
	}

	empty := get("/MediaSegments/missing")
	if len(empty) != 0 {
		t.Fatalf("missing item should return empty: %+v", empty)
	}
}

func TestManualSegmentsEndpoint(t *testing.T) {
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	admin, err := env.db.CreateUser("segadmin", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(admin.ID, "test")
	if err != nil {
		t.Fatal(err)
	}

	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/web/api/segments", strings.NewReader(body))
		req.Header.Set("X-Emby-Token", tok)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		env.handler.ServeHTTP(rr, req)
		return rr.Code
	}
	if code := post(`{"ItemId":"ep9","Segments":[
		{"Type":"Intro","StartTicks":780000000,"EndTicks":1680000000},
		{"Type":"Outro","StartTicks":13240000000,"EndTicks":14210000000}]}`); code != http.StatusOK {
		t.Fatalf("post status = %d", code)
	}

	req := httptest.NewRequest(http.MethodGet, "/MediaSegments/ep9", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	var out struct {
		Items []map[string]any
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 || out.Items[0]["Type"] != "Intro" {
		t.Fatalf("segments = %+v", out.Items)
	}

	// 再次标记应当整组替换，而不是追加。
	if code := post(`{"ItemId":"ep9","Segments":[
		{"Type":"Intro","StartTicks":100000000,"EndTicks":200000000}]}`); code != http.StatusOK {
		t.Fatalf("replace status = %d", code)
	}
	req = httptest.NewRequest(http.MethodGet, "/MediaSegments/ep9", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr = httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out.Items) != 1 {
		t.Fatalf("replace left %d segments", len(out.Items))
	}

	// 非管理员不能写。
	hash2, _ := store.HashPassword("pw")
	normal, _ := env.db.CreateUser("segnormal", hash2, false)
	tok2, _ := env.db.CreateToken(normal.ID, "test")
	req = httptest.NewRequest(http.MethodPost, "/web/api/segments",
		strings.NewReader(`{"ItemId":"ep9","Segments":[]}`))
	req.Header.Set("X-Emby-Token", tok2)
	rr = httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", rr.Code)
	}
}

func TestEmbyChaptersFromSegments(t *testing.T) {
	env := newTestEnv(t)
	hash, _ := store.HashPassword("pw")
	admin, err := env.db.CreateUser("chadmin", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := env.db.CreateToken(admin.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	lib, err := env.db.CreateLibrary("测试", []string{"/strm/test"}, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	it := &store.Item{ID: "ep-ch", LibraryID: lib.ID, ParentID: "s1", Type: store.TypeEpisode, Name: "E1"}
	if err := env.db.UpsertItem(it); err != nil {
		t.Fatal(err)
	}
	if err := env.db.ReplaceMediaSegments("ep-ch", []store.MediaSegment{
		{ItemID: "ep-ch", Type: store.SegmentIntro, StartTicks: 780_000000, EndTicks: 1680_000000},
		{ItemID: "ep-ch", Type: store.SegmentOutro, StartTicks: 13240_000000, EndTicks: 14210_000000},
	}, "manual", ""); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/Items/ep-ch?Fields=Chapters", nil)
	req.Header.Set("X-Emby-Token", tok)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Chapters []struct {
			StartPositionTicks int64
			MarkerType         string
		}
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Chapters) != 3 {
		t.Fatalf("chapters = %d, want 3: %s", len(out.Chapters), rr.Body.String())
	}
	if out.Chapters[0].MarkerType != "IntroStart" || out.Chapters[1].MarkerType != "IntroEnd" || out.Chapters[2].MarkerType != "CreditsStart" {
		t.Fatalf("unexpected markers: %+v", out.Chapters)
	}
	if out.Chapters[0].StartPositionTicks != 780_000000 || out.Chapters[2].StartPositionTicks != 13240_000000 {
		t.Fatalf("ticks: %+v", out.Chapters)
	}
}
