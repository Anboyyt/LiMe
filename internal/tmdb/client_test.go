package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// logo 优先中文，其次英文，再次无语言版本。
func TestPickLogo(t *testing.T) {
	logos := []Image{
		{FilePath: "/en.png", ISO6391: "en"},
		{FilePath: "/none.png"},
		{FilePath: "/zh.png", ISO6391: "zh"},
	}
	if got := PickLogo(logos, "zh-CN"); got != "/zh.png" {
		t.Fatalf("zh logo = %q", got)
	}
	if got := PickLogo(logos, "ja-JP"); got != "/en.png" {
		t.Fatalf("fallback logo = %q, want en", got)
	}
	if got := PickLogo([]Image{{FilePath: "/only.png"}}, "zh-CN"); got != "/only.png" {
		t.Fatalf("only logo = %q", got)
	}
	if got := PickLogo(nil, "zh-CN"); got != "" {
		t.Fatalf("empty logos = %q", got)
	}
}

func TestImageExt(t *testing.T) {
	if imageExt("/abc.png") != ".png" || imageExt("/abc.PNG") != ".png" {
		t.Fatal("png should keep png extension")
	}
	if imageExt("/abc.svg") != ".svg" {
		t.Fatal("svg should keep svg extension")
	}
	if imageExt("/abc.jpg") != ".jpg" {
		t.Fatal("jpg should stay jpg")
	}
}

// 第一个 key 返回 429 时自动换下一个。
func TestClientRotatesKeysOn429(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("api_key")
		seen = append(seen, key)
		if key == "bad" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42}`))
	}))
	defer srv.Close()

	c := New([]string{"bad", "good"}, "zh-CN", srv.URL, srv.URL, "", nil)
	var out struct {
		ID int `json:"id"`
	}
	if err := c.get(context.Background(), "/movie/1", url.Values{}, &out); err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.ID != 42 {
		t.Fatalf("id = %d, want 42", out.ID)
	}
	if len(seen) != 2 {
		t.Fatalf("attempts = %d, want 2 (%v)", len(seen), seen)
	}
}

// 多 key 时按轮询顺序使用。
func TestClientRoundRobin(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Query().Get("api_key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	c := New([]string{"k1", "k2"}, "zh-CN", srv.URL, srv.URL, "", nil)
	var out struct {
		ID int `json:"id"`
	}
	for i := 0; i < 2; i++ {
		if err := c.get(context.Background(), "/movie/1", url.Values{}, &out); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] == seen[1] {
		t.Fatalf("keys = %v, want alternating", seen)
	}
}

// 分级按 CN/HK/TW/US... 的顺序挑第一个非空值。
func TestCertificationPriority(t *testing.T) {
	m := &Movie{ReleaseDates: ReleaseDateList{Results: []CountryRelease{
		{ISO3166: "US", ReleaseDates: []ReleaseDate{{Certification: "PG-13"}}},
		{ISO3166: "HK", ReleaseDates: []ReleaseDate{{Certification: "IIA"}}},
		{ISO3166: "CN", ReleaseDates: []ReleaseDate{{Certification: ""}}},
	}}}
	if got := m.Certification(); got != "IIA" {
		t.Fatalf("certification = %q, want IIA", got)
	}
}

func TestContentRating(t *testing.T) {
	tv := &TV{ContentRatings: RatingList{Results: []CountryRating{
		{ISO3166: "US", Rating: "TV-MA"},
		{ISO3166: "JP", Rating: "R15+"},
	}}}
	if got := tv.ContentRating(); got != "TV-MA" {
		t.Fatalf("content rating = %q, want TV-MA", got)
	}
	if (&TV{}).ContentRating() != "" {
		t.Fatal("empty ratings should return empty")
	}
}

// 电影详情一次请求同时取 credits 和 release_dates，不再额外多发请求。
func TestMovieRequestsReleaseDates(t *testing.T) {
	var q url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"title":"Test","overview":"plot","release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"R"}]}]}}`))
	}))
	defer srv.Close()

	c := New([]string{"k"}, "zh-CN", srv.URL, srv.URL, "", nil)
	m, err := c.Movie(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Get("append_to_response"); got != "credits,release_dates,images" {
		t.Fatalf("append_to_response = %q", got)
	}
	if got := q.Get("include_image_language"); got != "zh,en,null" {
		t.Fatalf("include_image_language = %q", got)
	}
	if got := m.Certification(); got != "R" {
		t.Fatalf("certification = %q, want R", got)
	}
}

// 主语言缺简介时用备用语言补空字段，已有标题不被覆盖。
func TestMovieFallbackLanguage(t *testing.T) {
	var langs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := r.URL.Query().Get("language")
		langs = append(langs, lang)
		w.Header().Set("Content-Type", "application/json")
		switch lang {
		case "zh-CN":
			_, _ = w.Write([]byte(`{"id":1,"title":"中国名","overview":""}`))
		case "zh-TW":
			_, _ = w.Write([]byte(`{"id":1,"title":"中國名","overview":"繁體簡介"}`))
		default:
			_, _ = w.Write([]byte(`{"id":1,"title":"English","overview":"plot"}`))
		}
	}))
	defer srv.Close()

	c := New([]string{"k"}, "zh-CN", srv.URL, srv.URL, "", nil)
	m, err := c.Movie(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "中国名" {
		t.Fatalf("title = %q, want 中国名", m.Title)
	}
	if m.Overview != "繁體簡介" {
		t.Fatalf("overview = %q, want 繁體簡介", m.Overview)
	}
	if len(langs) != 2 || langs[1] != "zh-TW" {
		t.Fatalf("languages = %v, want [zh-CN zh-TW]", langs)
	}
}
