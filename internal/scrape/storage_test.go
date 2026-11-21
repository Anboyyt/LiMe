package scrape

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkStorageFindsOrphans(t *testing.T) {
	dir := t.TempDir()
	images := filepath.Join(dir, "images")
	if err := os.MkdirAll(images, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(images, "keep.jpg"), []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(images, "orphan.jpg"), []byte("123456"), 0o644); err != nil {
		t.Fatal(err)
	}

	refs := map[string]struct{}{"images/keep.jpg": {}}
	var st StorageStats
	if err := walkStorage(images, "images", refs, true, &st, nil); err != nil {
		t.Fatal(err)
	}
	if st.Images != 2 || st.ImageBytes != 10 || st.Orphans != 1 || st.OrphanBytes != 6 {
		t.Fatalf("stats = %+v", st)
	}
}
