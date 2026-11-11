package api

import (
	"net/http"
	"testing"
)

// 客户端缓存了已删除条目的 ID 时，进度上报不应返回 404，
// 否则部分播放器会整页报「连接失败」。
func TestSessionPlayingUnknownItemReturnsNoContent(t *testing.T) {
	f := newUserFixture(t)

	rr := f.do(http.MethodPost, "/emby/Sessions/Playing", map[string]any{
		"ItemId":        "deleted-item-id",
		"PositionTicks": 0,
	}, f.bobTok)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s, want 204", rr.Code, rr.Body.String())
	}
}
