package llmrun

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureStoreWritesPromptAndResponseBundle(t *testing.T) {
	dir := t.TempDir()
	store := CaptureStore{Dir: dir}
	store.Prompt("detail.user id", "prompt body")
	store.Response("detail.user id", map[string]any{"item": "ok"}, []byte("raw"), "text")

	files := map[string]string{
		"detail.user-id.prompt.txt":    "prompt body",
		"detail.user-id.response.raw":  "raw",
		"detail.user-id.response.text": "text",
	}
	for name, want := range files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(body) != want {
			t.Errorf("%s = %q, want %q", name, body, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "detail.user-id.response.json")); err != nil {
		t.Fatalf("response JSON missing: %v", err)
	}
}
