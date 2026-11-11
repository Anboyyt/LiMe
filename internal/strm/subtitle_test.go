package strm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindSubtitles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("movie.strm")
	write("movie.ass")
	write("movie.chs.srt")
	write("movie.en.vtt")
	write("other.ass")

	subs := FindSubtitles(filepath.Join(dir, "movie.strm"))
	if len(subs) != 3 {
		t.Fatalf("subtitles = %+v", subs)
	}
	if subs[0].Format != "ass" || subs[1].Format != "srt" || subs[2].Format != "vtt" {
		t.Fatalf("order = %+v", subs)
	}
	if subs[0].Language != "" || subs[1].Language != "chi" || subs[2].Language != "eng" {
		t.Fatalf("languages = %+v", subs)
	}
}
