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

// FullSHAStrategy fetches a specific commit with full history (no depth limit).
// This is used when the server supports allow-reachable-sha1-in-want but not shallow.
type FullSHAStrategy struct {
	auth         transport.AuthMethod
	fetchFunc    FetchFunc
	checkoutFunc CheckoutFunc
}

func NewFullSHAStrategy(auth transport.AuthMethod) *FullSHAStrategy {
	s := &FullSHAStrategy{auth: auth}
	s.checkoutFunc = defaultCheckout
	return s
}

func (s *FullSHAStrategy) Type() capability.StrategyType {
	return capability.StrategyFullSHA
}

func (s *FullSHAStrategy) Execute(ctx context.Context, r *git.Repository, req plumbing.Hash) error {

	fetchFunc := s.fetchFunc
	if fetchFunc == nil {
		fetchFunc = s.defaultFetch(req)
	}

	err := fetchFunc(ctx, r)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	if err := s.checkoutFunc(r, &req); err != nil {
		return err
	}

	return nil
}


func (s *FullSHAStrategy) defaultFetch(hash plumbing.Hash) FetchFunc {
	return func(ctx context.Context, r *git.Repository) error {
		refSpec := config.RefSpec(fmt.Sprintf("%s:refs/heads/temp", hash.String()))

		return r.FetchContext(ctx, &git.FetchOptions{
			RefSpecs: []config.RefSpec{refSpec},
			// No Depth - fetch full history up to this commit
			Auth: s.auth,
			Tags: git.NoTags,
		})
	}
}
