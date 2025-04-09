package ssh_test

import (
	"testing"

	"github.com/rancher/fleet/internal/ssh"
)

func TestIs(t *testing.T) {
	tests := map[string]struct {
		url       string
		expectSSH bool
	}{
		"http": {
			url:       "http://foo/bar",
			expectSSH: false,
		},
		"ftp": {
			url:       "ftp://foo/bar",
			expectSSH: false,
		},
		"http with @": {
			url:       "http://fleet-ci:foo@git-service.fleet-local.svc.cluster.local:8080/repo",
			expectSSH: false,
		},
		"simple ssh": {
			url:       "ssh://foo/bar",
			expectSSH: true,
		},
		"git ssh with @": {
			url:       "git@github.com:foo/bar.git",
			expectSSH: true,
		},
		"git+ssh": {
			url:       "git+ssh://foo/bar.git",
			expectSSH: true,
		},
		"invalid with ssh": {
			url:       "sshfoo://foo/bar.git",
			expectSSH: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			isSSH := ssh.Is(test.url)

			if isSSH != test.expectSSH {
				t.Errorf("expected SSH match to be %t, got %t", test.expectSSH, isSSH)
			}
		})
	}
}
