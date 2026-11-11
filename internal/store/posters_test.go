package store

import "testing"

func TestTMDBImagePath(t *testing.T) {
	cases := map[string]bool{
		"images/0123456789abcdef01234567.jpg": true,
		"images/0123456789abcdef01234567.png": true,
		"images/0123456789abcdef0123456.jpg":  false, // 23 位
		"images/0123456789ABCDEF01234567.jpg": false, // 大写
		"library-images/x-custom.jpg":         false,
		"images/abc-primary-1700000000.jpg":   false, // 面板上传
		"images/0123456789abcdef01234567.gif": false, // 动图
		"":                                    false,
	}
	for rel, want := range cases {
		if got := TMDBImagePath(rel); got != want {
			t.Fatalf("TMDBImagePath(%q) = %v, want %v", rel, got, want)
		}
	}
}
