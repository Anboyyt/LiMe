package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"lime/internal/store"
)

func TestJellyfinSeriesStatus(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"Returning Series": "Continuing",
		"In Production":    "Continuing",
		"Planned":          "Continuing",
		"Ended":            "Ended",
		"Canceled":         "Ended",
		"cancelled":        "Ended",
	}
	for in, want := range cases {
		if got := jellyfinSeriesStatus(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestSeriesStatusReturnedInDTO(t *testing.T) {
	env, user, tok := newAuthedEnv(t)

	lib, err := env.db.UpsertLibrary("TV", "/strm/tv", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "s1", LibraryID: lib.ID, ParentID: lib.ID,
		Type: store.TypeSeries, Name: "剧", SeriesStatus: "Ended",
	}); err != nil {
		t.Fatal(err)
	}

	f := &userFixture{env: env, bob: user, bobTok: tok}
	rr := f.do(http.MethodGet, "/Items/s1", nil, tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["Status"] != "Ended" {
		t.Errorf("Status = %v, want Ended", got["Status"])
	}
}
