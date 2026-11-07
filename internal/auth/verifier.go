package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// Verifier は OIDC issuer の JWKS で JWT の署名・iss・aud・exp を検証する。
// 検証に通った JWT はそのまま STS の WebIdentityToken として使われる。
type Verifier struct {
	issuer   string
	audience string
	jwksURL  string
	cache    *jwk.Cache
}

func NewVerifier(ctx context.Context, issuer, audience string, httpClient *http.Client) (*Verifier, error) {
	jwksURL, err := discoverJWKSURL(ctx, issuer, httpClient)
	if err != nil {
		return nil, err
	}

	cache := jwk.NewCache(ctx)
	if err := cache.Register(jwksURL, jwk.WithHTTPClient(httpClient), jwk.WithMinRefreshInterval(5*time.Minute)); err != nil {
		return nil, fmt.Errorf("register jwks: %w", err)
	}
	if _, err := cache.Refresh(ctx, jwksURL); err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}

	return &Verifier{issuer: issuer, audience: audience, jwksURL: jwksURL, cache: cache}, nil
}

func (v *Verifier) Verify(ctx context.Context, token string) error {
	keySet, err := v.cache.Get(ctx, v.jwksURL)
	if err != nil {
		return fmt.Errorf("jwks: %w", err)
	}
	_, err = jwt.Parse(
		[]byte(token),
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithAcceptableSkew(30*time.Second),
	)
	return err
}

func discoverJWKSURL(ctx context.Context, issuer string, httpClient *http.Client) (string, error) {
	// iss の比較は完全一致なので issuer は設定値のまま保ち、末尾の / は discovery URL を組むときだけ落とす
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc discovery: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc discovery: status %d", res.StatusCode)
	}

	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("oidc discovery: %w", err)
	}
	if doc.JWKSURI == "" {
		return "", fmt.Errorf("oidc discovery: jwks_uri is empty")
	}
	return doc.JWKSURI, nil
}
