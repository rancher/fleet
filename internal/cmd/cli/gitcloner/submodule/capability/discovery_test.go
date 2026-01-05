package capability

import (
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"testing"
	"errors"
	"strings"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"

)

func TestCapabilities_CanFetchBySHA(t *testing.T) {
	tests := []struct {
		name     string
		caps     Capabilities
		expected bool
	}{
		{
			name:     "both false",
			caps:     Capabilities{},
			expected: false,
		},
		{
			name:     "only reachable",
			caps:     Capabilities{AllowReachableSHA1InWant: true},
			expected: true,
		},
		{
			name:     "both true",
			caps:     Capabilities{AllowReachableSHA1InWant: true, AllowTipSHA1InWant: true},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.caps.CanFetchBySHA(); got != tt.expected {
				t.Errorf("CanFetchBySHA() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCapabilities_CanShallow(t *testing.T) {
	c := Capabilities{Shallow: true}
	if !c.CanFetchShallow() {
		t.Error("expected true")
	}

	c = Capabilities{Shallow: false}
	if c.CanFetchShallow() {
		t.Error("expected false")
	}
}

func TestCapabilitiesFromList(t *testing.T) {
	tests := []struct {
		name     string
		caps     []capability.Capability
		expected Capabilities
	}{
		{
			name:     "empty",
			caps:     nil,
			expected: Capabilities{},
		},
		{
			name:     "shallow only",
			caps:     []capability.Capability{capability.Shallow},
			expected: Capabilities{Shallow: true},
		},
		{
			name: "all capabilities",
			caps: []capability.Capability{
				capability.AllowReachableSHA1InWant,
				capability.AllowTipSHA1InWant,
				capability.Shallow,
			},
			expected: Capabilities{
				AllowReachableSHA1InWant: true,
				AllowTipSHA1InWant:       true,
				Shallow:                  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := capability.NewList()
			for _, c := range tt.caps {
				if err := list.Set(c); err != nil {
					t.Fatalf("failed to set capability %v: %v", c, err)
				}
			}

			result := capabilitiesFromList(list)

			if *result != tt.expected {
				t.Errorf("got %+v, want %+v", *result, tt.expected)
			}
		})
	}
}

func TestChooseStrategy(t *testing.T) {

	tests := []struct {
		name     string
		caps     *Capabilities
		expected StrategyType
	}{
		{
			name: "shallow SHA - can fetch by SHA and swallow",
			caps: &Capabilities{
				AllowReachableSHA1InWant: true,
				Shallow:                  true,
			},
			expected: StrategyShallowSHA,
		},
		{
			name: "full SHA - can fetch by SHA but no shallow",
			caps: &Capabilities{
				AllowReachableSHA1InWant: true,
				Shallow:                  false,
			},
			expected: StrategyFullSHA,
		},
		{
			name: "incremental deepen - shallow only",
			caps: &Capabilities{
				Shallow: true,
			},
			expected: StrategyIncrementalDeepen,
		},
		{
			name:     "full clone - no capabilities",
			caps:     &Capabilities{},
			expected: StrategyFullClone,
		},
	}

	d := &CapabilityDetector{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.ChooseStrategy(tt.caps)
			if got != tt.expected {
				t.Errorf("got %s, want %s", got, tt.expected)
			}
		})
	}
}

func TestStrategyType_String(t *testing.T) {
	tests := []struct {
		st       StrategyType
		expected string
	}{
		{StrategyShallowSHA, "ShallowSHA"},
		{StrategyFullSHA, "FullSHA"},
		{StrategyIncrementalDeepen, "StrategyIncrementalDeepen"},
		{StrategyFullClone, "StrategyFullClone"},
		{StrategyType(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.st.String(); got != tt.expected {
				t.Errorf("got %s, want %s", got, tt.expected)
			}
		})
	}
}

// --- Mocks ---

type mockSession struct {
	advRefs  *packp.AdvRefs
	advErr   error
	closeErr error
	closed   bool
}

func (m *mockSession) AdvertisedReferences() (*packp.AdvRefs, error) {
	return m.advRefs, m.advErr
}

func (m *mockSession) Close() error {
	m.closed = true
	return m.closeErr
}

type mockSessionFactory struct {
	session    UploadPackSession
	sessionErr error

	// Capture arguments for verification
	lastURL  string
	lastAuth transport.AuthMethod
}

func (f *mockSessionFactory) NewSession(url string, auth transport.AuthMethod) (UploadPackSession, error) {
	f.lastURL = url
	f.lastAuth = auth
	return f.session, f.sessionErr
}

// --- Helper ---

func newAdvRefsWithCaps(t *testing.T, caps ...capability.Capability) *packp.AdvRefs {
	t.Helper()
	list := capability.NewList()
	for _, c := range caps {
		if err := list.Set(c); err != nil {
			t.Fatalf("failed to set capability %v: %v", c, err)
		}
	}
	return &packp.AdvRefs{Capabilities: list}
}

func TestDetect_Success(t *testing.T) {
	advRefs := newAdvRefsWithCaps(
		t,
		capability.AllowReachableSHA1InWant,
		capability.Shallow,
	)

	session := &mockSession{advRefs: advRefs}
	factory := &mockSessionFactory{session: session}
	detector := NewCapabilityDetectorWithFactory(factory)

	caps, err := detector.Detect("https://github.com/foo/bar", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !caps.AllowReachableSHA1InWant {
		t.Error("expected AllowReachableSHA1InWant to be true")
	}
	if !caps.Shallow {
		t.Error("expected Shallow to be true")
	}
	if caps.AllowTipSHA1InWant {
		t.Error("expected AllowTipSHA1InWant to be false")
	}
}

func TestDetect_SessionFactoryError(t *testing.T) {
	factory := &mockSessionFactory{
		sessionErr: errors.New("connection refused"),
	}
	detector := NewCapabilityDetectorWithFactory(factory)

	_, err := detector.Detect("https://github.com/foo/bar", nil)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "create session") {
		t.Errorf("expected 'create session' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected 'connection refused' in error, got: %v", err)
	}
}

func TestDetect_AdvertisedRefsError(t *testing.T) {
	session := &mockSession{
		advErr: errors.New("protocol error"),
	}
	factory := &mockSessionFactory{session: session}
	detector := NewCapabilityDetectorWithFactory(factory)

	_, err := detector.Detect("https://github.com/foo/bar", nil)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "advertised refs") {
		t.Errorf("expected 'advertised refs' in error, got: %v", err)
	}
}

func TestDetect_SessionClosed(t *testing.T) {
	advRefs := newAdvRefsWithCaps(t)
	session := &mockSession{advRefs: advRefs}
	factory := &mockSessionFactory{session: session}
	detector := NewCapabilityDetectorWithFactory(factory)

	_, _ = detector.Detect("https://github.com/foo/bar", nil)

	if !session.closed {
		t.Error("session was not closed")
	}
}

func TestDetect_SessionClosedOnError(t *testing.T) {
	session := &mockSession{
		advErr: errors.New("some error"),
	}
	factory := &mockSessionFactory{session: session}
	detector := NewCapabilityDetectorWithFactory(factory)

	_, _ = detector.Detect("https://github.com/foo/bar", nil)

	if !session.closed {
		t.Error("session was not closed after error")
	}
}

func TestDetect_PassesURLAndAuth(t *testing.T) {
	advRefs := newAdvRefsWithCaps(t)
	session := &mockSession{advRefs: advRefs}
	factory := &mockSessionFactory{session: session}
	detector := NewCapabilityDetectorWithFactory(factory)

	expectedURL := "https://github.com/test/repo"
	_, _ = detector.Detect(expectedURL, nil)

	if factory.lastURL != expectedURL {
		t.Errorf("URL not passed correctly: got %s, want %s", factory.lastURL, expectedURL)
	}
}


func TestNewDefaultSessionFactory(t *testing.T) {
	factory := NewDefaultSessionFactory()
	if factory == nil {
		t.Fatal("expected non-nil factory")
	}
}

func TestNewCapabilityDetector(t *testing.T) {
	detector := NewCapabilityDetector()
	if detector == nil {
		t.Fatal("expected non-nil detector")
	}
	if detector.factory == nil {
		t.Fatal("expected non-nil factory")
	}
}

func TestNewCapabilityDetectorWithFactory(t *testing.T) {
	factory := &mockSessionFactory{}
	detector := NewCapabilityDetectorWithFactory(factory)

	if detector == nil {
		t.Fatal("expected non-nil detector")
	}
	if detector.factory != factory {
		t.Error("factory not set correctly")
	}
}

func TestDetect_GitHub(t *testing.T) {
	detector := NewCapabilityDetector()

	caps, err := detector.Detect("https://github.com/go-git/go-git", nil)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}

	// GitHub supports shallow clones
	if !caps.Shallow {
		t.Error("expected GitHub to support shallow clones")
	}

	t.Logf("GitHub capabilities: AllowReachableSHA1InWant=%v, AllowTipSHA1InWant=%v, Shallow=%v",
		caps.AllowReachableSHA1InWant,
		caps.AllowTipSHA1InWant,
		caps.Shallow,
	)

	strategy := detector.ChooseStrategy(caps)
	t.Logf("Chosen strategy: %s", strategy)
}

func TestDetect_GitLab(t *testing.T) {
	detector := NewCapabilityDetector()

	caps, err := detector.Detect("https://gitlab.com/gitlab-org/gitlab", nil)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}

	// GitLab supports shallow clones
	if !caps.Shallow {
		t.Error("expected GitLab to support shallow clones")
	}

	t.Logf("GitLab capabilities: AllowReachableSHA1InWant=%v, AllowTipSHA1InWant=%v, Shallow=%v",
		caps.AllowReachableSHA1InWant,
		caps.AllowTipSHA1InWant,
		caps.Shallow,
	)

	strategy := detector.ChooseStrategy(caps)
	t.Logf("Chosen strategy: %s", strategy)
}

func TestDetect_InvalidURL(t *testing.T) {
	detector := NewCapabilityDetector()

	_, err := detector.Detect("https://nonexistent.invalid/repo", nil)
	if err == nil {
		t.Error("expected error for invalid URL")
	}

	t.Logf("Expected error: %v", err)
}
