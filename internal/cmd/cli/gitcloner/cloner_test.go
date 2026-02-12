package gitcloner

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/google/go-cmp/cmp"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule"
)

type fakeGetter struct{}

func (fakeGetter) Get(_ string, appID, instID int64, key []byte) (*httpgit.BasicAuth, error) {
	return &httpgit.BasicAuth{
		Username: "x-access-token",
		Password: "token",
	}, nil
}

func TestCloneRepo(t *testing.T) {
	const (
		passwordFile        = "passFile"
		passwordFileContent = "1234"
		sshPrivateKeyFile   = "sshFile"

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
		githubAppKeyFile = "githubAppKeyFile"
	)
	var (
		pathCalled                 string
		isBareCalled               bool
		cloneOptsCalled            *git.CloneOptions
		updateSubmodulesCalled     bool
		updateSubmodulesOptsCalled *git.SubmoduleUpdateOptions
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
		if name == githubAppKeyFile {
			return []byte(sshPrivateKeyFileContent), nil
		}
		return nil, errors.New("file not found")
	}
	fileStat = func(name string) (os.FileInfo, error) {
		if name == githubAppKeyFile {
			return nil, nil
		}
		return nil, errors.New("file not found")
	}
	updateSubmodules = func(r *git.Repository, opts *git.SubmoduleUpdateOptions) error {
		updateSubmodulesCalled = true
		updateSubmodulesOptsCalled = opts
		return nil
	}
	origGetter := appAuthGetter
	appAuthGetter = fakeGetter{}
	defer func() {
		plainClone = git.PlainClone
		readFile = os.ReadFile
		fileStat = os.Stat
		appAuthGetter = origGetter
		updateSubmodules = submodule.UpdateSubmodules
	}()

	expectedSubmoduleUpdateOpts := &git.SubmoduleUpdateOptions{
		Init:              true,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
		Depth:             1,
	}

	tests := map[string]struct {
		opts                        *GitCloner
		expectedCloneOpts           *git.CloneOptions
		expectedSubmoduleUpdateOpts *git.SubmoduleUpdateOptions
		expectedErr                 error
	}{
		"branch no auth": {
			opts: &GitCloner{
				Repo:   "https://repo",
				Path:   "path",
				Branch: "master",
			},
			expectedCloneOpts: &git.CloneOptions{
				URL:               "https://repo",
				Depth:             1,
				SingleBranch:      true,
				ReferenceName:     "master",
				RecurseSubmodules: git.NoRecurseSubmodules,
				Tags:              git.NoTags,
			},
			expectedSubmoduleUpdateOpts: expectedSubmoduleUpdateOpts,
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
				Depth:         1,
				SingleBranch:  true,
				ReferenceName: "master",
				Auth: &httpgit.BasicAuth{
					Username: "user",
					Password: passwordFileContent,
				},
				RecurseSubmodules: git.NoRecurseSubmodules,
				Tags:              git.NoTags,
			},
			expectedSubmoduleUpdateOpts: &git.SubmoduleUpdateOptions{
				Init:              true,
				RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
				Depth:             1,
				Auth: &httpgit.BasicAuth{
					Username: "user",
					Password: passwordFileContent,
				},
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
				Depth:             1,
				SingleBranch:      true,
				ReferenceName:     "master",
				Auth:              sshAuth,
				RecurseSubmodules: git.NoRecurseSubmodules,
				Tags:              git.NoTags,
			},
			expectedSubmoduleUpdateOpts: &git.SubmoduleUpdateOptions{
				Init:              true,
				RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
				Depth:             1,
				Auth:              sshAuth,
			},
		},
		"branch github app auth": {
			opts: &GitCloner{
				Repo:                  "https://repo",
				Path:                  "path",
				Branch:                "master",
				GitHubAppID:           123,
				GitHubAppInstallation: 456,
				GitHubAppKeyFile:      githubAppKeyFile,
			},
			expectedCloneOpts: &git.CloneOptions{
				URL:           "https://repo",
				Depth:         1,
				SingleBranch:  true,
				ReferenceName: "master",
				Auth: &httpgit.BasicAuth{
					Username: "x-access-token",
					Password: "token",
				},
				RecurseSubmodules: git.NoRecurseSubmodules,
				Tags:              git.NoTags,
			},
			expectedSubmoduleUpdateOpts: &git.SubmoduleUpdateOptions{
				Init:              true,
				RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
				Depth:             1,
				Auth: &httpgit.BasicAuth{
					Username: "x-access-token",
					Password: "token",
				},
			},
		},
		"password file does not exist": {
			opts: &GitCloner{
				Repo:         "https://repo",
				Branch:       "master",
				PasswordFile: "doesntexist",
				Username:     "user",
			},
			expectedCloneOpts:           nil,
			expectedSubmoduleUpdateOpts: nil,
			expectedErr:                 errors.New(`failed to create auth from options for repo="https://repo" branch="master" revision="" path="": file not found`),
		},
		"ca file does not exist": {
			opts: &GitCloner{
				Repo:         "https://repo",
				Branch:       "master",
				CABundleFile: "doesntexist",
			},
			expectedCloneOpts:           nil,
			expectedSubmoduleUpdateOpts: nil,
			expectedErr:                 errors.New(`failed to read CA bundle from file for repo="https://repo" branch="master" revision="" path="": file not found`),
		},
		"ssh private key file does not exist": {
			opts: &GitCloner{
				Repo:              "https://repo",
				Branch:            "master",
				SSHPrivateKeyFile: "doesntexist",
			},
			expectedCloneOpts:           nil,
			expectedSubmoduleUpdateOpts: nil,
			expectedErr:                 errors.New(`failed to create auth from options for repo="https://repo" branch="master" revision="" path="": file not found`),
		},
		"github app key file does not exist": {
			opts: &GitCloner{
				Repo:                  "https://repo",
				Branch:                "master",
				GitHubAppID:           123,
				GitHubAppInstallation: 456,
				GitHubAppKeyFile:      "doesntexist",
			},
			expectedCloneOpts:           nil,
			expectedSubmoduleUpdateOpts: nil,
			expectedErr:                 errors.New(`failed to create auth from options for repo="https://repo" branch="master" revision="" path="": failed to resolve GitHub app private key from path: file not found`),
		},
	}

	for name, test := range tests {
		// clear mock vars
		pathCalled = ""
		cloneOptsCalled = nil
		updateSubmodulesCalled = false
		updateSubmodulesOptsCalled = nil

		t.Run(name, func(t *testing.T) {
			c := Cloner{}
			err := c.CloneRepo(test.opts)
			if test.expectedErr == nil && err != nil {
				t.Fatalf("err unexpected: %v", err)
			}
			if test.expectedErr != nil {
				if err == nil {
					t.Fatalf("err expected to be [%v], got [%v]", test.expectedErr, err)
				}
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
			if test.expectedSubmoduleUpdateOpts != nil {
				if !updateSubmodulesCalled {
					t.Fatal("expected updateSubmodules to be called, but it wasn't")
				}
				if !cmp.Equal(updateSubmodulesOptsCalled, test.expectedSubmoduleUpdateOpts, sshKeyComparer) {
					t.Fatalf("expected submodule update options %v, got %v", test.expectedSubmoduleUpdateOpts, updateSubmodulesOptsCalled)
				}
			} else if updateSubmodulesCalled {
				t.Fatal("expected updateSubmodules NOT to be called, but it was")
			}

			if !cmp.Equal(transport.UnsupportedCapabilities, []capability.Capability{capability.ThinPack}) {
				t.Errorf("expected transport.UnsupportedCapabilities []capability.Capability{capability.ThinPack}, got %v", transport.UnsupportedCapabilities)
			}
		})
	}
}

func TestCloneRepo_SubmoduleUpdateError(t *testing.T) {
	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		return &git.Repository{}, nil
	}
	updateSubmodules = func(r *git.Repository, opts *git.SubmoduleUpdateOptions) error {
		return errors.New("submodule update failed")
	}
	defer func() {
		plainClone = git.PlainClone
		updateSubmodules = submodule.UpdateSubmodules
	}()

	c := Cloner{}
	err := c.CloneRepo(&GitCloner{
		Repo:   "https://repo",
		Path:   "path",
		Branch: "master",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "submodule update failed" {
		t.Fatalf("expected error 'submodule update failed', got '%s'", err.Error())
	}
}

func TestCloneRepo_CloneError(t *testing.T) {
	var updateSubmodulesCalled bool

	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		return nil, errors.New("clone failed")
	}
	updateSubmodules = func(r *git.Repository, opts *git.SubmoduleUpdateOptions) error {
		updateSubmodulesCalled = true
		return nil
	}
	defer func() {
		plainClone = git.PlainClone
		updateSubmodules = submodule.UpdateSubmodules
	}()

	c := Cloner{}
	err := c.CloneRepo(&GitCloner{
		Repo:   "https://repo",
		Path:   "path",
		Branch: "master",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if updateSubmodulesCalled {
		t.Fatal("expected updateSubmodules NOT to be called when clone fails")
	}
}

func TestCloneRevision(t *testing.T) {
	var (
		cloneOptsCalled            *git.CloneOptions
		updateSubmodulesCalled     bool
		updateSubmodulesOptsCalled *git.SubmoduleUpdateOptions
	)

	// Create a temp directory with a real git repo for testing
	tempDir := t.TempDir()

	// Initialize a real git repository so we can use its methods
	testRepo, err := git.PlainInit(tempDir, false)
	if err != nil {
		t.Fatalf("failed to init test repo: %v", err)
	}

	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		cloneOptsCalled = o
		return testRepo, nil
	}
	updateSubmodules = func(r *git.Repository, opts *git.SubmoduleUpdateOptions) error {
		updateSubmodulesCalled = true
		updateSubmodulesOptsCalled = opts
		return nil
	}

	// We need to mock the repository methods, but go-git doesn't use interfaces
	// So we'll test via integration with a real repo that has a commit
	// First, create a commit in the test repo
	wt, err := testRepo.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}

	// Create a file and commit it
	testFile := tempDir + "/test.txt"
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	if _, err := wt.Add("test.txt"); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}
	commitHash, err := wt.Commit("test commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	defer func() {
		plainClone = git.PlainClone
		updateSubmodules = submodule.UpdateSubmodules
	}()

	tests := map[string]struct {
		opts              *GitCloner
		expectedCloneOpts *git.CloneOptions
		expectedErr       error
	}{
		"revision clone success": {
			opts: &GitCloner{
				Repo:     "https://repo",
				Path:     "path",
				Revision: commitHash.String(),
			},
			expectedCloneOpts: &git.CloneOptions{
				URL:               "https://repo",
				Depth:             1,
				RecurseSubmodules: git.NoRecurseSubmodules,
				Tags:              git.NoTags,
			},
		},
		"revision clone with auth": {
			opts: &GitCloner{
				Repo:         "https://repo",
				Path:         "path",
				Revision:     commitHash.String(),
				Username:     "user",
				PasswordFile: "passFile",
			},
			expectedCloneOpts: &git.CloneOptions{
				URL:   "https://repo",
				Depth: 1,
				Auth: &httpgit.BasicAuth{
					Username: "user",
					Password: "1234",
				},
				RecurseSubmodules: git.NoRecurseSubmodules,
				Tags:              git.NoTags,
			},
		},
	}

	readFile = func(name string) ([]byte, error) {
		if name == "passFile" {
			return []byte("1234"), nil
		}
		return nil, errors.New("file not found")
	}

	for name, test := range tests {
		cloneOptsCalled = nil
		updateSubmodulesCalled = false
		updateSubmodulesOptsCalled = nil

		t.Run(name, func(t *testing.T) {
			c := Cloner{}
			err := c.CloneRepo(test.opts)

			if test.expectedErr != nil {
				if err == nil {
					t.Fatalf("expected error [%v], got nil", test.expectedErr)
				}
				if err.Error() != test.expectedErr.Error() {
					t.Fatalf("expected error [%s], got [%s]", test.expectedErr.Error(), err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify clone options (revision clone doesn't use SingleBranch/ReferenceName)
			if !cmp.Equal(cloneOptsCalled, test.expectedCloneOpts) {
				t.Fatalf("expected clone options %+v, got %+v", test.expectedCloneOpts, cloneOptsCalled)
			}

			// Verify updateSubmodules was called
			if !updateSubmodulesCalled {
				t.Fatal("expected updateSubmodules to be called")
			}

			// Verify submodule update options
			expectedSubmoduleOpts := &git.SubmoduleUpdateOptions{
				Init:              true,
				RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
				Depth:             1,
				Auth:              test.expectedCloneOpts.Auth,
			}
			if !cmp.Equal(updateSubmodulesOptsCalled, expectedSubmoduleOpts) {
				t.Fatalf("expected submodule options %+v, got %+v", expectedSubmoduleOpts, updateSubmodulesOptsCalled)
			}
		})
	}
}

func TestCloneRevision_ResolveRevisionError(t *testing.T) {
	tempDir := t.TempDir()
	testRepo, _ := git.PlainInit(tempDir, false)

	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		return testRepo, nil
	}
	defer func() {
		plainClone = git.PlainClone
	}()

	c := Cloner{}
	err := c.CloneRepo(&GitCloner{
		Repo:     "https://repo",
		Path:     "path",
		Revision: "nonexistent-revision",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to resolve revision") {
		t.Fatalf("expected 'failed to resolve revision' error, got: %s", err.Error())
	}
}

func TestCloneRevision_CloneError(t *testing.T) {
	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		return nil, errors.New("clone failed")
	}
	defer func() {
		plainClone = git.PlainClone
	}()

	c := Cloner{}
	err := c.CloneRepo(&GitCloner{
		Repo:     "https://repo",
		Path:     "path",
		Revision: "abc123",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to clone repo from revision") {
		t.Fatalf("expected 'failed to clone repo from revision' error, got: %s", err.Error())
	}
}

func TestCloneRevision_SubmoduleUpdateError(t *testing.T) {
	tempDir := t.TempDir()
	testRepo, _ := git.PlainInit(tempDir, false)

	// Create a commit
	wt, _ := testRepo.Worktree()
	testFile := tempDir + "/test.txt"
	err := os.WriteFile(testFile, []byte("test"), 0644)
	if err != nil {
		t.Fatalf("failed to create the file %s: %v", testFile, err)
	}
	_, err = wt.Add("test.txt")
	if err != nil {
		t.Fatalf("failed to add the file test.txt: %v", err)
	}
	commitHash, _ := wt.Commit("test commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})

	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		return testRepo, nil
	}
	updateSubmodules = func(r *git.Repository, opts *git.SubmoduleUpdateOptions) error {
		return errors.New("submodule update failed")
	}
	defer func() {
		plainClone = git.PlainClone
		updateSubmodules = submodule.UpdateSubmodules
	}()

	c := Cloner{}
	err = c.CloneRepo(&GitCloner{
		Repo:     "https://repo",
		Path:     "path",
		Revision: commitHash.String(),
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "submodule update failed" {
		t.Fatalf("expected 'submodule update failed', got: %s", err.Error())
	}
}
