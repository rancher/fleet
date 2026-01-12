package submodule

//go:generate mockgen --build_flags=--mod=mod -source=fetcher.go  -destination=../../../../mocks/git_fetcher_mock.go  -package=mocks github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule Strategy,CapabilityDetector

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/capability"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/strategy"
)

// Strategy defines the interface for fetch strategies.
// Each strategy implements a different approach to fetching a specific commit
// from a remote Git server, optimized for different server capabilities.
type Strategy interface {
	Type() capability.StrategyType
	// Execute fetches the specified commit from the remote.
	Execute(ctx context.Context, r *git.Repository, CommitHash plumbing.Hash) error
}

// CapabilityDetectorInterface abstracts capability detection for testing.
// It allows injecting mock detectors to test Fetcher behavior without
// making actual network calls to Git servers.
type CapabilityDetector interface {
	// Detect queries the Git server at the given URL for its capabilities.
	// Returns the server's advertised capabilities or an error if unreachable.
	Detect(url string, auth transport.AuthMethod) (*capability.Capabilities, error)
	// ChooseStrategy selects the optimal fetch strategy based on capabilities.
	ChooseStrategy(caps *capability.Capabilities) capability.StrategyType
}

// Fetcher orchestrates the fetching of a specific commit from a remote repository.
// It automatically detects server capabilities and selects the optimal strategy,
// or uses a forced strategy if configured (useful for testing or debugging).
//
type Fetcher struct {
	detector       CapabilityDetector
	// strategies maps strategy types to their implementations.
	strategies     map[capability.StrategyType]Strategy
	// Auth contains credentials for authenticating with the remote server.
	Auth           transport.AuthMethod
	// repository is the local go-git repository where fetched objects are stored.
	repository     *git.Repository
	remoteName     string
	// url is the remote repository URL
	url            string
	// forcedStrategy bypasses capability detection when set.
	forcedStrategy *capability.StrategyType // Allows bypassing capability detection

}

// FetcherOption configures a Fetcher instance.
// Use with NewFetcher to customize behavior.
type FetcherOption func(*Fetcher)

// WithDetector injects a custom capability detector.
// Primarily used for testing to avoid network calls.
func WithDetector(d CapabilityDetector) FetcherOption {
	return func(f *Fetcher) { f.detector = d }
}

// WithStrategies injects custom strategy implementations.
// Primarily used for testing to verify strategy selection.
func WithStrategies(s map[capability.StrategyType]Strategy) FetcherOption {
	return func(f *Fetcher) { f.strategies = s }
}

func WithRemoteName(name string) FetcherOption {
	return func(f *Fetcher) { f.remoteName = name }
}

// WithForcedStrategy forces the use of a specific strategy,
// completely bypassing capability detection.
// Useful for tests where you want to verify the behavior
// of each strategy against the same server.
func WithForcedStrategy(st capability.StrategyType) FetcherOption {
	return func(f *Fetcher) {
		f.forcedStrategy = &st
	}
}

func NewFetcher(auth transport.AuthMethod, repo *git.Repository, opts ...FetcherOption) (*Fetcher, error) {
	f := &Fetcher{
		Auth:       auth,
		repository: repo,
		remoteName: "origin",
	}

	for _, opt := range opts {
		opt(f)
	}

	url, err := f.extractRemoteURL()
	if err != nil {
		return nil, err
	}
	f.url = url

	// Defaults
	if f.detector == nil {
		f.detector = capability.NewCapabilityDetector()
	}
	if f.strategies == nil {
		f.strategies = map[capability.StrategyType]Strategy{
			capability.StrategyShallowSHA:        strategy.NewShallowSHAStrategy(auth),
			capability.StrategyFullSHA:           strategy.NewFullSHAStrategy(auth),
			capability.StrategyIncrementalDeepen: strategy.NewIncrementalStrategy(auth),
			capability.StrategyFullClone:         strategy.NewFullCloneStrategy(auth),
		}
	}

	return f, nil
}

// Fetch retrieves (fetch + checkout) a specific commit from the remote repository.
func (f *Fetcher) Fetch(ctx context.Context, opts *plumbing.Hash) error {
	var strategyType capability.StrategyType

	if f.forcedStrategy != nil {
		// Bypass detection: use the forced strategy
		strategyType = *f.forcedStrategy
	} else {
		// Normal behavior: detect capabilities and choose strategy
		caps, err := f.detector.Detect(f.url, f.Auth)
		if err != nil {
			return fmt.Errorf("discovery: %w", err)
		}
		if caps == nil {
			caps = &capability.Capabilities{}
		}
		// Select strategy based on what the server supports.
		strategyType = f.detector.ChooseStrategy(caps)
	}
	// Look up the strategy implementation.
	st, ok := f.strategies[strategyType]
	if !ok {
		return fmt.Errorf("strategy %s not implemented", strategyType)
	}

	// Execute the selected strategy to fetch the commit.
	// The strategy handles all git protocol details internally.
	err := st.Execute(ctx, f.repository, *opts)
	if err != nil {
		return fmt.Errorf("fetch with strategy %s: %w", strategyType, err)
	}

	return nil
}

func (f *Fetcher) extractRemoteURL() (string, error) {
	remote, err := f.repository.Remote(f.remoteName)
	if err != nil {
		return "", fmt.Errorf("getting remote %s: %w", f.remoteName, err)
	}
	config := remote.Config()
	if len(config.URLs) == 0 {
		return "", fmt.Errorf("remote %s has no URLs", f.remoteName)
	}
	return config.URLs[0], nil
}
