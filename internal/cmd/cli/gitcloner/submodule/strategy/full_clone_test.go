package strategy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/capability"
)

func TestFullCloneStrategy_Type(t *testing.T) {
	s := NewFullCloneStrategy(nil)
	if s.Type() != capability.StrategyFullClone {
		t.Errorf("expected %v, got %v", capability.StrategyFullClone, s.Type())
	}
}

func TestFullCloneStrategy_Success(t *testing.T) {
	fetchCalled := false
	checkoutCalled := false
	expectedHash := plumbing.NewHash("abc123")

	s := &FullCloneStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository) error {
			fetchCalled = true
			return nil
		},
		checkoutFunc: func(r *git.Repository, hash *plumbing.Hash) error {
			checkoutCalled = true
			if *hash != expectedHash {
				t.Errorf("expected hash %v, got %v", expectedHash, hash)
			}
			return nil
		},
	}

	err := s.Execute(context.Background(), nil, expectedHash)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fetchCalled {
		t.Error("fetch was not called")
	}
	if !checkoutCalled {
		t.Error("checkout was not called")
	}
}

func TestFullCloneStrategy_FetchError(t *testing.T) {
	s := &FullCloneStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository) error {
			return errors.New("network error")
		},
		checkoutFunc: func(r *git.Repository, hash *plumbing.Hash) error {
			t.Fatal("checkout should not be called after fetch error")
			return nil
		},
	}
	CommitHash := plumbing.NewHash("abc123")
	err := s.Execute(context.Background(), nil, CommitHash)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "fetch") {
		t.Errorf("expected 'fetch' in error: %v", err)
	}
	if !strings.Contains(err.Error(), "network error") {
		t.Errorf("expected wrapped error: %v", err)
	}
}

func TestFullCloneStrategy_CheckoutError(t *testing.T) {
	s := &FullCloneStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository) error {
			return nil
		},
		checkoutFunc: func(r *git.Repository, hash *plumbing.Hash) error {
			return errors.New("checkout failed")
		},
	}

	CommitHash :=  plumbing.NewHash("abc123")
	err := s.Execute(context.Background(), nil, CommitHash)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "checkout failed") {
		t.Errorf("expected checkout error: %v", err)
	}
}

func TestFullCloneStrategy_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := &FullCloneStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository) error {
			return ctx.Err()
		},
		checkoutFunc: func(r *git.Repository, hash *plumbing.Hash) error {
			return nil
		},
	}
	CommitHash :=  plumbing.NewHash("abc123")
	err := s.Execute(ctx, nil, CommitHash)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context canceled error: %v", err)
	}
}

func TestNewFullCloneStrategy(t *testing.T) {
	s := NewFullCloneStrategy(nil)

	if s == nil {
		t.Fatal("expected non-nil strategy")
	}
	if s.checkoutFunc == nil {
		t.Error("expected checkoutFunc to be set")
	}
}
