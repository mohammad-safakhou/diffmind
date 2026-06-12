package ast

import "testing"

func entryMap(entries []ConfigEntry) map[string]string {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		m[e.Key] = e.Value
	}
	return m
}

// V3b: each item of a sequence of mappings must keep its own key space. The
// old indentation-walker collapsed every listener onto "listeners.queue" and
// lost "name" entirely — common in Spring Cloud Stream and helm values.
func TestParseYAMLSequenceOfMappings(t *testing.T) {
	src := []byte(`
listeners:
  - name: orders
    queue: orders-queue
  - name: billing
    queue: billing-queue
`)
	got := entryMap(parseYAMLEntries(src))
	want := map[string]string{
		"listeners[0].name":  "orders",
		"listeners[0].queue": "orders-queue",
		"listeners[1].name":  "billing",
		"listeners[1].queue": "billing-queue",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("want %s=%q, got %q (all: %v)", k, v, got[k], got)
		}
	}
}

// V3c: inline lists must split into items (not one opaque string) and block
// scalar sequences must not be dropped.
func TestParseYAMLLists(t *testing.T) {
	src := []byte(`
queues:
  names: [orders, billing]
topics:
  - audit
  - metrics
`)
	got := entryMap(parseYAMLEntries(src))
	want := map[string]string{
		"queues.names[0]": "orders",
		"queues.names[1]": "billing",
		"topics[0]":       "audit",
		"topics[1]":       "metrics",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("want %s=%q, got %q (all: %v)", k, v, got[k], got)
		}
	}
}

// Spring multi-document files: entries of a profile-activated document carry
// that profile, so the V3a precedence rule holds within one file, not just
// across application-<profile>.yml files.
func TestParseYAMLMultiDocProfiles(t *testing.T) {
	src := []byte(`
queue:
  name: orders-local
---
spring:
  config:
    activate:
      on-profile: prod
queue:
  name: orders-prod
`)
	entries := parseYAMLEntries(src)
	byProfile := map[string]string{}
	for _, e := range entries {
		if e.Key == "queue.name" {
			byProfile[e.Profile] = e.Value
		}
	}
	if byProfile[""] != "orders-local" {
		t.Errorf("base doc entry should have no profile, got %v", byProfile)
	}
	if byProfile["prod"] != "orders-prod" {
		t.Errorf("overlay entry should carry profile prod, got %v", byProfile)
	}
}

func TestParseYAMLAnchorsAndScalars(t *testing.T) {
	src := []byte(`
defaults: &db
  url: jdbc:postgresql://db:5432/app
primary: *db
flag: true
port: 8080
empty:
`)
	got := entryMap(parseYAMLEntries(src))
	if got["primary.url"] != "jdbc:postgresql://db:5432/app" {
		t.Errorf("alias should resolve, got %q", got["primary.url"])
	}
	if got["flag"] != "true" || got["port"] != "8080" {
		t.Errorf("scalar types should render as text, got flag=%q port=%q", got["flag"], got["port"])
	}
	if _, ok := got["empty"]; ok {
		t.Errorf("null value must not produce an entry")
	}
}

func TestParseYAMLKeepsSourceLines(t *testing.T) {
	src := []byte("queue:\n  name: orders\n")
	entries := parseYAMLEntries(src)
	if len(entries) != 1 || entries[0].Line != 2 {
		t.Fatalf("want one entry at line 2, got %+v", entries)
	}
}

// Malformed YAML must degrade, not abort: documents that parsed are kept.
func TestParseYAMLMalformedTail(t *testing.T) {
	src := []byte("queue:\n  name: orders\n---\n\t<<bad\n")
	got := entryMap(parseYAMLEntries(src))
	if got["queue.name"] != "orders" {
		t.Errorf("valid leading document should survive a malformed tail, got %v", got)
	}
}
