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
	if blk, _ := pem.Decode(p.pem); blk == nil || !x509.IsEncryptedPEMBlock(blk) && blk.Type != "RSA PRIVATE KEY" {
		return "", fmt.Errorf("githubapp: %d does not have a valid private key", p.appID)
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
