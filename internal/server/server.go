package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/matsudamper/s3-image-resizer/internal/auth"
	"github.com/matsudamper/s3-image-resizer/internal/config"
	"github.com/matsudamper/s3-image-resizer/internal/image"
	"github.com/matsudamper/s3-image-resizer/internal/storage"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/matsudamper/s3-image-resizer/internal/server")

const (
	maxRequestBodyBytes = 16 * 1024
	maxKeyLength        = 1024
	defaultQuality      = 80
)

type ObjectRef struct {
	Bucket string `json:"bucket"`
	Key    string `json:"key"`
}

type ResizeRequest struct {
	Source      ObjectRef `json:"source"`
	Destination ObjectRef `json:"destination"`
	Resize      struct {
		ShortSide     int   `json:"shortSide"`
		MaxLongSide   int   `json:"maxLongSide"`
		OnlyScaleDown *bool `json:"onlyScaleDown"`
	} `json:"resize"`
	Output struct {
		Format  string `json:"format"`
		Quality int    `json:"quality"`
	} `json:"output"`
}

type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.code + ": " + e.message }

func newAPIError(status int, code, message string) *apiError {
	return &apiError{status: status, code: code, message: message}
}

type Server struct {
	cfg      config.Config
	verifier *auth.Verifier
	storage  *storage.Client
	sem      chan struct{}
	logger   *slog.Logger
}

func New(cfg config.Config, verifier *auth.Verifier, store *storage.Client, logger *slog.Logger) *Server {
	return &Server{
		cfg:      cfg,
		verifier: verifier,
		storage:  store,
		sem:      make(chan struct{}, cfg.MaxConcurrency),
		logger:   logger,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/resize", s.handleResize)
	return mux
}

func (s *Server) handleResize(w http.ResponseWriter, r *http.Request) {
	if err := s.resize(r); err != nil {
		var apiErr *apiError
		if !errors.As(err, &apiErr) {
			apiErr = newAPIError(http.StatusInternalServerError, "INTERNAL_ERROR", "internal error")
		}
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("resize.error", apiErr.code))
		if apiErr.status >= 500 {
			span.SetStatus(codes.Error, apiErr.code)
		}
		if apiErr.status >= 500 {
			s.logger.Error("resize failed", "error", err)
		} else {
			s.logger.Info("resize rejected", "code", apiErr.code, "message", apiErr.message)
		}
		writeError(w, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resize(r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()

	token, err := bearerToken(r)
	if err != nil {
		return err
	}
	if err := s.verifier.Verify(ctx, token); err != nil {
		return newAPIError(http.StatusUnauthorized, "INVALID_TOKEN", "token verification failed")
	}

	var req ResizeRequest
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxRequestBodyBytes)).Decode(&req); err != nil {
		return newAPIError(http.StatusBadRequest, "INVALID_REQUEST", "malformed json: "+err.Error())
	}
	if err := s.validate(&req); err != nil {
		return err
	}

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return newAPIError(http.StatusServiceUnavailable, "BUSY", "too many concurrent requests")
	}

	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("resize.source.bucket", req.Source.Bucket),
		attribute.String("resize.source.key", req.Source.Key),
		attribute.String("resize.destination.key", req.Destination.Key),
	)

	session, err := s.assumeRole(ctx, token)
	if err != nil {
		return newAPIError(http.StatusBadGateway, "STORAGE_ERROR", err.Error())
	}

	source, err := s.getSource(ctx, session, req)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return newAPIError(http.StatusNotFound, "SOURCE_NOT_FOUND", "source object not found")
	case errors.Is(err, storage.ErrTooLarge):
		return newAPIError(http.StatusRequestEntityTooLarge, "TOO_LARGE", "source object exceeds size limit")
	case err != nil:
		return newAPIError(http.StatusBadGateway, "STORAGE_ERROR", err.Error())
	}

	output, err := s.convert(ctx, source, req)
	switch {
	case errors.Is(err, image.ErrUnsupportedFormat):
		return newAPIError(http.StatusUnsupportedMediaType, "UNSUPPORTED_FORMAT", "source format is not supported")
	case errors.Is(err, image.ErrTooManyPixels):
		return newAPIError(http.StatusRequestEntityTooLarge, "TOO_LARGE", "source resolution exceeds limit")
	case errors.Is(err, image.ErrDecode):
		return newAPIError(http.StatusUnprocessableEntity, "DECODE_FAILED", err.Error())
	case err != nil:
		return err
	}

	if err := s.putDestination(ctx, session, req, output); err != nil {
		return newAPIError(http.StatusBadGateway, "STORAGE_ERROR", err.Error())
	}
	return nil
}

func (s *Server) assumeRole(ctx context.Context, token string) (*storage.Session, error) {
	ctx, span := tracer.Start(ctx, "sts.assume_role_with_web_identity")
	defer span.End()
	session, err := s.storage.AssumeWithWebIdentity(ctx, token)
	recordError(span, err)
	return session, err
}

func (s *Server) getSource(ctx context.Context, session *storage.Session, req ResizeRequest) ([]byte, error) {
	ctx, span := tracer.Start(ctx, "s3.get_object")
	defer span.End()
	source, err := session.Get(ctx, req.Source.Bucket, req.Source.Key, s.cfg.MaxSourceBytes)
	recordError(span, err)
	span.SetAttributes(attribute.Int("resize.source.bytes", len(source)))
	return source, err
}

func (s *Server) convert(ctx context.Context, source []byte, req ResizeRequest) ([]byte, error) {
	_, span := tracer.Start(ctx, "image.resize")
	defer span.End()
	output, err := image.Resize(
		source,
		s.cfg.MaxSourcePixels,
		image.ResizeSpec{
			ShortSide:     req.Resize.ShortSide,
			MaxLongSide:   req.Resize.MaxLongSide,
			OnlyScaleDown: *req.Resize.OnlyScaleDown,
		},
		image.OutputSpec{Quality: req.Output.Quality},
	)
	recordError(span, err)
	span.SetAttributes(attribute.Int("resize.output.bytes", len(output)))
	return output, err
}

func (s *Server) putDestination(ctx context.Context, session *storage.Session, req ResizeRequest, output []byte) error {
	ctx, span := tracer.Start(ctx, "s3.put_object")
	defer span.End()
	err := session.Put(ctx, req.Destination.Bucket, req.Destination.Key, "image/webp", output)
	recordError(span, err)
	return err
}

func recordError(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func (s *Server) validate(req *ResizeRequest) error {
	for name, ref := range map[string]ObjectRef{"source": req.Source, "destination": req.Destination} {
		if ref.Bucket == "" {
			return newAPIError(http.StatusBadRequest, "INVALID_REQUEST", name+".bucket is required")
		}
		if err := validateKey(ref.Key); err != nil {
			return newAPIError(http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("%s.key: %v", name, err))
		}
	}
	if req.Source == req.Destination {
		return newAPIError(http.StatusBadRequest, "INVALID_REQUEST", "source and destination must differ")
	}
	if req.Resize.ShortSide < 1 {
		return newAPIError(http.StatusBadRequest, "INVALID_REQUEST", "resize.shortSide must be >= 1")
	}
	if req.Resize.MaxLongSide < req.Resize.ShortSide {
		return newAPIError(http.StatusBadRequest, "INVALID_REQUEST", "resize.maxLongSide must be >= resize.shortSide")
	}
	if req.Resize.OnlyScaleDown == nil {
		onlyScaleDown := true
		req.Resize.OnlyScaleDown = &onlyScaleDown
	}
	if req.Output.Format != "webp" {
		return newAPIError(http.StatusBadRequest, "INVALID_REQUEST", "output.format must be \"webp\"")
	}
	if req.Output.Quality == 0 {
		req.Output.Quality = defaultQuality
	}
	if req.Output.Quality < 1 || req.Output.Quality > 100 {
		return newAPIError(http.StatusBadRequest, "INVALID_REQUEST", "output.quality must be 1..100")
	}
	return nil
}

func validateKey(key string) error {
	switch {
	case key == "":
		return errors.New("empty")
	case len(key) > maxKeyLength:
		return errors.New("too long")
	case strings.HasPrefix(key, "/"):
		return errors.New("must not start with /")
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("contains empty, . or .. path segment")
		}
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return errors.New("contains control character")
		}
	}
	return nil
}

func bearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || len(header) == len(prefix) {
		return "", newAPIError(http.StatusUnauthorized, "INVALID_TOKEN", "missing bearer token")
	}
	return header[len(prefix):], nil
}

func writeError(w http.ResponseWriter, err *apiError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   err.code,
		"message": err.message,
	})
}
