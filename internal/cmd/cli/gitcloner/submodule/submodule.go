package submodule

//go:generate mockgen --build_flags=--mod=mod -source=submodule.go -destination=../../../../mocks/submodule_updater_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule SubmoduleFetcher,FetcherFactory

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/strategy"

)



type SubmoduleFetcher interface {
	Fetch(ctx context.Context, opt *strategy.FetchRequest) error
}

type FetcherFactory func(auth transport.AuthMethod, repo *git.Repository) (SubmoduleFetcher, error)

func DefaultFetcherFactory(auth transport.AuthMethod, repo *git.Repository) (SubmoduleFetcher,error) {
	return NewFetcher(auth, repo)
}

type SubmoduleUpdater struct {
	fetcherFactory FetcherFactory
}
type UpdaterOption func(*SubmoduleUpdater)

func WithFetcherFactory(factory FetcherFactory) UpdaterOption {
	return func(u *SubmoduleUpdater) {
		u.fetcherFactory = factory
	}
}

func NewSubmoduleUpdater(opts ...UpdaterOption) *SubmoduleUpdater {
	u := &SubmoduleUpdater{
		fetcherFactory: DefaultFetcherFactory,
	}
	for _, opt := range opts {
		opt(u)
	}
	return u
}

func (u *SubmoduleUpdater) UpdateSubmodules(r *git.Repository, o *git.SubmoduleUpdateOptions) error {
	w , err := r.Worktree()
	if errors.Is(err, git.ErrIsBareRepository) {
		return fmt.Errorf("repository is bare: %w", err)
	}
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}

	s, err := w.Submodules()
	if err != nil {
		return fmt.Errorf("getting submodules: %w", err)
	}
		o.Init = true
	return u.UpdateContext(context.Background(), s, o)
}

func (u *SubmoduleUpdater) UpdateContext(ctx context.Context, s git.Submodules, o *git.SubmoduleUpdateOptions) error {
	for _, sub := range s {
		if err := u.submoduleUpdateContext(ctx, sub, o); err != nil {
			return err
		}
	}
	return nil
}

func (u *SubmoduleUpdater) submoduleUpdateContext(ctx context.Context, s *git.Submodule, o *git.SubmoduleUpdateOptions) error {
	return u.update(ctx, s, o)
}


func (u *SubmoduleUpdater) update(ctx context.Context, s *git.Submodule, o *git.SubmoduleUpdateOptions) error {
	if err := s.Init(); err != nil {
		return fmt.Errorf("initializing submodule: %w", err)
	}

	status, err := s.Status()
	if err != nil {
		return fmt.Errorf("getting submodule status: %w", err)
	}

	r, err := s.Repository()
	if err != nil {
		return fmt.Errorf("getting submodule repository: %w", err)
	}

	f, err := u.fetcherFactory(o.Auth, r)
	if err != nil {
		return fmt.Errorf("creating fetcher: %w", err)
	}

	fr := &strategy.FetchRequest{
		CommitHash: status.Expected,
	}
	if err := f.Fetch(ctx, fr); err != nil {
		return fmt.Errorf("fetching submodule: %w", err)
	}

	return u.doRecursiveUpdate(ctx, s, r, o)
}

func (u *SubmoduleUpdater) doRecursiveUpdate(ctx context.Context, s *git.Submodule, r *git.Repository, o *git.SubmoduleUpdateOptions) error {
	if o.RecurseSubmodules == git.NoRecurseSubmodules {
		return nil
	}

	w, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree for recursive update: %w", err)
	}

	l, err := w.Submodules()
	if err != nil {
		return fmt.Errorf("getting nested submodules: %w", err)
	}

	newOpts := *o
	newOpts.RecurseSubmodules--

	return u.UpdateContext(ctx, l, &newOpts)
}

var defaultUpdater = NewSubmoduleUpdater()

func UpdateSubmodules(r *git.Repository, o *git.SubmoduleUpdateOptions) error {
	return defaultUpdater.UpdateSubmodules(r, o)
}

func UpdateContext(ctx context.Context, s git.Submodules, o *git.SubmoduleUpdateOptions) error {
	return defaultUpdater.UpdateContext(ctx, s, o)
}

func SubmoduleUpdateContext(ctx context.Context, s *git.Submodule, o *git.SubmoduleUpdateOptions) error {
	return defaultUpdater.submoduleUpdateContext(ctx, s, o)
}

