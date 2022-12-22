package githelper

import (
	"bytes"
	"os"
	"os/exec"
	"path"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

type Git struct {
	URL       string
	User      string
	SSHKey    string
	SSHPubKey string
	Branch    string
}

func New() *Git {
	g := &Git{
		User:      os.Getenv("GIT_REPO_USER"),
		SSHKey:    os.Getenv("GIT_SSH_KEY"),
		SSHPubKey: os.Getenv("GIT_SSH_PUBKEY"),
		URL:       os.Getenv("GIT_REPO_URL"),
		Branch:    os.Getenv("GIT_REPO_BRANCH"),
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
	if g.User == "" || g.SSHKey == "" || g.URL == "" || g.SSHPubKey == "" {
		panic("GIT_REPO_USER, GIT_SSH_KEY, GIT_SSH_PUBKEY, GIT_REPO_URL, GIT_REPO_HOST must be set")
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

func (g *Git) Create(repodir string, from string, subdir string) (*git.Repository, error) {
	//fmt.Printf("Creating git repository in %s\n", repodir)
	s := osfs.New(path.Join(repodir, ".git"))
	repo, err := git.Init(filesystem.NewStorage(s, cache.NewObjectLRUDefault()), osfs.New(repodir))
	if err != nil {
		return nil, err
	}

	_, err = repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{g.URL},
	})
	if err != nil {
		return nil, err
	}

	//fmt.Printf("Copying %s to %s\n", from, to)
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

	//fmt.Printf("Pushing to remote: %s\n", g.SSHKey)
	keys, err := ssh.NewPublicKeysFromFile(g.User, g.SSHKey, "")
	if err != nil {
		return nil, err
	}

	err = repo.Push(&git.PushOptions{
		Auth:     keys,
		Progress: os.Stdout,
		// Force push, so our initial state is deterministic
		RefSpecs: []config.RefSpec{config.RefSpec("+refs/heads/master:refs/heads/" + g.Branch)},
	})

	return repo, err
}

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

	keys, err := ssh.NewPublicKeysFromFile(g.User, g.SSHKey, "")
	if err != nil {
		return "", err
	}

	return h.String(), repo.Push(&git.PushOptions{
		Auth:     keys,
		Progress: os.Stdout,
		RefSpecs: []config.RefSpec{config.RefSpec("refs/heads/master:refs/heads/" + g.Branch)},
	})
}
