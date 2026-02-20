package audit

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditAppendListPruneExport(t *testing.T) {
	root := t.TempDir()
	manifestKey := make([]byte, 32)
	for i := range manifestKey {
		manifestKey[i] = byte(i + 11)
	}
	t.Setenv("DIFFMIND_AUDIT_MANIFEST_HMAC_KEY_B64", base64.StdEncoding.EncodeToString(manifestKey))
	t.Setenv("DIFFMIND_AUDIT_MANIFEST_KEY_ID", "manifest-key-1")

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
	if result.Manifest == "" {
		t.Fatalf("expected export manifest path")
	}
	if _, err := os.Stat(result.Manifest); err != nil {
		t.Fatalf("missing export manifest file: %v", err)
	}
	plainVerify, err := VerifyExportManifest(result.Manifest)
	if err != nil {
		t.Fatalf("verify plain export manifest: %v", err)
	}
	if !plainVerify.Valid || !plainVerify.Signed || !plainVerify.SignatureValid {
		t.Fatalf("unexpected plain export verify result: %+v", plainVerify)
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
	if enc.Manifest == "" {
		t.Fatalf("expected encrypted export manifest path")
	}
	encVerify, err := VerifyExportManifest(enc.Manifest)
	if err != nil {
		t.Fatalf("verify encrypted export manifest: %v", err)
	}
	if !encVerify.Valid || !encVerify.Signed || !encVerify.SignatureValid {
		t.Fatalf("unexpected encrypted export verify result: %+v", encVerify)
	}
	if err := os.WriteFile(enc.Path, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper encrypted payload: %v", err)
	}
	encTampered, err := VerifyExportManifest(enc.Manifest)
	if err != nil {
		t.Fatalf("verify tampered encrypted export manifest: %v", err)
	}
	if encTampered.Valid {
		t.Fatalf("expected tampered export verify to fail")
	}
}

func TestAuditIntegrityVerification(t *testing.T) {
	root := t.TempDir()
	if err := AppendEvent(root, Event{Timestamp: time.Now().UTC().Add(-2 * time.Hour), Action: "a", TenantID: "t1", Principal: "u1", Decision: "allow"}); err != nil {
		t.Fatalf("append event1: %v", err)
	}
	if err := AppendEvent(root, Event{Timestamp: time.Now().UTC().Add(-1 * time.Hour), Action: "b", TenantID: "t1", Principal: "u1", Decision: "allow"}); err != nil {
		t.Fatalf("append event2: %v", err)
	}

	okRes, err := VerifyEvents(root, VerifyRequest{TenantID: "", EnforceChain: true})
	if err != nil {
		t.Fatalf("verify events: %v", err)
	}
	if !okRes.Valid {
		t.Fatalf("expected valid chain, got issues=%v", okRes.Issues)
	}
	if okRes.Checked != 2 {
		t.Fatalf("expected checked=2, got %d", okRes.Checked)
	}

	path := eventsPath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	tampered := strings.Replace(string(data), `"decision":"allow"`, `"decision":"deny"`, 1)
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered audit log: %v", err)
	}

	badRes, err := VerifyEvents(root, VerifyRequest{TenantID: "", EnforceChain: true})
	if err != nil {
		t.Fatalf("verify tampered events: %v", err)
	}
	if badRes.Valid {
		t.Fatalf("expected tampered chain to be invalid")
	}
	if len(badRes.Issues) == 0 {
		t.Fatalf("expected tampered chain issues")
	}
}
