package discovery

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// V3a: the winner between base and profile configs must be a defined rule, not
// map-iteration luck — a flipping value flips resource names and identity keys
// run-to-run with the LLM held constant.
func TestConfigValueProfilePrecedence(t *testing.T) {
	indexOf := func(files map[string][]string) *astpkg.ProjectIndex {
		idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{}}
		for path, kv := range files {
			idx.Configs[path] = cfg(path, kv...)
		}
		return idx
	}

	cases := []struct {
		name   string
		files  map[string][]string
		key    string
		want   string
		wantOK bool
	}{
		{
			name:   "single file resolves",
			files:  map[string][]string{"application.yml": {"queue.name", "orders"}},
			key:    "queue.name",
			want:   "orders", wantOK: true,
		},
		{
			name: "all files agree",
			files: map[string][]string{
				"application.yml":      {"queue.name", "orders"},
				"application-prod.yml": {"queue.name", "orders"},
			},
			key:  "queue.name",
			want: "orders", wantOK: true,
		},
		{
			name: "unknown active profile + disagreeing override -> unresolved",
			files: map[string][]string{
				"application.yml":      {"queue.name", "orders-local"},
				"application-prod.yml": {"queue.name", "orders-prod"},
			},
			key:    "queue.name",
			wantOK: false,
		},
		{
			name: "known active profile wins over base",
			files: map[string][]string{
				"application.yml":      {"queue.name", "orders-local", "spring.profiles.active", "prod"},
				"application-prod.yml": {"queue.name", "orders-prod"},
			},
			key:  "queue.name",
			want: "orders-prod", wantOK: true,
		},
		{
			name: "known active profile without the key falls back to base",
			files: map[string][]string{
				"application.yml":      {"queue.name", "orders", "spring.profiles.active", "prod"},
				"application-prod.yml": {"other.key", "x"},
			},
			key:  "queue.name",
			want: "orders", wantOK: true,
		},
		{
			name: "placeholder active profile is unknown -> unresolved on disagreement",
			files: map[string][]string{
				"application.yml":      {"queue.name", "orders-local", "spring.profiles.active", "${SPRING_PROFILES_ACTIVE:local}"},
				"application-prod.yml": {"queue.name", "orders-prod"},
			},
			key:    "queue.name",
			wantOK: false,
		},
		{
			name: "profiles disagree among themselves without base -> unresolved",
			files: map[string][]string{
				"application-prod.yml":  {"queue.name", "orders-prod"},
				"application-stage.yml": {"queue.name", "orders-stage"},
			},
			key:    "queue.name",
			wantOK: false,
		},
		{
			name: "base authoritative when profile only adds other keys",
			files: map[string][]string{
				"application.yml":      {"queue.name", "orders"},
				"application-prod.yml": {"db.url", "jdbc:postgresql://prod/x"},
			},
			key:  "queue.name",
			want: "orders", wantOK: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ConfigValue(indexOf(c.files), c.key)
			if got != c.want || ok != c.wantOK {
				t.Errorf("want (%q,%v), got (%q,%v)", c.want, c.wantOK, got, ok)
			}
		})
	}
}

func TestConfigProfile(t *testing.T) {
	cases := []struct{ path, want string }{
		{"application.yml", ""},
		{"src/main/resources/application-prod.yml", "prod"},
		{"bootstrap-stage.yaml", "stage"},
		{"helm/values-staging.yaml", "staging"},
		{"helm/values.yaml", ""},
		{"config/database.json", ""},
	}
	for _, c := range cases {
		if got := configProfile(c.path); got != c.want {
			t.Errorf("configProfile(%q): want %q, got %q", c.path, c.want, got)
		}
	}
}

// The never-lose-data fallback chain must survive the stricter ConfigValue: an
// unresolvable placeholder still yields a stable name from the key segment.
func TestResolveResourceNameUnresolvedFallsBackToKeySegment(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml":      cfg("application.yml", "services.aws.sqs.catalogue-target-response-sqs.url", "http://localhost/catalogue-local"),
		"application-prod.yml": cfg("application-prod.yml", "services.aws.sqs.catalogue-target-response-sqs.url", "https://sqs.eu-west-1.amazonaws.com/1/catalogue-prod"),
	}}
	got := ResolveResourceName(idx, "${services.aws.sqs.catalogue-target-response-sqs.url}")
	if got != "catalogue-target-response-sqs" {
		t.Errorf("unresolved value should fall back to the key segment, got %q", got)
	}
}
