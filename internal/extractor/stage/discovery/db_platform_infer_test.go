package discovery

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

func cfg(path string, kv ...string) *astpkg.ConfigFile {
	cf := &astpkg.ConfigFile{Path: path}
	for i := 0; i+1 < len(kv); i += 2 {
		cf.Entries = append(cf.Entries, astpkg.ConfigEntry{Key: kv[i], Value: kv[i+1]})
	}
	return cf
}

func TestInferConfigDBPlatform(t *testing.T) {
	// Single postgres datasource -> postgres.
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml", "spring.datasource.url", "jdbc:postgresql://db:5432/ats"),
	}}
	if got := InferConfigDBPlatform(idx); got != "postgres" {
		t.Errorf("want postgres, got %q", got)
	}
	// Two distinct platforms -> ambiguous -> "".
	idx2 := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"a.yml": cfg("a.yml", "x", "jdbc:postgresql://h/d"),
		"b.yml": cfg("b.yml", "y", "jdbc:mysql://h/d"),
	}}
	if got := InferConfigDBPlatform(idx2); got != "" {
		t.Errorf("ambiguous should be empty, got %q", got)
	}
	// No datasource -> "".
	if got := InferConfigDBPlatform(&astpkg.ProjectIndex{}); got != "" {
		t.Errorf("none should be empty, got %q", got)
	}
}

func TestStampInferredDBPlatform(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml", "spring.datasource.url", "jdbc:postgresql://db/ats"),
	}}
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{Type: "db_operation", Name: "read orders", Platform: "database"}}, // generic -> stamped
		{BaseEntity: model.BaseEntity{Type: "db_operation", Name: "read events", Platform: "athena"}},   // specific -> untouched
		{BaseEntity: model.BaseEntity{Type: "outbound_http", Name: "GET /x", Platform: "http"}},         // non-db -> untouched
	}
	StampInferredDBPlatform(idx, deps)
	if deps[0].Platform != "postgres" {
		t.Errorf("generic db op should be stamped postgres, got %q", deps[0].Platform)
	}
	if deps[1].Platform != "athena" {
		t.Errorf("specific platform must not be overwritten, got %q", deps[1].Platform)
	}
	if deps[2].Platform != "http" {
		t.Errorf("non-db dep must be untouched, got %q", deps[2].Platform)
	}
}
