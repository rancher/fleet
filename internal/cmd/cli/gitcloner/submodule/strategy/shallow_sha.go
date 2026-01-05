package strategy

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/capability"
)

// ShallowSHAStrategy fetches a specific commit with depth=1 (shallow clone).
// This is the most efficient strategy when the server supports both
// allow-reachable-sha1-in-want and shallow.
type ShallowSHAStrategy struct {
	auth transport.AuthMethod
	fetchFunc    FetchFunc
	checkoutFunc CheckoutFunc
}

func  NewShallowSHAStrategy(auth transport.AuthMethod) *ShallowSHAStrategy {
	s := &ShallowSHAStrategy{auth: auth}
	s.checkoutFunc = defaultCheckout
	return s
}

func (s *ShallowSHAStrategy) Type() capability.StrategyType {
	return capability.StrategyShallowSHA
}

func (s *ShallowSHAStrategy) Execute(ctx context.Context,r *git.Repository, req *FetchRequest) error {
	fetchFunc := s.fetchFunc
	if fetchFunc == nil {
		fetchFunc = s.defaultFetch(req.CommitHash)
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

func (s *ShallowSHAStrategy) defaultFetch(hash plumbing.Hash) FetchFunc {
	return func(ctx context.Context, r *git.Repository) error {
		refSpec := config.RefSpec(fmt.Sprintf("%s:refs/heads/temp", hash.String()))

		return r.FetchContext(ctx, &git.FetchOptions{
			RefSpecs: []config.RefSpec{refSpec},
			Depth:    1,
			Auth:     s.auth,
			Tags:     git.NoTags,
		})
	}
}


