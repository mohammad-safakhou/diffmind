package audit

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Event struct {
	Timestamp time.Time      `json:"timestamp"`
	Action    string         `json:"action"`
	TenantID  string         `json:"tenant_id"`
	Principal string         `json:"principal"`
	Method    string         `json:"method"`
	Path      string         `json:"path"`
	Decision  string         `json:"decision"`
	Reason    string         `json:"reason,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type ExportRequest struct {
	From     *time.Time
	To       *time.Time
	TenantID string
	Encrypt  bool
	KeyB64   string
	KeyID    string
}

type ExportResult struct {
	Path      string `json:"path"`
	Encrypted bool   `json:"encrypted"`
	KeyID     string `json:"key_id,omitempty"`
	Count     int    `json:"count"`
}

func AppendEvent(root string, event Event) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	path := eventsPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir audit dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append audit event: %w", err)
	}
	return nil
}

func ListEvents(root string, tenantID string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 200
	}
	path := eventsPath(root)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Event{}, nil
		}
		return nil, err
	}
	defer f.Close()

	events := make([]Event, 0)
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if tenantID != "" && e.TenantID != tenantID {
			continue
		}
		events = append(events, e)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(events) > limit {
		return events[len(events)-limit:], nil
	}
	return events, nil
}

func PruneEvents(root string, tenantID string, cutoff time.Time) (int, error) {
	path := eventsPath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	lines := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(lines))
	deleted := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			kept = append(kept, line)
			continue
		}
		if tenantID != "" && e.TenantID != tenantID {
			kept = append(kept, line)
			continue
		}
		if !e.Timestamp.Before(cutoff) {
			kept = append(kept, line)
			continue
		}
		deleted++
	}
	payload := strings.Join(kept, "\n")
	if payload != "" {
		payload += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		return 0, err
	}
	return deleted, nil
}

func ExportEvents(root string, req ExportRequest) (ExportResult, error) {
	events, err := ListEvents(root, strings.TrimSpace(req.TenantID), 0)
	if err != nil {
		return ExportResult{}, err
	}
	filtered := make([]Event, 0, len(events))
	for _, e := range events {
		if req.From != nil && e.Timestamp.Before(*req.From) {
			continue
		}
		if req.To != nil && e.Timestamp.After(*req.To) {
			continue
		}
		filtered = append(filtered, e)
	}
	data, err := json.MarshalIndent(map[string]any{
		"generated_at": time.Now().UTC(),
		"count":        len(filtered),
		"events":       filtered,
	}, "", "  ")
	if err != nil {
		return ExportResult{}, err
	}
	data = append(data, '\n')

	exportDir := filepath.Join(root, "audit", "exports")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return ExportResult{}, err
	}
	base := fmt.Sprintf("export-%d", time.Now().UTC().UnixNano())
	path := filepath.Join(exportDir, base+".json")
	result := ExportResult{Path: path, Encrypted: false, Count: len(filtered)}
	if !req.Encrypt {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return ExportResult{}, err
		}
		return result, nil
	}

	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.KeyB64))
	if err != nil {
		return ExportResult{}, fmt.Errorf("decode encryption key: %w", err)
	}
	if len(key) != 32 {
		return ExportResult{}, fmt.Errorf("encryption key must be 32 bytes (base64)")
	}
	enc, err := encryptAESGCM(data, key)
	if err != nil {
		return ExportResult{}, err
	}
	path = filepath.Join(exportDir, base+".json.enc")
	if err := os.WriteFile(path, enc, 0o644); err != nil {
		return ExportResult{}, err
	}
	result.Path = path
	result.Encrypted = true
	result.KeyID = strings.TrimSpace(req.KeyID)
	return result, nil
}

func encryptAESGCM(plain []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plain, nil)
	out := append(nonce, ciphertext...)
	return out, nil
}

func eventsPath(root string) string {
	return filepath.Join(root, "audit", "events.jsonl")
}
