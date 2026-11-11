package api

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"lime/internal/dto"
	"lime/internal/store"
)

type sessionEntry struct {
	UserName       string `json:"UserName"`
	NowPlayingItem *struct {
		Name string `json:"Name"`
	} `json:"NowPlayingItem"`
	PlayState *struct {
		IsPaused bool `json:"IsPaused"`
	} `json:"PlayState"`
}

type wsReply struct {
	MessageType string          `json:"MessageType"`
	MessageId   string          `json:"MessageId"`
	Data        json.RawMessage `json:"Data"`
}

// sessions 解析 Sessions 消息里的数组，其它消息返回 nil。
func (r wsReply) sessions(t *testing.T) []sessionEntry {
	var out []sessionEntry
	if err := json.Unmarshal(r.Data, &out); err != nil {
		t.Fatalf("decode sessions: %v (%s)", err, r.Data)
	}
	return out
}

// one 解析 PlaybackStart/PlaybackStopped 消息里的单条会话。
func (r wsReply) one(t *testing.T) sessionEntry {
	var out sessionEntry
	if err := json.Unmarshal(r.Data, &out); err != nil {
		t.Fatalf("decode session: %v (%s)", err, r.Data)
	}
	return out
}

func TestWebSocketPlaybackNotifications(t *testing.T) {
	f := newUserFixture(t)

	lib, err := f.env.db.CreateLibrary("test", []string{os.TempDir()}, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID:        "item1",
		LibraryID: lib.ID,
		Type:      store.TypeMovie,
		Name:      "测试电影",
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(f.env.handler)
	defer srv.Close()

	conn, br := dialWS(t, srv, "/socket?api_key="+f.bobTok)
	defer conn.Close()
	reader := newWSReader(t, conn, br)

	// 订阅会话快照。
	writeMaskedText(t, conn, `{"MessageType":"SessionsStart","Data":"0,1500"}`)
	initial := reader.next("Sessions")
	if got := initial.sessions(t); len(got) != 0 {
		t.Fatalf("initial sessions = %+v, want empty", got)
	}

	// 播放开始：应同时收到 Emby 风格的 PlaybackStart 和订阅快照。
	postPlaying(t, f, "/Sessions/Playing", false)

	start := reader.next("PlaybackStart")
	if got := start.one(t); got.UserName != "bob" || got.NowPlayingItem == nil || got.NowPlayingItem.Name != "测试电影" {
		t.Fatalf("PlaybackStart = %+v", got)
	}

	snap := reader.next("Sessions")
	if got := snap.sessions(t); len(got) != 1 || got[0].PlayState == nil || got[0].PlayState.IsPaused {
		t.Fatalf("playing snapshot = %+v", got)
	}

	// 暂停：Emby 没有独立事件，靠 Sessions 快照的 IsPaused 表达。
	postPlaying(t, f, "/Sessions/Playing/Progress", true)

	snap = reader.next("Sessions")
	if got := snap.sessions(t); len(got) != 1 || got[0].PlayState == nil || !got[0].PlayState.IsPaused {
		t.Fatalf("paused snapshot = %+v", got)
	}

	// 停止：PlaybackStopped + 快照清空。
	rr := f.do(http.MethodPost, "/Sessions/Playing/Stopped", map[string]any{
		"ItemId":        "item1",
		"PlaySessionId": "sess1",
		"PositionTicks": 30_000_000,
	}, f.bobTok)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("stopped status = %d", rr.Code)
	}

	stop := reader.next("PlaybackStopped")
	if got := stop.one(t); got.UserName != "bob" || got.NowPlayingItem == nil || got.NowPlayingItem.Name != "测试电影" {
		t.Fatalf("PlaybackStopped = %+v", got)
	}

	snap = reader.next("Sessions")
	if got := snap.sessions(t); len(got) != 0 {
		t.Fatalf("stopped snapshot = %+v, want empty", got)
	}
}

// 不订阅 Sessions 的连接也应该能收到开始/停止通知，这是 Emby 的默认行为。
func TestWebSocketPlaybackNotificationsWithoutSubscription(t *testing.T) {
	f := newUserFixture(t)

	lib, err := f.env.db.CreateLibrary("test", []string{os.TempDir()}, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID:        "item1",
		LibraryID: lib.ID,
		Type:      store.TypeMovie,
		Name:      "测试电影",
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(f.env.handler)
	defer srv.Close()

	conn, br := dialWS(t, srv, "/socket?api_key="+f.bobTok)
	defer conn.Close()
	reader := newWSReader(t, conn, br)

	postPlaying(t, f, "/Sessions/Playing", false)
	if got := reader.next("PlaybackStart").one(t); got.NowPlayingItem == nil || got.NowPlayingItem.Name != "测试电影" {
		t.Fatalf("PlaybackStart = %+v", got)
	}

	rr := f.do(http.MethodPost, "/Sessions/Playing/Stopped", map[string]any{
		"ItemId":        "item1",
		"PlaySessionId": "sess1",
		"PositionTicks": 30_000_000,
	}, f.bobTok)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("stopped status = %d", rr.Code)
	}
	if got := reader.next("PlaybackStopped").one(t); got.NowPlayingItem == nil || got.NowPlayingItem.Name != "测试电影" {
		t.Fatalf("PlaybackStopped = %+v", got)
	}
}

// 暂停后会话必须还留在 /Sessions 里，面板才能显示「已暂停」。
func TestPausedSessionRemainsVisible(t *testing.T) {
	f := newUserFixture(t)
	createMovie(t, f, "item1", "测试电影")

	if rr := f.do(http.MethodPost, "/Sessions/Playing", map[string]any{
		"ItemId": "item1", "PlaySessionId": "s1", "PositionTicks": 1_000_000_000,
	}, f.bobTok); rr.Code != http.StatusNoContent {
		t.Fatalf("playing = %d", rr.Code)
	}
	if rr := f.do(http.MethodPost, "/Sessions/Playing/Progress", map[string]any{
		"ItemId": "item1", "PlaySessionId": "s1",
		"PositionTicks": 2_000_000_000, "IsPaused": true,
	}, f.bobTok); rr.Code != http.StatusNoContent {
		t.Fatalf("progress = %d", rr.Code)
	}

	rr := f.do(http.MethodGet, "/Sessions", nil, f.bobTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("sessions = %d: %s", rr.Code, rr.Body.String())
	}
	var sessions []dto.SessionInfoDto
	if err := json.Unmarshal(rr.Body.Bytes(), &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].PlayState == nil || !sessions[0].PlayState.IsPaused {
		t.Fatalf("PlayState = %+v, want paused", sessions[0].PlayState)
	}
}

// 部分客户端停止上报不带 PositionTicks，不能把已有的续播进度清零。
func TestStopWithZeroPositionKeepsResumeProgress(t *testing.T) {
	f := newUserFixture(t)
	createMovie(t, f, "item1", "测试电影")

	post := func(path string, body map[string]any) {
		if rr := f.do(http.MethodPost, path, body, f.bobTok); rr.Code != http.StatusNoContent {
			t.Fatalf("%s = %d", path, rr.Code)
		}
	}
	post("/Sessions/Playing", map[string]any{
		"ItemId": "item1", "PlaySessionId": "s1", "PositionTicks": 1_000_000_000,
	})
	post("/Sessions/Playing/Progress", map[string]any{
		"ItemId": "item1", "PlaySessionId": "s1",
		"PositionTicks": 2_000_000_000, "IsPaused": true,
	})
	post("/Sessions/Playing/Stopped", map[string]any{
		"ItemId": "item1", "PlaySessionId": "s1", "PositionTicks": 0,
	})

	states, err := f.env.db.Playstates(f.bob.ID, []string{"item1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := states["item1"].PositionTicks; got != 2_000_000_000 {
		t.Fatalf("position = %d, want 2000000000", got)
	}
}

func TestPlaybackHistoryAPI(t *testing.T) {
	f := newUserFixture(t)
	createMovie(t, f, "item1", "测试电影")

	post := func(path string, body map[string]any) {
		if rr := f.do(http.MethodPost, path, body, f.bobTok); rr.Code != http.StatusNoContent {
			t.Fatalf("%s = %d", path, rr.Code)
		}
	}
	post("/Sessions/Playing", map[string]any{
		"ItemId": "item1", "PlaySessionId": "s1", "PositionTicks": 1_000_000_000,
	})
	post("/Sessions/Playing/Progress", map[string]any{
		"ItemId": "item1", "PlaySessionId": "s1",
		"PositionTicks": 2_000_000_000, "IsPaused": true,
	})
	// 同一暂停状态重复上报不应产生第二条 pause。
	post("/Sessions/Playing/Progress", map[string]any{
		"ItemId": "item1", "PlaySessionId": "s1",
		"PositionTicks": 2_100_000_000, "IsPaused": true,
	})
	post("/Sessions/Playing/Progress", map[string]any{
		"ItemId": "item1", "PlaySessionId": "s1",
		"PositionTicks": 2_200_000_000, "IsPaused": false,
	})
	post("/Sessions/Playing/Stopped", map[string]any{
		"ItemId": "item1", "PlaySessionId": "s1", "PositionTicks": 2_300_000_000,
	})

	rr := f.do(http.MethodGet, "/web/api/playback-history", nil, f.adminTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("history = %d: %s", rr.Code, rr.Body.String())
	}
	var rows []webPlaybackHistory
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}

	want := []string{"stop", "unpause", "pause", "start"}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d (%+v), want %d", len(rows), rows, len(want))
	}
	for i, ev := range want {
		if rows[i].Event != ev {
			t.Fatalf("event[%d] = %q, want %q", i, rows[i].Event, ev)
		}
	}
	if rows[0].ItemName != "测试电影" || rows[0].UserName != "bob" {
		t.Fatalf("row = %+v", rows[0])
	}

	// 播放记录只有管理员能看。
	if rr := f.do(http.MethodGet, "/web/api/playback-history", nil, f.bobTok); rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin history = %d, want 403", rr.Code)
	}
}

func createMovie(t *testing.T, f *userFixture, id, name string) {
	t.Helper()
	lib, err := f.env.db.CreateLibrary("lib-"+id, []string{os.TempDir()}, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.db.UpsertItem(&store.Item{
		ID: id, LibraryID: lib.ID, Type: store.TypeMovie, Name: name,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWebSocketSessionsRequiresAuth(t *testing.T) {
	h := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	conn, br := dialWS(t, srv, "/socket")
	defer conn.Close()

	writeMaskedText(t, conn, `{"MessageType":"SessionsStart","Data":"0,1500"}`)

	// 未认证连接允许握手保活，但不应该收到任何会话推送。
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, payload, err := readWSFrame(br); err == nil {
		t.Fatalf("unexpected frame: %s", payload)
	}
}

func postPlaying(t *testing.T, f *userFixture, path string, paused bool) {
	t.Helper()
	rr := f.do(http.MethodPost, path, map[string]any{
		"ItemId":        "item1",
		"PlaySessionId": "sess1",
		"PositionTicks": 30_000_000,
		"IsPaused":      paused,
	}, f.bobTok)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("%s status = %d", path, rr.Code)
	}
}

// wsReader 按 MessageType 取消息，顺便缓存提前到达的其它类型，
// 因为 PlaybackStart 和 Sessions 是两条独立路径，顺序不保证。
type wsReader struct {
	t       *testing.T
	conn    net.Conn
	br      *bufio.Reader
	pending []wsReply
}

func newWSReader(t *testing.T, conn net.Conn, br *bufio.Reader) *wsReader {
	return &wsReader{t: t, conn: conn, br: br}
}

func (r *wsReader) next(want string) wsReply {
	r.t.Helper()
	for i, m := range r.pending {
		if m.MessageType == want {
			r.pending = append(r.pending[:i], r.pending[i+1:]...)
			return m
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		_ = r.conn.SetReadDeadline(deadline)
		opcode, payload, err := readWSFrame(r.br)
		if err != nil {
			r.t.Fatalf("waiting for %s: %v", want, err)
		}
		if opcode != 0x1 {
			r.t.Fatalf("opcode = %x, want text", opcode)
		}
		var reply wsReply
		if err := json.Unmarshal(payload, &reply); err != nil {
			r.t.Fatalf("decode: %v (%s)", err, payload)
		}
		if reply.MessageType == want {
			return reply
		}
		r.pending = append(r.pending, reply)
	}
}

func dialWS(t *testing.T, srv *httptest.Server, path string) (net.Conn, *bufio.Reader) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: test\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}

	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("status = %q", status)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	return conn, br
}

func writeMaskedText(t *testing.T, conn net.Conn, body string) {
	t.Helper()
	payload := []byte(body)
	if len(payload) >= 126 {
		t.Fatalf("test payload too large: %d", len(payload))
	}
	mask := [4]byte{1, 2, 3, 4}
	frame := []byte{0x81, 0x80 | byte(len(payload)), mask[0], mask[1], mask[2], mask[3]}
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}
}
