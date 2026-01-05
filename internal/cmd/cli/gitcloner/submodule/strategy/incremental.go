package strategy

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/capability"
)

const (
	// MaxDeepenIterations is the maximum number of deepen attempts before giving up
	MaxDeepenIterations = 100
)

// IncrementalDeepenStrategy fetches using shallow clone then deepens incrementally
// until the target commit is reachable. This is used when the server supports
// shallow but not allow-reachable-sha1-in-want.
type IncrementalDeepenStrategy struct {
	auth             transport.AuthMethod
	fetchFunc        DepthFetchFunc
	commitExistsFunc CommitExistsFunc
	checkoutFunc     CheckoutFunc
}

func NewIncrementalStrategy(auth transport.AuthMethod) *IncrementalDeepenStrategy {
	s := &IncrementalDeepenStrategy{auth: auth}
	s.fetchFunc = s.defaultFetch
	s.commitExistsFunc = defaultCommitExists
	s.checkoutFunc = defaultCheckout
	return s
}

func (s *IncrementalDeepenStrategy) Type() capability.StrategyType {
	return capability.StrategyIncrementalDeepen
}

func (s *IncrementalDeepenStrategy) Execute(ctx context.Context, r *git.Repository, req plumbing.Hash) error {
	// Start with depth 1 and increase until we find the commit
	for depth := 1; depth <= MaxDeepenIterations; depth++ {
		err := s.fetchFunc(ctx, r, depth)
		// "already up-to-date" is not a real error, it just means nothing new was fetched
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return fmt.Errorf("fetch at depth %d: %w", depth, err)
		}

		if s.commitExistsFunc(r, req) {
			return s.checkoutFunc(r, &req)
		}
	}

	return fmt.Errorf("commit %s not found after deepening to %d", req, MaxDeepenIterations)
}

func (s *IncrementalDeepenStrategy) defaultFetch(ctx context.Context, r *git.Repository, depth int) error {
	refSpecs := []config.RefSpec{"refs/heads/*:refs/remotes/origin/*"}

	return r.FetchContext(ctx, &git.FetchOptions{
		RefSpecs: refSpecs,
		Depth:    depth,
		Auth:     s.auth,
		Tags:     git.NoTags,
	})
}
