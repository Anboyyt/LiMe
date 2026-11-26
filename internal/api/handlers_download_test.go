package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lime/internal/config"
	"lime/internal/dto"
	"lime/internal/store"
)

func downloadFixture(t *testing.T, mutate func(*config.Config)) (*userFixture, *store.Item) {
	t.Helper()
	f := newUserFixtureWithConfig(t, mutate)
	it := playbackItem(t, f, "dl1")
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "dlsrc", ItemID: it.ID, StrmPath: "/strm/dl1.strm",
		URL: "http://pansave.local/strm/dl1", Cloud: "115",
		Container: "mkv", FileName: "测试 第 1 集.mkv", Size: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}
	return f, it
}

func TestDownloadRedirectsToMediaURL(t *testing.T) {
	f, it := downloadFixture(t, func(c *config.Config) {
		c.DownloadMode = "redirect"
		c.ResolveRedirect = false
	})

	rr := f.do(http.MethodGet, "/Items/"+it.ID+"/Download", nil, f.bobTok)
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != "http://pansave.local/strm/dl1" {
		t.Fatalf("location = %q", got)
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "filename*=UTF-8''") {
		t.Fatalf("content-disposition = %q", cd)
	}
}

func TestDownloadRequiresAuth(t *testing.T) {
	f, it := downloadFixture(t, nil)

	rr := f.do(http.MethodGet, "/Items/"+it.ID+"/Download", nil, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestDownloadDisabledHidesCapability(t *testing.T) {
	f, it := downloadFixture(t, func(c *config.Config) {
		c.DownloadMode = "off"
	})

	rr := f.do(http.MethodGet, "/Items/"+it.ID+"/Download", nil, f.bobTok)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("download status = %d, want 403", rr.Code)
	}

	rr = f.do(http.MethodGet, "/Users/Me", nil, f.bobTok)
	var u dto.UserDto
	if err := json.Unmarshal(rr.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	if u.Policy.EnableContentDownloading {
		t.Fatal("policy still allows downloading")
	}

	rr = f.do(http.MethodGet, "/Items/"+it.ID, nil, f.bobTok)
	var item dto.BaseItemDto
	if err := json.Unmarshal(rr.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.CanDownload {
		t.Fatal("CanDownload should be false when downloads are off")
	}
}

func TestDownloadCanProxyBytes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-3" {
			t.Errorf("range = %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 0-3/8")
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("data"))
	}))
	defer upstream.Close()

	f, it := downloadFixture(t, func(c *config.Config) {
		c.DownloadMode = "proxy"
		c.ResolveRedirect = false
	})
	if err := f.env.db.UpsertMediaSource(&store.MediaSource{
		ID: "dlproxy", ItemID: it.ID, StrmPath: "/strm/dlproxy.strm",
		URL: upstream.URL + "/movie.mkv", Container: "mkv", FileName: "movie.mkv",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/Items/"+it.ID+"/Download?MediaSourceId=dlproxy", nil)
	req.Header.Set("X-Emby-Token", f.bobTok)
	req.Header.Set("Range", "bytes=0-3")
	rr := httptest.NewRecorder()
	f.env.handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "data" {
		t.Fatalf("body = %q", rr.Body.String())
	}
}
