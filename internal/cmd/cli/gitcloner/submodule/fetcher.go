package submodule

//go:generate mockgen --build_flags=--mod=mod -source=fetcher.go  -destination=../../../../mocks/git_fetcher_mock.go  -package=mocks github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule Strategy,CapabilityDetector

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/capability"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner/submodule/strategy"
)

type Strategy interface {
	Type() capability.StrategyType
	Execute(ctx context.Context, r *git.Repository, req *strategy.FetchRequest)  error
}

type CapabilityDetector interface {
	Detect(url string, auth transport.AuthMethod) (*capability.Capabilities, error)
	ChooseStrategy(caps *capability.Capabilities) capability.StrategyType
}

type Fetcher struct {
	detector   CapabilityDetector
	strategies map[capability.StrategyType]Strategy
	Auth       transport.AuthMethod
	repository *git.Repository
	remoteName string
	url string
	forcedStrategy *capability.StrategyType // Allows bypassing capability detection

}


type FetcherOption func(*Fetcher)

func WithDetector(d CapabilityDetector) FetcherOption {
    return func(f *Fetcher) { f.detector = d }
}

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
            capability.StrategyFullClone:          strategy.NewFullCloneStrategy(auth),
        }
    }

    return f, nil
}


func (f *Fetcher) Fetch(ctx context.Context, opts *strategy.FetchRequest) error {
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
		strategyType = f.detector.ChooseStrategy(caps)
	}
	st, ok := f.strategies[strategyType]
	if !ok {
		return fmt.Errorf("strategy %s not implemented", strategyType)
	}

	err := st.Execute(ctx,f.repository, opts)
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
