package githelper

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/rancher/fleet/e2e/testenv"
)

type Git struct {
	URL      string
	Branch   string
	Username string
	Password string
}

func New(url string) *Git {
	g := &Git{
		URL:      url,
		Branch:   os.Getenv("GIT_REPO_BRANCH"),
		Username: os.Getenv("GIT_HTTP_USER"),
		Password: os.Getenv("GIT_HTTP_PASSWORD"),
	}
	if g.Branch == "" {
		g.Branch = "master"
	}

	g.check()

	return g
}

func author() *object.Signature {
	return &object.Signature{
		Name:  "CI",
		Email: "fleet@example.org",
		When:  time.Now(),
	}
}

func (g *Git) check() {
	if g.URL == "" {
		panic("git repo URL must be set")
	}

	if g.Username == "" || g.Password == "" {
		panic("repo with HTTP auth: GIT_HTTP_USER, GIT_HTTP_PASSWORD must be set")
	}
}

// CreateHTTP creates a git repository at the specified repodir, with contents from `from/subdir`, and sets a
// remote using g's URL, inserting username and password into that HTTP URL for password-based auth.
// This is not secure, but should be enough for testing against ephemeral repos located on the same host.
func (g *Git) CreateHTTP(repodir, from, subdir string) (*git.Repository, error) {
	s := osfs.New(path.Join(repodir, ".git"))
	repo, err := git.Init(filesystem.NewStorage(s, cache.NewObjectLRUDefault()), osfs.New(repodir))
	if err != nil {
		return nil, err
	}

	// insert username and password into remote URL.
	url := g.URL
	if before, after, found := strings.Cut(g.URL, "//"); found {
		url = fmt.Sprintf("%s//%s:%s@%s", before, g.Username, g.Password, after)

	}

	_, err = repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{url},
	})
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("cp", "-a", from, path.Join(repodir, subdir)) //nolint:gosec // test code should never receive user input
	err = cmd.Run()
	if err != nil {
		return nil, err
	}

	w, err := repo.Worktree()
	if err != nil {
		return nil, err
	}

	if _, err := w.Add("."); err != nil {
		return nil, err
	}

	_, err = w.Commit("add chart", &git.CommitOptions{
		Author: author(),
	})
	if err != nil {
		return nil, err
	}

	err = repo.Push(&git.PushOptions{
		Progress: os.Stdout,
		// Force push, so our initial state is deterministic
		RefSpecs: []config.RefSpec{config.RefSpec("+refs/heads/master:refs/heads/" + g.Branch)},
	})

	return repo, err
}

// Update commits and pushes the current state of the worktree to the remote.
func (g *Git) Update(repo *git.Repository) (string, error) {
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

	keys := http.BasicAuth{Username: g.Username, Password: g.Password}

	return h.String(), repo.Push(&git.PushOptions{
		Auth:     &keys,
		Progress: os.Stdout,
		RefSpecs: []config.RefSpec{config.RefSpec("refs/heads/master:refs/heads/" + g.Branch)},
	})
}

// BuildGitHostname builds the hostname of a cluster-local git repo from the provided namespace.
func BuildGitHostname(ns string) (string, error) {
	if ns == "" {
		return "", errors.New("namespace is required")
	}

	return fmt.Sprintf("git-service.%s.svc.cluster.local", ns), nil
}

// GetExternalRepoIP retrieves the external IP where our local git server can be reached, based on the provided port and
// repo name.
func GetExternalRepoIP(env *testenv.Env, port int, repoName string) (string, error) {
	systemk := env.Kubectl.Namespace(env.Namespace)

	externalIP, err := systemk.Get("service", "git-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("http://%s:%d/%s", externalIP, port, repoName), nil
}
