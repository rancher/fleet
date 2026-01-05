package strategy

import (
	"context"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/capability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Repository tests
// =============================================================================
func newTestRepository(t *testing.T, remoteUrl string) *git.Repository {
	t.Helper()
	fs := memfs.New()
	r, err := git.Init(memory.NewStorage(), fs)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}
	_, err = r.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteUrl},
	})
	if err != nil {
		t.Fatalf("failed to create remote: %v", err)
	}

	return r
}

func newTestRepositoryWithRemote(t *testing.T, remoteName, remoteURL string) *git.Repository {
	t.Helper()

	fs := memfs.New()
	r, err := git.Init(memory.NewStorage(), fs)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	_, err = r.CreateRemote(&config.RemoteConfig{
		Name: remoteName,
		URLs: []string{remoteURL},
	})
	if err != nil {
		t.Fatalf("failed to create remote: %v", err)
	}

	return r
}

// =============================================================================
// Integration tests - Strategy behavior verification
// =============================================================================

const (
	// Fixture repository with known history
	fixtureRepoURL = "https://github.com/git-fixtures/basic"

	// Known commit SHA in the repository (HEAD of master at time of writing)
	// This commit has a history of ~9 commits
	fixtureCommitSHA = "1669dce138d9b841a518c64b10914d88f5e488ea"
)

// Expected object counts per strategy for the fixture repository.
// These values must be determined empirically by running the script git-count.sh
//
// To determine the correct values:
// 1. Run: ./git-counts.sh
// 2. Note the printed values
// 3. Update the constants below

var expectedObjectCounts = map[capability.StrategyType]int{
	// ShallowSHA (depth=1): 1 commit + tree + blobs
	capability.StrategyShallowSHA: 6,

	// FullSHA: all commits up to that SHA + trees + blobs
	capability.StrategyFullSHA: 13,

	//  Incremental Shallow:  all the commit from the tip to the commit  + tree + blobs
	capability.StrategyIncrementalDeepen: 24,

	// FullClone: entire repository
	capability.StrategyFullClone: 31,
}

// countObjects counts all objects in the repository
func countObjects(t *testing.T, repo *git.Repository) int {
	t.Helper()

	iter, err := repo.Storer.IterEncodedObjects(plumbing.AnyObject)
	require.NoError(t, err)

	count := 0
	err = iter.ForEach(func(obj plumbing.EncodedObject) error {
		count++
		return nil
	})
	require.NoError(t, err)

	return count
}

// executeStrategy executes the appropriate strategy for the given type
func executeStrategy(t *testing.T, ctx context.Context, repo *git.Repository, strategyType capability.StrategyType, req *plumbing.Hash) error {
	t.Helper()

	switch strategyType {
	case capability.StrategyShallowSHA:
		return NewShallowSHAStrategy(nil).Execute(ctx, repo, *req)
	case capability.StrategyFullSHA:
		return NewFullSHAStrategy(nil).Execute(ctx, repo, *req)
	case capability.StrategyIncrementalDeepen:
		return NewIncrementalStrategy(nil).Execute(ctx, repo, *req)
	case capability.StrategyFullClone:
		return NewFullCloneStrategy(nil).Execute(ctx, repo, *req)
	default:
		t.Fatalf("unknown strategy type: %v", strategyType)
		return nil
	}
}

// TestStrategiesWithExpectedCounts verifies that each strategy produces
// exactly the expected number of objects
func TestStrategiesWithExpectedCounts(t *testing.T) {

	commitHash := plumbing.NewHash(fixtureCommitSHA)
	ctx := context.Background()

	for strategyType, expectedCount := range expectedObjectCounts {
		t.Run(strategyType.String(), func(t *testing.T) {
			repo := newTestRepository(t, fixtureRepoURL)

			err := executeStrategy(t, ctx, repo, strategyType, &commitHash)
			require.NoError(t, err)

			count := countObjects(t, repo)
			assert.Equal(t, expectedCount, count,
				"Strategy %s should produce exactly %d objects, got %d",
				strategyType, expectedCount, count)
		})
	}
}
