package image

import (
	"errors"
	"fmt"

	"github.com/davidbyttow/govips/v2/vips"
)

var (
	ErrUnsupportedFormat = errors.New("unsupported image format")
	ErrTooManyPixels     = errors.New("too many pixels")
	ErrDecode            = errors.New("decode failed")
)

type ResizeSpec struct {
	// ShortSide は縮小後の短辺の目標ピクセル数。元画像がこれより小さければ拡大しない。
	ShortSide int
	// MaxLongSide は縮小後の長辺の上限。縦長画像で ShortSide を満たすと長辺が伸びすぎるのを抑える。
	MaxLongSide int
	// OnlyScaleDown が true なら元画像が目標より小さくても拡大しない。
	OnlyScaleDown bool
}

type OutputSpec struct {
	Quality int
}

func Startup(concurrency int) {
	vips.LoggingSettings(nil, vips.LogLevelError)
	vips.Startup(&vips.Config{
		ConcurrencyLevel: concurrency,
		MaxCacheMem:      0,
		MaxCacheSize:     0,
		MaxCacheFiles:    0,
		ReportLeaks:      false,
	})
}

func Shutdown() {
	vips.Shutdown()
}

// Resize は入力をマジックバイトで検証し、向きを反映してから等倍縮小し、
// sRGB の WebP（メタデータなし）を返す。アニメーションは 1 フレーム目だけ使う。
func Resize(data []byte, maxPixels int64, resize ResizeSpec, output OutputSpec) ([]byte, error) {
	if _, ok := DetectFormat(data); !ok {
		return nil, ErrUnsupportedFormat
	}

	img, err := vips.NewImageFromBuffer(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecode, err)
	}
	defer img.Close()

	width, height := int64(img.Width()), int64(img.Height())
	if width <= 0 || height <= 0 {
		return nil, ErrDecode
	}
	if width*height > maxPixels {
		return nil, ErrTooManyPixels
	}

	if err := img.AutoRotate(); err != nil {
		return nil, fmt.Errorf("%w: autorotate: %v", ErrDecode, err)
	}

	if scale := scaleFactor(img.Width(), img.Height(), resize); scale != 1 {
		// 拡大を許した場合も出力の画素数は入力と同じ上限に収める
		outWidth, outHeight := int64(float64(img.Width())*scale), int64(float64(img.Height())*scale)
		if outWidth*outHeight > maxPixels {
			return nil, ErrTooManyPixels
		}
		if err := img.Resize(scale, vips.KernelLanczos3); err != nil {
			return nil, fmt.Errorf("%w: resize: %v", ErrDecode, err)
		}
	}

	if img.HasICCProfile() {
		if err := img.OptimizeICCProfile(); err != nil {
			return nil, fmt.Errorf("%w: icc: %v", ErrDecode, err)
		}
	}
	if err := img.ToColorSpace(vips.InterpretationSRGB); err != nil {
		return nil, fmt.Errorf("%w: colorspace: %v", ErrDecode, err)
	}
	if err := img.RemoveMetadata(); err != nil {
		return nil, fmt.Errorf("%w: metadata: %v", ErrDecode, err)
	}

	params := vips.NewWebpExportParams()
	params.Quality = output.Quality
	params.StripMetadata = true
	params.Lossless = false
	params.ReductionEffort = 4

	out, _, err := img.ExportWebp(params)
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrDecode, err)
	}
	return out, nil
}

// scaleFactor は min(ShortSide/短辺, MaxLongSide/長辺) を返す。OnlyScaleDown なら 1 を上限にする。
func scaleFactor(width, height int, spec ResizeSpec) float64 {
	short, long := width, height
	if short > long {
		short, long = long, short
	}
	scale := float64(spec.ShortSide) / float64(short)
	if byLong := float64(spec.MaxLongSide) / float64(long); byLong < scale {
		scale = byLong
	}
	if spec.OnlyScaleDown && scale > 1 {
		return 1
	}
	return scale
}
