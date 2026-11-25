package gui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveNV12AsPNG(t *testing.T) {
	const w, h = 16, 12
	yPlane := make([]byte, w*h)
	for i := range yPlane {
		yPlane[i] = 128
	}
	uvPlane := make([]byte, w*(h/2))
	for i := 0; i < len(uvPlane); i += 2 {
		uvPlane[i] = 128
		uvPlane[i+1] = 128
	}

	outPath := filepath.Join(t.TempDir(), "shots", "frame.png")
	if err := SaveNV12AsPNG(outPath, w, h, w, w, yPlane, uvPlane); err != nil {
		t.Fatalf("SaveNV12AsPNG: %v", err)
	}
	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open output PNG: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("unexpected PNG bounds: %v", img.Bounds())
	}
}
