package capability

import (
	"fmt"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Capabilities represents fetch-related capabilities advertised by a Git server.
type Capabilities struct {
	AllowReachableSHA1InWant bool
	AllowTipSHA1InWant       bool
	Shallow                  bool
}

type StrategyType int


const (
	StrategyShallowSHA StrategyType = iota
	StrategyFullSHA
	StrategyIncrementalDeepen
	StrategyFullClone
)

// CanFetchBySHA returns true if the server allows fetching by arbitrary SHA.
func (c *Capabilities) CanFetchBySHA() bool {
	return c.AllowReachableSHA1InWant
}

// CanFetchShallow returns true if the server supports shallow clones.
func (c *Capabilities) CanFetchShallow() bool {
	return c.Shallow
}

// CapabilityDetector detects Git server capabilities.
type CapabilityDetector struct {
	factory SessionFactory
}


// NewCapabilityDetector creates a new CapabilityDetector with the default session factory.
func NewCapabilityDetector() *CapabilityDetector {
	return &CapabilityDetector{
		factory: NewDefaultSessionFactory(),
	}
}


// NewCapabilityDetectorWithFactory creates a new CapabilityDetector with a custom session factory.
func NewCapabilityDetectorWithFactory(factory SessionFactory) *CapabilityDetector {
	return &CapabilityDetector{
		factory: factory,
	}
}

// Detect ask the server for capabilities and return the supported capabilities
// Detect queries the Git server at the given URL and returns its capabilities.
func (d *CapabilityDetector) Detect(url string, auth transport.AuthMethod) (*Capabilities, error) {
	// the session is lazy: go-git return a session without a real connection
	session, err := d.factory.NewSession(url, auth)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	defer session.Close()
    // The connection is created only when you start to use the session
	advRefs, err := session.AdvertisedReferences()
	if err != nil {
		return nil, fmt.Errorf("get advertised refs: %w", err)
	}

	return capabilitiesFromList(advRefs.Capabilities), nil
}

func capabilitiesFromList(caps *capability.List) *Capabilities {
	return &Capabilities{
		AllowReachableSHA1InWant: caps.Supports(capability.AllowReachableSHA1InWant),
		AllowTipSHA1InWant:       caps.Supports(capability.AllowTipSHA1InWant),
		Shallow:                  caps.Supports(capability.Shallow),
	}
}

// The `ChooseStrategy` method in the `CapabilityDetector` struct is
// determining the appropriate strategy type based on the capabilities
// provided by the Git server. It takes a `Capabilities` struct as input
// and evaluates the capabilities to decide which strategy should be used
// for fetching data from the server.
func (d *CapabilityDetector) ChooseStrategy(caps *Capabilities) StrategyType {
	if caps.CanFetchBySHA() && caps.CanFetchShallow() {
		return StrategyShallowSHA
	}

	if caps.CanFetchBySHA() && !caps.CanFetchShallow() {
		return StrategyFullSHA
	}

	if caps.Shallow {
		return StrategyIncrementalDeepen
	}

	return StrategyFullClone
}

func (st StrategyType) String() string {
	switch st {
	case StrategyShallowSHA:
		return "ShallowSHA"
	case StrategyFullSHA:
		return "FullSHA"
	case StrategyIncrementalDeepen:
		return "StrategyIncrementalDeepen"
	case StrategyFullClone:
		return "StrategyFullClone"
	default:
		return "Unknown"
	}
}
