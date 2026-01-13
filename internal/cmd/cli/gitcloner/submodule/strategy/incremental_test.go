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

func TestIncrementalDeepenStrategy_Type(t *testing.T) {
	s := NewIncrementalStrategy(nil)
	if s.Type() != capability.StrategyIncrementalDeepen {
		t.Errorf("expected %v, got %v", capability.StrategyIncrementalDeepen, s.Type())
	}
}

func TestIncrementalDeepenStrategy_CommitFoundAtDepth1(t *testing.T) {
	fetchCalls := 0
	checkoutCalled := false
	expectedHash := plumbing.NewHash("abc123")

	s := &IncrementalDeepenStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository, depth int) error {
			fetchCalls++
			return nil
		},
		commitExistsFunc: func(r *git.Repository, hash plumbing.Hash) bool {
			return true // commit found immediately
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
	if fetchCalls != 1 {
		t.Errorf("expected 1 fetch call, got %d", fetchCalls)
	}
	if !checkoutCalled {
		t.Error("checkout was not called")
	}
}

func TestIncrementalDeepenStrategy_CommitFoundAtDepth5(t *testing.T) {
	fetchCalls := 0
	targetDepth := 5

	s := &IncrementalDeepenStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository, depth int) error {
			fetchCalls++
			return nil
		},
		commitExistsFunc: func(r *git.Repository, hash plumbing.Hash) bool {
			return fetchCalls >= targetDepth // found at depth 5
		},
		checkoutFunc: func(r *git.Repository, hash *plumbing.Hash) error {
			return nil
		},
	}

	commitHash := plumbing.NewHash("abc123")
	err := s.Execute(context.Background(), nil, commitHash)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fetchCalls != targetDepth {
		t.Errorf("expected %d fetch calls, got %d", targetDepth, fetchCalls)
	}
}

func TestIncrementalDeepenStrategy_CommitNotFound(t *testing.T) {
	fetchCalls := 0

	s := &IncrementalDeepenStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository, depth int) error {
			fetchCalls++
			return nil
		},
		commitExistsFunc: func(r *git.Repository, hash plumbing.Hash) bool {
			return false // commit NEVER found
		},
		checkoutFunc: func(r *git.Repository, hash *plumbing.Hash) error {
			t.Fatal("checkout should not be called")
			return nil
		},
	}

	commitHash := plumbing.NewHash("abc123")
	err := s.Execute(context.Background(), nil, commitHash)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found after deepening") {
		t.Errorf("unexpected error message: %v", err)
	}
	if !strings.Contains(err.Error(), commitHash.String()) {
		t.Errorf("expected commit hash in error: %v", err)
	}
	if fetchCalls != MaxDeepenIterations {
		t.Errorf("expected %d fetch calls, got %d", MaxDeepenIterations, fetchCalls)
	}
}

func TestIncrementalDeepenStrategy_FetchError(t *testing.T) {
	s := &IncrementalDeepenStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository, depth int) error {
			return errors.New("network error")
		},
		commitExistsFunc: func(r *git.Repository, hash plumbing.Hash) bool {
			t.Fatal("commitExists should not be called after fetch error")
			return false
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
	if !strings.Contains(err.Error(), "fetch at depth") {
		t.Errorf("expected 'fetch at depth' in error: %v", err)
	}
	if !strings.Contains(err.Error(), "network error") {
		t.Errorf("expected wrapped error: %v", err)
	}
}

func TestIncrementalDeepenStrategy_FetchAlreadyUpToDate(t *testing.T) {
	// git.NoErrAlreadyUpToDate should NOT be treated as an error
	fetchCalls := 0

	s := &IncrementalDeepenStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository, depth int) error {
			fetchCalls++
			return git.NoErrAlreadyUpToDate
		},
		commitExistsFunc: func(r *git.Repository, hash plumbing.Hash) bool {
			return fetchCalls >= 3 // found at depth 3
		},
		checkoutFunc: func(r *git.Repository, hash *plumbing.Hash) error {
			return nil
		},
	}
	CommitHash := plumbing.NewHash("abc123")
	err := s.Execute(context.Background(), nil, CommitHash)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fetchCalls != 3 {
		t.Errorf("expected 3 fetch calls, got %d", fetchCalls)
	}
}

func TestIncrementalDeepenStrategy_CheckoutError(t *testing.T) {
	s := &IncrementalDeepenStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository, depth int) error {
			return nil
		},
		commitExistsFunc: func(r *git.Repository, hash plumbing.Hash) bool {
			return true
		},
		checkoutFunc: func(r *git.Repository, hash *plumbing.Hash) error {
			return errors.New("checkout failed")
		},
	}
	CommitHash := plumbing.NewHash("abc123")
	err := s.Execute(context.Background(), nil, CommitHash)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "checkout failed") {
		t.Errorf("expected checkout error: %v", err)
	}
}

func TestIncrementalDeepenStrategy_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := &IncrementalDeepenStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository, depth int) error {
			return ctx.Err()
		},
		commitExistsFunc: func(r *git.Repository, hash plumbing.Hash) bool {
			return false
		},
		checkoutFunc: func(r *git.Repository, hash *plumbing.Hash) error {
			return nil
		},
	}
	CommitHash := plumbing.NewHash("abc123")
	err := s.Execute(ctx, nil, CommitHash)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context canceled error: %v", err)
	}
}

func TestIncrementalDeepenStrategy_DepthPassedCorrectly(t *testing.T) {
	depths := []int{}

	s := &IncrementalDeepenStrategy{
		fetchFunc: func(ctx context.Context, r *git.Repository, depth int) error {
			depths = append(depths, depth)
			return nil
		},
		commitExistsFunc: func(r *git.Repository, hash plumbing.Hash) bool {
			return len(depths) >= 3 // found at depth 3
		},
		checkoutFunc: func(r *git.Repository, hash *plumbing.Hash) error {
			return nil
		},
	}
	commitHash := plumbing.NewHash("abc123")
	err := s.Execute(context.Background(), nil, commitHash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []int{1, 2, 3}
	if len(depths) != len(expected) {
		t.Fatalf("expected %d depths, got %d", len(expected), len(depths))
	}
	for i, d := range expected {
		if depths[i] != d {
			t.Errorf("depth[%d]: expected %d, got %d", i, d, depths[i])
		}
	}
}

func TestNewIncrementalStrategy(t *testing.T) {
	s := NewIncrementalStrategy(nil)

	if s == nil {
		t.Fatal("expected non-nil strategy")
	}
	if s.fetchFunc == nil {
		t.Error("expected fetchFunc to be set")
	}
	if s.commitExistsFunc == nil {
		t.Error("expected commitExistsFunc to be set")
	}
	if s.checkoutFunc == nil {
		t.Error("expected checkoutFunc to be set")
	}
}
