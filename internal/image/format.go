package image

import "bytes"

type Format string

const (
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
	FormatGIF  Format = "gif"
	FormatWebP Format = "webp"
	FormatHEIF Format = "heif"
)

// heifBrands は ISO BMFF の ftyp major brand のうち HEIF/HEIC として受け付けるもの。
// govips v2 の判定（heic / mif1 / msf1）と揃える。それ以外の brand はロードできず 422 になるので受け付けない。
var heifBrands = map[string]struct{}{
	"heic": {}, "mif1": {}, "msf1": {},
}

// DetectFormat は拡張子や Content-Type ではなくマジックバイトで形式を判定する。
// 対応外なら ok=false を返す。
func DetectFormat(data []byte) (Format, bool) {
	switch {
	case len(data) >= 3 && bytes.Equal(data[:3], []byte{0xFF, 0xD8, 0xFF}):
		return FormatJPEG, true
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return FormatPNG, true
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		return FormatGIF, true
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return FormatWebP, true
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		if _, ok := heifBrands[string(data[8:12])]; ok {
			return FormatHEIF, true
		}
	}
	return "", false
}
