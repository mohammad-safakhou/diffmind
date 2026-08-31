package reconcile

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

func TestCanonicalizeRoutePath(t *testing.T) {
	cases := map[string]string{
		"/users/{id}":          "/users/{}",
		"/users/:id":           "/users/{}",
		"/users/<int:id>":      "/users/{}",
		"/users/<id>":          "/users/{}",
		"/files/*":             "/files/{}",
		"/a/**":                "/a/{}",
		"/Orders/":             "/orders",
		"orders":               "/orders",
		"/a//b":                "/a/b",
		"/users/{id}/posts/:p": "/users/{}/posts/{}",
		"/static/css":          "/static/css",
		"":                     "",
		"/":                    "/",
		"/o/{id:[0-9]+}":       "/o/{}",
	}
	for in, want := range cases {
		if got := CanonicalizeRoutePath(in); got != want {
			t.Errorf("CanonicalizeRoutePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The same route in different framework param syntaxes must produce one
// SemanticKey, so deterministic ({id}) and Express-style (:id)
// spelling collapse instead of duplicating.
func TestRouteParamSyntaxesShareSemanticKey(t *testing.T) {
	a := model.BaseEntity{Type: "http_route", Name: "GET /users/{id}"}
	b := model.BaseEntity{Type: "http_route", Name: "get /users/:id"}
	if SemanticKeyLoose(a) != SemanticKeyLoose(b) {
		t.Errorf("param syntaxes did not unify:\n  %q\n  %q", SemanticKeyLoose(a), SemanticKeyLoose(b))
	}
	// A genuinely different path must NOT collapse.
	c := model.BaseEntity{Type: "http_route", Name: "GET /accounts/{id}"}
	if SemanticKeyLoose(a) == SemanticKeyLoose(c) {
		t.Errorf("distinct routes wrongly merged: %q", SemanticKeyLoose(a))
	}
}
