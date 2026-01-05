package capability

import (
	"fmt"

	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
)

// UploadPackSession represents a Git upload-pack session.
// This interface wraps transport.UploadPackSession to enable testing.
type UploadPackSession interface {
	AdvertisedReferences() (*packp.AdvRefs, error)
	Close() error
}

// SessionFactory creates upload-pack sessions to Git servers.
// Implementations can be swapped for testing or custom transport logic.
type SessionFactory interface {
	NewSession(url string, auth transport.AuthMethod) (UploadPackSession, error)
}

// DefaultSessionFactory is the production implementation that connects to real Git servers.
type DefaultSessionFactory struct{}

// NewDefaultSessionFactory creates a new DefaultSessionFactory.
func NewDefaultSessionFactory() *DefaultSessionFactory {
	return &DefaultSessionFactory{}
}


// NewSession creates a new upload-pack session to the specified Git server.
func (f *DefaultSessionFactory) NewSession(url string, auth transport.AuthMethod) (UploadPackSession, error) {
	endpoint, err := transport.NewEndpoint(url)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint %q: %w", url, err)
	}

	cli, err := client.NewClient(endpoint)
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	session, err := cli.NewUploadPackSession(endpoint, auth)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return session, nil
}
