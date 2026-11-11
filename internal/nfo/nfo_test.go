package nfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleNFO = `<?xml version="1.0" encoding="UTF-8" ?>
<movie>
  <title><![CDATA[SONE-846 绝顶高潮 最强女优]]></title>
  <originaltitle><![CDATA[SONE-846 絶頂の向こう側へ]]></originaltitle>
  <plot><![CDATA[剧情简介]]></plot>
  <year>2025</year>
  <runtime>172</runtime>
  <mpaa>JP-18+</mpaa>
  <rating>9.3</rating>
  <genre>巨根</genre>
  <genre>痴女</genre>
  <genre>片商: S1 NO.1 STYLE</genre>
  <genre>发行: S1 NO.1 STYLE</genre>
  <genre>巨根</genre>
  <studio>S1 NO.1 STYLE</studio>
  <director>苺原</director>
  <actor>
    <name>瀬戸環奈</name>
    <type>Actor</type>
  </actor>
  <poster>poster.jpg</poster>
  <thumb>thumb.jpg</thumb>
  <fanart>fanart.jpg</fanart>
  <premiered>2025-09-08</premiered>
  <uniqueid type="tmdb">123456</uniqueid>
</movie>`

func TestParse(t *testing.T) {
	m, err := Parse([]byte(sampleNFO))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Title != "SONE-846 绝顶高潮 最强女优" {
		t.Fatalf("title = %q", m.Title)
	}
	if m.OriginalTitle != "SONE-846 絶頂の向こう側へ" {
		t.Fatalf("original title = %q", m.OriginalTitle)
	}
	if m.Year != 2025 || m.PremiereDate() != "2025-09-08" {
		t.Fatalf("year/date = %d %q", m.Year, m.PremiereDate())
	}
	if got := m.RuntimeTicks(); got != 172*60*ticksPerSecond {
		t.Fatalf("runtime ticks = %d", got)
	}
	if m.OfficialRating() != "JP-18+" {
		t.Fatalf("rating class = %q", m.OfficialRating())
	}
	if m.Rating != 9.3 {
		t.Fatalf("rating = %v", m.Rating)
	}
	if len(m.Genres) != 2 || m.Genres[0] != "巨根" || m.Genres[1] != "痴女" {
		t.Fatalf("genres = %v", m.Genres)
	}
	if len(m.Actors) != 1 || m.Actors[0].Name != "瀬戸環奈" || m.Actors[0].Type != "Actor" {
		t.Fatalf("actors = %+v", m.Actors)
	}
	if m.TmdbID != "123456" {
		t.Fatalf("tmdb = %q", m.TmdbID)
	}
	if m.Poster != "poster.jpg" || m.Thumb != "thumb.jpg" || m.Fanart != "fanart.jpg" {
		t.Fatalf("images = %q %q %q", m.Poster, m.Thumb, m.Fanart)
	}
	if !strings.Contains(m.GenresJSON(), "巨根") {
		t.Fatalf("genres json = %q", m.GenresJSON())
	}
	if !strings.Contains(m.PeopleJSON(), `"Name":"瀬戸環奈"`) || !strings.Contains(m.PeopleJSON(), `"Type":"Director"`) {
		t.Fatalf("people json = %q", m.PeopleJSON())
	}
}

func TestLoadAndLocalImages(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ABC-001.nfo"), []byte(sampleNFO), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"poster.jpg", "fanart.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := Load(dir, "ABC-001")
	if m == nil {
		t.Fatal("nfo not loaded")
	}
	if got := m.PosterFile(dir, "ABC-001"); got != filepath.Join(dir, "poster.jpg") {
		t.Fatalf("poster = %q", got)
	}
	if got := m.BackdropFile(dir, "ABC-001"); got != filepath.Join(dir, "fanart.jpg") {
		t.Fatalf("backdrop = %q", got)
	}
}

func TestPosterFallbackWithoutNFO(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "folder.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var m *Metadata
	if got := m.PosterFile(dir, "X"); got != filepath.Join(dir, "folder.jpg") {
		t.Fatalf("poster = %q", got)
	}
}

func TestLoadMissing(t *testing.T) {
	if m := Load(t.TempDir(), "nope"); m != nil {
		t.Fatalf("expected nil, got %+v", m)
	}
}
