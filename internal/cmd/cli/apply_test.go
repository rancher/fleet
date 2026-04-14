package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	ssh "github.com/rancher/fleet/internal/ssh"
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
		name                string
		apply               Apply
		knownHosts          string
		helmInsecureSkipTLS bool
		expectedOpts        *apply.Options
		expectedErr         error
	}{
		"Auth is empty if no arguments are provided": {
			apply:        Apply{},
			expectedOpts: &apply.Options{},
			expectedErr:  nil,
		},
		"FLEET_KNOWN_HOSTS env var sets SSHKnownHosts in opts": {
			apply:        Apply{},
			knownHosts:   "some-known-host",
			expectedOpts: &apply.Options{Auth: bundlereader.Auth{SSHKnownHosts: []byte("some-known-host")}},
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
		"HelmInsecureSkipTLS sets InsecureSkipVerify in opts": {
			apply:               Apply{},
			helmInsecureSkipTLS: true,
			expectedOpts:        &apply.Options{Auth: bundlereader.Auth{InsecureSkipVerify: true}},
			expectedErr:         nil,
		},
		"Error if file doesn't exist": {
			apply:        Apply{HelmCredentialsByPathFile: "notfound"},
			expectedOpts: &apply.Options{},
			expectedErr:  errorNotFound,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if test.knownHosts != "" {
				t.Setenv(ssh.KnownHostsEnvVar, test.knownHosts)
			}
			opts := &apply.Options{}
			err := test.apply.addAuthToOpts(opts, mockReadFile, false, test.helmInsecureSkipTLS)
			if !cmp.Equal(opts, test.expectedOpts) {
				t.Errorf("opts don't match: expected %v, got %v", test.expectedOpts, opts)
			}
			if !errors.Is(err, test.expectedErr) {
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

func TestCurrentCommit(t *testing.T) {
	t.Run("returns empty string for non-git directory", func(t *testing.T) {
		dir := t.TempDir()
		got := currentCommit(dir)
		if got != "" {
			t.Errorf("expected empty string for non-git dir, got %q", got)
		}
	})

	t.Run("returns HEAD commit SHA for git repository", func(t *testing.T) {
		dir := t.TempDir()

		repo, err := gogit.PlainInit(dir, false)
		if err != nil {
			t.Fatalf("PlainInit: %v", err)
		}
		wt, err := repo.Worktree()
		if err != nil {
			t.Fatalf("Worktree: %v", err)
		}

		if err := os.WriteFile(filepath.Join(dir, "README"), []byte("test"), 0600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err = wt.Add("README"); err != nil {
			t.Fatalf("Add: %v", err)
		}
		expected, err := wt.Commit("initial commit", &gogit.CommitOptions{
			Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
		})
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}

		got := currentCommit(dir)
		if got != expected.String() {
			t.Errorf("expected SHA %q, got %q", expected.String(), got)
		}
		if matched, _ := regexp.MatchString(`^[0-9a-f]{40}$`, got); !matched {
			t.Errorf("expected a 40-char hex SHA, got %q", got)
		}
	})

	t.Run("returns HEAD commit SHA from nested subdirectory", func(t *testing.T) {
		dir := t.TempDir()

		repo, err := gogit.PlainInit(dir, false)
		if err != nil {
			t.Fatalf("PlainInit: %v", err)
		}
		wt, err := repo.Worktree()
		if err != nil {
			t.Fatalf("Worktree: %v", err)
		}

		if err := os.WriteFile(filepath.Join(dir, "README"), []byte("test"), 0600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err = wt.Add("README"); err != nil {
			t.Fatalf("Add: %v", err)
		}
		expected, err := wt.Commit("initial commit", &gogit.CommitOptions{
			Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
		})
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}

		subdir := filepath.Join(dir, "some", "nested")
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}

		got := currentCommit(subdir)
		if got != expected.String() {
			t.Errorf("expected SHA %q from nested subdir, got %q", expected.String(), got)
		}
		if matched, _ := regexp.MatchString(`^[0-9a-f]{40}$`, got); !matched {
			t.Errorf("expected a 40-char hex SHA from nested subdir, got %q", got)
		}
	})
}
