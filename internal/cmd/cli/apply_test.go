package cli

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/google/go-cmp/cmp"
	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	"sigs.k8s.io/yaml"
)

const (
	username = "user"

	password_file    = "password_file"
	password_content = "pass"

	caCerts_file    = "caCerts_file"
	caCerts_content = "caCerts"

	sshPrivateKey_file    = "sshPrivateKey_file"
	sshPrivateKey_content = `-----BEGIN RSA PRIVATE KEY-----
MIICWgIBAAKBgGTqdoXqegaD8lhf2N/OtRsBSS819XfJ3kTzEWiv8vum2i691ZB1
p9ci7sQwcsazseWbVGsUGLDesfkyiay+vE2qpsHF3wZjb0tdf6GxdloFu5rrK026
3DPWtVv53HnZS+2xXlRpuo+K5gi5ANcY3ZE4RDhymbeRKMJgZTatgAivAgMBAAEC
gYBDLNOytu99cM2cSEkMSgPCMyvtMkTw9T5wtUCMaDsdiubHoHQOElOkYMuBayKr
5CfySGB8WsdIzSS5VgvRIrIjQh/9H2bTjYSfTidS+750gbtIkJImK9IgACMxZHEL
PHiOOeLVM4Dp8PMM0peX6zDS5wuhFJmQHBXqnuHcE7ZnYQJBAMciaho33v5jhUWL
tLOSmYL3muiOHXzEbCIcbznkzBkJptFPR8KKNye/XQYifYBlrobsdt9SKrGbdIJG
BrXSU9ECQQCBu9kn44gxxkifAvKQ7qvWB9osRiWjjuBMKnPzCSx3Pf6unnAxe+JI
6ha7FmNkD5tuHpfkZcvyWP8upfNhgjR/AkBl8eFdwMKhezOMMgR1dhSu7rHYYoEI
EcrF/8aVXeN64e0L9Mlo97da2uX1sQyNAgFCQ6Zrl7YRrOMNmmnvBVkxAkAGG16c
hxRpK2lNuujKM8H5AEOf4+lvqpEaZMEyhpMGRe/QLnsfiTJctlA9nE8vbaCmbWA/
Cx+vl8rjWkJ7q5JnAkAEAf5e4xGkia0Tmphb7d12eW7BBgLzFDZwWOFLPI/GOG8I
hQjLk9jcmo+WMSGJyd0ewpjskLsY0PO8zMhr5AGl
-----END RSA PRIVATE KEY-----`

	helmSecretsNameByPath_file = "helmSecretsNameByPath_file"
)

var helmSecretsNameByPath_content = map[string]bundlereader.Auth{"path": {Username: username, Password: password_content}}

func TestAddAuthToOpts(t *testing.T) {
	tests := map[string]struct {
		name         string
		apply        Apply
		expectedOpts *apply.Options
		expectedErr  error
	}{
		"Auth is empty if no arguments are provided": {
			apply:        Apply{},
			expectedOpts: &apply.Options{},
			expectedErr:  nil,
		},
		"Auth contains values from username, password, caCerts and sshPrivatey when helmSecretsNameByPath not provided": {
			apply:        Apply{PasswordFile: password_file, Username: username, CACertsFile: caCerts_file, SSHPrivateKeyFile: sshPrivateKey_file},
			expectedOpts: &apply.Options{Auth: bundlereader.Auth{Username: username, Password: password_content, CABundle: []byte(caCerts_content), SSHPrivateKey: []byte(sshPrivateKey_content)}},
			expectedErr:  nil,
		},
		"AuthByPath contains values from HelmCredentialsByPathFile if provided": {
			apply:        Apply{HelmCredentialsByPathFile: helmSecretsNameByPath_file},
			expectedOpts: &apply.Options{AuthByPath: helmSecretsNameByPath_content},
			expectedErr:  nil,
		},
		"HelmCredentialsByPathFile has priority over username and password for a generic helm secret if both are provided": {
			apply:        Apply{HelmCredentialsByPathFile: helmSecretsNameByPath_file, PasswordFile: password_file, Username: username, CACertsFile: caCerts_file, SSHPrivateKeyFile: sshPrivateKey_file},
			expectedOpts: &apply.Options{AuthByPath: helmSecretsNameByPath_content},
			expectedErr:  nil,
		},
		"Error if file doesn't exist": {
			apply:        Apply{HelmCredentialsByPathFile: "notfound"},
			expectedOpts: &apply.Options{},
			expectedErr:  errorNotFound,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			opts := &apply.Options{}
			err := test.apply.addHelmAuthToOpts(opts, mockReadFile)
			if !cmp.Equal(opts, test.expectedOpts) {
				t.Errorf("opts don't match: expected %v, got %v", test.expectedOpts, opts)
			}
			if err != test.expectedErr {
				t.Errorf("errors don't match: expected %v, got %v", test.expectedErr, err)
			}
		})
	}
}

func TestAddGitAuthToOpts(t *testing.T) {
	sshAuth, _ := gossh.NewPublicKeys("git", []byte(sshPrivateKey_content), "")
	sshKeyComparer := cmp.Comparer(func(x, y gossh.PublicKeys) bool {
		return x.User == y.User &&
			x.Signer.PublicKey().Type() == y.Signer.PublicKey().Type() &&
			cmp.Equal(x.Signer.PublicKey().Marshal(), y.Signer.PublicKey().Marshal())
	})

	tests := map[string]struct {
		name         string
		apply        Apply
		expectedOpts *apply.Options
		expectedErr  error
	}{
		"GitAuth is empty if no arguments are provided": {
			apply:        Apply{},
			expectedOpts: &apply.Options{},
			expectedErr:  nil,
		},
		"GitAuth contains basic auth from git username and password": {
			apply:        Apply{GitPasswordFile: password_file, GitUsername: username},
			expectedOpts: &apply.Options{GitAuth: &httpgit.BasicAuth{Username: username, Password: password_content}},
			expectedErr:  nil,
		},
		"GitAuth contains ssh auth from git parameters": {
			apply:        Apply{GitRepo: "ssh://git@localhost/test/test-repo", GitSSHPrivateKey: sshPrivateKey_file},
			expectedOpts: &apply.Options{GitAuth: sshAuth},
			expectedErr:  nil,
		},
		"Opts contains caBundle": {
			apply:        Apply{GitCABundleFile: caCerts_file},
			expectedOpts: &apply.Options{GitCABundle: []byte(caCerts_content)},
			expectedErr:  nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			opts := &apply.Options{}
			err := test.apply.addGitAuthToOpts(opts, mockReadFile)
			fmt.Println(reflect.TypeOf(opts.GitAuth))
			if !cmp.Equal(opts, test.expectedOpts, sshKeyComparer) {
				t.Errorf("opts don't match: expected %v, got %v", test.expectedOpts, opts)
			}
			if err != test.expectedErr {
				t.Errorf("errors don't match: expected %v, got %v", test.expectedErr, err)
			}
		})
	}
}

var errorNotFound = errors.New("not found")

func mockReadFile(name string) ([]byte, error) {
	switch name {
	case helmSecretsNameByPath_file:
		b, err := yaml.Marshal(helmSecretsNameByPath_content)
		if err != nil {
			return nil, err
		}
		return b, nil
	case password_file:
		return []byte(password_content), nil
	case caCerts_file:
		return []byte(caCerts_content), nil
	case sshPrivateKey_file:
		return []byte(sshPrivateKey_content), nil
	}

	return nil, errorNotFound
}
