package strategy

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/capability"
)

// FullCloneStrategy fetches the entire repository (all branches and tags).
// This is the fallback strategy when no optimizations are available.
type FullCloneStrategy struct {
	auth transport.AuthMethod
	fetchFunc    FetchFunc
	checkoutFunc CheckoutFunc
}

func NewFullCloneStrategy(auth transport.AuthMethod) *FullCloneStrategy {
	s := &FullCloneStrategy{auth: auth}
	s.checkoutFunc = defaultCheckout
	return s
}

func (s *FullCloneStrategy) Type() capability.StrategyType {
	return capability.StrategyFullClone
}

func (s *FullCloneStrategy) Execute(ctx context.Context, r *git.Repository, req *FetchRequest) error {
	fetchFunc := s.fetchFunc
	if fetchFunc == nil {
		fetchFunc = s.defaultFetch()
	}
	err := fetchFunc(ctx, r)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	if err := s.checkoutFunc(r, req.CommitHash); err != nil {
		return err
	}

	return nil
}

func (s *FullCloneStrategy) defaultFetch() FetchFunc {
	return func(ctx context.Context, r *git.Repository) error {
		return r.FetchContext(ctx, &git.FetchOptions{
			RefSpecs: []config.RefSpec{"refs/heads/*:refs/remotes/origin/*"},
			Auth:     s.auth,
			Tags:     git.AllTags,
		})
	}
}
