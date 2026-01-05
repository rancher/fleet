package submodule

import (
	"context"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/capability"

	"github.com/rancher/fleet/internal/mocks"
	"go.uber.org/mock/gomock"
	"testing"
	"errors"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Repository tests
// =============================================================================
func newTestRepository(t *testing.T, remoteUrl string)  *git.Repository {
	t.Helper()
	fs := memfs.New()
	r, err := git.Init(memory.NewStorage(),fs)
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
// NewFetcher tests
// =============================================================================

func TestNewFetcher_Success(t *testing.T) {
	r := newTestRepository(t, "https://github.com/test/repo.git")
	f, err := NewFetcher(nil,r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected fetcher, got nil")
	}
}

func TestNewFetcher_noRemote(t *testing.T) {
	r, _ := git.Init(memory.NewStorage(), nil)
	_, err := NewFetcher(nil, r)
	if err == nil {
		t.Fatal("expected error")
	}

}

func TestNewFetcher_RemoteNoURLs(t *testing.T) {
	repo, _ := git.Init(memory.NewStorage(), nil)
	//nolint:errcheck // CreateRemote fails with empty URLs, but we need this invalid state to test NewFetcher
	_, _ = repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{},
	})


	_, err := NewFetcher(nil, repo)

	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewFetcher_WithCustomRemoteName(t *testing.T) {
	repo := newTestRepositoryWithRemote(t, "upstream", "https://github.com/test/repo.git")

	f, err := NewFetcher(nil, repo, WithRemoteName("upstream"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected fetcher, got nil")
	}
}

func TestNewFetcher_WithCustomRemoteName_NotFound(t *testing.T) {
	repo := newTestRepository(t, "https://github.com/test/repo.git")

	_, err := NewFetcher(nil, repo, WithRemoteName("upstream"))
	if err == nil {
		t.Fatal("expected error")
	}
}


// =============================================================================
// Fetch tests
// =============================================================================


func TestFetch_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDetector := mocks.NewMockCapabilityDetector(ctrl)
	mockStrategy := mocks.NewMockStrategy(ctrl)

	testURL := "https://github.com/test/repo.git"
	r := newTestRepository(t, testURL)
	caps := &capability.Capabilities{Shallow: true}

	//Setup expectations
	mockDetector.EXPECT().
		Detect(testURL, nil).
		Return(caps, nil)

	mockDetector.EXPECT().
		ChooseStrategy(caps).
		Return(capability.StrategyShallowSHA)

	mockStrategy.EXPECT().
		Execute(gomock.Any(), gomock.Any(),gomock.Any()).
		Return(nil)

	f, err := NewFetcher(nil, r,
		WithDetector(mockDetector),
		WithStrategies(map[capability.StrategyType]Strategy{
			capability.StrategyShallowSHA: mockStrategy,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = f.Fetch(context.Background(), &plumbing.Hash{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFetch_DetectError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDetector := mocks.NewMockCapabilityDetector(ctrl)
	r := newTestRepository(t, "https://github.com/test/repo.git")


	mockDetector.EXPECT().
		Detect(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("connection refused"))

	f, err := NewFetcher(nil, r,
		WithDetector(mockDetector),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = f.Fetch(context.Background(), &plumbing.Hash{})
	if err == nil {
		t.Fatal("expected error")
	}
}


func TestFetch_StrategyNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDetector := mocks.NewMockCapabilityDetector(ctrl)

	r := newTestRepository(t, "https://github.com/test/repo.git")
	caps := &capability.Capabilities{}

	mockDetector.EXPECT().
		Detect(gomock.Any(), gomock.Any()).
		Return(caps, nil)

	mockDetector.EXPECT().
		ChooseStrategy(caps).
		Return(capability.StrategyShallowSHA)

	f, err := NewFetcher(nil, r,
		WithDetector(mockDetector),
		WithStrategies(map[capability.StrategyType]Strategy{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Fetch(context.Background(), &plumbing.Hash{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFetch_StrategyError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDetector := mocks.NewMockCapabilityDetector(ctrl)
	mockStrategy := mocks.NewMockStrategy(ctrl)

	r := newTestRepository(t, "https://github.com/test/repo.git")
	caps := &capability.Capabilities{}

	mockDetector.EXPECT().
		Detect(gomock.Any(), gomock.Any()).
		Return(caps, nil)

	mockDetector.EXPECT().
		ChooseStrategy(caps).
		Return(capability.StrategyShallowSHA)

	mockStrategy.EXPECT().
		Execute(gomock.Any(), gomock.Any(),gomock.Any()).
		Return(errors.New("fetch failed"))

	f, err := NewFetcher(nil, r,
		WithDetector(mockDetector),
		WithStrategies(map[capability.StrategyType]Strategy{
			capability.StrategyShallowSHA: mockStrategy,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = f.Fetch(context.Background(), &plumbing.Hash{})

	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFetch_NilCaps(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDetector := mocks.NewMockCapabilityDetector(ctrl)
	mockStrategy := mocks.NewMockStrategy(ctrl)


	r := newTestRepository(t, "https://github.com/test/repo.git")

	mockDetector.EXPECT().
		Detect(gomock.Any(), gomock.Any()).
		Return(nil, nil)

	mockDetector.EXPECT().
		ChooseStrategy(gomock.Any()).
		Return(capability.StrategyFullClone)

	mockStrategy.EXPECT().
		Execute(gomock.Any(), gomock.Any(),gomock.Any()).
		Return(nil)

	f, err := NewFetcher(nil, r,
		WithDetector(mockDetector),
		WithStrategies(map[capability.StrategyType]Strategy{
			capability.StrategyFullClone: mockStrategy,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = f.Fetch(context.Background(), &plumbing.Hash{})


	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}


// =============================================================================
// WithForcedStrategy tests (unit tests with mocks)
// =============================================================================

func TestFetch_WithForcedStrategy_BypassesDetection(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDetector := mocks.NewMockCapabilityDetector(ctrl)
	mockStrategy := mocks.NewMockStrategy(ctrl)

	r := newTestRepository(t, "https://github.com/test/repo.git")

	// Detector should NOT be called when strategy is forced
	// no EXPECT calls for mockDetector

	mockStrategy.EXPECT().
		Execute(gomock.Any(),gomock.Any(),gomock.Any()).
		Return(nil)

	f, err := NewFetcher(nil, r,
		WithDetector(mockDetector),
		WithStrategies(map[capability.StrategyType]Strategy{
			capability.StrategyShallowSHA: mockStrategy,
		}),
		WithForcedStrategy(capability.StrategyShallowSHA),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Fetch(context.Background(), &plumbing.Hash{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFetch_WithForcedStrategy_Strategy_Not_Found(t *testing.T) {
	r := newTestRepository(t,"https://github.com/test/repo.git")

	f, err := NewFetcher(nil, r,
		WithStrategies(map[capability.StrategyType]Strategy{}),
		WithForcedStrategy(capability.StrategyShallowSHA),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = f.Fetch(context.Background(), &plumbing.Hash{})
	if err == nil {
		t.Fatal("expected error for missing strategy")
	}
}


func TestFetch_WithForcedStrategy_StrategyError (t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockStrategy := mocks.NewMockStrategy(ctrl)
	r := newTestRepository(t,"https://github.com/test/repo.git")

	mockStrategy.EXPECT().
		Execute(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("strategy execution failed"))

	f, err := NewFetcher(nil, r,
		WithStrategies(map[capability.StrategyType]Strategy{
			capability.StrategyFullSHA: mockStrategy,
		}),
		WithForcedStrategy(capability.StrategyFullSHA),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = f.Fetch(context.Background(), &plumbing.Hash{})
	if err == nil {
		t.Fatal("expected error")
	}
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
// These values must be determined empirically by running the script git-counts.sh
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


// TestStrategiesWithExpectedCounts verifies that each strategy produces
// exactly the expected number of objects
func TestStrategiesWithExpectedCounts(t *testing.T) {

	commitHash := plumbing.NewHash(fixtureCommitSHA)
	ctx := context.Background()

	for stratType, expectedCount := range expectedObjectCounts {
		t.Run(string(stratType.String()), func(t *testing.T) {
			repo := newTestRepository(t, fixtureRepoURL)

			fetcher, err := NewFetcher(
				nil,
				repo,
				WithForcedStrategy(stratType),
			)
			require.NoError(t, err)

			err = fetcher.Fetch(ctx, &commitHash)
			require.NoError(t, err)

			count := countObjects(t, repo)
			assert.Equal(t, expectedCount, count,
				"Strategy %s should produce exactly %d objects, got %d",
				stratType, expectedCount, count)
		})
	}
}
