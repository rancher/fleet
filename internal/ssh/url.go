package ssh

import (
	"strings"

	giturls "github.com/rancher/fleet/pkg/git-urls"
)

// Is checks if the provided string s is a valid SSH URL, returning a boolean.
func Is(s string) bool {
	url, err := giturls.Parse(s)
	if err != nil {
		return false
	}

	return strings.HasSuffix(url.Scheme, "ssh")
}
