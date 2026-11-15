package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseEvents(t *testing.T) {
	cases := []struct {
		raw  string
		want map[string]bool
	}{
		{"", map[string]bool{"start": true, "pause": true, "resume": true, "stop": true, "stall": true, "health": true}},
		{"all", map[string]bool{"start": true, "pause": true, "resume": true, "stop": true, "stall": true, "health": true}},
		{"none", map[string]bool{}},
		{"start,stop", map[string]bool{"start": true, "stop": true}},
		{" start , PAUSE ", map[string]bool{"start": true, "pause": true}},
		{"bogus", map[string]bool{}},
	}
	for _, c := range cases {
		got := ParseEvents(c.raw)
		if len(got) != len(c.want) {
			t.Fatalf("ParseEvents(%q) = %v", c.raw, got)
		}
		for k := range c.want {
			if !got[k] {
				t.Fatalf("ParseEvents(%q) 缺少 %s", c.raw, k)
			}
		}
	}
}

func TestEventsString(t *testing.T) {
	if got := EventsString(ParseEvents("")); got != "" {
		t.Fatalf("全开应返回空串，got %q", got)
	}
	if got := EventsString(ParseEvents("none")); got != "none" {
		t.Fatalf("全关应返回 none，got %q", got)
	}
	if got := EventsString(ParseEvents("pause,start")); got != "start,pause" {
		t.Fatalf("事件顺序不稳定：%q", got)
	}
}

func TestEventEnabled(t *testing.T) {
	tg := &Telegram{log: slog.Default()}
	tg.SetConfig(Config{Token: "t", ChatID: "c", Enabled: true, Events: ParseEvents("start")})
	if !tg.EventEnabled("start") || tg.EventEnabled("pause") {
		t.Fatal("事件过滤不符合配置")
	}
	tg.SetConfig(Config{Token: "t", ChatID: "c", Enabled: false, Events: ParseEvents("all")})
	if tg.EventEnabled("start") {
		t.Fatal("总开关关闭时不应推送")
	}
	tg.SetConfig(Config{Token: "", ChatID: "c", Enabled: true, Events: ParseEvents("all")})
	if tg.EventEnabled("start") {
		t.Fatal("缺 token 时不应推送")
	}
}

func TestSendNow(t *testing.T) {
	var gotPath, gotText, gotChat string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			ChatID    string `json:"chat_id"`
			Text      string `json:"text"`
			ParseMode string `json:"parse_mode"`
		}
		_ = json.Unmarshal(body, &payload)
		gotChat = payload.ChatID
		gotText = payload.Text
		if payload.ParseMode != "HTML" {
			t.Errorf("parse_mode = %q", payload.ParseMode)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tg := New(Config{Token: "123:abc", ChatID: "-100", Enabled: true, Events: ParseEvents("all")}, slog.Default())
	tg.mu.Lock()
	tg.apiBase = srv.URL
	tg.client = srv.Client()
	tg.mu.Unlock()

	if err := tg.SendNow(context.Background(), "<b>测试</b>"); err != nil {
		t.Fatalf("SendNow: %v", err)
	}
	if gotPath != "/bot123:abc/sendMessage" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotChat != "-100" || gotText != "<b>测试</b>" {
		t.Fatalf("payload chat=%q text=%q", gotChat, gotText)
	}
}

func TestSendPhoto(t *testing.T) {
	var gotPath, gotCaption, gotChat, gotFile string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			t.Errorf("multipart: %v", err)
		}
		gotCaption = r.FormValue("caption")
		gotChat = r.FormValue("chat_id")
		f, _, err := r.FormFile("photo")
		if err == nil {
			b, _ := io.ReadAll(f)
			gotFile = string(b)
			f.Close()
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tg := New(Config{Token: "123:abc", ChatID: "-100", Enabled: true, Events: ParseEvents("all")}, slog.Default())
	tg.mu.Lock()
	tg.apiBase = srv.URL
	tg.client = srv.Client()
	tg.mu.Unlock()

	if err := tg.sendPhoto(context.Background(), "<b>播放</b>", []byte("JPEG"), "cover.jpg"); err != nil {
		t.Fatalf("sendPhoto: %v", err)
	}
	if gotPath != "/bot123:abc/sendPhoto" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotChat != "-100" || gotCaption != "<b>播放</b>" || gotFile != "JPEG" {
		t.Fatalf("chat=%q caption=%q file=%q", gotChat, gotCaption, gotFile)
	}
}

func TestSendErrorOmitsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	tg := New(Config{Token: "SECRET-TOKEN", ChatID: "-100", Enabled: true, Events: ParseEvents("all")}, slog.Default())
	tg.mu.Lock()
	tg.apiBase = srv.URL
	tg.client = srv.Client()
	tg.mu.Unlock()

	err := tg.SendNow(context.Background(), "x")
	if err == nil {
		t.Fatal("应返回错误")
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("错误信息泄漏 token: %v", err)
	}
}
