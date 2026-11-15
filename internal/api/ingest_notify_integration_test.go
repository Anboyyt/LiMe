package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lime/internal/store"
)

// 验证 worker 真的会发送、并在发送成功后标记片源，避免重启后重复通知。
func TestProcessIngestNotificationsSendsAndMarks(t *testing.T) {
	env := newTestEnv(t)
	lib, err := env.db.UpsertLibrary("综艺", "/strm/综艺", "tvshows")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "s1", LibraryID: lib.ID, ParentID: lib.ID, Type: store.TypeSeries,
		Name: "测试剧", SortName: "测试剧", ProductionYear: 2025, TmdbID: "123",
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.db.UpsertItem(&store.Item{
		ID: "se1", LibraryID: lib.ID, ParentID: "s1", Type: store.TypeSeason,
		Name: "第 2 季", SortName: "Season 02", SeriesID: "s1", IndexNumber: 2,
	}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 3} {
		id := "e" + string(rune('0'+n))
		if err := env.db.UpsertItem(&store.Item{
			ID: id, LibraryID: lib.ID, ParentID: "se1", Type: store.TypeEpisode,
			Name:     "第 " + string(rune('0'+n)) + " 集",
			SeriesID: "s1", SeasonID: "se1", ParentIndexNumber: 2, IndexNumber: n,
		}); err != nil {
			t.Fatal(err)
		}
		if err := env.db.UpsertMediaSource(&store.MediaSource{
			ID: "src" + string(rune('0'+n)), ItemID: id,
			StrmPath: "/strm/综艺/华语综艺/测试剧/S02E0" + string(rune('0'+n)) + ".strm",
			URL:      "http://example/" + id, Cloud: "115", Size: 1 << 30,
			FileName: "测试剧.2025.S02E0" + string(rune('0'+n)) + ".1080p.WEB-DL.SDR.25fps.MyTVSuper.H.264-ADWeb.mp4",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 让片源超过合并延迟。
	if _, err := env.db.Exec(
		`UPDATE media_sources SET ingested_at = ? WHERE ingested_at > 0`,
		time.Now().Add(-time.Minute).Unix(),
	); err != nil {
		t.Fatal(err)
	}

	for k, v := range map[string]string{
		"ingest_tg_enabled":   "true",
		"ingest_tg_mode":      "custom",
		"ingest_tg_bot_token": "123:test",
		"ingest_tg_chat_id":   "-1001",
		"ingest_tg_delay":     "10s",
	} {
		if err := env.db.SetSetting(k, v); err != nil {
			t.Fatal(err)
		}
	}

	var gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]string
		_ = json.Unmarshal(body, &p)
		gotText = p["text"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	env.api.ingestNotifier.SetAPIBaseForTest(srv.URL)
	env.api.ingestNotifier.SetConfig(env.api.ingestTGConfig())
	env.api.processIngestNotifications()

	for _, want := range []string{"测试剧 (2025) S02 E01-E03", "文件：3个", "本季：S02 共3集"} {
		if !strings.Contains(gotText, want) {
			t.Fatalf("message missing %q: %q", want, gotText)
		}
	}
	if n, err := env.db.CountPendingIngest(time.Now().Unix() + 5); err != nil || n != 0 {
		t.Fatalf("pending after send = %d, %v; want 0", n, err)
	}
}
