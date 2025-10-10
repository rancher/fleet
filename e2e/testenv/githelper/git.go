package githelper

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/infra/cmd"
)

type gitAuth interface {
	check()
	getKeys() (transport.AuthMethod, error)
	getURL() string
	setURL(url string)
}

type HTTPAuth struct {
	URL      string
	Username string
	Password string
}

func (ha HTTPAuth) check() {
	if ha.URL == "" {
		panic("git repo URL must be set")
	}

	if ha.Username == "" || ha.Password == "" {
		panic("repo with HTTP auth: GIT_HTTP_USER, GIT_HTTP_PASSWORD must be set")
	}
}

func (ha HTTPAuth) getKeys() (transport.AuthMethod, error) {
	return &http.BasicAuth{Username: ha.Username, Password: ha.Password}, nil
}

func (ha HTTPAuth) getURL() string {
	url := ha.URL

	// insert username and password into remote URL.
	// This is not secure, but should be enough for testing against ephemeral repos located on the same host.
	if before, after, found := strings.Cut(url, "//"); found {
		url = fmt.Sprintf("%s//%s:%s@%s", before, ha.Username, ha.Password, after)
	}

	return url
}

func (ha *HTTPAuth) setURL(url string) {
	ha.URL = url
}

type SSHAuth struct {
	URL       string
	User      string
	SSHKey    string
	SSHPubKey string
}

func (sa SSHAuth) check() {
	if sa.URL == "" {
		panic("git repo URL must be set")
	}

	if sa.User == "" || sa.SSHKey == "" || sa.SSHPubKey == "" {
		panic("GIT_REPO_USER, GIT_SSH_KEY, GIT_SSH_PUBKEY must be set")
	}
}

func (sa SSHAuth) getKeys() (transport.AuthMethod, error) {
	keys, err := ssh.NewPublicKeysFromFile(sa.User, sa.SSHKey, "")
	if err != nil {
		return nil, err
	}

	return keys, nil
}

func (sa SSHAuth) getURL() string {
	return sa.URL
}

func (sa *SSHAuth) setURL(url string) {
	sa.URL = url
}

// Git represents a git repo with auth.
type Git struct {
	User   string
	Branch string
	Auth   gitAuth
}

// NewHTTP creates a new Git instance with HTTP auth, using environment variables.
func NewHTTP(addr string) *Git {
	g := newGit()
	g.Auth = &HTTPAuth{
		Username: os.Getenv("GIT_HTTP_USER"),
		Password: os.Getenv("GIT_HTTP_PASSWORD"),
	}
	if addr != "" {
		g.Auth.setURL(addr)
	} else {
		g.Auth.setURL(os.Getenv("GIT_REPO_URL"))
	}
	g.Auth.check()

	return g
}

// NewSSH creates a new Git instance with SSH auth, using environment variables.
func NewSSH() *Git {
	g := newGit()
	g.Auth = &SSHAuth{
		User:      os.Getenv("GIT_REPO_USER"),
		SSHKey:    os.Getenv("GIT_SSH_KEY"),
		SSHPubKey: os.Getenv("GIT_SSH_PUBKEY"),
	}
	g.Auth.setURL(os.Getenv("GIT_REPO_URL"))
	g.Auth.check()

	return g
}

// newGit creates a new Git instance using environment variables.
func newGit() *Git {
	g := &Git{
		User:   os.Getenv("GIT_REPO_USER"),
		Branch: os.Getenv("GIT_REPO_BRANCH"),
	}

	if g.Branch == "" {
		g.Branch = "master"
	}

	return g
}

func (g *Git) GetURL() string {
	return g.Auth.getURL()
}

func (g *Git) GetInClusterURL(host string, port int, repoName string) string {
	addr := g.Auth.getURL()

	if before, _, found := strings.Cut(addr, "@"); found {
		addr = fmt.Sprintf("%s@%s:%d/%s", before, host, port, repoName)
	}

	return addr
}

// Create creates a git repository at the specified repodir, with contents from `from/subdir`, and sets a remote using
// g's URL.
func (g *Git) Create(repodir string, from string, subdir string) (*git.Repository, error) {
	s := osfs.New(path.Join(repodir, ".git"))
	storer := filesystem.NewStorage(s, cache.NewObjectLRUDefault())
	fs := osfs.New(repodir)
	repo, err := git.Init(storer, fs)
	if err != nil {
		return nil, err
	}

	_, err = repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{g.Auth.getURL()},
	})
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("cp", "-a", from, path.Join(repodir, subdir)) //nolint:gosec // test code should never receive user input
	err = cmd.Run()
	if err != nil {
		return nil, err
	}

	_, err = g.Update(repo, UpdateForce)

	return repo, err
}

// Add adds files to a repo, commits them and pushed the changes.
func (g *Git) Add(repodir, from, subdir string) (string, error) {
	s := osfs.New(path.Join(repodir, ".git"))
	storer := filesystem.NewStorage(s, cache.NewObjectLRUDefault())
	fs := osfs.New(repodir)
	repo, err := git.Open(storer, fs)
	if err != nil {
		return "", err
	}

	cmd := exec.Command("cp", "-a", from, path.Join(repodir, subdir)) //nolint:gosec // test code should never receive user input
	err = cmd.Run()
	if err != nil {
		return "", err
	}

	hash, err := g.Update(repo)

	return hash, err
}

type UpdateOption int

const (
	UpdateForce UpdateOption = iota
)

// Update commits and pushes the current state of the worktree to the remote.
func (g *Git) Update(repo *git.Repository, updateOptions ...UpdateOption) (string, error) {
	w, err := repo.Worktree()
	if err != nil {
		return "", err
	}

	if _, err := w.Add("."); err != nil {
		return "", err
	}

	h, err := w.Commit("add chart", &git.CommitOptions{
		Author: author(),
	})
	if err != nil {
		return "", err
	}

	po := git.PushOptions{
		Progress: os.Stdout,
		RefSpecs: []config.RefSpec{config.RefSpec("refs/heads/master:refs/heads/" + g.Branch)},
		// This prevents IP SANs from being needed on the git server cert; the TLS verification we are most
		// interested in is the one happening in Fleet's controllers and jobs.
		InsecureSkipTLS: true,
		Force:           slices.Contains(updateOptions, UpdateForce),
	}
	k, err := g.Auth.getKeys()
	if err != nil {
		return "", err
	}

	if k != nil {
		po.Auth = k
	}

	return h.String(), repo.Push(&po)
}

// CheckoutRemote checks the specified remote branch from the given repository out
func (g *Git) CheckoutRemote(repo *git.Repository, branch string) error {
	w, err := repo.Worktree()
	if err != nil {
		return err
	}
	_ = repo.Fetch(&git.FetchOptions{RefSpecs: []config.RefSpec{"refs/*:refs/*"}})
	if err := w.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(branch)}); err != nil {
		return err
	}
	return nil
}

func author() *object.Signature {
	return &object.Signature{
		Name:  "CI",
		Email: "fleet@example.org",
		When:  time.Now(),
	}
}

// CreateKnownHosts works around https://github.com/go-git/go-git/issues/411
func CreateKnownHosts(path string, host string) (string, error) {
	cmd := exec.Command("/bin/sh", "-c", "ssh-keyscan "+host+" >> "+path) //nolint:gosec // test code should never receive user input

	var b bytes.Buffer
	cmd.Stdout = &b
	cmd.Stderr = &b

	err := cmd.Run()
	return b.String(), err
}

// BuildGitHostname builds the hostname of a cluster-local git repo from the provided namespace.
func BuildGitHostname() string {
	return fmt.Sprintf("git-service.%s.svc.cluster.local", cmd.InfraNamespace)
}

// GetExternalRepoAddr retrieves the external URL where our local git server can be reached, based on the provided port
// and repo name.
func GetExternalRepoAddr(env *testenv.Env, port int, repoName string) (string, error) {
	if v := os.Getenv("external_ip"); v != "" {
		return fmt.Sprintf("http://%s:%d/%s", v, port, repoName), nil
	}

	systemk := env.Kubectl.Namespace(cmd.InfraNamespace)

	externalIP, err := systemk.Get("service", "git-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
	if err != nil {
		return "", fmt.Errorf("failed to get ingress ip for git-service in %s: %w", cmd.InfraNamespace, err)
	}

	return fmt.Sprintf("http://%s:%d/%s", externalIP, port, repoName), nil
}
