package api

import (
	"strings"
	"testing"
)

func TestBuildProtocolURL(t *testing.T) {
	httpURL := "http://192.168.5.133:886/Videos/abc/stream?static=true&MediaSourceId=def"
	cases := []struct {
		key  string
		want string
	}{
		{"vlc", "vlc://" + httpURL},
		{"potplayer", "potplayer://" + httpURL},
		{"mpv", "mpv://" + httpURL},
	}
	for _, c := range cases {
		spec, ok := playerByKey(c.key)
		if !ok {
			t.Fatalf("player %s not found", c.key)
		}
		if got := buildProtocolURL(spec, httpURL); got != c.want {
			t.Fatalf("%s = %q, want %q", c.key, got, c.want)
		}
	}

	// IINA / Infuse 必须编码直链，不然 & 会把 query 截断。
	for _, key := range []string{"iina", "infuse"} {
		spec, _ := playerByKey(key)
		got := buildProtocolURL(spec, httpURL)
		if !strings.Contains(got, "static%3Dtrue%26MediaSourceId%3Ddef") {
			t.Fatalf("%s url not encoded: %s", key, got)
		}
	}
}
