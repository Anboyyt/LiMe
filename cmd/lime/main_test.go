package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutoDetectLibraries(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"电影", "剧集", "动漫", "纪录片", "演唱会", "随便什么"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	specs := autoDetectLibraries([]string{root})
	if len(specs) != 6 {
		t.Fatalf("got %d libraries, want 6: %+v", len(specs), specs)
	}

	want := map[string]string{
		"电影":   "movies",
		"剧集":   "tvshows",
		"动漫":   "tvshows",
		"纪录片":  "tvshows",
		"演唱会":  "movies",
		"随便什么": "mixed",
	}
	for _, s := range specs {
		if want[s.Name] != s.Kind {
			t.Errorf("%s kind = %q, want %q", s.Name, s.Kind, want[s.Name])
		}
		if s.Path != filepath.Join(root, s.Name) {
			t.Errorf("%s path = %q", s.Name, s.Path)
		}
	}
}

func TestAutoDetectLibrariesSingleFlatRoot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Example 2024", "Another 2023"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	specs := autoDetectLibraries([]string{root})
	if len(specs) != 1 {
		t.Fatalf("got %d libraries, want 1: %+v", len(specs), specs)
	}
	if specs[0].Kind != "mixed" || specs[0].Path != root {
		t.Errorf("unexpected spec: %+v", specs[0])
	}
}

func TestAutoDetectLibrariesMissingRoot(t *testing.T) {
	if specs := autoDetectLibraries([]string{filepath.Join(t.TempDir(), "nope")}); len(specs) != 0 {
		t.Fatalf("want no libraries, got %+v", specs)
	}
}
