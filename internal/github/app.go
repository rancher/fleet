package github

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

// NewApp creates a new GitHubApp instance with the provided app ID, installation ID,
// and private key, and returns a pointer to it.
func NewApp(appID, installID int64, pem []byte) *GitHubApp {
	return &GitHubApp{appID: appID, installID: installID, pem: pem}
}

// GetToken retrieves a GitHub App installation token using the provided app ID,
// installation ID, and private key (PEM format). It returns the token as a string
// or an error if the process fails.
func (app *GitHubApp) GetToken(ctx context.Context) (string, error) {
	err := app.checkIfPrivateKeyIsValid()
	if err != nil {
		return "", err
	}

	tr := http.DefaultTransport
	itr, err := ghinstallation.New(tr, app.appID, app.installID, app.pem)
	if err != nil {
		return "", err
	}

	token, err := itr.Token(ctx)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (app *GitHubApp) checkIfPrivateKeyIsValid() error {
	blk, _ := pem.Decode(app.pem)
	if blk == nil {
		return fmt.Errorf("githubapp: pem decode failed for app %d", app.appID)
	}
	if blk.Type != "RSA PRIVATE KEY" {
		return fmt.Errorf("githubapp: unsupported key type %q for app %d", blk.Type, app.appID)
	}
	if _, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err != nil {
		return fmt.Errorf("githubapp: invalid RSA key for app %d: %w", app.appID, err)
	}
	return nil
}
