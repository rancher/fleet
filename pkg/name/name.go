// Package name provides functions for truncating and hashing strings and for generating valid k8s resource names.
package name

import (
	"crypto/md5" // nolint:gosec // Non-crypto use
	"encoding/hex"
	"fmt"
)

// Limit the length of a string to count characters. If the string's length is
// greater than count, it will be truncated and a hash will be appended to the
// end.
// If count is too small to include the shortened hash the string is simply
// truncated.
func Limit(s string, count int) string {
	if len(s) <= count {
		return s
	}

	if count <= 6 {
		return s[:count]
	}
	return fmt.Sprintf("%s-%s", s[:count-6], Hex(s, 5))
}

// Hex returns a hex-encoded hash of the string and truncates it to length.
// Warning: truncating the 32 character hash makes collisions more likely.
func Hex(s string, length int) string {
	h := md5.Sum([]byte(s)) // nolint:gosec // Non-crypto use
	d := hex.EncodeToString(h[:])
	return d[:length]
}
