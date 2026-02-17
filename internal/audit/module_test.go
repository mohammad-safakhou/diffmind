package audit

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditAppendListPruneExport(t *testing.T) {
	root := t.TempDir()
	if err := AppendEvent(root, Event{Timestamp: time.Now().UTC().Add(-48 * time.Hour), Action: "a", TenantID: "t1", Principal: "u", Decision: "allow"}); err != nil {
		t.Fatalf("append old: %v", err)
	}
	if err := AppendEvent(root, Event{Timestamp: time.Now().UTC(), Action: "b", TenantID: "t1", Principal: "u", Decision: "allow"}); err != nil {
		t.Fatalf("append new: %v", err)
	}
	events, err := ListEvents(root, "t1", 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	deleted, err := PruneEvents(root, "t1", time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("prune events: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted event, got %d", deleted)
	}
	result, err := ExportEvents(root, ExportRequest{TenantID: "t1", Encrypt: false})
	if err != nil {
		t.Fatalf("export events: %v", err)
	}
	if result.Path == "" {
		t.Fatalf("expected export path")
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("missing export file: %v", err)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	enc, err := ExportEvents(root, ExportRequest{TenantID: "t1", Encrypt: true, KeyB64: base64.StdEncoding.EncodeToString(key), KeyID: "kms-test"})
	if err != nil {
		t.Fatalf("encrypted export: %v", err)
	}
	if !enc.Encrypted || enc.KeyID != "kms-test" {
		t.Fatalf("unexpected encrypted export metadata: %+v", enc)
	}
	if filepath.Ext(enc.Path) != ".enc" {
		t.Fatalf("expected encrypted export extension, got %s", enc.Path)
	}
}
