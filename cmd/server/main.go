package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/matsudamper/s3-image-resizer/internal/auth"
	"github.com/matsudamper/s3-image-resizer/internal/config"
	"github.com/matsudamper/s3-image-resizer/internal/image"
	"github.com/matsudamper/s3-image-resizer/internal/server"
	"github.com/matsudamper/s3-image-resizer/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	verifier, err := auth.NewVerifier(ctx, cfg.OIDCIssuer, cfg.OIDCAudience, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		logger.Error("oidc setup failed", "error", err)
		os.Exit(1)
	}

	store := storage.NewClient(storage.Options{
		STSEndpoint:        cfg.STSEndpoint,
		STSRoleARN:         cfg.STSRoleARN,
		STSRoleSessionName: cfg.STSRoleSessionName,
		STSDurationSeconds: cfg.STSDurationSeconds,
		S3Endpoint:         cfg.S3Endpoint,
		S3Region:           cfg.S3Region,
		S3PathStyleAccess:  cfg.S3PathStyleAccess,
	})

	image.Startup(cfg.MaxConcurrency)
	defer image.Shutdown()

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.New(cfg, verifier, store, logger).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 10*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Shutdown は処理中のハンドラを待つので、終わるまで main を抜けて libvips を落とさない
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown did not finish cleanly", "error", err)
		}
	}()

	logger.Info("listening", "addr", cfg.ListenAddr)
	listenErr := httpServer.ListenAndServe()
	stop()
	<-shutdownDone
	if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
		logger.Error("server stopped", "error", listenErr)
		image.Shutdown()
		os.Exit(1)
	}
}
