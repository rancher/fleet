package gitcloner

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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

// initTestRepoWithCommit returns a real go-git repo containing a single
// commit, plus the commit hash. A populated *git.Repository is required
// because the full-clone fallback (fullCloneRevision) calls ResolveRevision
// and Worktree on the value returned from plainClone, and because
// TestCloneCommitShallow_Body fetches from it as a source repository; an
// empty stub would not satisfy either.
func initTestRepoWithCommit(t *testing.T) (*git.Repository, plumbing.Hash) {
	t.Helper()
	tempDir := t.TempDir()
	r, err := git.PlainInit(tempDir, false)
	if err != nil {
		t.Fatalf("failed to init test repo: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}
	if err := os.WriteFile(tempDir+"/test.txt", []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	if _, err := wt.Add("test.txt"); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}
	h, err := wt.Commit("test commit", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("failed to commit: %v", err)
	}
	return r, h
}

// TestCloneRevision_TagShortcut covers the cheap path: cloneRevision first
// tries refs/tags/<revision> with Depth: 1, and on success skips the
// remaining fallbacks.
func TestCloneRevision_TagShortcut(t *testing.T) {
	testRepo, _ := initTestRepoWithCommit(t)

	var calls []*git.CloneOptions
	var submoduleOpts *git.SubmoduleUpdateOptions
	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		calls = append(calls, o)
		return testRepo, nil
	}
	updateSubmodules = func(r *git.Repository, opts *git.SubmoduleUpdateOptions) error {
		submoduleOpts = opts
		return nil
	}
	defer func() {
		plainClone = git.PlainClone
		updateSubmodules = submodule.UpdateSubmodules
	}()

	c := Cloner{}
	if err := c.CloneRepo(&GitCloner{
		Repo:     "https://repo",
		Path:     t.TempDir(),
		Revision: "v1.2.3",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 clone attempt, got %d", len(calls))
	}
	got := calls[0]
	if got.ReferenceName != plumbing.NewTagReferenceName("v1.2.3") {
		t.Errorf("expected ReferenceName refs/tags/v1.2.3, got %q", got.ReferenceName)
	}
	if !got.SingleBranch {
		t.Error("expected SingleBranch=true on the tag-shortcut clone")
	}
	if got.Depth != 1 {
		t.Errorf("expected Depth=1, got %d", got.Depth)
	}
	if got.Tags != git.NoTags {
		t.Errorf("expected Tags=NoTags, got %v", got.Tags)
	}
	if got.RecurseSubmodules != git.NoRecurseSubmodules {
		t.Errorf("expected RecurseSubmodules=NoRecurseSubmodules, got %v", got.RecurseSubmodules)
	}
	if submoduleOpts == nil {
		t.Fatal("expected updateSubmodules to be called")
	}
}

// TestCloneRevision_BranchShortcut covers the case where the revision names
// a branch — the tag attempt fails, then the branch attempt succeeds.
func TestCloneRevision_BranchShortcut(t *testing.T) {
	testRepo, _ := initTestRepoWithCommit(t)

	var calls []*git.CloneOptions
	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		calls = append(calls, o)
		// Fail the tag attempt, succeed the branch attempt.
		if o.ReferenceName.IsTag() {
			return nil, errors.New("reference not found")
		}
		return testRepo, nil
	}
	updateSubmodules = func(r *git.Repository, opts *git.SubmoduleUpdateOptions) error {
		return nil
	}
	defer func() {
		plainClone = git.PlainClone
		updateSubmodules = submodule.UpdateSubmodules
	}()

	c := Cloner{}
	if err := c.CloneRepo(&GitCloner{
		Repo:     "https://repo",
		Path:     t.TempDir(),
		Revision: "release-1.0",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 clone attempts (tag, branch), got %d", len(calls))
	}
	branch := calls[1]
	if branch.ReferenceName != plumbing.NewBranchReferenceName("release-1.0") {
		t.Errorf("expected ReferenceName refs/heads/release-1.0, got %q", branch.ReferenceName)
	}
	if !branch.SingleBranch || branch.Depth != 1 || branch.Tags != git.NoTags {
		t.Errorf("unexpected branch clone opts: %+v", branch)
	}
}

// TestCloneRevision_CommitShallow covers the fast path for a full commit SHA:
// cloneRevision must skip the tag/branch ref attempts entirely and resolve the
// commit through the shallow commit fetch, never falling back to a full clone.
func TestCloneRevision_CommitShallow(t *testing.T) {
	_, commitHash := initTestRepoWithCommit(t)

	var cloneCalls int
	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		cloneCalls++
		return nil, errors.New("unexpected clone")
	}
	var commitCalled bool
	cloneCommit = func(opts *GitCloner, auth transport.AuthMethod, caBundle []byte) error {
		commitCalled = true
		return nil
	}
	defer func() {
		plainClone = git.PlainClone
		cloneCommit = cloneCommitShallow
	}()

	c := Cloner{}
	if err := c.CloneRepo(&GitCloner{
		Repo:     "https://repo",
		Path:     t.TempDir(),
		Revision: commitHash.String(),
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !commitCalled {
		t.Fatal("expected the shallow commit fetch to be used for a full SHA")
	}
	if cloneCalls != 0 {
		t.Fatalf("expected no plainClone calls for a full SHA, got %d", cloneCalls)
	}
}

// TestCloneCommitShallow_Body exercises the real cloneCommitShallow body
// (PlainInit → CreateRemote → Fetch-by-SHA → Checkout) end-to-end. It
// complements TestCloneRevision_CommitShallow, which mocks the entire
// cloneCommit var and never runs the function body.
func TestCloneCommitShallow_Body(t *testing.T) {
	srcRepo, commitHash := initTestRepoWithCommit(t)
	srcWt, err := srcRepo.Worktree()
	if err != nil {
		t.Fatalf("getting source worktree: %v", err)
	}
	// Enable SHA-based fetching on the local source repo so go-git's local
	// transport advertises allow-reachable-sha1-in-want.
	srcCfg, err := srcRepo.Config()
	if err != nil {
		t.Fatalf("reading source repo config: %v", err)
	}
	srcCfg.Raw.SetOption("uploadpack", "", "allowReachableSHA1InWant", "true")
	if err := srcRepo.Storer.SetConfig(srcCfg); err != nil {
		t.Fatalf("setting source repo config: %v", err)
	}

	var submoduleOpts *git.SubmoduleUpdateOptions
	updateSubmodules = func(r *git.Repository, opts *git.SubmoduleUpdateOptions) error {
		submoduleOpts = opts
		return nil
	}
	defer func() { updateSubmodules = submodule.UpdateSubmodules }()

	destPath := t.TempDir()
	if err := cloneCommitShallow(&GitCloner{
		Repo:     srcWt.Filesystem.Root(),
		Path:     destPath,
		Revision: commitHash.String(),
	}, nil, nil); err != nil {
		t.Fatalf("cloneCommitShallow: %v", err)
	}

	cloned, err := git.PlainOpen(destPath)
	if err != nil {
		t.Fatalf("opening cloned repo: %v", err)
	}
	head, err := cloned.Head()
	if err != nil {
		t.Fatalf("getting HEAD: %v", err)
	}
	if head.Hash() != commitHash {
		t.Errorf("expected HEAD %s, got %s", commitHash, head.Hash())
	}
	if _, err := os.Stat(destPath + "/test.txt"); err != nil {
		t.Errorf("expected test.txt in cloned worktree: %v", err)
	}
	if submoduleOpts == nil {
		t.Fatal("expected updateSubmodules to be called")
	}
	if !submoduleOpts.Init {
		t.Error("expected submodule Init=true")
	}
	if submoduleOpts.Depth != 1 {
		t.Errorf("expected Depth=1, got %d", submoduleOpts.Depth)
	}
}

// TestCloneRevision_FullCloneFallback covers a full commit SHA on a server
// that rejects fetch-by-SHA: the shallow commit fetch fails, so cloneRevision
// falls back to a full clone + ResolveRevision against the real test repo.
func TestCloneRevision_FullCloneFallback(t *testing.T) {
	testRepo, commitHash := initTestRepoWithCommit(t)

	var calls []*git.CloneOptions
	var submoduleOpts *git.SubmoduleUpdateOptions
	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		calls = append(calls, o)
		return testRepo, nil
	}
	cloneCommit = func(opts *GitCloner, auth transport.AuthMethod, caBundle []byte) error {
		return errors.New("server does not support exact SHA1 refspec")
	}
	updateSubmodules = func(r *git.Repository, opts *git.SubmoduleUpdateOptions) error {
		submoduleOpts = opts
		return nil
	}
	readFile = func(name string) ([]byte, error) {
		if name == "passFile" {
			return []byte("1234"), nil
		}
		return nil, errors.New("file not found")
	}
	defer func() {
		plainClone = git.PlainClone
		cloneCommit = cloneCommitShallow
		updateSubmodules = submodule.UpdateSubmodules
		readFile = os.ReadFile
	}()

	c := Cloner{}
	if err := c.CloneRepo(&GitCloner{
		Repo:         "https://repo",
		Path:         t.TempDir(),
		Revision:     commitHash.String(),
		Username:     "user",
		PasswordFile: "passFile",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the full clone hits plainClone; the SHA path skips the ref attempts.
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 clone attempt (full), got %d", len(calls))
	}
	full := calls[0]
	// The full clone must NOT pin a ref or depth, and must rely on go-git's
	// default Tags mode (AllTags) so ResolveRevision can find any ref.
	if full.ReferenceName != "" {
		t.Errorf("expected empty ReferenceName on full clone, got %q", full.ReferenceName)
	}
	if full.SingleBranch {
		t.Error("expected SingleBranch=false on full clone")
	}
	if full.Depth != 0 {
		t.Errorf("expected Depth=0 on full clone, got %d", full.Depth)
	}
	if full.Tags != git.InvalidTagMode {
		// InvalidTagMode is the zero value; go-git treats it as AllTags.
		t.Errorf("expected default Tags mode on full clone, got %v", full.Tags)
	}
	expectedAuth := &httpgit.BasicAuth{Username: "user", Password: "1234"}
	if !cmp.Equal(full.Auth, expectedAuth) {
		t.Errorf("expected auth %+v, got %+v", expectedAuth, full.Auth)
	}
	if submoduleOpts == nil || submoduleOpts.Auth == nil {
		t.Fatal("expected updateSubmodules to be called with auth")
	}
}

// TestCloneRevision_ResolveRevisionError exercises the third-attempt path:
// the full clone succeeds, but the requested revision is not present in the
// repo so ResolveRevision errors out.
func TestCloneRevision_ResolveRevisionError(t *testing.T) {
	tempDir := t.TempDir()
	testRepo, _ := git.PlainInit(tempDir, false)

	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		// Fail both shortcuts to reach the full-clone path, then return the
		// empty test repo so ResolveRevision has nothing to resolve.
		if o.ReferenceName != "" {
			return nil, errors.New("reference not found")
		}
		return testRepo, nil
	}
	defer func() {
		plainClone = git.PlainClone
	}()

	c := Cloner{}
	err := c.CloneRepo(&GitCloner{
		Repo:     "https://repo",
		Path:     t.TempDir(),
		Revision: "nonexistent-revision",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to resolve revision") {
		t.Fatalf("expected 'failed to resolve revision' error, got: %s", err.Error())
	}
}

// TestCloneRevision_CloneError ensures the final error is reported when all
// three clone strategies fail (e.g. network/auth failure that's independent
// of the ref).
func TestCloneRevision_CloneError(t *testing.T) {
	var attempts int
	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		attempts++
		return nil, errors.New("clone failed")
	}
	defer func() {
		plainClone = git.PlainClone
	}()

	c := Cloner{}
	err := c.CloneRepo(&GitCloner{
		Repo:     "https://repo",
		Path:     t.TempDir(),
		Revision: "abc123",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if attempts != 3 {
		t.Errorf("expected 3 clone attempts, got %d", attempts)
	}
	if !strings.Contains(err.Error(), "failed to clone repo from revision") {
		t.Fatalf("expected 'failed to clone repo from revision' error, got: %s", err.Error())
	}
}

func TestCloneRevision_SubmoduleUpdateError(t *testing.T) {
	testRepo, _ := initTestRepoWithCommit(t)

	plainClone = func(path string, isBare bool, o *git.CloneOptions) (*git.Repository, error) {
		// First attempt (tag) succeeds — exercises the early return that
		// still calls updateSubmodules.
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
	err := c.CloneRepo(&GitCloner{
		Repo: "https://repo",
		Path: t.TempDir(),
		// A tag name (not a full SHA) so the tag shortcut handles it.
		Revision: "v1.0.0",
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "submodule update failed" {
		t.Fatalf("expected 'submodule update failed', got: %s", err.Error())
	}
}
