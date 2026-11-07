package server

import (
	"net/http"
	"testing"

	"github.com/matsudamper/s3-image-resizer/internal/config"
)

func TestValidateKey(t *testing.T) {
	cases := map[string]bool{
		"img/2026-11/a.jpg":        true,
		"a":                        true,
		"":                         false,
		"/img/a.jpg":               false,
		"img//a.jpg":               false,
		"img/../etc/passwd":        false,
		"img/./a.jpg":              false,
		"img/a\n.jpg":              false,
		"img/a.jpg/":               false,
		string(make([]byte, 1025)): false,
	}
	for key, want := range cases {
		if got := validateKey(key) == nil; got != want {
			t.Errorf("validateKey(%q) valid=%v want %v", key, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	s := &Server{cfg: config.Config{AllowedBuckets: []string{"images"}}}

	valid := func() ResizeRequest {
		var r ResizeRequest
		r.Source = ObjectRef{Bucket: "images", Key: "img/a.jpg"}
		r.Destination = ObjectRef{Bucket: "images", Key: "img/a.thumb.webp"}
		r.Resize.ShortSide = 384
		r.Resize.MaxLongSide = 1536
		r.Output.Format = "webp"
		r.Output.Quality = 80
		return r
	}

	if err := s.validate(ptr(valid())); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	cases := map[string]func(r *ResizeRequest){
		"bucket not allowed":    func(r *ResizeRequest) { r.Source.Bucket = "other" },
		"dest bucket":           func(r *ResizeRequest) { r.Destination.Bucket = "other" },
		"same object":           func(r *ResizeRequest) { r.Destination = r.Source },
		"bad key":               func(r *ResizeRequest) { r.Destination.Key = "../x" },
		"shortSide zero":        func(r *ResizeRequest) { r.Resize.ShortSide = 0 },
		"maxLongSide too small": func(r *ResizeRequest) { r.Resize.MaxLongSide = 100 },
		"format":                func(r *ResizeRequest) { r.Output.Format = "png" },
		"quality over":          func(r *ResizeRequest) { r.Output.Quality = 101 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := valid()
			mutate(&r)
			err := s.validate(&r)
			if err == nil {
				t.Fatal("expected error")
			}
			if apiErr, ok := err.(*apiError); !ok || apiErr.status != http.StatusBadRequest {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}

	t.Run("quality defaults", func(t *testing.T) {
		r := valid()
		r.Output.Quality = 0
		if err := s.validate(&r); err != nil || r.Output.Quality != defaultQuality {
			t.Fatalf("err=%v quality=%d", err, r.Output.Quality)
		}
	})
}

func ptr(r ResizeRequest) *ResizeRequest { return &r }
