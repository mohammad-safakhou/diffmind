package audit

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	PrevHash  string         `json:"prev_hash,omitempty"`
	Hash      string         `json:"hash,omitempty"`
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
	Manifest  string `json:"manifest_path,omitempty"`
	Signed    bool   `json:"signed,omitempty"`
}

type ExportManifest struct {
	GeneratedAt        time.Time `json:"generated_at"`
	ExportPath         string    `json:"export_path"`
	Encrypted          bool      `json:"encrypted"`
	KeyID              string    `json:"key_id,omitempty"`
	Count              int       `json:"count"`
	TenantID           string    `json:"tenant_id,omitempty"`
	From               string    `json:"from,omitempty"`
	To                 string    `json:"to,omitempty"`
	PayloadSHA256      string    `json:"payload_sha256"`
	SignatureAlgorithm string    `json:"signature_algorithm,omitempty"`
	SignatureKeyID     string    `json:"signature_key_id,omitempty"`
	Signature          string    `json:"signature,omitempty"`
}

type VerifyExportResult struct {
	Valid          bool     `json:"valid"`
	ManifestPath   string   `json:"manifest_path"`
	ExportPath     string   `json:"export_path,omitempty"`
	Encrypted      bool     `json:"encrypted"`
	Count          int      `json:"count"`
	Signed         bool     `json:"signed"`
	SignatureValid bool     `json:"signature_valid"`
	Issues         []string `json:"issues,omitempty"`
}

type VerifyRequest struct {
	TenantID     string
	EnforceChain bool
}

type VerifyResult struct {
	Valid         bool     `json:"valid"`
	Checked       int      `json:"checked"`
	TenantID      string   `json:"tenant_id,omitempty"`
	ChainEnforced bool     `json:"chain_enforced"`
	Issues        []string `json:"issues,omitempty"`
}

func AppendEvent(root string, event Event) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	event.Hash = ""
	lastHash, err := lastEventHash(root)
	if err != nil {
		return err
	}
	event.PrevHash = lastHash
	hash, err := eventHash(event)
	if err != nil {
		return fmt.Errorf("hash audit event: %w", err)
	}
	event.Hash = hash
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

func VerifyEvents(root string, req VerifyRequest) (VerifyResult, error) {
	path := eventsPath(root)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return VerifyResult{
				Valid:         true,
				Checked:       0,
				TenantID:      strings.TrimSpace(req.TenantID),
				ChainEnforced: req.EnforceChain,
			}, nil
		}
		return VerifyResult{}, err
	}
	defer f.Close()

	res := VerifyResult{
		Valid:         true,
		Checked:       0,
		TenantID:      strings.TrimSpace(req.TenantID),
		ChainEnforced: req.EnforceChain,
		Issues:        []string{},
	}

	expectedPrev := ""
	s := bufio.NewScanner(f)
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			res.Valid = false
			res.Issues = append(res.Issues, fmt.Sprintf("line %d: invalid json", lineNo))
			continue
		}
		if req.TenantID != "" && e.TenantID != req.TenantID {
			continue
		}
		res.Checked++
		if strings.TrimSpace(e.Hash) == "" {
			res.Valid = false
			res.Issues = append(res.Issues, fmt.Sprintf("line %d: missing hash", lineNo))
			continue
		}
		actual := e.Hash
		e.Hash = ""
		expected, err := eventHash(e)
		if err != nil {
			res.Valid = false
			res.Issues = append(res.Issues, fmt.Sprintf("line %d: hash computation failed", lineNo))
			continue
		}
		if actual != expected {
			res.Valid = false
			res.Issues = append(res.Issues, fmt.Sprintf("line %d: hash mismatch", lineNo))
		}
		if req.EnforceChain {
			if e.PrevHash != expectedPrev {
				res.Valid = false
				res.Issues = append(res.Issues, fmt.Sprintf("line %d: prev_hash mismatch", lineNo))
			}
			expectedPrev = actual
		}
	}
	if err := s.Err(); err != nil {
		return VerifyResult{}, err
	}
	return res, nil
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
		manifestPath, signed, err := writeExportManifest(root, result.Path, data, req, result)
		if err != nil {
			return ExportResult{}, err
		}
		result.Manifest = manifestPath
		result.Signed = signed
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
	manifestPath, signed, err := writeExportManifest(root, result.Path, enc, req, result)
	if err != nil {
		return ExportResult{}, err
	}
	result.Manifest = manifestPath
	result.Signed = signed
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

func lastEventHash(root string) (string, error) {
	path := eventsPath(root)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	last := ""
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
		if strings.TrimSpace(e.Hash) != "" {
			last = strings.TrimSpace(e.Hash)
		}
	}
	if err := s.Err(); err != nil {
		return "", fmt.Errorf("scan audit log: %w", err)
	}
	return last, nil
}

func eventHash(e Event) (string, error) {
	payload, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func VerifyExportManifest(manifestPath string) (VerifyExportResult, error) {
	manifestPath = strings.TrimSpace(manifestPath)
	if manifestPath == "" {
		return VerifyExportResult{}, fmt.Errorf("manifest path is required")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return VerifyExportResult{}, err
	}
	var manifest ExportManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return VerifyExportResult{}, fmt.Errorf("decode manifest: %w", err)
	}
	res := VerifyExportResult{
		Valid:          true,
		ManifestPath:   manifestPath,
		ExportPath:     strings.TrimSpace(manifest.ExportPath),
		Encrypted:      manifest.Encrypted,
		Count:          manifest.Count,
		Signed:         strings.TrimSpace(manifest.Signature) != "",
		SignatureValid: false,
		Issues:         []string{},
	}
	if res.ExportPath == "" {
		res.Valid = false
		res.Issues = append(res.Issues, "manifest missing export_path")
		return res, nil
	}
	payload, err := os.ReadFile(res.ExportPath)
	if err != nil {
		res.Valid = false
		res.Issues = append(res.Issues, fmt.Sprintf("read export payload: %v", err))
		return res, nil
	}
	if sha256Hex(payload) != strings.TrimSpace(manifest.PayloadSHA256) {
		res.Valid = false
		res.Issues = append(res.Issues, "payload digest mismatch")
	}
	if res.Signed {
		sigKeyB64 := strings.TrimSpace(os.Getenv("DIFFMIND_AUDIT_MANIFEST_HMAC_KEY_B64"))
		if sigKeyB64 == "" {
			res.Valid = false
			res.Issues = append(res.Issues, "manifest signature present but DIFFMIND_AUDIT_MANIFEST_HMAC_KEY_B64 not set")
		} else {
			key, err := base64.StdEncoding.DecodeString(sigKeyB64)
			if err != nil || len(key) == 0 {
				res.Valid = false
				res.Issues = append(res.Issues, "invalid DIFFMIND_AUDIT_MANIFEST_HMAC_KEY_B64")
			} else if signManifest(manifest, key) != strings.TrimSpace(manifest.Signature) {
				res.Valid = false
				res.Issues = append(res.Issues, "manifest signature mismatch")
			} else {
				res.SignatureValid = true
			}
		}
	}
	return res, nil
}

func writeExportManifest(root string, exportPath string, payload []byte, req ExportRequest, result ExportResult) (string, bool, error) {
	manifest := ExportManifest{
		GeneratedAt:   time.Now().UTC(),
		ExportPath:    strings.TrimSpace(exportPath),
		Encrypted:     result.Encrypted,
		KeyID:         strings.TrimSpace(result.KeyID),
		Count:         result.Count,
		TenantID:      strings.TrimSpace(req.TenantID),
		PayloadSHA256: sha256Hex(payload),
	}
	if req.From != nil {
		manifest.From = req.From.UTC().Format(time.RFC3339)
	}
	if req.To != nil {
		manifest.To = req.To.UTC().Format(time.RFC3339)
	}
	signed := false
	sigKeyB64 := strings.TrimSpace(os.Getenv("DIFFMIND_AUDIT_MANIFEST_HMAC_KEY_B64"))
	if sigKeyB64 != "" {
		key, err := base64.StdEncoding.DecodeString(sigKeyB64)
		if err != nil || len(key) == 0 {
			return "", false, fmt.Errorf("decode DIFFMIND_AUDIT_MANIFEST_HMAC_KEY_B64: expected non-empty base64 key")
		}
		manifest.SignatureAlgorithm = "HMAC-SHA256"
		manifest.SignatureKeyID = strings.TrimSpace(os.Getenv("DIFFMIND_AUDIT_MANIFEST_KEY_ID"))
		manifest.Signature = signManifest(manifest, key)
		signed = true
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", false, err
	}
	manifestData = append(manifestData, '\n')
	manifestPath := filepath.Join(root, "audit", "exports", filepath.Base(exportPath)+".manifest.json")
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		return "", false, err
	}
	return manifestPath, signed, nil
}

func signManifest(manifest ExportManifest, key []byte) string {
	payload := struct {
		ExportPath    string `json:"export_path"`
		Encrypted     bool   `json:"encrypted"`
		KeyID         string `json:"key_id,omitempty"`
		Count         int    `json:"count"`
		TenantID      string `json:"tenant_id,omitempty"`
		From          string `json:"from,omitempty"`
		To            string `json:"to,omitempty"`
		PayloadSHA256 string `json:"payload_sha256"`
	}{
		ExportPath:    manifest.ExportPath,
		Encrypted:     manifest.Encrypted,
		KeyID:         manifest.KeyID,
		Count:         manifest.Count,
		TenantID:      manifest.TenantID,
		From:          manifest.From,
		To:            manifest.To,
		PayloadSHA256: manifest.PayloadSHA256,
	}
	body, _ := json.Marshal(payload)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
