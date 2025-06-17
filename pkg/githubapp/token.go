package githubapp

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
)

type Provider struct {
	appID, installID int64
	pemPath          string

	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

func New(appID, installID int64, pemPath string) *Provider {
	return &Provider{appID: appID, installID: installID, pemPath: pemPath}
}

func (p *Provider) GetToken(ctx context.Context) (string, error) {
	p.mu.RLock()
	token := p.token
	exp := p.expiresAt
	p.mu.RUnlock()

	if token != "" && time.Until(exp) > 5*time.Minute {
		return token, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	// This second check protects against race conditions between releasing
	// the read lock and acquiring the write lock.
	if p.token != "" && time.Until(p.expiresAt) > 5*time.Minute {
		return p.token, nil
	}

	tr := http.DefaultTransport
	itr, err := ghinstallation.NewKeyFromFile(tr, p.appID, p.installID, p.pemPath)
	if err != nil {
		return "", err
	}

	token, err = itr.Token(ctx)
	if err != nil {
		return "", err
	}
	expTime, _, err := itr.Expiry()
	if err != nil {
		return "", err
	}

	p.token = token
	p.expiresAt = expTime
	return token, nil
}
