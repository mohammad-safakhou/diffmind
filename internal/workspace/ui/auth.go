package ui

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

// SetAuthToken enables HTTP authentication for the dashboard and every API
// route except /healthz. Bearer authentication is intended for agents and
// scripts; HTTP Basic keeps the browser UI usable without a separate login
// implementation. The Basic username is ignored and the token is the password.
func (s *Server) SetAuthToken(token string) {
	s.authToken = strings.TrimSpace(token)
}

func (s *Server) authenticated(next http.Handler) http.Handler {
	if s.authToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if !secureTokenEqual(requestToken(r), s.authToken) {
			w.Header().Set("WWW-Authenticate", `Basic realm="DiffMind", charset="UTF-8"`)
			writeErr(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	})
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
