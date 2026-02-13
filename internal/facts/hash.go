package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func HashSnippet(snippet string) string {
	normalized := strings.TrimSpace(snippet)
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
