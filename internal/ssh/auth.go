package ssh

import (
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	golangssh "golang.org/x/crypto/ssh"
)

// NewSSHPublicKeys creates a go-git SSH auth method from the given PEM key and
// optional known_hosts bytes. If knownHosts is empty, InsecureIgnoreHostKey is
// used as the host key callback, matching Fleet's default behaviour across all
// git cloning code paths.
func NewSSHPublicKeys(user string, keyPEM []byte, knownHosts []byte) (*gossh.PublicKeys, error) {
	pubKeys, err := gossh.NewPublicKeys(user, keyPEM, "")
	if err != nil {
		return nil, err
	}
	if len(knownHosts) > 0 {
		pubKeys.HostKeyCallback, err = CreateKnownHostsCallBack(knownHosts)
		if err != nil {
			return nil, err
		}
	} else {
		//nolint:gosec // G106: InsecureIgnoreHostKey is the intentional fallback
		// when no known_hosts are provided; matches behaviour across gitcloner,
		// bundlereader, and pkg/git.
		pubKeys.HostKeyCallback = golangssh.InsecureIgnoreHostKey()
	}
	return pubKeys, nil
}
