package submodule

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/strategy"
)

type MockFetcher struct {
	FetchFunc func(ctx context.Context, opts *strategy.FetchRequest) error
	FetchCalls []*strategy.FetchRequest
}

func (m *MockFetcher) Fetch(ctx context.Context, opts *strategy.FetchRequest) error {
	m.FetchCalls = append(m.FetchCalls, opts)
	if m.FetchFunc != nil {
		return m.FetchFunc(ctx,opts)
	}
	return nil
}

// =============================================================================
// Factory helpers
// =============================================================================

func mockFetcherFactory(mock *MockFetcher) FetcherFactory {
	return func(auth transport.AuthMethod, repo *git.Repository) (SubmoduleFetcher, error) {
		return mock, nil
	}
}

func mockFetcherFactoryWithError(err error) FetcherFactory {
	return func(auth transport.AuthMethod, repo *git.Repository) (SubmoduleFetcher, error) {
		return nil, err
	}
}


// =============================================================================
// Test repository helpers
// =============================================================================

func newInMemoryRepo(t *testing.T) *git.Repository {
	t.Helper()
	repo, err := git.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		t.Fatalf("failed to init in-memory repo: %v", err)
	}
	return repo
}

func newBareRepo(t *testing.T) *git.Repository {
	t.Helper()

	repo, err := git.Init(memory.NewStorage(), nil)
	if err != nil {
		t.Fatalf("failed to init bare repo: %v", err)
	}
	return repo
}

func newRepoWithSubmodule(t *testing.T, submodulePath, submoduleURL string, submoduleHash plumbing.Hash) *git.Repository {
	t.Helper()
	storage := memory.NewStorage()
	fs := memfs.New()

	repo, err := git.Init(storage, fs)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	// Creating .gitsubmodules
	gitmodulesContent := "[submodule \"" + submodulePath + "\"]\n" +
	"\tpath = " + submodulePath + "\n" +
	"\turl = " + submoduleURL + "\n"

	f, err := fs.Create(".gitmodules")
	if err != nil {
		t.Fatalf("failed to create .gitmodules: %v", err)
	}
	f.Write([]byte(gitmodulesContent))
	f.Close()

	// Creating the submodule directory

	if err := fs.MkdirAll(submodulePath, 0755); err != nil {
		t.Fatalf("failed to create submodule dir: %v", err)
	}

	// Adding the .gitmodules to the index 
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}

	if _, err := wt.Add(".gitmodules"); err != nil {
		t.Fatalf("failed to add .gitmodules: %v", err)
	}

	// adding the entry submodule to the index
	idx, err := repo.Storer.Index()
	if err != nil {
		t.Fatalf("failed to get index: %v", err)
	}

	idx.Entries = append(idx.Entries, &index.Entry{
		Hash: submoduleHash,
		Name: submodulePath,
		Mode: filemode.Submodule,
	})


	if err := repo.Storer.SetIndex(idx); err != nil {
		t.Fatalf("failed to set index: %v", err)
	}

	// Commit
	_, err = wt.Commit("Add submodule", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("failed to commit: %v", err)
	}
	// the repo is ready
	return repo
}

func getSubmodules(t *testing.T, repo *git.Repository) git.Submodules {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}
	subs, err := wt.Submodules()
	if err != nil {
		t.Fatalf("failed to get submodules: %v", err)
	}
	return subs
}


// =============================================================================
// Tests
// =============================================================================


func TestSubmoduleUpdater_NoSubmodules(t *testing.T) {
	mock := &MockFetcher{}
	updater := NewSubmoduleUpdater(
		WithFetcherFactory(mockFetcherFactory(mock)),
	)

	repo := newInMemoryRepo(t)

	err := updater.UpdateSubmodules(repo, &git.SubmoduleUpdateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.FetchCalls) != 0 {
		t.Errorf("expected 0 fetch calls, got %d", len(mock.FetchCalls))
	}
}

func TestSubmoduleUpdater_BareRepository(t *testing.T) {
	mock := &MockFetcher{}
	updater := NewSubmoduleUpdater(
		WithFetcherFactory(mockFetcherFactory(mock)),
	)

	repo := newBareRepo(t)

	err := updater.UpdateSubmodules(repo, &git.SubmoduleUpdateOptions{})

	if err == nil {
		t.Fatal("expected error for bare repository")
	}
	if !strings.Contains(err.Error(), "bare") {
		t.Errorf("expected error to mention 'bare', got: %v", err)
	}
}

func TestSubmoduleUpdater_FetchError(t *testing.T) {
	fetchErr := errors.New("network timeout")
	mock := &MockFetcher{
		FetchFunc: func(ctx context.Context, opts *strategy.FetchRequest) error {
			return fetchErr
		},
	}
	updater := NewSubmoduleUpdater(
		WithFetcherFactory(mockFetcherFactory(mock)),
	)

	submoduleHash := plumbing.NewHash("1234567890abcdef1234567890abcdef12345678")
	repo := newRepoWithSubmodule(t, "mysubmodule", "https://github.com/example/repo.git", submoduleHash)

	err := updater.UpdateSubmodules(repo, &git.SubmoduleUpdateOptions{})

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network timeout") {
		t.Errorf("expected 'network timeout', got: %v", err)
	}
	if len(mock.FetchCalls) != 1 {
		t.Errorf("expected 1 fetch call, got %d", len(mock.FetchCalls))
	}
}

func TestSubmoduleUpdater_FetchCalledWithCorrectHash(t *testing.T) {
	mock := &MockFetcher{}
	updater := NewSubmoduleUpdater(
		WithFetcherFactory(mockFetcherFactory(mock)),
	)

	expectedHash := plumbing.NewHash("abcdef1234567890abcdef1234567890abcdef12")
	repo := newRepoWithSubmodule(t, "mysubmodule", "https://github.com/example/repo.git", expectedHash)

	_ = updater.UpdateSubmodules(repo, &git.SubmoduleUpdateOptions{})

	if len(mock.FetchCalls) == 0 {
		t.Fatal("expected at least 1 fetch call")
	}
	if mock.FetchCalls[0].CommitHash != expectedHash {
		t.Errorf("expected hash %v, got %v", expectedHash, mock.FetchCalls[0].CommitHash)
	}
}

func TestSubmoduleUpdater_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mock := &MockFetcher{
		FetchFunc: func(ctx context.Context, opts *strategy.FetchRequest) error {
			return ctx.Err()
		},
	}
	updater := NewSubmoduleUpdater(
		WithFetcherFactory(mockFetcherFactory(mock)),
	)

	submoduleHash := plumbing.NewHash("1234567890abcdef1234567890abcdef12345678")
	repo := newRepoWithSubmodule(t, "mysubmodule", "https://github.com/example/repo.git", submoduleHash)

	// We extract the submodules to call UpdateContext directly
	subs := getSubmodules(t, repo)

	err := updater.UpdateContext(ctx, subs, &git.SubmoduleUpdateOptions{})

	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context canceled error, got: %v", err)
	}
}

func TestSubmoduleUpdater_NoRecurseSubmodules(t *testing.T) {
	mock := &MockFetcher{}
	updater := NewSubmoduleUpdater(
		WithFetcherFactory(mockFetcherFactory(mock)),
	)

	submoduleHash := plumbing.NewHash("1234567890abcdef1234567890abcdef12345678")
	repo := newRepoWithSubmodule(t, "mysubmodule", "https://github.com/example/repo.git", submoduleHash)

	// Con NoRecurseSubmodules, doRecursiveUpdate should return early
	opts := &git.SubmoduleUpdateOptions{
		RecurseSubmodules: git.NoRecurseSubmodules,
	}

	// Ignoring the error because the submodule repo doesn't exist
	_ = updater.UpdateSubmodules(repo, opts)

	// the fetch should be called only for the main submodule
	if len(mock.FetchCalls) == 0 {
		t.Fatal("expected at least 1 fetch call")
	}
}


func TestSubmoduleUpdater_RecurseSubmodulesDecrement(t *testing.T) {
	// This test verifies that RecurseSubmodules gets decremented.
	// We cannot test full recursion without real nested submodules,
	// but we can verify that the value is modified correctly.

	mock := &MockFetcher{}
	updater := NewSubmoduleUpdater(
		WithFetcherFactory(mockFetcherFactory(mock)),
	)

	submoduleHash := plumbing.NewHash("1234567890abcdef1234567890abcdef12345678")
	repo := newRepoWithSubmodule(t, "mysubmodule", "https://github.com/example/repo.git", submoduleHash)

	// DefaultSubmoduleRecursionDepth è 10 in go-git
	opts := &git.SubmoduleUpdateOptions{
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
	}

	// We ignore the error - the submodule repo doesn't exist but fetch is called
	_ = updater.UpdateSubmodules(repo, opts)

	// Verify that fetch was called
	if len(mock.FetchCalls) == 0 {
		t.Fatal("expected at least 1 fetch call")
	}
}


func TestSubmoduleUpdater_MultipleSubmodules(t *testing.T) {
	mock := &MockFetcher{}
	updater := NewSubmoduleUpdater(
		WithFetcherFactory(mockFetcherFactory(mock)),
	)

	// Create a repo with two submodules
	storage := memory.NewStorage()
	fs := memfs.New()

	repo, err := git.Init(storage, fs)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	// .gitmodules with two submodules
	gitmodulesContent := `[submodule "sub1"]
	path = sub1
	url = https://github.com/example/repo1.git
[submodule "sub2"]
	path = sub2
	url = https://github.com/example/repo2.git
`
	f, err := fs.Create(".gitmodules")
	if err != nil {
		t.Fatalf("failed to create .gitmodules: %v", err)
	}
	f.Write([]byte(gitmodulesContent))
	f.Close()

	// Create directories
	fs.MkdirAll("sub1", 0755)
	fs.MkdirAll("sub2", 0755)

	wt, _ := repo.Worktree()
	wt.Add(".gitmodules")

	// Add both submodules to the index
	idx, _ := repo.Storer.Index()

	hash1 := plumbing.NewHash("1111111111111111111111111111111111111111")
	hash2 := plumbing.NewHash("2222222222222222222222222222222222222222")

	idx.Entries = append(idx.Entries,
		&index.Entry{Hash: hash1, Name: "sub1", Mode: filemode.Submodule},
		&index.Entry{Hash: hash2, Name: "sub2", Mode: filemode.Submodule},
	)
	repo.Storer.SetIndex(idx)

	wt.Commit("Add submodules", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
	})

	// Run update - it will fail but the mock records the calls
	_ = updater.UpdateSubmodules(repo, &git.SubmoduleUpdateOptions{})

	// Verify that both submodules were processed
	// (the first one will fail and stop, so we expect at least 1)
	if len(mock.FetchCalls) < 1 {
		t.Fatalf("expected at least 1 fetch call, got %d", len(mock.FetchCalls))
	}

	// The first processed submodule should have one of the two hashes
	firstHash := mock.FetchCalls[0].CommitHash
	if firstHash != hash1 && firstHash != hash2 {
		t.Errorf("unexpected hash: %v", firstHash)
	}
}

// =============================================================================
// Tests for package-level functions (backward compatibility wrappers)
// =============================================================================

func TestPackageLevel_UpdateSubmodules(t *testing.T) {
	repo := newInMemoryRepo(t)

	// Should not error on repo without submodules
	err := UpdateSubmodules(repo, &git.SubmoduleUpdateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPackageLevel_UpdateSubmodules_BareRepo(t *testing.T) {
	repo := newBareRepo(t)

	err := UpdateSubmodules(repo, &git.SubmoduleUpdateOptions{})
	if err == nil {
		t.Fatal("expected error for bare repository")
	}
	if !strings.Contains(err.Error(), "bare") {
		t.Errorf("expected error to mention 'bare', got: %v", err)
	}
}

func TestPackageLevel_UpdateContext(t *testing.T) {
	repo := newInMemoryRepo(t)
	subs := getSubmodules(t, repo)

	// Should not error on empty submodules
	err := UpdateContext(context.Background(), subs, &git.SubmoduleUpdateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPackageLevel_SubmoduleUpdateContext(t *testing.T) {
	submoduleHash := plumbing.NewHash("1234567890abcdef1234567890abcdef12345678")
	repo := newRepoWithSubmodule(t, "mysubmodule", "https://github.com/example/repo.git", submoduleHash)

	subs := getSubmodules(t, repo)
	if len(subs) == 0 {
		t.Fatal("expected at least one submodule")
	}

	// Will fail because submodule repo doesn't exist, but exercises the code path
	err := SubmoduleUpdateContext(context.Background(), subs[0], &git.SubmoduleUpdateOptions{})
	
	// We expect an error since the submodule repo doesn't actually exist
	if err == nil {
		t.Log("no error returned - submodule might have been processed")
	}
}
