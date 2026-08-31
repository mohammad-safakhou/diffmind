package util

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func StableID(parts ...string) string {
	normParts := make([]string, 0, len(parts))
	for _, p := range parts {
		normParts = append(normParts, strings.ToLower(strings.TrimSpace(p)))
	}
	normalized := strings.Join(normParts, "|")
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(normalized))))
	return hex.EncodeToString(sum[:16])
}
