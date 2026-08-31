package util

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

// ContentHash produces a deterministic hex-encoded SHA-256 hash from the
// supplied key parts.  Parts are lower-cased, trimmed and joined with "|"
// before hashing so that identical logical inputs always yield the same ID.
func ContentHash(parts ...string) string {
	normalised := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			normalised = append(normalised, p)
		}
	}
	sort.Strings(normalised)
	payload := strings.Join(normalised, "|")
	h := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", h[:16]) // 128-bit hex prefix for readability
}
