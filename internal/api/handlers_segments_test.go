package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
