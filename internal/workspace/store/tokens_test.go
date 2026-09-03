package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func tokenFixture(t *testing.T) (*Store, *ProjectToken, string) {
	t.Helper()
	s := newTestStore(t)
	p, err := s.CreateProject(Project{Name: "token-project"})
	if err != nil {
		t.Fatal(err)
	}
	meta, secret, err := s.IssueProjectToken(p.ID, "coding agent", "viewer", "admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return s, meta, secret
}

func TestTokenLifecycleHashOnlyRestartAndRotation(t *testing.T) {
	s, metadata, secret := tokenFixture(t)
	path := filepath.Join(s.projectDir(metadata.ProjectID), "tokens.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(secret, ".")
	if !IsProjectToken(secret) || strings.Contains(string(body), secret) || strings.Contains(string(body), parts[3]) {
		t.Fatal("plaintext persisted")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	reopened, err := New(s.HomeDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.AuthenticateProjectToken(secret)
	if err != nil || !reflect.DeepEqual(metadata, got) {
		t.Fatalf("restart %+v %v", got, err)
	}
	listed, err := reopened.ListProjectTokens(metadata.ProjectID)
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(*metadata, listed[0]) {
		t.Fatalf("listing %v %v", listed, err)
	}
	encoded, _ := json.Marshal(listed)
	if strings.Contains(string(encoded), "sha256") || strings.Contains(string(encoded), secret) {
		t.Fatal("verifier exposed")
	}
	_, replacement, err := s.IssueProjectToken(metadata.ProjectID, "replacement", "editor", "admin", time.Minute)
	if err != nil || secret == replacement {
		t.Fatalf("rotation %v", err)
	}
	revoked, err := s.RevokeProjectToken(metadata.ProjectID, metadata.ID, "another-admin")
	if err != nil || revoked.RevokedAt == nil || revoked.RevokedBy != "another-admin" {
		t.Fatalf("revoke %+v %v", revoked, err)
	}
	again, err := s.RevokeProjectToken(metadata.ProjectID, metadata.ID, "admin")
	if err != nil || !reflect.DeepEqual(revoked, again) {
		t.Fatalf("idempotent revoke %+v %v", again, err)
	}
	if _, err := reopened.AuthenticateProjectToken(secret); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revocation %v", err)
	}
	if _, err := reopened.ActiveProjectToken(metadata.ProjectID, metadata.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active revoked token %v", err)
	}
	if _, err := reopened.AuthenticateProjectToken(replacement); err != nil {
		t.Fatalf("rotation revoked replacement: %v", err)
	}
	if !metadata.CreatedAt.Equal(revoked.CreatedAt) || !metadata.ExpiresAt.Equal(revoked.ExpiresAt) {
		t.Fatal("revocation rewrote dates")
	}
}

func TestTokenExpiryExactBoundaryAndMalformedCredentials(t *testing.T) {
	s, meta, secret := tokenFixture(t)
	for _, tc := range []struct {
		now    time.Time
		active bool
	}{
		{meta.CreatedAt.Add(-time.Nanosecond), false}, {meta.CreatedAt, true}, {meta.ExpiresAt.Add(-time.Nanosecond), true}, {meta.ExpiresAt, false}, {meta.ExpiresAt.Add(time.Nanosecond), false},
	} {
		if tokenActive(*meta, tc.now) != tc.active {
			t.Fatalf("boundary %+v", tc)
		}
	}
	parts := strings.Split(secret, ".")
	for _, value := range []string{
		"", "dmt1.", secret + ".extra", strings.Repeat("a", 513),
		strings.Replace(secret, parts[2], strings.Repeat("0", 32), 1),
		strings.Replace(secret, parts[3], strings.Repeat("0", 64), 1),
		strings.Replace(secret, parts[1], base64.RawURLEncoding.EncodeToString([]byte("../private")), 1),
		strings.Replace(secret, parts[1], base64.RawURLEncoding.EncodeToString([]byte("missing")), 1),
		strings.Replace(secret, parts[1], "!", 1), strings.Replace(secret, parts[3], "x", 1),
	} {
		if _, err := s.AuthenticateProjectToken(value); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("malformed token error: %v", err)
		}
	}
	file, _ := s.readTokens(meta.ProjectID)
	file.Tokens[0].CreatedAt = time.Now().Add(-2 * time.Hour).UTC()
	file.Tokens[0].ExpiresAt = time.Now().Add(-time.Hour).UTC()
	if err := s.saveTokens(meta.ProjectID, file); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateProjectToken(secret); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := s.ActiveProjectToken(meta.ProjectID, meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired in-flight: %v", err)
	}
}

func TestTokenValidationCorruptionAndNoOverwrite(t *testing.T) {
	s, meta, secret := tokenFixture(t)
	for _, tc := range []struct {
		name, role string
		ttl        time.Duration
	}{
		{"", "viewer", time.Hour}, {" padded", "viewer", time.Hour}, {"line\nbreak", "viewer", time.Hour}, {strings.Repeat("x", 101), "viewer", time.Hour},
		{"agent", "admin", time.Hour}, {"agent", "", time.Hour}, {"agent", "viewer", 59 * time.Second}, {"agent", "viewer", 366 * 24 * time.Hour},
	} {
		if _, value, err := s.IssueProjectToken(meta.ProjectID, tc.name, tc.role, "admin", tc.ttl); !errors.Is(err, ErrInvalidTokenRequest) || value != "" {
			t.Fatalf("bad issue %+v: %v", tc, err)
		}
	}
	path := filepath.Join(s.projectDir(meta.ProjectID), "tokens.json")
	good, _ := os.ReadFile(path)
	for _, corrupt := range []string{
		`{`, `{"version":2,"tokens":[]}`, `{"version":1,"tokens":null}`, `{"version":1,"tokens":[],"future":true}`, string(good) + " {}",
		strings.Replace(string(good), `"viewer"`, `"admin"`, 1), strings.Replace(string(good), `"project_id": "token-project"`, `"project_id": "other"`, 1),
		strings.Replace(string(good), `"sha256": "`, `"sha256": "xx`, 1), strings.Repeat("x", maxTokenFileBytes+1),
	} {
		if err := os.WriteFile(path, []byte(corrupt), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AuthenticateProjectToken(secret); err == nil {
			t.Fatal("corrupt token authenticated")
		}
		if _, err := s.ListProjectTokens(meta.ProjectID); err == nil {
			t.Fatal("corrupt listing")
		}
		if _, _, err := s.IssueProjectToken(meta.ProjectID, "new", "viewer", "admin", time.Hour); err == nil {
			t.Fatal("corrupt registry overwritten")
		}
		if _, err := s.RevokeProjectToken(meta.ProjectID, meta.ID, "admin"); err == nil {
			t.Fatal("corrupt registry revoked")
		}
		after, _ := os.ReadFile(path)
		if string(after) != corrupt {
			t.Fatal("modified corrupt registry")
		}
	}
	// Refuse symlinks and never mutate their target.
	other := filepath.Join(t.TempDir(), "original.json")
	if err := os.WriteFile(other, good, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.IssueProjectToken(meta.ProjectID, "new", "viewer", "admin", time.Hour); err == nil {
		t.Fatal("followed symlink")
	}
	if _, err := s.AuthenticateProjectToken(secret); err == nil {
		t.Fatal("authenticated via symlink")
	}
	if _, err := s.RevokeProjectToken(meta.ProjectID, meta.ID, "admin"); err == nil {
		t.Fatal("revoked via symlink")
	}
	after, _ := os.ReadFile(other)
	if string(after) != string(good) {
		t.Fatal("symlink target modified")
	}
}

func TestTokenConcurrentIssueRevokeAndCapacity(t *testing.T) {
	s, meta, _ := tokenFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			issued, value, err := s.IssueProjectToken(meta.ProjectID, "parallel", "viewer", "admin", time.Hour)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := s.AuthenticateProjectToken(value); err != nil {
				t.Error(err)
			}
			if _, err := s.RevokeProjectToken(meta.ProjectID, issued.ID, "admin"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	list, err := s.ListProjectTokens(meta.ProjectID)
	if err != nil || len(list) != 25 {
		t.Fatalf("lost updates %d %v", len(list), err)
	}
	seen := map[string]bool{}
	for _, token := range list {
		if seen[token.ID] || (token.ID != meta.ID && token.RevokedAt == nil) {
			t.Fatal("duplicate id/lost revoke")
		}
		seen[token.ID] = true
	}
	file, _ := s.readTokens(meta.ProjectID)
	for len(file.Tokens) < maxProjectTokens {
		record := file.Tokens[0]
		record.ID = fmt.Sprintf("%032x", len(file.Tokens))
		file.Tokens = append(file.Tokens, record)
	}
	if err := s.saveTokens(meta.ProjectID, file); err != nil {
		t.Fatal(err)
	}
	if _, secret, err := s.IssueProjectToken(meta.ProjectID, "overflow", "viewer", "admin", time.Hour); !errors.Is(err, ErrTokenCapacity) || secret != "" {
		t.Fatalf("capacity %v", err)
	}
	if _, err := s.RevokeProjectToken(meta.ProjectID, meta.ID, "admin"); err != nil {
		t.Fatalf("full registry blocked revocation: %v", err)
	}
}
