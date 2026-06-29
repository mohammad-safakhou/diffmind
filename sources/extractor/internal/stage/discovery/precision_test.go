package discovery

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func configIndex(entries map[string]string) *astpkg.ProjectIndex {
	var ce []astpkg.ConfigEntry
	for k, v := range entries {
		ce = append(ce, astpkg.ConfigEntry{Key: k, Value: v})
	}
	return &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": {Path: "application.yml", Format: "yaml", Entries: ce},
	}}
}

func objectiveByType(t *testing.T, typ string) objectives.Objective {
	t.Helper()
	for _, obj := range objectives.Default() {
		if obj.Type == typ {
			return obj
		}
	}
	t.Fatalf("missing default objective %q", typ)
	return objectives.Objective{}
}

func TestResolveResourceName(t *testing.T) {
	t.Run("non-placeholder passes through", func(t *testing.T) {
		if got := ResolveResourceName(nil, "orders-created"); got != "orders-created" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("key-segment fallback when value unresolved", func(t *testing.T) {
		// The exact run defect: ${...sqs.catalogue-target-response-sqs.url} with
		// no config value should still yield the queue name.
		got := ResolveResourceName(configIndex(nil), "${services.aws.sqs.catalogue-target-response-sqs.url}")
		if got != "catalogue-target-response-sqs" {
			t.Errorf("key-segment fallback got %q", got)
		}
	})
	t.Run("resolves URL value to trailing segment", func(t *testing.T) {
		idx := configIndex(map[string]string{"app.queue.url": "https://sqs.eu-central-1.amazonaws.com/123456789012/my-real-queue"})
		if got := ResolveResourceName(idx, "${app.queue.url}"); got != "my-real-queue" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("resolves ARN value to trailing segment", func(t *testing.T) {
		idx := configIndex(map[string]string{"app.q": "arn:aws:sqs:eu-central-1:123456789012:campaign-changes.fifo"})
		if got := ResolveResourceName(idx, "${app.q}"); got != "campaign-changes.fifo" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("default value when key absent", func(t *testing.T) {
		if got := ResolveResourceName(configIndex(nil), "${missing.key:fallback-queue}"); got != "fallback-queue" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("one level of indirection", func(t *testing.T) {
		idx := configIndex(map[string]string{"a": "${b}", "b": "inner-queue"})
		if got := ResolveResourceName(idx, "${a}"); got != "inner-queue" {
			t.Errorf("got %q", got)
		}
	})
}

func TestEntityFromFrameworkBindingResolvesQueuePlaceholder(t *testing.T) {
	idx := configIndex(nil)
	obj := objectiveByType(t, "queue_consumer")
	e, ok := EntityFromFrameworkBinding(idx, obj, astpkg.FrameworkBinding{
		Framework: "spring", Kind: "queue_consumer",
		Symbol:  "com.example.Listener.onMessage",
		Trigger: "sqs: ${services.aws.sqs.catalogue-target-response-sqs.url}",
		File:    "src/main/java/com/example/Listener.java",
		Range:   astpkg.Range{StartLine: 10, EndLine: 10},
	})
	if !ok {
		t.Fatal("expected entity")
	}
	if e.Name != "catalogue-target-response-sqs" || e.Details["queue"] != "catalogue-target-response-sqs" {
		t.Fatalf("placeholder not resolved: name=%q queue=%v", e.Name, e.Details["queue"])
	}
	if e.Details["platform"] != "sqs" {
		t.Fatalf("platform=%v", e.Details["platform"])
	}
}
