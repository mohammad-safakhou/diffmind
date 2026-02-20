package security

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func contextFromJWTHeader(h http.Header, cfg AuthConfig) (Context, error) {
	authz := strings.TrimSpace(h.Get("Authorization"))
	if authz == "" {
		return Context{}, errors.New("missing Authorization header")
	}
	parts := strings.SplitN(authz, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "bearer") {
		return Context{}, errors.New("invalid Authorization header")
	}
	claims, err := decodeAndVerifyJWT(strings.TrimSpace(parts[1]), cfg)
	if err != nil {
		return Context{}, err
	}
	ctx, err := contextFromJWTClaims(claims, cfg)
	if err != nil {
		return Context{}, err
	}
	for k, vals := range h {
		if len(vals) == 0 {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(k))
		if !strings.HasPrefix(lower, "x-diffmind-attr-") {
			continue
		}
		attrKey := strings.TrimPrefix(lower, "x-diffmind-attr-")
		if _, exists := ctx.Attrs[attrKey]; !exists {
			ctx.Attrs[attrKey] = strings.TrimSpace(vals[0])
		}
	}
	return ctx, nil
}

func decodeAndVerifyJWT(token string, cfg AuthConfig) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid jwt token format")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode jwt header: %w", err)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode jwt payload: %w", err)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode jwt signature: %w", err)
	}

	header := map[string]any{}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("decode jwt header json: %w", err)
	}
	alg := strings.ToUpper(strings.TrimSpace(anyToString(header["alg"])))
	if alg == "" {
		return nil, errors.New("missing jwt alg")
	}
	signingInput := parts[0] + "." + parts[1]
	switch alg {
	case "HS256":
		secret := strings.TrimSpace(cfg.JWTHS256Secret)
		if secret == "" {
			return nil, errors.New("missing DIFFMIND_AUTH_JWT_HS256_SECRET")
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(signingInput))
		expected := mac.Sum(nil)
		if !hmac.Equal(expected, sigBytes) {
			return nil, errors.New("jwt signature verification failed")
		}
	case "RS256":
		pubKey, err := loadRSAPublicKey(cfg, header)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256([]byte(signingInput))
		if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash[:], sigBytes); err != nil {
			return nil, errors.New("jwt signature verification failed")
		}
	default:
		return nil, fmt.Errorf("unsupported jwt alg %q", alg)
	}

	claims := map[string]any{}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("decode jwt claims json: %w", err)
	}
	if err := validateJWTTimeClaims(claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func loadRSAPublicKey(cfg AuthConfig, header map[string]any) (*rsa.PublicKey, error) {
	if key, err := loadRSAPublicKeyFromJWKS(cfg, header); err == nil && key != nil {
		return key, nil
	}
	pemData := strings.TrimSpace(cfg.JWTRS256PublicKeyPEM)
	if pemData == "" {
		path := strings.TrimSpace(cfg.JWTRS256PublicKeyPath)
		if path == "" {
			return nil, errors.New("missing RS256 public key configuration")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read jwt rs256 public key: %w", err)
		}
		pemData = string(data)
	}
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, errors.New("invalid RS256 public key pem")
	}
	keyAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		pub, certErr := x509.ParseCertificate(block.Bytes)
		if certErr == nil {
			if rsaPub, ok := pub.PublicKey.(*rsa.PublicKey); ok {
				return rsaPub, nil
			}
		}
		return nil, fmt.Errorf("parse RS256 public key: %w", err)
	}
	pubKey, ok := keyAny.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("RS256 public key is not RSA")
	}
	return pubKey, nil
}

type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwkSet struct {
	Keys []jwkKey `json:"keys"`
}

type oidcDiscovery struct {
	JWKSURI string `json:"jwks_uri"`
}

type jwksCacheEntry struct {
	expiresAt time.Time
	set       jwkSet
}

var (
	jwksCacheMu sync.RWMutex
	jwksCache   = map[string]jwksCacheEntry{}
	jwtHTTPGet  = http.Get
)

func loadRSAPublicKeyFromJWKS(cfg AuthConfig, header map[string]any) (*rsa.PublicKey, error) {
	jwksURL, err := resolveJWKSURL(cfg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(jwksURL) == "" {
		return nil, nil
	}
	set, err := fetchJWKSSet(jwksURL, cfg.JWTJWKSCacheSeconds)
	if err != nil {
		return nil, fmt.Errorf("load jwks set: %w", err)
	}
	kid := strings.TrimSpace(anyToString(header["kid"]))
	return pickRSAPublicKeyFromJWKSet(set, kid)
}

func resolveJWKSURL(cfg AuthConfig) (string, error) {
	if explicit := strings.TrimSpace(cfg.JWTJWKSURL); explicit != "" {
		return explicit, nil
	}
	issuer := strings.TrimSpace(cfg.JWTOIDCIssuer)
	if issuer == "" {
		return "", nil
	}
	issuer = strings.TrimRight(issuer, "/")
	discoveryURL := issuer + "/.well-known/openid-configuration"
	resp, err := jwtHTTPGet(discoveryURL)
	if err != nil {
		return "", fmt.Errorf("fetch oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("oidc discovery status=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read oidc discovery: %w", err)
	}
	var discovery oidcDiscovery
	if err := json.Unmarshal(body, &discovery); err != nil {
		return "", fmt.Errorf("decode oidc discovery: %w", err)
	}
	return strings.TrimSpace(discovery.JWKSURI), nil
}

func fetchJWKSSet(jwksURL string, cacheSeconds int) (jwkSet, error) {
	now := time.Now().UTC()
	if cacheSeconds < 0 {
		cacheSeconds = 0
	}
	jwksCacheMu.RLock()
	entry, exists := jwksCache[jwksURL]
	jwksCacheMu.RUnlock()
	if exists && now.Before(entry.expiresAt) {
		return entry.set, nil
	}
	resp, err := jwtHTTPGet(jwksURL)
	if err != nil {
		return jwkSet{}, fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return jwkSet{}, fmt.Errorf("jwks status=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return jwkSet{}, fmt.Errorf("read jwks: %w", err)
	}
	var set jwkSet
	if err := json.Unmarshal(body, &set); err != nil {
		return jwkSet{}, fmt.Errorf("decode jwks: %w", err)
	}
	jwksCacheMu.Lock()
	jwksCache[jwksURL] = jwksCacheEntry{
		expiresAt: now.Add(time.Duration(cacheSeconds) * time.Second),
		set:       set,
	}
	jwksCacheMu.Unlock()
	return set, nil
}

func pickRSAPublicKeyFromJWKSet(set jwkSet, kid string) (*rsa.PublicKey, error) {
	candidates := []jwkKey{}
	for _, key := range set.Keys {
		if !strings.EqualFold(strings.TrimSpace(key.Kty), "RSA") {
			continue
		}
		if strings.TrimSpace(key.N) == "" || strings.TrimSpace(key.E) == "" {
			continue
		}
		if kid != "" && strings.TrimSpace(key.Kid) != kid {
			continue
		}
		candidates = append(candidates, key)
	}
	if kid == "" && len(candidates) == 0 {
		// No kid in header: fallback to all RSA keys in set.
		for _, key := range set.Keys {
			if strings.EqualFold(strings.TrimSpace(key.Kty), "RSA") && strings.TrimSpace(key.N) != "" && strings.TrimSpace(key.E) != "" {
				candidates = append(candidates, key)
			}
		}
	}
	if len(candidates) == 0 {
		if kid != "" {
			return nil, fmt.Errorf("jwks key not found for kid=%q", kid)
		}
		return nil, errors.New("jwks contains no usable RSA keys")
	}
	if kid == "" && len(candidates) > 1 {
		return nil, errors.New("jwt header missing kid with multiple jwks rsa keys")
	}
	key := candidates[0]
	nBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(key.N))
	if err != nil {
		return nil, fmt.Errorf("decode jwk modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(key.E))
	if err != nil {
		return nil, fmt.Errorf("decode jwk exponent: %w", err)
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}
	if e <= 0 {
		return nil, errors.New("invalid jwk exponent")
	}
	pub := &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: e,
	}
	return pub, nil
}

func validateJWTTimeClaims(claims map[string]any) error {
	now := time.Now().UTC().Unix()
	if exp, ok := parseNumericClaim(claims["exp"]); ok && now >= exp {
		return errors.New("jwt token expired")
	}
	if nbf, ok := parseNumericClaim(claims["nbf"]); ok && now < nbf {
		return errors.New("jwt token not yet valid")
	}
	return nil
}

func parseNumericClaim(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return int64(x), true
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func contextFromJWTClaims(claims map[string]any, cfg AuthConfig) (Context, error) {
	tenantClaim := firstNonEmpty(cfg.TenantClaim, "tenant")
	principalClaim := firstNonEmpty(cfg.PrincipalClaim, "sub")
	rolesClaim := firstNonEmpty(cfg.RolesClaim, "roles")
	scopesClaim := firstNonEmpty(cfg.ScopesClaim, "scopes")
	attrsClaim := firstNonEmpty(cfg.AttrsClaim, "attrs")

	tenantID := strings.TrimSpace(anyToString(claimByPath(claims, tenantClaim)))
	if tenantID == "" {
		tenantID = strings.TrimSpace(anyToString(claimByPath(claims, "tenant_id")))
	}
	if tenantID == "" {
		tenantID = strings.TrimSpace(anyToString(claimByPath(claims, "tid")))
	}
	principal := strings.TrimSpace(anyToString(claimByPath(claims, principalClaim)))
	if principal == "" {
		principal = strings.TrimSpace(anyToString(claimByPath(claims, "principal")))
	}
	if tenantID == "" {
		return Context{}, errors.New("missing tenant claim in jwt")
	}
	if principal == "" {
		return Context{}, errors.New("missing principal claim in jwt")
	}

	roles := claimToStringSlice(claimByPath(claims, rolesClaim), ",")
	if len(roles) == 0 {
		roles = claimToStringSlice(claimByPath(claims, "role"), ",")
	}
	if len(roles) == 0 {
		roles = claimToStringSlice(claimByPath(claims, "realm_access.roles"), ",")
	}
	scopes := scopeClaimToStringSlice(claimByPath(claims, scopesClaim))
	if len(scopes) == 0 {
		scopes = scopeClaimToStringSlice(claimByPath(claims, "scope"))
	}
	if len(scopes) == 0 {
		scopes = scopeClaimToStringSlice(claimByPath(claims, "scp"))
	}

	attrs := map[string]string{}
	if rawAttrs, ok := claimByPath(claims, attrsClaim).(map[string]any); ok {
		for k, v := range rawAttrs {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			attrs[k] = strings.TrimSpace(anyToString(v))
		}
	}
	return Context{
		TenantID:  tenantID,
		Principal: principal,
		Roles:     dedupeSortedLower(roles),
		Scopes:    dedupeSortedLower(scopes),
		Attrs:     attrs,
	}, nil
}

func claimByPath(claims map[string]any, path string) any {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if v, ok := claims[path]; ok {
		return v
	}
	parts := strings.Split(path, ".")
	var cur any = claims
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil
		}
		asMap, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		next, exists := asMap[part]
		if !exists {
			return nil
		}
		cur = next
	}
	return cur
}

func claimToStringSlice(v any, sep string) []string {
	out := []string{}
	switch x := v.(type) {
	case nil:
		return out
	case []any:
		for _, item := range x {
			s := strings.TrimSpace(anyToString(item))
			if s != "" {
				out = append(out, s)
			}
		}
	case []string:
		for _, item := range x {
			s := strings.TrimSpace(item)
			if s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, part := range strings.Split(x, sep) {
			s := strings.TrimSpace(part)
			if s != "" {
				out = append(out, s)
			}
		}
	default:
		s := strings.TrimSpace(anyToString(x))
		if s != "" {
			out = append(out, s)
		}
	}
	return dedupeSortedLower(out)
}

func scopeClaimToStringSlice(v any) []string {
	out := claimToStringSlice(v, ",")
	if len(out) == 1 && strings.Contains(out[0], " ") {
		return claimToStringSlice(v, " ")
	}
	return out
}

func anyToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		return fmt.Sprint(v)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func dedupeSortedLower(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}
