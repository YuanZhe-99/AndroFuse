package gui

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

// SaveNV12AsPNG converts a planar NV12 frame (BT.709 limited-range) to RGBA and writes it atomically to path.
//
// Notes: Called only on explicit user screenshot requests so the steady-state render path remains zero-alloc (INV-04).
func SaveNV12AsPNG(path string, w, h, yStride, uvStride int, yPlane, uvPlane []byte) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("invalid frame dimensions %dx%d", w, h)
	}
	if yStride < w {
		yStride = w
	}
	if uvStride < w {
		uvStride = w
	}
	if len(yPlane) < (h-1)*yStride+w {
		return fmt.Errorf("truncated Y plane")
	}
	uvRows := (h + 1) / 2
	if len(uvPlane) < (uvRows-1)*uvStride+w {
		return fmt.Errorf("truncated UV plane")
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		yRow := yPlane[y*yStride:]
		uvRow := uvPlane[(y/2)*uvStride:]
		for x := range w {
			yy := int(yRow[x]) - 16
			if yy < 0 {
				yy = 0
			}
			uvIdx := (x &^ 1)
			u := int(uvRow[uvIdx]) - 128
			v := int(uvRow[uvIdx+1]) - 128

			c := 298 * yy
			r := clampByte((c + 459*v + 128) >> 8)
			g := clampByte((c - 55*u - 136*v + 128) >> 8)
			b := clampByte((c + 541*u + 128) >> 8)
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	encErr := png.Encode(f, img)
	closeErr := f.Close()
	if encErr != nil {
		_ = os.Remove(tmp)
		return encErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, path)
}

func clampByte(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
