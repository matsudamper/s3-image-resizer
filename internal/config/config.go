package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr string

	OIDCIssuer   string
	OIDCAudience string

	STSEndpoint        string
	STSRoleARN         string
	STSRoleSessionName string
	STSDurationSeconds int32

	S3Endpoint        string
	S3Region          string
	S3PathStyleAccess bool

	AllowedBuckets []string

	MaxSourceBytes  int64
	MaxSourcePixels int64
	MaxConcurrency  int
	RequestTimeout  time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:         getenv("LISTEN_ADDR", ":8080"),
		OIDCIssuer:         os.Getenv("OIDC_ISSUER"),
		OIDCAudience:       os.Getenv("OIDC_AUDIENCE"),
		STSEndpoint:        os.Getenv("STS_ENDPOINT"),
		STSRoleARN:         os.Getenv("STS_ROLE_ARN"),
		STSRoleSessionName: getenv("STS_ROLE_SESSION_NAME", "s3-image-resizer"),
		S3Endpoint:         os.Getenv("S3_ENDPOINT"),
		S3Region:           getenv("S3_REGION", "us-east-1"),
	}

	var err error
	if cfg.STSDurationSeconds, err = getenvInt32("STS_DURATION_SECONDS", 900); err != nil {
		return cfg, err
	}
	if cfg.S3PathStyleAccess, err = getenvBool("S3_PATH_STYLE_ACCESS", true); err != nil {
		return cfg, err
	}
	if cfg.MaxSourceBytes, err = getenvInt64("MAX_SOURCE_BYTES", 50*1024*1024); err != nil {
		return cfg, err
	}
	if cfg.MaxSourcePixels, err = getenvInt64("MAX_SOURCE_PIXELS", 60_000_000); err != nil {
		return cfg, err
	}
	if cfg.MaxConcurrency, err = getenvInt("MAX_CONCURRENCY", 2); err != nil {
		return cfg, err
	}
	if cfg.RequestTimeout, err = getenvDuration("REQUEST_TIMEOUT", 60*time.Second); err != nil {
		return cfg, err
	}
	for _, b := range strings.Split(os.Getenv("ALLOWED_BUCKETS"), ",") {
		if b = strings.TrimSpace(b); b != "" {
			cfg.AllowedBuckets = append(cfg.AllowedBuckets, b)
		}
	}

	for name, value := range map[string]string{
		"OIDC_ISSUER":   cfg.OIDCIssuer,
		"OIDC_AUDIENCE": cfg.OIDCAudience,
		"STS_ENDPOINT":  cfg.STSEndpoint,
		"STS_ROLE_ARN":  cfg.STSRoleARN,
		"S3_ENDPOINT":   cfg.S3Endpoint,
	} {
		if value == "" {
			return cfg, fmt.Errorf("%s is required", name)
		}
	}
	if len(cfg.AllowedBuckets) == 0 {
		return cfg, fmt.Errorf("ALLOWED_BUCKETS is required")
	}
	if cfg.MaxConcurrency < 1 {
		return cfg, fmt.Errorf("MAX_CONCURRENCY must be >= 1")
	}
	return cfg, nil
}

func (c Config) BucketAllowed(bucket string) bool {
	for _, b := range c.AllowedBuckets {
		if b == bucket {
			return true
		}
	}
	return false
}

func getenv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func getenvInt(name string, fallback int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}

func getenvInt32(name string, fallback int32) (int32, error) {
	n, err := getenvInt64(name, int64(fallback))
	return int32(n), err
}

func getenvInt64(name string, fallback int64) (int64, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}

func getenvBool(name string, fallback bool) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	return b, nil
}

func getenvDuration(name string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return d, nil
}
