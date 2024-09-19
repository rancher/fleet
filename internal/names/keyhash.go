package names

import (
	"crypto/sha256"
	"encoding/hex"
)

// KeyHash returns the first 12 hex characters of the hash of the first 100 chars
// of the input string
func KeyHash(s string) string {
	if len(s) > 100 {
		s = s[:100]
	}
	d := sha256.Sum256([]byte(s))
	return hex.EncodeToString(d[:])[:12]
}
