package git

import (
	"fmt"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
)

type options struct {
	Credential        *corev1.Secret
	CABundle          []byte
	InsecureTLSVerify bool
	Headers           map[string]string
	Timeout           time.Duration
	log               logr.Logger
}

// RemoteRef represents a remote reference is a git repository
type RemoteRef struct {
	Name string
	Hash string
}

type RemoteLister interface {
	// List returns a generic RemoteRef slice of remote references in a git repository
	List(appendPeeled bool) ([]*RemoteRef, error)
}

// GoGitRemoteLister implements the RemoteLister interface using the go-git library
type GoGitRemoteLister struct {
	URL             string
	Auth            transport.AuthMethod
	CABundle        []byte
	InsecureSkipTLS bool
}

func (g *GoGitRemoteLister) List(appendPeeled bool) ([]*RemoteRef, error) {
	remote := gogit.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		URLs: []string{g.URL},
	})
	opts := &gogit.ListOptions{
		Auth:            g.Auth,
		CABundle:        g.CABundle,
		InsecureSkipTLS: g.InsecureSkipTLS,
	}
	if appendPeeled {
		opts.PeelingOption = gogit.AppendPeeled
	}
	refs, err := remote.List(opts)
	if err != nil {
		return nil, err
	}
	var retRefs []*RemoteRef
	for _, ref := range refs {
		retRefs = append(retRefs, &RemoteRef{Name: ref.Name().String(), Hash: ref.Hash().String()})
	}
	return retRefs, nil
}

type Remote struct {
	Lister  RemoteLister
	URL     string
	Options *options
}

func NewRemote(url string, opts *options) (*Remote, error) {
	auth, err := GetAuthFromSecret(url, opts.Credential)
	if err != nil {
		return nil, err
	}
	return &Remote{
		URL:     url,
		Options: opts,
		Lister: &GoGitRemoteLister{
			URL:             url,
			Auth:            auth,
			CABundle:        opts.CABundle,
			InsecureSkipTLS: opts.InsecureTLSVerify,
		},
	}, nil
}

// RevisionCommit returns the commit for the given revision
func (r *Remote) RevisionCommit(revision string) (string, error) {
	if err := validateCommit(revision); err == nil {
		// revision is a commit already
		return revision, nil
	}
	refs, err := r.Lister.List(true)
	if err != nil {
		return "", err
	}

	refLighweightTag := formatRefForTag(revision, false)
	refAnnotatedTag := formatRefForTag(revision, true)
	commit := ""
	for _, ref := range refs {
		// if the annotated form is found, we can return the value now
		if ref.Name == refAnnotatedTag {
			return ref.Hash, nil
		}
		// if the lightweight form is found, we store and keep looking
		// because we could have the annotated one
		if ref.Name == refLighweightTag {
			commit = ref.Hash
		}
	}
	if commit != "" {
		return commit, nil
	}
	return "", fmt.Errorf("commit not found for revision: %s", revision)
}

// LatestBranchCommit returns the latest commit for the given branch
func (r *Remote) LatestBranchCommit(branch string) (string, error) {
	if err := validateBranch(branch); err != nil {
		return "", err
	}

	// check if the url is one of the supported for getting the last commit with a specific commits url
	commitsURL := getVendorCommitsURL(r.URL, branch)
	if commitsURL != "" {
		// it is supported. Get the last commit with the vendor's url
		// (this is faster than running a whole ls-remote like operation)
		if commit, err := latestCommitFromCommitsURL(commitsURL, r.Options); err == nil {
			// in case of error it tries the full List operation
			return commit, nil
		}
	}

	refBranch := formatRefForBranch(branch)

	refs, err := r.Lister.List(false)
	if err != nil {
		return "", err
	}

	for _, ref := range refs {
		if ref.Name == refBranch {
			return ref.Hash, nil
		}
	}

	return "", fmt.Errorf("commit not found for branch: %s", branch)
}

func formatRefForBranch(branch string) string {
	return fmt.Sprintf("refs/heads/%s", branch)
}

func formatRefForTag(tag string, annotated bool) string {
	suffix := ""
	if annotated {
		suffix = "^{}"
	}
	return fmt.Sprintf("refs/tags/%s%s", tag, suffix)
}
