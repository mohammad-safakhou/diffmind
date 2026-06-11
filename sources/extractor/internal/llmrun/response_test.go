package llmrun

import (
	"reflect"
	"testing"
)

func TestScrapeJSONObject(t *testing.T) {
	tests := []struct {
		name string
		text string
		want map[string]any
	}{
		{
			name: "direct",
			text: `{"items":[]}`,
			want: map[string]any{"items": []any{}},
		},
		{
			name: "fenced",
			text: "result:\n```json\n{\"items\":[\"a\"]}\n```",
			want: map[string]any{"items": []any{"a"}},
		},
		{
			name: "surrounded by prose",
			text: `before {"item":{"name":"quoted } brace"}} after`,
			want: map[string]any{"item": map[string]any{"name": "quoted } brace"}},
		},
		{
			name: "invalid",
			text: "there is no object",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ScrapeJSONObject(test.text); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ScrapeJSONObject() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestPreviewStringEscapesWhitespaceBeforeTruncating(t *testing.T) {
	if got, want := PreviewString("a\nb\tc", 5), "a\\nb\\\u2026"; got != want {
		t.Fatalf("PreviewString() = %q, want %q", got, want)
	}
}
