package git

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rancher/wrangler/pkg/randomtoken"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	corev1 "k8s.io/api/core/v1"
	k8snet "k8s.io/apimachinery/pkg/util/net"
)

type options struct {
	Credential        *corev1.Secret
	CABundle          []byte
	InsecureTLSVerify bool
	Headers           map[string]string
}

func newGit(directory, url string, opts *options) (*git, error) {
	if err := validateURL(url); err != nil {
		return nil, err
	}

	if opts == nil {
		opts = &options{}
	}

	g := &git{
		URL:               url,
		Directory:         directory,
		caBundle:          opts.CABundle,
		insecureTLSVerify: opts.InsecureTLSVerify,
		secret:            opts.Credential,
		headers:           opts.Headers,
	}
	return g, g.setCredential(opts.Credential)
}

type git struct {
	URL               string
	Directory         string
	password          string
	agent             *agent.Agent
	caBundle          []byte
	insecureTLSVerify bool
	secret            *corev1.Secret
	headers           map[string]string
	knownHosts        []byte
}

// LsRemote runs ls-remote on git repo and returns the HEAD commit SHA
func (g *git) lsRemote(branch string, commit string) (string, error) {
	if err := validateBranch(branch); err != nil {
		return "", err
	}
	if commit != "" {
		if err := validateCommit(commit); err != nil {
			return "", err
		}
	}

	if changed, err := g.remoteSHAChanged(branch, commit); err != nil || !changed {
		return commit, err
	}

	output := &bytes.Buffer{}
	if err := g.gitCmd(output, "ls-remote", g.URL, formatRefForBranch(branch)); err != nil {
		return "", err
	}

	var lines []string
	s := bufio.NewScanner(output)
	for s.Scan() {
		lines = append(lines, s.Text())
	}

	return firstField(lines, fmt.Sprintf("no commit for branch: %s", branch))
}

func (g *git) httpClientWithCreds() (*http.Client, error) {
	var (
		username  string
		password  string
		tlsConfig tls.Config
	)

	if g.secret != nil {
		switch g.secret.Type {
		case corev1.SecretTypeBasicAuth:
			username = string(g.secret.Data[corev1.BasicAuthUsernameKey])
			password = string(g.secret.Data[corev1.BasicAuthPasswordKey])
		case corev1.SecretTypeTLS:
			cert, err := tls.X509KeyPair(g.secret.Data[corev1.TLSCertKey], g.secret.Data[corev1.TLSPrivateKeyKey])
			if err != nil {
				return nil, err
			}
			tlsConfig.Certificates = append(tlsConfig.Certificates, cert)
		}
	}

	if len(g.caBundle) > 0 {
		cert, err := x509.ParseCertificate(g.caBundle)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		pool.AddCert(cert)
		tlsConfig.RootCAs = pool
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tlsConfig
	transport.TLSClientConfig.InsecureSkipVerify = g.insecureTLSVerify

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
	if username != "" || password != "" {
		client.Transport = &basicRoundTripper{
			username: username,
			password: password,
			next:     client.Transport,
		}
	}

	return client, nil
}

func (g *git) remoteSHAChanged(branch, sha string) (bool, error) {
	formattedURL := formatGitURL(g.URL, branch)
	if formattedURL == "" {
		return true, nil
	}

	client, err := g.httpClientWithCreds()
	if err != nil {
		logrus.Warnf("Problem creating http client to check git remote sha of repo [%v]: %v", g.URL, err)
		return true, nil
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequest("GET", formattedURL, nil)
	if err != nil {
		logrus.Warnf("Problem creating request to check git remote sha of repo [%v]: %v", g.URL, err)
		return true, nil
	}

	req.Header.Set("Accept", "application/vnd.github.v3.sha")
	req.Header.Set("If-None-Match", fmt.Sprintf("\"%s\"", sha))
	for k, v := range g.headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		// Return timeout errors so caller can decide whether or not to proceed with updating the repo
		uErr := &url.Error{}
		if ok := errors.As(err, &uErr); ok && uErr.Timeout() {
			return false, errors.Wrapf(uErr, "Repo [%v] is not accessible", g.URL)
		}
		return true, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return false, nil
	}

	return true, nil
}

func (g *git) setCredential(cred *corev1.Secret) error {
	if cred == nil {
		return nil
	}

	if cred.Type == corev1.SecretTypeBasicAuth {
		username, password := cred.Data[corev1.BasicAuthUsernameKey], cred.Data[corev1.BasicAuthPasswordKey]
		if len(password) == 0 && len(username) == 0 {
			return nil
		}

		u, err := url.Parse(g.URL)
		if err != nil {
			return err
		}
		u.User = url.User(string(username))
		g.URL = u.String()
		g.password = string(password)
	} else if cred.Type == corev1.SecretTypeSSHAuth {
		key, err := ssh.ParseRawPrivateKey(cred.Data[corev1.SSHAuthPrivateKey])
		if err != nil {
			return err
		}
		sshAgent := agent.NewKeyring()
		err = sshAgent.Add(agent.AddedKey{
			PrivateKey: key,
		})
		if err != nil {
			return err
		}
		g.knownHosts = cred.Data["known_hosts"]
		g.agent = &sshAgent
	}

	return nil
}

func (g *git) gitCmd(output io.Writer, subCmd string, args ...string) error {
	kv := fmt.Sprintf("credential.helper=%s", `/bin/sh -c 'echo "password=$GIT_PASSWORD"'`)
	cmd := exec.Command("git", append([]string{"-c", kv, subCmd, "--"}, args...)...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("GIT_PASSWORD=%s", g.password))
	stderrBuf := &bytes.Buffer{}
	cmd.Stderr = stderrBuf
	cmd.Stdout = output

	if g.agent != nil {
		c, err := g.injectAgent(cmd)
		if err != nil {
			return err
		}
		defer c.Close()
	}

	if len(g.knownHosts) != 0 {
		f, err := ioutil.TempFile("", "known_hosts")
		if err != nil {
			return err
		}
		defer os.RemoveAll(f.Name())
		defer f.Close()

		if _, err := f.Write(g.knownHosts); err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("closing knownHosts file %s: %w", f.Name(), err)
		}

		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+fmt.Sprintf("ssh -o UserKnownHostsFile=%s", f.Name()))
	} else {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+fmt.Sprintf("ssh -o StrictHostKeyChecking=accept-new"))
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")

	if g.insecureTLSVerify {
		cmd.Env = append(cmd.Env, "GIT_SSL_NO_VERIFY=false")
	}

	if len(g.caBundle) > 0 {
		f, err := ioutil.TempFile("", "ca-pem-")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		defer f.Close()

		if _, err := f.Write(g.caBundle); err != nil {
			return fmt.Errorf("writing cabundle to %s: %w", f.Name(), err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("closing cabundle %s: %w", f.Name(), err)
		}
		cmd.Env = append(cmd.Env, "GIT_SSL_CAINFO="+f.Name())
	}

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("git %s error: %w, detail: %v", strings.Join(args, " "), err, stderrBuf.String())
	}
	return nil
}

func (g *git) injectAgent(cmd *exec.Cmd) (io.Closer, error) {
	r, err := randomtoken.Generate()
	if err != nil {
		return nil, err
	}

	tmpDir, err := ioutil.TempDir("", "ssh-agent")
	if err != nil {
		return nil, err
	}

	addr := &net.UnixAddr{
		Name: filepath.Join(tmpDir, r),
		Net:  "unix",
	}

	l, err := net.ListenUnix(addr.Net, addr)
	if err != nil {
		return nil, err
	}

	cmd.Env = append(cmd.Env, "SSH_AUTH_SOCK="+addr.Name)

	go func() {
		defer os.RemoveAll(tmpDir)
		defer l.Close()
		for {
			conn, err := l.Accept()
			if err != nil {
				if !k8snet.IsProbableEOF(err) {
					logrus.Errorf("failed to accept ssh-agent client connection: %v", err)
				}
				return
			}
			if err := agent.ServeAgent(*g.agent, conn); err != nil && err != io.EOF {
				logrus.Errorf("failed to handle ssh-agent client connection: %v", err)
			}
		}
	}()

	return l, nil
}

func formatGitURL(endpoint, branch string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}

	pathParts := strings.Split(u.Path, "/")
	switch u.Hostname() {
	case "github.com":
		if len(pathParts) >= 3 {
			org := pathParts[1]
			repo := strings.TrimSuffix(pathParts[2], ".git")
			return fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", org, repo, branch)
		}
	case "git.rancher.io":
		repo := strings.TrimSuffix(pathParts[1], ".git")
		u.Path = fmt.Sprintf("/repos/%s/commits/%s", repo, branch)
		return u.String()
	}

	return ""
}

func firstField(lines []string, errText string) (string, error) {
	if len(lines) == 0 {
		return "", errors.New(errText)
	}

	fields := strings.Fields(lines[0])
	if len(fields) == 0 {
		return "", errors.New(errText)
	}

	if len(fields[0]) == 0 {
		return "", errors.New(errText)
	}

	return fields[0], nil
}

func formatRefForBranch(branch string) string {
	return fmt.Sprintf("refs/heads/%s", branch)
}

type basicRoundTripper struct {
	username string
	password string
	next     http.RoundTripper
}

func (b *basicRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	request.SetBasicAuth(b.username, b.password)
	return b.next.RoundTrip(request)
}
