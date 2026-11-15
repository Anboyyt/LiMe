package api

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestTelegramPhotoResizesToJPEG(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "backdrop.png")
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	for y := 0; y < 1080; y++ {
		for x := 0; x < 1920; x++ {
			img.Set(x, y, color.RGBA{R: 30, G: 120, B: 60, A: 255})
		}
	}
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	data, name, err := telegramPhoto(src)
	if err != nil {
		t.Fatal(err)
	}
	if name != "cover.jpg" {
		t.Fatalf("name = %q", name)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := decoded.Bounds(); b.Dx() != 1280 || b.Dy() != 720 {
		t.Fatalf("size = %dx%d, want 1280x720", b.Dx(), b.Dy())
	}
}
