// Package names provides functions for truncating and hashing strings and for generating valid k8s resource names.
package names

import (
	"crypto/md5" //nolint:gosec // Non-crypto use
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/sirupsen/logrus"
)

var (
	disallowedChars = regexp.MustCompile(`[^a-zA-Z0-9-]+`)
	helmReleaseName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	dnsLabelSafe    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	multiDash       = regexp.MustCompile("-+")
)

// Limit the length of a string to count characters. If the string's length is
// greater than count, it will be truncated and a separator will be appended to
// the end, followed by a hash.
// If the last character of the truncated string is the separator, then the
// separator itself is omitted. This prevents the result from containing two
// consecutive separators. In such a case, the result will be [count -1]
// characters long.
// If count is too small to include the shortened hash the string is simply
// truncated.
func Limit(s string, count int) string {
	if len(s) <= count {
		return s
	}

	const hexLen int = 5
	separator := "-"

	if count <= hexLen+len(separator) {
		return s[:count]
	}

	nbCharsBeforeTrim := count - hexLen - len(separator)

	// If the last character of the truncated string is the separator, include it instead of the separator.
	if string(s[nbCharsBeforeTrim-1]) == separator {
		separator = ""
	}

	return fmt.Sprintf("%s%s%s", s[:nbCharsBeforeTrim], separator, Hex(s, hexLen))
}

// Hex returns a hex-encoded hash of the string and truncates it to length.
// Warning: truncating the 32 character hash makes collisions more likely.
func Hex(s string, length int) string {
	h := md5.Sum([]byte(s)) //nolint:gosec // Non-crypto use
	d := hex.EncodeToString(h[:])
	return d[:length]
}

// HelmReleaseName uses the provided string to create a unique name. The
// resulting name is DNS label safe (RFC1123) and complies with Helm's regex
// for release names.
func HelmReleaseName(str string) string {
	needHex := false
	bak := str

	str = strings.ReplaceAll(str, "/", "-")

	// avoid collision from different case
	if str != strings.ToLower(str) {
		needHex = true
	}

	// avoid collision from disallowed characters
	if disallowedChars.MatchString(str) {
		needHex = true
	}

	if needHex {
		// append checksum before cleaning up the string
		str = fmt.Sprintf("%s-%s", str, Hex(str, 8))
	}

	// clean up new name
	str = strings.ToLower(str)
	str = disallowedChars.ReplaceAllLiteralString(str, "-")
	str = multiDash.ReplaceAllString(str, "-")
	str = strings.TrimLeft(str, "-")
	str = strings.TrimRight(str, "-")

	// shorten name to 53 characters, the limit for helm release names
	if helmReleaseName.MatchString(str) && dnsLabelSafe.MatchString(str) {
		short := Limit(str, v1alpha1.MaxHelmReleaseNameLen)
		if short != str {
			logrus.Debugf("shorten bundle name %v to %v", str, short)
		}
		return short
	}

	// if the string ends up empty or otherwise invalid, fall back to just
	// a checksum of the original input
	logrus.Debugf("couldn't derive a valid bundle name, using checksum instead for '%s'", str)
	return Hex(bak, 24)
}
