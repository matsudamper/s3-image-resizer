package image

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"

	"github.com/davidbyttow/govips/v2/vips"
)

func TestMain(m *testing.M) {
	Startup(1)
	code := m.Run()
	Shutdown()
	os.Exit(code)
}

func TestScaleFactor(t *testing.T) {
	spec := ResizeSpec{ShortSide: 384, MaxLongSide: 1536, OnlyScaleDown: true}
	cases := []struct {
		name          string
		width, height int
		want          float64
	}{
		{"square larger", 1000, 1000, 0.384},
		{"landscape", 4000, 3000, 0.128},
		{"portrait", 3000, 4000, 0.128},
		{"tall receipt limited by long side", 1000, 8000, 0.192},
		{"smaller than target is not upscaled", 200, 300, 1},
		{"exact short side", 384, 1000, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scaleFactor(c.width, c.height, spec)
			if got < c.want-1e-9 || got > c.want+1e-9 {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}

	t.Run("upscale when OnlyScaleDown is false", func(t *testing.T) {
		got := scaleFactor(192, 300, ResizeSpec{ShortSide: 384, MaxLongSide: 1536})
		if got < 2-1e-9 || got > 2+1e-9 {
			t.Fatalf("got %v want 2", got)
		}
	})
	t.Run("upscale is still capped by MaxLongSide", func(t *testing.T) {
		got := scaleFactor(100, 1000, ResizeSpec{ShortSide: 384, MaxLongSide: 1536})
		if got < 1.536-1e-9 || got > 1.536+1e-9 {
			t.Fatalf("got %v want 1.536", got)
		}
	})
}

func TestDetectFormat(t *testing.T) {
	cases := map[string]struct {
		data []byte
		want Format
		ok   bool
	}{
		"jpeg": {[]byte{0xFF, 0xD8, 0xFF, 0xE0}, FormatJPEG, true},
		"png":  {[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, FormatPNG, true},
		"gif":  {[]byte("GIF89a......"), FormatGIF, true},
		"webp": {[]byte("RIFF....WEBPVP8 "), FormatWebP, true},
		"heic": {append([]byte{0, 0, 0, 0x18}, []byte("ftypheic")...), FormatHEIF, true},
		"mif1": {append([]byte{0, 0, 0, 0x18}, []byte("ftypmif1")...), FormatHEIF, true},
		"heix": {append([]byte{0, 0, 0, 0x18}, []byte("ftypheix")...), "", false},
		"mp4":  {append([]byte{0, 0, 0, 0x18}, []byte("ftypisom")...), "", false},
		"svg":  {[]byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>"), "", false},
		"pdf":  {[]byte("%PDF-1.4"), "", false},
		"bmp":  {[]byte("BM......"), "", false},
		"tiff": {[]byte("II*\x00"), "", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := DetectFormat(c.data)
			if ok != c.ok || got != c.want {
				t.Fatalf("got (%q, %v) want (%q, %v)", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestResizeProducesWebpWithExpectedSize(t *testing.T) {
	src := encodePNG(t, 1200, 400)

	out, err := Resize(src, 60_000_000, ResizeSpec{ShortSide: 100, MaxLongSide: 1000, OnlyScaleDown: true}, OutputSpec{Quality: 80})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := DetectFormat(out); got != FormatWebP {
		t.Fatalf("output format %q", got)
	}

	img, err := vips.NewImageFromBuffer(out)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	if img.Width() != 300 || img.Height() != 100 {
		t.Fatalf("got %dx%d want 300x100", img.Width(), img.Height())
	}
}

func TestResizeDoesNotUpscale(t *testing.T) {
	src := encodeJPEG(t, 50, 80)
	out, err := Resize(src, 60_000_000, ResizeSpec{ShortSide: 384, MaxLongSide: 1536, OnlyScaleDown: true}, OutputSpec{Quality: 80})
	if err != nil {
		t.Fatal(err)
	}
	img, err := vips.NewImageFromBuffer(out)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	if img.Width() != 50 || img.Height() != 80 {
		t.Fatalf("got %dx%d want 50x80", img.Width(), img.Height())
	}
}

func TestResizeRejectsTooManyPixels(t *testing.T) {
	src := encodePNG(t, 200, 200)
	_, err := Resize(src, 10_000, ResizeSpec{ShortSide: 10, MaxLongSide: 10}, OutputSpec{Quality: 80})
	if !errors.Is(err, ErrTooManyPixels) {
		t.Fatalf("got %v want ErrTooManyPixels", err)
	}
}

func TestResizeRejectsUpscaleBeyondPixelLimit(t *testing.T) {
	src := encodePNG(t, 100, 100)
	_, err := Resize(src, 1_000_000, ResizeSpec{ShortSide: 2000, MaxLongSide: 2000}, OutputSpec{Quality: 80})
	if !errors.Is(err, ErrTooManyPixels) {
		t.Fatalf("got %v want ErrTooManyPixels", err)
	}
}

func TestResizeRejectsUnsupportedFormat(t *testing.T) {
	_, err := Resize([]byte("%PDF-1.4 ..."), 60_000_000, ResizeSpec{ShortSide: 10, MaxLongSide: 10}, OutputSpec{Quality: 80})
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("got %v want ErrUnsupportedFormat", err)
	}
}

func TestResizeRejectsCorruptImage(t *testing.T) {
	src := encodePNG(t, 100, 100)
	corrupt := append([]byte{}, src[:40]...)
	_, err := Resize(corrupt, 60_000_000, ResizeSpec{ShortSide: 10, MaxLongSide: 10}, OutputSpec{Quality: 80})
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("got %v want ErrDecode", err)
	}
}

func encodePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, newTestImage(width, height)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, newTestImage(width, height), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newTestImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	return img
}
