package githubapp

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
)

type GitHubApp struct {
	appID, installID int64
	pem              []byte
}

func NewApp(appID, installID int64, pem []byte) *GitHubApp {
	return &GitHubApp{appID: appID, installID: installID, pem: pem}
}

func (p *GitHubApp) GetToken(ctx context.Context) (string, error) {
	err := p.checkIfPrivateKeyIsValid()
	if err != nil {
		return "", err
	}

	tr := http.DefaultTransport
	itr, err := ghinstallation.New(tr, p.appID, p.installID, []byte(p.pem))
	if err != nil {
		return "", err
	}

	token, err := itr.Token(ctx)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (p *GitHubApp) checkIfPrivateKeyIsValid() error {
	blk, _ := pem.Decode(p.pem)
	if blk == nil {
		return fmt.Errorf("githubapp: pem decode failed for app %d", p.appID)
	}
	if blk.Type != "RSA PRIVATE KEY" {
		return fmt.Errorf("githubapp: unsupported key type %q for app %d", blk.Type, p.appID)
	}
	if _, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err != nil {
		return fmt.Errorf("githubapp: invalid RSA key for app %d: %w", p.appID, err)
	}
	return nil
}
