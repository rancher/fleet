package cli

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
)

const (
	username = "user"

	password_file    = "password_file"
	password_content = "pass"

	caCerts_file    = "caCerts_file"
	caCerts_content = "caCerts"

	sshPrivateKey_file    = "sshPrivateKey_file"
	sshPrivateKey_content = "sshPrivateKey"

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
		"Auth contains values from OpenSSH private key when provided": {
			apply:        Apply{SSHPrivateKeyFile: "openssh_private_key_file"},
			expectedOpts: &apply.Options{Auth: bundlereader.Auth{SSHPrivateKey: []byte("openssh_private_key_content")}},
			expectedErr:  nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			opts := &apply.Options{}
			err := test.apply.addAuthToOpts(opts, mockReadFile)
			if !cmp.Equal(opts, test.expectedOpts) {
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
	case "openssh_private_key_file":
		return []byte("openssh_private_key_content"), nil
	}

	return nil, errorNotFound
}
