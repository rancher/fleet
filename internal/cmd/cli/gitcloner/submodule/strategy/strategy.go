package strategy

import (
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

type CheckoutOptions struct {
	Hash plumbing.Hash
	// SparsePatterns []string
}

// Checkout performs a git checkout to the specified hash.
func Checkout(r *git.Repository, opts *CheckoutOptions) error {
	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}

	err = wt.Checkout(&git.CheckoutOptions{
		Hash:  opts.Hash,
		Force: true,
	})
	if err != nil {
		return fmt.Errorf("checkout: %w", err)
	}

	return nil
}

// defaultCheckout is the default implementation of CheckoutFunc.
func defaultCheckout(r *git.Repository, hash *plumbing.Hash) error {
	opts := &CheckoutOptions{Hash: *hash}
	if err := Checkout(r, opts); err != nil {
		return fmt.Errorf("checkout failed: %w", err)
	}
	return nil
}

// defaultCommitExists is the default implementation of CommitExistsFunc.
func defaultCommitExists(r *git.Repository, hash plumbing.Hash) bool {
	_, err := r.CommitObject(hash)
	return err == nil
}
