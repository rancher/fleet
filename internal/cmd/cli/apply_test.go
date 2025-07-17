package cli

import (
	"errors"
	"os"
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

func TestSetEnv(t *testing.T) {
	tests := map[string]struct {
		envValue              string
		knownHostsPath        string
		expectedGitSSHCommand string
		expectedErr           error
	}{
		"unset env var": {
			knownHostsPath:        "/foo/bar",
			expectedGitSSHCommand: "ssh -o UserKnownHostsFile=/foo/bar",
		},
		"set env var without options": {
			envValue:              "ssh",
			knownHostsPath:        "/foo/bar",
			expectedGitSSHCommand: "ssh -o UserKnownHostsFile=/foo/bar",
		},
		"set env var with other options": {
			envValue:              "ssh -o stricthostkeychecking=yes",
			knownHostsPath:        "/foo/bar",
			expectedGitSSHCommand: "ssh -o stricthostkeychecking=yes -o UserKnownHostsFile=/foo/bar",
		},
		"set env var with other options and known hosts file option": {
			envValue:              "ssh -o stricthostkeychecking=yes -o userknownhostsFile=/another/file",
			knownHostsPath:        "/foo/bar",
			expectedGitSSHCommand: "ssh -o stricthostkeychecking=yes -o UserKnownHostsFile=/foo/bar",
		},
		"set env var with other options and known hosts file option specified multiple times": {
			envValue:              "ssh -o userknownhostsFile=/another/file -o UserKnownHostsFile=/yet/another/file -o stricthostkeychecking=yes",
			knownHostsPath:        "/foo/bar",
			expectedGitSSHCommand: "ssh -o stricthostkeychecking=yes -o UserKnownHostsFile=/foo/bar",
		},
	}

	bkpEnv := os.Getenv("GIT_SSH_COMMAND")
	defer os.Setenv("GIT_SSH_COMMAND", bkpEnv)

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			defer os.Unsetenv("GIT_SSH_COMMAND")

			if test.envValue != "" {
				os.Setenv("GIT_SSH_COMMAND", test.envValue)
			} else {
				os.Unsetenv("GIT_SSH_COMMAND")
			}

			restore, err := setEnv(test.knownHostsPath)
			if err != test.expectedErr {
				t.Errorf("expected err %v, got %v", test.expectedErr, err)
			}

			if gitSSHCommand := os.Getenv("GIT_SSH_COMMAND"); gitSSHCommand != test.expectedGitSSHCommand {
				t.Errorf("expected GIT_SSH_COMMAND %q, got %q", test.expectedGitSSHCommand, gitSSHCommand)
			}

			if restoreErr := restore(); restoreErr != nil {
				t.Errorf("expected nil restore error, got %v", restoreErr)
			}

			restoredEnvValue, isSet := os.LookupEnv("GIT_SSH_COMMAND")
			if restoredEnvValue != test.envValue {
				t.Errorf(
					"expected restored GIT_SSH_COMMAND value to be %q, got %t/%q",
					test.envValue,
					isSet,
					restoredEnvValue,
				)
			}
		})
	}
}

func TestWriteTmpKnownHosts(t *testing.T) {
	tests := map[string]struct {
		knownHosts       string
		isSet            bool
		expectFileExists bool
	}{
		"does not write to known hosts file if FLEET_KNOWN_HOSTS is unset": {},
		"does not write to known hosts file if FLEET_KNOWN_HOSTS is empty": {isSet: true},
		"writes FLEET_KNOWN_HOSTS to custom known hosts file if set": {
			knownHosts:       "foo",
			isSet:            true,
			expectFileExists: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if test.isSet {
				if err := os.Setenv("FLEET_KNOWN_HOSTS", test.knownHosts); err != nil {
					t.Errorf("failed to set FLEET_KNOWN_HOSTS env var: %v", err)
				}

				defer os.Unsetenv("FLEET_KNOWN_HOSTS")
			}

			khPath, err := writeTmpKnownHosts()
			if err != nil {
				t.Errorf("expected nil error from writeTmpKnownHosts, got: %v", err)
			}

			if !test.expectFileExists {
				return
			}

			gotKnownHosts, err := os.ReadFile(khPath)
			if err != nil {
				t.Errorf("failed to read known_hosts file: %v", err)
			}

			defer os.RemoveAll(khPath)

			if test.knownHosts != "" {
				if string(gotKnownHosts) != test.knownHosts {
					t.Errorf("known_hosts mismatch: expected\n\t%s\ngot:\n\t%s", test.knownHosts, gotKnownHosts)
				}
			}
		})
	}
}

func TestAddAuthToOpts(t *testing.T) {
	tests := map[string]struct {
		name         string
		apply        Apply
		knownHosts   string
		expectedOpts *apply.Options
		expectedErr  error
	}{
		"Auth is empty if no arguments are provided": {
			apply:        Apply{},
			expectedOpts: &apply.Options{},
			expectedErr:  nil,
		},
		"known_hosts file is populated if the env var is set": {
			apply:        Apply{},
			expectedOpts: &apply.Options{},
			expectedErr:  nil,
			knownHosts:   "foo",
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
			err := test.apply.addAuthToOpts(opts, mockReadFile, false, false)
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
	}

	return nil, errorNotFound
}
