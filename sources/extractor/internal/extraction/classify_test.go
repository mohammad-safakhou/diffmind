package extraction

import (
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// Structured detail values must never be rendered into grouping fields: a real
// run emitted instance "map[connection_source:... url_template:jdbc:...]" when
// the LLM returned connection details as an object, splitting one database into
// several downstream identities.
func TestDeriveGroupingIgnoresStructuredDetails(t *testing.T) {
	b := model.BaseEntity{
		Type: "db_operation",
		Name: "write traffic_configuration",
		Details: map[string]any{
			"connection_string": map[string]any{
				"url_template": "jdbc:postgresql://${DATABASE_HOST}/${DATABASE_NAME}",
				"type":         "PostgreSQL",
			},
			"table":     "traffic_configuration",
			"operation": "insert",
		},
	}
	platform, instance, _, opKind := DeriveGrouping(b)
	if strings.HasPrefix(instance, "map[") {
		t.Fatalf("structured detail leaked into instance: %q", instance)
	}
	if instance != "traffic_configuration" {
		t.Errorf("instance should fall through to the next scalar key, got %q", instance)
	}
	if platform != "database" {
		// Generic here is correct: StampInferredDBPlatform later fills the
		// configured engine; classify must not guess from a structured value.
		t.Errorf("platform should stay generic without a scalar hint, got %q", platform)
	}
	if opKind != "write" {
		t.Errorf("operation_kind should be canonical write, got %q", opKind)
	}
}

func TestScalarDetail(t *testing.T) {
	cases := []struct {
		in     any
		want   string
		wantOK bool
	}{
		{"  orders  ", "orders", true},
		{42, "42", true},
		{3.5, "3.5", true},
		{true, "true", true},
		{map[string]any{"a": 1}, "", false},
		{[]any{"a"}, "", false},
		{nil, "", false},
	}
	for _, c := range cases {
		got, ok := scalarDetail(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("scalarDetail(%v): want (%q,%v), got (%q,%v)", c.in, c.want, c.wantOK, got, ok)
		}
	}
}
