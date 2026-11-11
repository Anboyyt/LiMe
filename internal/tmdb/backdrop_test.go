package tmdb

import "testing"

func TestPickBackdrop(t *testing.T) {
	imgs := []Image{
		{FilePath: "/small.jpg", Width: 1280, VoteAverage: 9.5},
		{FilePath: "/big.jpg", Width: 3840, VoteAverage: 5.0},
		{FilePath: "/same.jpg", Width: 3840, VoteAverage: 7.0},
		{FilePath: "", Width: 9999},
	}
	if got := PickBackdrop(imgs); got != "/same.jpg" {
		t.Fatalf("PickBackdrop = %q, want /same.jpg", got)
	}
	if got := PickBackdrop(nil); got != "" {
		t.Fatalf("空列表应返回空串，got %q", got)
	}
	if got := PickBackdrop([]Image{{Width: 100}}); got != "" {
		t.Fatalf("没有 file_path 应返回空串，got %q", got)
	}
}
