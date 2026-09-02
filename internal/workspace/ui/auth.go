package ui

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

// Role is the authorization level attached to an authenticated identity.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
)

const (
	proxySecretHeader = "X-DiffMind-Proxy-Secret"
	proxyUserHeader   = "X-DiffMind-User"
	proxyRoleHeader   = "X-DiffMind-Role"
)

type identityContextKey struct{}

// Identity describes the caller available to handlers and the audit log.
type Identity struct {
	User       string `json:"user"`
	Role       Role   `json:"role"`
	AuthMethod string `json:"auth_method"`
}

// SetAuthToken enables HTTP authentication for the dashboard and every API
// route except /healthz. Bearer authentication is intended for agents and
// scripts; HTTP Basic keeps the browser UI usable without a separate login
// implementation. The Basic username is ignored and the token is the password.
func (s *Server) SetAuthToken(token string) {
	s.authToken = strings.TrimSpace(token)
}

// SetTrustedProxySecret enables per-user identity supplied by a trusted OIDC
// proxy. The proxy must strip client-provided DiffMind identity headers and
// inject the secret, user, and role headers itself.
func (s *Server) SetTrustedProxySecret(secret string) {
	s.proxySecret = strings.TrimSpace(secret)
}

func (s *Server) accessControlled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		requestID := newRequestID()
		w.Header().Set("X-Request-ID", requestID)
		recorder := newStatusRecorder(w)
		identity := Identity{User: "anonymous", Role: RoleViewer, AuthMethod: "none"}
		if isAuditedRequest(r.Method, r.URL.Path) {
			defer func() { s.writeAudit(r, requestID, identity, recorder.statusCode()) }()
		}

		resolved, err := s.authenticate(r)
		if err != nil {
			if s.authToken != "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="DiffMind", charset="UTF-8"`)
			}
			writeErr(recorder, http.StatusUnauthorized, err)
			return
		}
		identity = resolved
		if !authorized(identity.Role, r.Method, r.URL.Path) {
			writeErr(recorder, http.StatusForbidden, errors.New("insufficient role"))
			return
		}
		next.ServeHTTP(recorder, r.WithContext(context.WithValue(r.Context(), identityContextKey{}, identity)))
	})
}

func (s *Server) authenticate(r *http.Request) (Identity, error) {
	if s.authToken == "" && s.proxySecret == "" {
		return Identity{User: "local", Role: RoleAdmin, AuthMethod: "local"}, nil
	}
	if secureTokenEqual(requestToken(r), s.authToken) {
		return Identity{User: "shared-token", Role: RoleAdmin, AuthMethod: "token"}, nil
	}
	if secureTokenEqual(strings.TrimSpace(r.Header.Get(proxySecretHeader)), s.proxySecret) {
		user := strings.TrimSpace(r.Header.Get(proxyUserHeader))
		if user == "" {
			return Identity{}, errors.New("trusted proxy did not provide a user")
		}
		role, ok := parseRole(r.Header.Get(proxyRoleHeader))
		if !ok {
			return Identity{}, errors.New("trusted proxy provided an invalid role")
		}
		return Identity{User: user, Role: role, AuthMethod: "trusted_proxy"}, nil
	}
	return Identity{}, errors.New("authentication required")
}

func parseRole(value string) (Role, bool) {
	role := Role(strings.ToLower(strings.TrimSpace(value)))
	if role == "" {
		return RoleViewer, true
	}
	switch role {
	case RoleViewer, RoleEditor, RoleAdmin:
		return role, true
	default:
		return "", false
	}
}

func authorized(role Role, method, path string) bool {
	if path == "/mcp" || method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return role == RoleViewer || role == RoleEditor || role == RoleAdmin
	}
	if role == RoleAdmin {
		return true
	}
	if role != RoleEditor {
		return false
	}
	return method != http.MethodDelete && !(method == http.MethodPost && path == "/api/v1/refresh")
}

func identityFromContext(ctx context.Context) Identity {
	identity, _ := ctx.Value(identityContextKey{}).(Identity)
	return identity
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, identityFromContext(r.Context()))
}

func requestToken(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) > len("Bearer ") && strings.EqualFold(auth[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(auth[len("Bearer "):])
	}
	if _, password, ok := r.BasicAuth(); ok {
		return password
	}
	return strings.TrimSpace(r.Header.Get("X-DiffMind-Token"))
}

func secureTokenEqual(got, want string) bool {
	if got == "" || want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
