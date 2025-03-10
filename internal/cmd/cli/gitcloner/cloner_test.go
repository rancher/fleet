package gitcloner

import (
	"errors"
	"os"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/google/go-cmp/cmp"
)

func TestCloneRepo(t *testing.T) {
	const (
		passwordFile        = "passFile"
		passwordFileContent = "1234"
		sshPrivateKeyFile   = "sshFile"
		//nolint:gosec // it's only test data
		sshPrivateKeyFileContent = `-----BEGIN RSA PRIVATE KEY-----
MIICXQIBAAKBgQC1ZuFGlFeAFqeS6p04QsliOXG3NH1/lQC4UMXdQ0F73ciYBPKq
iQZcoyOu8a2Hsi5HvxDqR1rreTAkJ37C3ErrmKcE1CUJwxBVqkgE17Fzw63QBu0X
0OVtaUarG8Pd9HuKbXPK8HXFTEh6F5hoqmzCmG7cRHmagBeh1SqZm1awzQIDAQAB
AoGAChHZ84cMjGm1h6xKafMbJr61l0vso4Zr8c9aDHxNSEj5d6beqaTNm5rawj1c
Oqojc4whrj+jxmqFx5wBp2N/LRi7GhpPco4wy8gg2t/OjmcR+jTRJgT1x1Co9W58
U+O5c001YFTNoa1UUUBweqye/sX/k5GBCUt0V2G839Cn+8ECQQD2K2eZcyUeeBHT
/YhGAq++mmfVEkzMY7U+G59oeF038zXX+wtMwoKmC9/LHwVPWpnzL/oMu3zZqv4a
jzCOAdZpAkEAvKVas8KUctHUBvDoU6hq9bVyIZMZZnlBfysuFEeJLU8efp/n4KRO
93EyhcXe2FmOC/VzGbkiQobmAqVvIwTixQJBAIKYZE20GG0hpdOhHTqHElU79PnE
y5ljDDP204rI0Ctui5IZTNVcG5ObmQ5ZVqfSmPm66hz3GjUf0c6lSE0ODIECQHB0
silO6We5JggtPJICaCCpVawmIJIx3pWMjB+StXfJHoilknkb+ecQF+ofFsUqPb9r
Rn4jGwVFnYAeVq4tj3ECQQCyeMeCprz5AQ8HSd16Asd3zhv7N7olpb4XMIP6YZXy
udiSlDctMM/X3ZM2JN5M1rtAJ2WR3ZQtmWbOjZAbG2Eq
-----END RSA PRIVATE KEY-----`
	)
	var (
		pathCalled      string
		isBareCalled    bool
		cloneOptsCalled *git.CloneOptions
	)

	sshAuth, _ := gossh.NewPublicKeys("git", []byte(sshPrivateKeyFileContent), "")
	sshKeyComparer := cmp.Comparer(func(x, y gossh.PublicKeys) bool {
		return x.User == y.User &&
			x.Signer.PublicKey().Type() == y.Signer.PublicKey().Type() &&
			cmp.Equal(x.Signer.PublicKey().Marshal(), y.Signer.PublicKey().Marshal())
	})
	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		pathCalled = path
		isBareCalled = isBare
		cloneOptsCalled = o

		return &git.Repository{}, nil
	}
	readFile = func(name string) ([]byte, error) {
		if name == passwordFile {
			return []byte(passwordFileContent), nil
		}
		if name == sshPrivateKeyFile {
			return []byte(sshPrivateKeyFileContent), nil
		}
		return nil, errors.New("file not found")
	}
	defer func() {
		plainClone = git.PlainClone
		readFile = os.ReadFile
	}()

	tests := map[string]struct {
		opts              *GitCloner
		expectedCloneOpts *git.CloneOptions
		expectedErr       error
	}{
		"branch no auth": {
			opts: &GitCloner{
				Repo:   "https://repo",
				Path:   "path",
				Branch: "master",
			},
			expectedCloneOpts: &git.CloneOptions{
				URL:               "https://repo",
				SingleBranch:      true,
				ReferenceName:     "master",
				RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			},
		},
		"branch basic auth": {
			opts: &GitCloner{
				Repo:         "https://repo",
				Path:         "path",
				Branch:       "master",
				Username:     "user",
				PasswordFile: passwordFile,
			},
			expectedCloneOpts: &git.CloneOptions{
				URL:           "https://repo",
				SingleBranch:  true,
				ReferenceName: "master",
				Auth: &httpgit.BasicAuth{
					Username: "user",
					Password: passwordFileContent,
				},
				RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			},
		},
		"branch ssh auth": {
			opts: &GitCloner{
				Repo:              "ssh://git@localhost/test/test-repo",
				Path:              "path",
				Branch:            "master",
				SSHPrivateKeyFile: sshPrivateKeyFile,
			},
			expectedCloneOpts: &git.CloneOptions{
				URL:               "ssh://git@localhost/test/test-repo",
				SingleBranch:      true,
				ReferenceName:     "master",
				Auth:              sshAuth,
				RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			},
		},
		"password file does not exist": {
			opts: &GitCloner{
				Repo:         "https://repo",
				Branch:       "master",
				PasswordFile: "doesntexist",
				Username:     "user",
			},
			expectedCloneOpts: nil,
			expectedErr:       errors.New(`failed to create auth from options for repo="https://repo" branch="master" revision="" path="": file not found`),
		},
		"ca file does not exist": {
			opts: &GitCloner{
				Repo:         "https://repo",
				Branch:       "master",
				CABundleFile: "doesntexist",
			},
			expectedCloneOpts: nil,
			expectedErr:       errors.New(`failed to read CA bundle from file for repo="https://repo" branch="master" revision="" path="": file not found`),
		},
		"ssh private key file does not exist": {
			opts: &GitCloner{
				Repo:              "https://repo",
				Branch:            "master",
				SSHPrivateKeyFile: "doesntexist",
			},
			expectedCloneOpts: nil,
			expectedErr:       errors.New(`failed to create auth from options for repo="https://repo" branch="master" revision="" path="": file not found`),
		},
	}

	for name, test := range tests {
		// clear mock vars
		pathCalled = ""
		cloneOptsCalled = nil

		t.Run(name, func(t *testing.T) {
			c := Cloner{}
			err := c.CloneRepo(test.opts)
			if err == nil && test.expectedErr != nil {
				t.Fatalf("err expected to be [%v], got [%v]", test.expectedErr, err)
			} else if test.expectedErr != nil && err == nil {
				t.Fatalf("err expected to be [%v], got [%v]", test.expectedErr, err)
			} else if test.expectedErr != nil && err != nil {
				if !cmp.Equal(test.expectedErr.Error(), err.Error()) {
					t.Fatalf("err expected to be [%s], got [%s]", test.expectedErr.Error(), err.Error())
				}
			}

			if pathCalled != test.opts.Path {
				t.Fatalf("path expected to be %v, got %v", test.opts.Path, pathCalled)
			}

			if isBareCalled {
				t.Fatalf("isBareCalled expected to be false, got %v", isBareCalled)
			}

			if !cmp.Equal(cloneOptsCalled, test.expectedCloneOpts, sshKeyComparer) {
				t.Fatalf("expected options %v, got %v", test.expectedCloneOpts, cloneOptsCalled)
			}

			if !cmp.Equal(transport.UnsupportedCapabilities, []capability.Capability{capability.ThinPack}) {
				t.Errorf("expected transport.UnsupportedCapabilities []capability.Capability{capability.ThinPack}, got %v", transport.UnsupportedCapabilities)
			}
		})
	}
}
