package bundlereader

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-getter/v2"
	"github.com/stretchr/testify/assert"
)

// mockGetter is a test double for the Getter interface that allows
// controlling timing to test synchronization behavior.
type mockGetter struct {
	inGet     chan struct{} // signals when Get is entered
	canFinish chan struct{} // signals when Get can return
}

func newMockGetter() *mockGetter {
	return &mockGetter{
		inGet:     make(chan struct{}, 1),
		canFinish: make(chan struct{}),
	}
}

func (g *mockGetter) Get(ctx context.Context, req *getter.Request) (*getter.GetResult, error) {
	g.inGet <- struct{}{} // signal that we're inside Get
	<-g.canFinish         // wait for permission to finish
	return &getter.GetResult{}, nil
}

func TestGetMutexForGitHTTPS(t *testing.T) {
	mg := newMockGetter()
	ctx := context.Background()

	// Auth with CABundle triggers mutex acquisition for git::https:// URLs
	auth := Auth{CABundle: []byte("test-ca-bundle")}

	// Request that will be detected as git::https://
	req := &getter.Request{Src: "git::https://example.com/repo.git"}

	var wg sync.WaitGroup
	wg.Add(2)

	// Start first call
	go func() {
		defer wg.Done()
		_ = get(ctx, mg, req, auth)
	}()

	// Wait for first call to enter Get (it now holds the mutex)
	<-mg.inGet

	// Start second call
	secondEntered := make(chan struct{})
	go func() {
		defer wg.Done()
		close(secondEntered) // signal that goroutine started
		_ = get(ctx, mg, req, auth)
	}()

	// Wait for second goroutine to start
	<-secondEntered

	// Give second goroutine time to potentially enter Get (if mutex wasn't working)
	time.Sleep(50 * time.Millisecond)

	// Verify second call hasn't entered Get yet (should be blocked on mutex)
	select {
	case <-mg.inGet:
		t.Fatal("second call entered Get while first still running - mutex not working!")
	default:
		// Good - second call is blocked as expected
	}

	// Let first call finish
	mg.canFinish <- struct{}{}

	// Now second call should enter Get
	select {
	case <-mg.inGet:
		// Good - second call entered after first finished
	case <-time.After(1 * time.Second):
		t.Fatal("second call never entered Get after first finished")
	}

	// Let second call finish
	mg.canFinish <- struct{}{}

	// Wait for both goroutines to complete
	wg.Wait()
}

func TestGetNoMutexForNonGitHTTPS(t *testing.T) {
	ctx := context.Background()

	// Auth without CABundle or InsecureSkipVerify - no mutex needed
	auth := Auth{}

	// Regular HTTP request (not git::https://)
	req := &getter.Request{Src: "https://example.com/file.tar.gz"}

	// Create two mock getters to track concurrent access
	var concurrentCalls int
	var mu sync.Mutex
	var maxConcurrent int

	concurrentGetter := &concurrencyTrackingGetter{
		onGet: func() {
			mu.Lock()
			concurrentCalls++
			if concurrentCalls > maxConcurrent {
				maxConcurrent = concurrentCalls
			}
			mu.Unlock()

			time.Sleep(50 * time.Millisecond)

			mu.Lock()
			concurrentCalls--
			mu.Unlock()
		},
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_ = get(ctx, concurrentGetter, req, auth)
	}()

	go func() {
		defer wg.Done()
		_ = get(ctx, concurrentGetter, req, auth)
	}()

	wg.Wait()

	// Without mutex protection, both calls should run concurrently
	assert.Equal(t, 2, maxConcurrent, "expected concurrent execution when mutex is not needed")
}

// concurrencyTrackingGetter tracks concurrent calls to Get
type concurrencyTrackingGetter struct {
	onGet func()
}

func (g *concurrencyTrackingGetter) Get(ctx context.Context, req *getter.Request) (*getter.GetResult, error) {
	if g.onGet != nil {
		g.onGet()
	}
	return &getter.GetResult{}, nil
}

func TestNeedsGitSSLEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected bool
	}{
		{
			name:     "git https needs env vars",
			src:      "git::https://github.com/example/repo.git",
			expected: true,
		},
		{
			name:     "regular https does not need env vars",
			src:      "https://example.com/file.tar.gz",
			expected: false,
		},
		{
			name:     "git ssh does not need env vars",
			src:      "git::ssh://git@github.com/example/repo.git",
			expected: false,
		},
		{
			name:     "github.com shorthand needs env vars",
			src:      "github.com/foo/bar",
			expected: true,
		},
		{
			name:     "gitlab.com shorthand needs env vars",
			src:      "gitlab.com/foo/bar",
			expected: true,
		},
		{
			name:     "bitbucket.org shorthand needs env vars",
			src:      "bitbucket.org/foo/bar",
			expected: true,
		},
		{
			name:     "unknown domain without protocol does not need env vars",
			src:      "example.com/foo/bar",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &getter.Request{Src: tt.src}
			result := needsGitSSLEnvVars(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}
