package utils

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	gogs "github.com/gogits/go-gogs-client"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	cp "github.com/otiai10/copy"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
)

const (
	GogsUser      = "test"
	GogsPass      = "pass"
	GogsHTTPSPort = "3000"
	GogsSSHPort   = "22"

	gogsContainerTimeout  = 240 * time.Second
	gogsContainerInterval = 10 * time.Second
)

// MakeSSHKeyPair generates an RSA-4096 key pair for git SSH authentication.
// Returns the public key in authorized_keys format and the private key in PEM format.
func MakeSSHKeyPair() (publicKey, privateKey string, _ error) {
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return "", "", err
	}
	var privBuf strings.Builder
	if err := pem.Encode(&privBuf, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	}); err != nil {
		return "", "", err
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		return "", "", err
	}
	return string(ssh.MarshalAuthorizedKey(pub)), privBuf.String(), nil
}

// CreateTempFolder returns a temporary directory for test data.
// On GitHub Actions, os.MkdirTemp is used to avoid cleanup failures;
// locally GinkgoT().TempDir() is used so Ginkgo cleans up automatically.
func CreateTempFolder(prefix string) string {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		tmp, err := os.MkdirTemp("", prefix)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		return tmp
	}
	return ginkgo.GinkgoT().TempDir()
}

// GetGogsHTTPSURL returns the mapped HTTPS base URL for a running gogs container.
func GetGogsHTTPSURL(ctx context.Context, container testcontainers.Container) (string, error) {
	port, err := container.MappedPort(ctx, GogsHTTPSPort)
	if err != nil {
		return "", err
	}
	host, err := container.Host(ctx)
	if err != nil {
		return "", err
	}
	return "https://" + host + ":" + port.Port(), nil
}

// GetGogsSSHURL returns the mapped SSH base URL for a running gogs container.
func GetGogsSSHURL(ctx context.Context, container testcontainers.Container) (string, error) {
	port, err := container.MappedPort(ctx, GogsSSHPort)
	if err != nil {
		return "", err
	}
	host, err := container.Host(ctx)
	if err != nil {
		return "", err
	}
	return "ssh://git@" + host + ":" + port.Port(), nil
}

// CreateGogsContainerWithHTTPS starts a gogs container backed by assetPath, generates a
// TLS certificate, and waits until the HTTPS endpoint accepts connections.
// It returns the container, the CA bundle PEM bytes, and an authenticated gogs client.
// assetPath must be the gitserver directory to bind-mount into the container at /data.
func CreateGogsContainerWithHTTPS(ctx context.Context, assetPath string) (testcontainers.Container, []byte, *gogs.Client, error) {
	tmpDir := CreateTempFolder("gogs")
	if err := cp.Copy(assetPath, tmpDir); err != nil {
		return nil, nil, nil, err
	}
	for _, dir := range []string{"git", filepath.Join("git", ".ssh")} {
		if err := os.Chmod(filepath.Join(tmpDir, dir), 0700); err != nil {
			return nil, nil, nil, fmt.Errorf("chmod %s: %w", dir, err)
		}
	}

	req := testcontainers.ContainerRequest{
		Image:        "gogs/gogs:0.13",
		ExposedPorts: []string{GogsHTTPSPort + "/tcp", GogsSSHPort + "/tcp"},
		WaitingFor:   wait.ForListeningPort("22/tcp").WithStartupTimeout(gogsContainerTimeout),
		HostConfigModifier: func(hc *dockercontainer.HostConfig) {
			hc.Binds = []string{tmpDir + ":/data:z"}
		},
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if _, _, err = container.Exec(ctx, []string{"./gogs", "cert", "-ca=true", "-duration=8760h0m0s", "-host=localhost"}); err != nil {
		return container, nil, nil, err
	}
	if _, _, err = container.Exec(ctx, []string{"chown", "git:git", "cert.pem", "key.pem"}); err != nil {
		return container, nil, nil, err
	}
	caReader, err := container.CopyFileFromContainer(ctx, "/app/gogs/cert.pem")
	if err != nil {
		return container, nil, nil, err
	}
	defer caReader.Close()
	caBundle, err := io.ReadAll(caReader)
	if err != nil {
		return container, nil, nil, err
	}

	stopTimeout := gogsContainerTimeout
	if err = container.Stop(ctx, &stopTimeout); err != nil {
		return container, nil, nil, err
	}
	if err = container.Start(ctx); err != nil {
		return container, nil, nil, err
	}

	httpsURL, err := GetGogsHTTPSURL(ctx, container)
	if err != nil {
		return container, caBundle, nil, err
	}

	var client *gogs.Client
	gomega.Eventually(func() error {
		c := gogs.NewClient(httpsURL, "")
		conf := &tls.Config{InsecureSkipVerify: true}
		addr := strings.TrimPrefix(httpsURL, "https://")
		if _, err := (&tls.Dialer{Config: conf}).DialContext(ctx, "tcp", addr); err != nil {
			ginkgo.GinkgoWriter.Printf("error dialing %s: %v\n", addr, err)
			// debug: try plain TCP to distinguish TLS negotiation from connectivity failures
			conn, tcpErr := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
			if tcpErr != nil {
				ginkgo.GinkgoWriter.Printf("error dialing without TLS: %v\n", tcpErr)
			} else {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				buf := make([]byte, 512)
				n, _ := conn.Read(buf)
				ginkgo.GinkgoWriter.Printf("plain TCP response: %s\n", buf[:n])
			}
			return err
		}
		tr := &http.Transport{TLSClientConfig: conf}
		httpClient := &http.Client{Transport: tr}
		c.SetHTTPClient(httpClient)
		token, tokenErr := c.CreateAccessToken(GogsUser, GogsPass, gogs.CreateAccessTokenOption{Name: "test"})
		if tokenErr != nil {
			ginkgo.GinkgoWriter.Printf("error creating access token: %v\n", tokenErr)
			return tokenErr
		}
		client = gogs.NewClient(httpsURL, token.Sha1)
		client.SetHTTPClient(httpClient)
		return nil
	}, gogsContainerTimeout, gogsContainerInterval).ShouldNot(gomega.HaveOccurred())

	return container, caBundle, client, nil
}

// GetGogsKnownHostEntry reads the container's SSH ECDSA host public key and returns
// a known_hosts format line suitable for use with CreateKnownHostsCallBack.
func GetGogsKnownHostEntry(ctx context.Context, container testcontainers.Container) (string, error) {
	port, err := container.MappedPort(ctx, GogsSSHPort)
	if err != nil {
		return "", err
	}
	host, err := container.Host(ctx)
	if err != nil {
		return "", err
	}
	pubKeyReader, err := container.CopyFileFromContainer(ctx, "/data/ssh/ssh_host_ecdsa_key.pub")
	if err != nil {
		return "", err
	}
	defer pubKeyReader.Close()
	pubKeyBytes, err := io.ReadAll(pubKeyReader)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(pubKeyBytes))
	if len(fields) < 2 {
		return "", fmt.Errorf("unexpected known_hosts key format: %q", string(pubKeyBytes))
	}
	return fmt.Sprintf("[%s]:%s %s %s", host, port.Port(), fields[0], fields[1]), nil
}
