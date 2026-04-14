package strategy

import (
	"context"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// FetchFunc performs a git fetch operation
type FetchFunc func(ctx context.Context, r *git.Repository) error

// CheckoutFunc performs a git checkout operation
type CheckoutFunc func(r *git.Repository, hash *plumbing.Hash) error

// CommitExistFunc checks if a commit exists in the repository
type CommitExistsFunc func(r *git.Repository, hash plumbing.Hash) bool

// DepthFetchFunc performs a git fetch at a specific depth
type DepthFetchFunc func(ctx context.Context, r *git.Repository, depth int) error
