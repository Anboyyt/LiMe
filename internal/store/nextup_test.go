package store

import (
	"strconv"
	"testing"
)

func seedEpisode(t *testing.T, db *DB, id, seriesID, seasonID string, season, episode int) {
	t.Helper()
	if err := db.UpsertItem(&Item{
		ID:                id,
		LibraryID:         "lib",
		ParentID:          seasonID,
		Type:              TypeEpisode,
		Name:              "第 " + strconv.Itoa(episode) + " 集",
		SortName:          "0" + strconv.Itoa(episode),
		SeriesID:          seriesID,
		SeasonID:          seasonID,
		IndexNumber:       episode,
		ParentIndexNumber: season,
	}); err != nil {
		t.Fatal(err)
	}
}

func nextUpIDs(t *testing.T, db *DB, userID, seriesID string) []string {
	t.Helper()
	items, _, err := db.NextUp(userID, 50, 0, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

func TestNextUp(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)
	seedEpisode(t, db, "e3", "s1", "se1", 1, 3)

	if ids := nextUpIDs(t, db, "u1", ""); len(ids) != 0 {
		t.Fatalf("no progress should give empty next up, got %v", ids)
	}

	// 看完 e1 → 下一集 e2
	if err := db.SetPlayed("u1", "e1", true); err != nil {
		t.Fatal(err)
	}
	if ids := nextUpIDs(t, db, "u1", ""); len(ids) != 1 || ids[0] != "e2" {
		t.Fatalf("after watching e1: %v, want [e2]", ids)
	}

	// e2 看到一半 → 还是 e2
	if err := db.UpsertPlaystate("u1", "e2", 6000, false); err != nil {
		t.Fatal(err)
	}
	if ids := nextUpIDs(t, db, "u1", ""); len(ids) != 1 || ids[0] != "e2" {
		t.Fatalf("partial e2: %v, want [e2]", ids)
	}

	// 看完 e2 → e3
	if err := db.SetPlayed("u1", "e2", true); err != nil {
		t.Fatal(err)
	}
	if ids := nextUpIDs(t, db, "u1", ""); len(ids) != 1 || ids[0] != "e3" {
		t.Fatalf("after watching e2: %v, want [e3]", ids)
	}

	// 全看完 → 空
	if err := db.SetPlayed("u1", "e3", true); err != nil {
		t.Fatal(err)
	}
	if ids := nextUpIDs(t, db, "u1", ""); len(ids) != 0 {
		t.Fatalf("all watched: %v, want []", ids)
	}

	// 指定剧集过滤
	if ids := nextUpIDs(t, db, "u1", "other-series"); len(ids) != 0 {
		t.Fatalf("series filter: %v, want []", ids)
	}
}

func TestNextUpLimit(t *testing.T) {
	db := newTestDB(t)
	seedPlaybackFixture(t, db)
	seedEpisode(t, db, "e3", "s1", "se1", 1, 3)

	if err := db.SetPlayed("u1", "e1", true); err != nil {
		t.Fatal(err)
	}

	items, total, err := db.NextUp("u1", 1, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != "e2" {
		t.Fatalf("limit page: total=%d items=%v", total, items)
	}

	items, total, err = db.NextUp("u1", 1, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 0 {
		t.Fatalf("second page: total=%d items=%v, want empty", total, items)
	}
}
