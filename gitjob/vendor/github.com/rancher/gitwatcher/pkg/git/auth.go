package git

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"strings"
)

const (
	BasicAuthUsernameKey = "username"
	BasicAuthPasswordKey = "password"
	SSHAuthPrivateKey    = "ssh-privatekey"
)

var ErrNoSecret = fmt.Errorf("failed to find one of the following keys in secret: %v", []string{
	BasicAuthUsernameKey,
	BasicAuthPasswordKey,
	SSHAuthPrivateKey,
})

func noop() {}

type Auth struct {
	Basic Basic
	SSH   SSH
}

type Basic struct {
	Username string
	Password string
}

type SSH struct {
	Key []byte
}

func FromSecret(secret map[string][]byte) (Auth, error) {
	auth := Auth{}
	ok := auth.Basic.fromSecret(secret)
	ok = ok || auth.SSH.fromSecret(secret)
	if !ok {
		return auth, ErrNoSecret
	}
	return auth, nil
}

func (a Auth) Populate(url string) (string, []string, func()) {
	url = a.Basic.populate(url)
	env, close := a.SSH.populate()
	if len(env) == 0 {
		env = []string{"GIT_SSH_COMMAND=ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no"}
	}
	return url, env, close
}

func (b *Basic) fromSecret(secret map[string][]byte) bool {
	username, unameOK := secret[BasicAuthUsernameKey]
	if unameOK {
		b.Username = string(username)
	}
	password, pwdOK := secret[BasicAuthPasswordKey]
	if pwdOK {
		b.Password = string(password)
	}

	return unameOK && pwdOK
}

func (b *Basic) populate(gitURL string) string {
	if b.Username == "" && b.Password == "" {
		return gitURL
	}

	u, err := url.Parse(gitURL)
	if err != nil || !strings.HasPrefix(u.Scheme, "http") {
		return gitURL
	}

	u.User = url.UserPassword(b.Username, b.Password)
	return u.String()
}

func (s *SSH) fromSecret(secret map[string][]byte) bool {
	key, ok := secret[SSHAuthPrivateKey]
	if ok {
		s.Key = key
	}
	return ok
}

func (s *SSH) populate() ([]string, func()) {
	if len(s.Key) == 0 {
		return nil, noop
	}

	f, err := ioutil.TempFile("", "ssh-key")
	if err != nil {
		return nil, noop
	}
	close := func() {
		f.Close()
		os.Remove(f.Name())
	}

	if _, err := f.Write(s.Key); err != nil {
		return nil, close
	}

	if err := f.Close(); err != nil {
		return nil, close
	}

	return []string{
		fmt.Sprintf("GIT_SSH_COMMAND=ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s", f.Name()),
	}, close
}
