package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"lime/internal/config"
)

func TestRewriteHost(t *testing.T) {
	cases := []struct {
		name, raw, target, want string
	}{
		{
			name:   "host only",
			raw:    "http://192.168.1.155:16868/strm/115/drive?drive_id=5&file_id=9&file_name=a.mp4",
			target: "192.168.1.133:16868",
			want:   "http://192.168.1.133:16868/strm/115/drive?drive_id=5&file_id=9&file_name=a.mp4",
		},
		{
			name:   "target with scheme",
			raw:    "http://192.168.1.155:16868/strm/115/drive?file_id=9",
			target: "https://pan.example.com",
			want:   "https://pan.example.com/strm/115/drive?file_id=9",
		},
		{
			name:   "keep path and query order",
			raw:    "http://h:1/p?b=2&a=1",
			target: "cdn:2",
			want:   "http://cdn:2/p?b=2&a=1",
		},
	}

	for _, c := range cases {
		if got := rewriteHost(c.raw, c.target); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestIsPrivateHost(t *testing.T) {
	cases := map[string]bool{
		"192.168.1.133:886": true,
		"10.0.0.2":          true,
		"127.0.0.1:886":     true,
		"[::1]:886":         true,
		"nas":               true,
		"nas.local":         false,
		"lime.example.com":  false,
		"8.8.8.8":           false,
	}
	for host, want := range cases {
		if got := isPrivateHost(host); got != want {
			t.Errorf("isPrivateHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestRewriteTargetPicksLocalForLAN(t *testing.T) {
	h := &Handler{cfg: config.Config{
		RewriteEnabled: true,
		RewriteFrom:    "192.168.1.155:16868",
		RewriteLocal:   "192.168.1.133:16868",
		RewritePublic:  "https://pan.example.com",
	}}

	req := httptest.NewRequest("GET", "http://192.168.1.133:886/Videos/x/stream", nil)
	req.RemoteAddr = "192.168.1.20:54321"
	if got := h.rewriteTarget(req); got != "192.168.1.133:16868" {
		t.Errorf("LAN client got %q", got)
	}

	req = httptest.NewRequest("GET", "https://lime.example.com/Videos/x/stream", nil)
	req.RemoteAddr = "203.0.113.9:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := h.rewriteTarget(req); got != "https://pan.example.com" {
		t.Errorf("WAN client got %q", got)
	}
}

// PanSave 反代进来时给内网地址；公网域名直接访问时给公网地址，
// 否则客户端拿到 http://192.168.x.x 连不上。
func TestStreamBasePicksByRequestHost(t *testing.T) {
	h := &Handler{cfg: config.Config{InternalURL: "http://192.168.1.133:886"}}

	viaProxy := httptest.NewRequest("GET", "http://192.168.1.133:886/Items/x/PlaybackInfo", nil)
	if got := h.streamBase(viaProxy); got != "http://192.168.1.133:886" {
		t.Errorf("private host got %q", got)
	}

	public := httptest.NewRequest("POST", "https://lime.example.com/Items/x/PlaybackInfo", nil)
	public.Header.Set("X-Forwarded-Proto", "https")
	if got := h.streamBase(public); got != "https://lime.example.com" {
		t.Errorf("public host got %q", got)
	}

	// 没配 InternalURL 时一律用请求地址。
	h2 := &Handler{cfg: config.Config{}}
	if got := h2.streamBase(viaProxy); got != "http://192.168.1.133:886" {
		t.Errorf("no internal url got %q", got)
	}
}

func TestStreamLocationHonoursFrom(t *testing.T) {
	h := &Handler{cfg: config.Config{
		RewriteEnabled: true,
		RewriteFrom:    "192.168.1.155:16868",
		RewriteLocal:   "192.168.1.133:16868",
	}}

	req := httptest.NewRequest("GET", "http://192.168.1.133:886/Videos/x/stream", nil)
	req.RemoteAddr = "192.168.1.20:1234"

	raw := "http://192.168.1.155:16868/strm/115/drive?file_id=1"
	if got := h.streamLocation(req, raw); got != "http://192.168.1.133:16868/strm/115/drive?file_id=1" {
		t.Errorf("matching from: got %q", got)
	}

	other := "https://cdn.115.com/file/1"
	if got := h.streamLocation(req, other); got != other {
		t.Errorf("non-matching host should stay, got %q", got)
	}
}

func TestSafeLocationEncodesNonASCII(t *testing.T) {
	raw := "http://h:1/p?file_name=测试.mkv"
	got := safeLocation(raw)
	for _, r := range got {
		if r > 127 {
			t.Fatalf("still contains non-ASCII: %q", got)
		}
	}
	if !strings.Contains(got, "%E6%B5%8B") {
		t.Errorf("expected percent-encoded name, got %q", got)
	}

	ascii := "http://h:1/p?b=2&a=1"
	if safeLocation(ascii) != ascii {
		t.Errorf("ascii url should be untouched")
	}
}

func TestStreamLocationDisabled(t *testing.T) {
	h := &Handler{cfg: config.Config{RewriteEnabled: false}}
	req := httptest.NewRequest("GET", "http://192.168.1.133:886/Videos/x/stream", nil)
	raw := "http://192.168.1.155:16868/strm/115/drive?file_id=1"
	if got := h.streamLocation(req, raw); got != raw {
		t.Errorf("got %q", got)
	}
}
