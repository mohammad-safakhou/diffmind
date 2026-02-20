package security

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestContextFromHeadersWithConfig_JWTMode(t *testing.T) {
	cfg := AuthConfig{
		Mode:           AuthModeJWT,
		JWTHS256Secret: "test-secret",
	}
	token := signHS256JWT(t, map[string]any{
		"sub":    "alice",
		"tenant": "acme",
		"roles":  []string{"analyst", "tenant_admin"},
		"scopes": []string{"graph:read", "graph:write"},
		"exp":    time.Now().UTC().Add(5 * time.Minute).Unix(),
	}, cfg.JWTHS256Secret)

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("X-DiffMind-Attr-region", "us-east-1")
	ctx, err := ContextFromHeadersWithConfig(headers, cfg)
	if err != nil {
		t.Fatalf("unexpected jwt auth error: %v", err)
	}
	if ctx.TenantID != "acme" {
		t.Fatalf("expected tenant=acme, got %q", ctx.TenantID)
	}
	if ctx.Principal != "alice" {
		t.Fatalf("expected principal=alice, got %q", ctx.Principal)
	}
	if !ctx.HasRole("analyst") || !ctx.HasScope("graph:read") {
		t.Fatalf("expected mapped roles/scopes, got roles=%v scopes=%v", ctx.Roles, ctx.Scopes)
	}
	if ctx.Attrs["region"] != "us-east-1" {
		t.Fatalf("expected mapped attr from header, got attrs=%v", ctx.Attrs)
	}
}

func TestContextFromHeadersWithConfig_JWTModeRejectsInvalidToken(t *testing.T) {
	cfg := AuthConfig{
		Mode:           AuthModeJWT,
		JWTHS256Secret: "test-secret",
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer invalid.token.value")
	if _, err := ContextFromHeadersWithConfig(headers, cfg); err == nil {
		t.Fatalf("expected invalid token to fail auth")
	}
}

func TestContextFromHeadersWithConfig_AutoModeFallbackToHeaders(t *testing.T) {
	cfg := AuthConfig{Mode: AuthModeAuto}
	headers := http.Header{}
	headers.Set("X-DiffMind-Tenant", "default")
	headers.Set("X-DiffMind-Principal", "user")
	headers.Set("X-DiffMind-Roles", "analyst")
	headers.Set("X-DiffMind-Scopes", "graph:read")
	ctx, err := ContextFromHeadersWithConfig(headers, cfg)
	if err != nil {
		t.Fatalf("expected header fallback auth in auto mode: %v", err)
	}
	if ctx.TenantID != "default" || !ctx.HasRole("analyst") {
		t.Fatalf("unexpected context in auto header mode: %+v", ctx)
	}
}

func TestContextFromHeadersWithConfig_JWTMode_ProfileKeycloakNestedClaims(t *testing.T) {
	cfg := AuthConfig{
		Mode:           AuthModeJWT,
		Profile:        "keycloak",
		JWTHS256Secret: "test-secret",
	}
	token := signHS256JWT(t, map[string]any{
		"preferred_username": "bob",
		"tenant":             "acme",
		"realm_access": map[string]any{
			"roles": []string{"tenant_admin", "analyst"},
		},
		"scope": "graph:read graph:write",
		"exp":   time.Now().UTC().Add(5 * time.Minute).Unix(),
	}, cfg.JWTHS256Secret)
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	ctx, err := ContextFromHeadersWithConfig(headers, cfg)
	if err != nil {
		t.Fatalf("unexpected auth error: %v", err)
	}
	if ctx.Principal != "bob" || ctx.TenantID != "acme" {
		t.Fatalf("unexpected tenant/principal: %+v", ctx)
	}
	if !ctx.HasRole("tenant_admin") || !ctx.HasScope("graph:write") {
		t.Fatalf("expected nested roles and spaced scopes, got roles=%v scopes=%v", ctx.Roles, ctx.Scopes)
	}
}

func TestContextFromHeadersWithConfig_JWTMode_RS256ViaJWKS(t *testing.T) {
	resetJWTTestState(t)
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	jwksDoc := map[string]any{
		"keys": []map[string]any{
			jwkFromRSAPublicKey("k1", &priv.PublicKey),
		},
	}
	jwksJSON, err := json.Marshal(jwksDoc)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	jwksURL := "https://issuer.test/jwks"
	restore := installMockJWTHTTPGet(t, map[string]mockHTTPResponse{
		jwksURL: {status: http.StatusOK, body: string(jwksJSON)},
	}, nil)
	defer restore()

	cfg := AuthConfig{
		Mode:                AuthModeJWT,
		JWTJWKSURL:          jwksURL,
		JWTJWKSCacheSeconds: 300,
	}
	token := signRS256JWT(t, map[string]any{
		"sub":    "alice",
		"tenant": "acme",
		"roles":  []string{"analyst"},
		"scopes": []string{"graph:read"},
		"exp":    time.Now().UTC().Add(5 * time.Minute).Unix(),
	}, priv, "k1")
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	ctx, err := ContextFromHeadersWithConfig(headers, cfg)
	if err != nil {
		t.Fatalf("unexpected jwt jwks auth error: %v", err)
	}
	if ctx.TenantID != "acme" || ctx.Principal != "alice" {
		t.Fatalf("unexpected context from jwks token: %+v", ctx)
	}
}

func TestContextFromHeadersWithConfig_JWTMode_RS256ViaOIDCDiscoveryAndRotation(t *testing.T) {
	resetJWTTestState(t)
	oldKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate old rsa key: %v", err)
	}
	newKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate new rsa key: %v", err)
	}
	var rotation atomic.Int32
	issuerURL := "https://issuer-rotation.test"
	discoveryURL := issuerURL + "/.well-known/openid-configuration"
	jwksURL := issuerURL + "/jwks"
	discoveryBody := fmt.Sprintf(`{"jwks_uri":%q}`, jwksURL)
	restore := installMockJWTHTTPGet(t, map[string]mockHTTPResponse{
		discoveryURL: {status: http.StatusOK, body: discoveryBody},
	}, func(url string) (mockHTTPResponse, bool) {
		if url != jwksURL {
			return mockHTTPResponse{}, false
		}
		kid := "old"
		pub := &oldKey.PublicKey
		if rotation.Load() == 1 {
			kid = "new"
			pub = &newKey.PublicKey
		}
		doc := map[string]any{"keys": []map[string]any{jwkFromRSAPublicKey(kid, pub)}}
		data, _ := json.Marshal(doc)
		return mockHTTPResponse{status: http.StatusOK, body: string(data)}, true
	})
	defer restore()

	cfg := AuthConfig{
		Mode:                AuthModeJWT,
		JWTOIDCIssuer:       issuerURL,
		JWTJWKSCacheSeconds: 0, // force refresh to verify rotation behavior
	}

	oldToken := signRS256JWT(t, map[string]any{
		"sub":    "alice",
		"tenant": "acme",
		"roles":  []string{"analyst"},
		"exp":    time.Now().UTC().Add(5 * time.Minute).Unix(),
	}, oldKey, "old")
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+oldToken)
	if _, err := ContextFromHeadersWithConfig(headers, cfg); err != nil {
		t.Fatalf("unexpected auth error for old key token: %v", err)
	}

	rotation.Store(1)
	newToken := signRS256JWT(t, map[string]any{
		"sub":    "alice",
		"tenant": "acme",
		"roles":  []string{"analyst"},
		"exp":    time.Now().UTC().Add(5 * time.Minute).Unix(),
	}, newKey, "new")
	headers.Set("Authorization", "Bearer "+newToken)
	if _, err := ContextFromHeadersWithConfig(headers, cfg); err != nil {
		t.Fatalf("unexpected auth error for rotated key token: %v", err)
	}
}

func TestContextFromHeadersWithConfig_ProfileDefaultsCanBeOverridden(t *testing.T) {
	cfg := AuthConfig{
		Mode:           AuthModeJWT,
		Profile:        "entra",
		TenantClaim:    "tenant",
		PrincipalClaim: "sub",
		JWTHS256Secret: "test-secret",
	}
	token := signHS256JWT(t, map[string]any{
		"sub":    "alice",
		"tenant": "custom-tenant",
		"roles":  []string{"analyst"},
		"scp":    "graph:read",
		"exp":    time.Now().UTC().Add(5 * time.Minute).Unix(),
	}, cfg.JWTHS256Secret)
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	ctx, err := ContextFromHeadersWithConfig(headers, cfg)
	if err != nil {
		t.Fatalf("unexpected auth error: %v", err)
	}
	if ctx.TenantID != "custom-tenant" || ctx.Principal != "alice" {
		t.Fatalf("expected explicit claim override to win, got %+v", ctx)
	}
}

func signHS256JWT(t *testing.T, claims map[string]any, secret string) string {
	t.Helper()
	header := map[string]any{"alg": "HS256", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	h := base64.RawURLEncoding.EncodeToString(headerJSON)
	c := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := h + "." + c
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return strings.Join([]string{h, c, sig}, ".")
}

func signRS256JWT(t *testing.T, claims map[string]any, priv *rsa.PrivateKey, kid string) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "typ": "JWT"}
	if strings.TrimSpace(kid) != "" {
		header["kid"] = kid
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	h := base64.RawURLEncoding.EncodeToString(headerJSON)
	c := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := h + "." + c
	digest := sha256.Sum256([]byte(signingInput))
	sigBytes, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign rs256: %v", err)
	}
	sig := base64.RawURLEncoding.EncodeToString(sigBytes)
	return strings.Join([]string{h, c, sig}, ".")
}

func jwkFromRSAPublicKey(kid string, pub *rsa.PublicKey) map[string]any {
	e := big.NewInt(int64(pub.E)).Bytes()
	if len(e) == 0 {
		e = []byte{0}
	}
	return map[string]any{
		"kid": kid,
		"kty": "RSA",
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(e),
	}
}

type mockHTTPResponse struct {
	status int
	body   string
}

func installMockJWTHTTPGet(
	t *testing.T,
	static map[string]mockHTTPResponse,
	dynamic func(string) (mockHTTPResponse, bool),
) func() {
	t.Helper()
	orig := jwtHTTPGet
	jwtHTTPGet = func(url string) (*http.Response, error) {
		if res, ok := static[url]; ok {
			return mockHTTPResponseToHTTP(res), nil
		}
		if dynamic != nil {
			if res, ok := dynamic(url); ok {
				return mockHTTPResponseToHTTP(res), nil
			}
		}
		return nil, fmt.Errorf("unexpected url %q", url)
	}
	return func() {
		jwtHTTPGet = orig
	}
}

func mockHTTPResponseToHTTP(res mockHTTPResponse) *http.Response {
	status := res.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(res.body)),
		Header:     make(http.Header),
	}
}

func resetJWTTestState(t *testing.T) {
	t.Helper()
	jwksCacheMu.Lock()
	jwksCache = map[string]jwksCacheEntry{}
	jwksCacheMu.Unlock()
}
