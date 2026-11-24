package config

import (
	"os"
	"testing"
)

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestGetboolAuto(t *testing.T) {
	cases := []struct {
		raw     string
		set     bool
		enabled bool
		auto    bool
	}{
		{set: false, enabled: true, auto: true},
		{raw: "", set: true, enabled: true, auto: true},
		{raw: "auto", set: true, enabled: true, auto: true},
		{raw: "true", set: true, enabled: true, auto: false},
		{raw: "1", set: true, enabled: true, auto: false},
		{raw: "yes", set: true, enabled: true, auto: false},
		{raw: "false", set: true, enabled: false, auto: false},
		{raw: "0", set: true, enabled: false, auto: false},
		{raw: "off", set: true, enabled: false, auto: false},
		{raw: "garbage", set: true, enabled: true, auto: true},
	}
	for _, c := range cases {
		if c.set {
			t.Setenv("LIME_TEST_AUTO", c.raw)
		} else {
			unsetEnv(t, "LIME_TEST_AUTO")
		}
		enabled, auto := getboolAuto("LIME_TEST_AUTO")
		if enabled != c.enabled || auto != c.auto {
			t.Fatalf("getboolAuto(%q) = %v,%v; want %v,%v", c.raw, enabled, auto, c.enabled, c.auto)
		}
	}
}

func TestLoadPlaybackDefaultsToAuto(t *testing.T) {
	unsetEnv(t, "LIME_RESOLVE_REDIRECT")
	unsetEnv(t, "LIME_PLAYBACK_DIRECT_URL")

	cfg := Load()
	if !cfg.ResolveRedirect || !cfg.ResolveRedirectAuto {
		t.Fatalf("resolve = %v auto=%v, want auto", cfg.ResolveRedirect, cfg.ResolveRedirectAuto)
	}
	if !cfg.PlaybackDirectURL || !cfg.PlaybackDirectURLAuto {
		t.Fatalf("direct = %v auto=%v, want auto", cfg.PlaybackDirectURL, cfg.PlaybackDirectURLAuto)
	}
}

func TestLoadPlaybackExplicitValues(t *testing.T) {
	t.Setenv("LIME_RESOLVE_REDIRECT", "false")
	t.Setenv("LIME_PLAYBACK_DIRECT_URL", "true")

	cfg := Load()
	if cfg.ResolveRedirect || cfg.ResolveRedirectAuto {
		t.Fatalf("resolve = %v auto=%v, want off", cfg.ResolveRedirect, cfg.ResolveRedirectAuto)
	}
	if !cfg.PlaybackDirectURL || cfg.PlaybackDirectURLAuto {
		t.Fatalf("direct = %v auto=%v, want strict on", cfg.PlaybackDirectURL, cfg.PlaybackDirectURLAuto)
	}
}
