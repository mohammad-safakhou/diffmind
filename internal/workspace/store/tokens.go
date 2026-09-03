package store

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const maxProjectTokens = 1000

// Enough for 1000 maximally JSON-escaped records, including future revocations.
// Admission must not fill the file so completely that revocation cannot fit.
const maxTokenFileBytes = 8 << 20

var (
	ErrInvalidToken        = errors.New("invalid or expired project token")
	ErrInvalidTokenRequest = errors.New("invalid project token request")
	ErrTokenCapacity       = errors.New("project token history is full (1000 records)")
)

// ProjectToken is public metadata, never a credential or a stored verifier.
// Service grants are independent of proxy memberships and cannot grant admin.
type ProjectToken struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"project_id"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedBy string     `json:"created_by"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	RevokedBy string     `json:"revoked_by,omitempty"`
}

type tokenRecord struct {
	ProjectToken
	SHA256 string `json:"sha256"`
}
type tokenFile struct {
	Version int           `json:"version"`
	Tokens  []tokenRecord `json:"tokens"`
}

// IsProjectToken recognizes the reserved credential namespace even if malformed.
func IsProjectToken(value string) bool { return strings.HasPrefix(value, "dmt1.") }

func tokenLabel(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}
func tokenHex(value string, bytes int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == bytes && value == strings.ToLower(value)
}
func validateTokens(file tokenFile, pid string) error {
	if file.Version != 1 || file.Tokens == nil || len(file.Tokens) > maxProjectTokens {
		return errors.New("invalid token registry version or count")
	}
	seen := map[string]bool{}
	for _, token := range file.Tokens {
		if !tokenHex(token.ID, 16) || seen[token.ID] || token.ProjectID != pid || !tokenHex(token.SHA256, 32) || !tokenLabel(token.Name, 100) || !tokenLabel(token.CreatedBy, 256) {
			return errors.New("invalid token registry identity")
		}
		seen[token.ID] = true
		if token.Role != "viewer" && token.Role != "editor" {
			return errors.New("invalid token registry role")
		}
		lifetime := token.ExpiresAt.Sub(token.CreatedAt)
		if token.CreatedAt.IsZero() || lifetime < time.Minute || lifetime > 365*24*time.Hour {
			return errors.New("invalid token registry lifetime")
		}
		if (token.RevokedAt == nil && token.RevokedBy != "") || (token.RevokedAt != nil && (token.RevokedAt.Before(token.CreatedAt) || !tokenLabel(token.RevokedBy, 256))) {
			return errors.New("invalid token registry revocation")
		}
	}
	return nil
}

// Caller holds s.mu. The single-server lease provides the deployment's writer
// exclusion; this registry is not a multi-process token administration service.
func (s *Store) readTokens(pid string) (*tokenFile, error) {
	if !validID(pid) {
		return nil, ErrNotFound
	}
	if _, err := s.GetProject(pid); err != nil {
		return nil, err
	}
	path := filepath.Join(s.projectDir(pid), "tokens.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return &tokenFile{Version: 1, Tokens: []tokenRecord{}}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxTokenFileBytes {
		return nil, errors.New("invalid token registry file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, errors.New("token registry changed during read")
	}
	body, err := io.ReadAll(io.LimitReader(f, maxTokenFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxTokenFileBytes {
		return nil, errors.New("token registry too large")
	}
	var file tokenFile
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&file); err != nil {
		return nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("trailing token registry data")
	}
	if err := validateTokens(file, pid); err != nil {
		return nil, err
	}
	return &file, nil
}

func (s *Store) saveTokens(pid string, file *tokenFile) error {
	if err := validateTokens(*file, pid); err != nil {
		return err
	}
	body, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if len(body) > maxTokenFileBytes {
		return ErrTokenCapacity
	}
	// writeJSON uses a 0600 temp, syncs its contents and atomically renames it.
	// Also sync the directory before acknowledging issuance or revocation.
	if err := writeJSON(filepath.Join(s.projectDir(pid), "tokens.json"), file); err != nil {
		return err
	}
	dir, err := os.Open(s.projectDir(pid))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Store) ListProjectTokens(pid string) ([]ProjectToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.readTokens(pid)
	if err != nil {
		return nil, err
	}
	result := make([]ProjectToken, 0, len(file.Tokens))
	for i := len(file.Tokens) - 1; i >= 0; i-- {
		result = append(result, file.Tokens[i].ProjectToken)
	}
	return result, nil
}

// IssueProjectToken returns the plaintext exactly once. Only a SHA-256 verifier
// of the whole credential is persisted; the random secret has 256 bits.
func (s *Store) IssueProjectToken(pid, name, role, actor string, lifetime time.Duration) (*ProjectToken, string, error) {
	if !tokenLabel(name, 100) || !tokenLabel(actor, 256) || (role != "viewer" && role != "editor") || lifetime < time.Minute || lifetime > 365*24*time.Hour {
		return nil, "", fmt.Errorf("%w: name (1–100 bytes), viewer/editor role, and lifetime of 60–31536000 seconds required", ErrInvalidTokenRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.readTokens(pid)
	if err != nil {
		return nil, "", err
	}
	if len(file.Tokens) >= maxProjectTokens {
		return nil, "", ErrTokenCapacity
	}
	var random [48]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, "", err
	}
	id := hex.EncodeToString(random[:16])
	now := time.Now().UTC()
	metadata := ProjectToken{ID: id, ProjectID: pid, Name: name, Role: role, CreatedAt: now, ExpiresAt: now.Add(lifetime), CreatedBy: actor}
	value := "dmt1." + base64.RawURLEncoding.EncodeToString([]byte(pid)) + "." + id + "." + hex.EncodeToString(random[16:])
	digest := sha256.Sum256([]byte(value))
	file.Tokens = append(file.Tokens, tokenRecord{ProjectToken: metadata, SHA256: hex.EncodeToString(digest[:])})
	if err := s.saveTokens(pid, file); err != nil {
		return nil, "", err
	}
	return &metadata, value, nil
}

func (s *Store) RevokeProjectToken(pid, id, actor string) (*ProjectToken, error) {
	if !tokenHex(id, 16) {
		return nil, ErrNotFound
	}
	if !tokenLabel(actor, 256) {
		return nil, ErrInvalidTokenRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.readTokens(pid)
	if err != nil {
		return nil, err
	}
	for i := range file.Tokens {
		token := &file.Tokens[i]
		if token.ID != id {
			continue
		}
		if token.RevokedAt == nil {
			now := time.Now().UTC()
			token.RevokedAt, token.RevokedBy = &now, actor
			if err := s.saveTokens(pid, file); err != nil {
				return nil, err
			}
		}
		return &token.ProjectToken, nil // idempotent, preserving first revocation
	}
	return nil, ErrNotFound
}

func tokenActive(token ProjectToken, now time.Time) bool {
	return token.RevokedAt == nil && !now.Before(token.CreatedAt) && now.Before(token.ExpiresAt)
}

// ActiveProjectToken rechecks durable revocation/expiry for in-flight query and
// SSE identities. It is not an authentication method: the HTTP boundary must
// first verify possession of the secret via AuthenticateProjectToken.
func (s *Store) ActiveProjectToken(pid, id string) (*ProjectToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.readTokens(pid)
	if err != nil {
		return nil, err
	}
	for _, token := range file.Tokens {
		if token.ID == id && tokenActive(token.ProjectToken, time.Now()) {
			return &token.ProjectToken, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) AuthenticateProjectToken(value string) (*ProjectToken, error) {
	if len(value) > 512 {
		return nil, ErrInvalidToken
	}
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != "dmt1" || !tokenHex(parts[2], 16) || !tokenHex(parts[3], 32) {
		return nil, ErrInvalidToken
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	pid := string(decoded)
	if err != nil || !validID(pid) || base64.RawURLEncoding.EncodeToString(decoded) != parts[1] {
		return nil, ErrInvalidToken
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.readTokens(pid)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(value))
	for _, token := range file.Tokens {
		if token.ID != parts[2] {
			continue
		}
		want, _ := hex.DecodeString(token.SHA256) // validated when reading
		if subtle.ConstantTimeCompare(digest[:], want) == 1 && tokenActive(token.ProjectToken, time.Now()) {
			return &token.ProjectToken, nil
		}
		break
	}
	return nil, ErrInvalidToken
}
