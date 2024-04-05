package integrationtests

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockermount "github.com/docker/docker/api/types/mount"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/gogits/go-gogs-client"
	cp "github.com/otiai10/copy"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/ssh"

	"github.com/rancher/fleet/internal/mocks"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git"

	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

/*
These tests use gogs for testing integration with a git server. Gogs container uses data from assets/gitserver, which
contains one user, one public repository, and another private repository. Initial commits and fingerprint are provided as consts.
*/
const (
	gogsFingerPrint = "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBOLWGeeq/e1mK/zH47UeQeMtdh+NEz6j7xp5cAINcV2pPWgAsuyh5dumMv1RkC1rr0pmWekCoMnR2c4+PllRqrQ="
	gogsUser        = "test"
	gogsPass        = "pass"
	startupTimeout  = 60 * time.Second
)

var (
	gogsClient              *gogs.Client
	latestCommitPublicRepo  string
	latestCommitPrivateRepo string
	url                     string
	container               testcontainers.Container
)

func TestMain(m *testing.M) {
	teardown := setupSuite()
	code := m.Run()
	teardown()
	os.Exit(code)
}

func setupSuite() func() {
	ctx := context.Background()
	var err error
	t := &testing.T{}
	container, url, err = createGogsContainer(ctx, createTempFolder(t))
	require.NoError(t, err, "creating gogs container failed")

	return func() {
		terminateContainer(ctx, container, t)
	}
}

func TestLatestCommit_NoAuth(t *testing.T) {
	ctlr := gomock.NewController(t)
	defer ctlr.Finish()
	ctx := context.Background()

	tests := map[string]struct {
		gitrepo        *v1alpha1.GitRepo
		expectedCommit string
		expectedErr    error
	}{
		"public repo": {
			gitrepo: &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:   url + "/test/public-repo",
					Branch: "master",
				},
			},
			expectedCommit: latestCommitPublicRepo,
			expectedErr:    nil,
		},
		"private repo": {
			gitrepo: &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:   url + "/test/private-repo",
					Branch: "master",
				},
			},
			expectedCommit: "",
			expectedErr:    transport.ErrAuthenticationRequired,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			f := git.Fetch{}
			client := mocks.NewMockClient(ctlr)
			client.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(kerrors.NewNotFound(schema.GroupResource{}, "notfound"))
			latestCommit, err := f.LatestCommit(ctx, test.gitrepo, client)
			if err != test.expectedErr {
				t.Errorf("expected error is: %v, but got %v", test.expectedErr, err)
			}
			if latestCommit != test.expectedCommit {
				t.Errorf("latestCommit doesn't match. got %s, expected %s", latestCommit, test.expectedCommit)
			}
		})
	}

}

func TestLatestCommit_BasicAuth(t *testing.T) {
	ctlr := gomock.NewController(t)
	defer ctlr.Finish()
	ctx := context.Background()

	tests := map[string]struct {
		gitrepo        *v1alpha1.GitRepo
		expectedCommit string
		expectedErr    error
	}{
		"public repo": {
			gitrepo: &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:   url + "/test/public-repo",
					Branch: "master",
				},
			},
			expectedCommit: latestCommitPublicRepo,
			expectedErr:    nil,
		},
		"private repo": {
			gitrepo: &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:   url + "/test/private-repo",
					Branch: "master",
				},
			},
			expectedCommit: latestCommitPrivateRepo,
			expectedErr:    nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			secret := &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      git.DefaultSecretName,
					Namespace: test.gitrepo.Namespace,
				},
				Data: map[string][]byte{v1.BasicAuthUsernameKey: []byte(gogsUser), v1.BasicAuthPasswordKey: []byte(gogsPass)},
				Type: v1.SecretTypeBasicAuth,
			}
			f := git.Fetch{}
			client := fake.NewClientBuilder().WithRuntimeObjects(secret).Build()
			latestCommit, err := f.LatestCommit(ctx, test.gitrepo, client)
			if err != test.expectedErr {
				t.Errorf("expecter error is: %v, but got %v", test.expectedErr, err)
			}
			if latestCommit != test.expectedCommit {
				t.Errorf("latestCommit doesn't match. got %s, expected %s", latestCommit, test.expectedCommit)
			}
		})
	}
}

func TestLatestCommitSSH(t *testing.T) {
	require := require.New(t)
	ctlr := gomock.NewController(t)
	defer ctlr.Finish()
	ctx := context.Background()
	privateKey, err := createAndAddKeys()
	require.NoError(err)
	sshPort, err := container.MappedPort(ctx, "22")
	require.NoError(err)
	gogsKnownHosts := []byte("[localhost]:" + sshPort.Port() + " " + gogsFingerPrint)

	tests := map[string]struct {
		gitrepo        *v1alpha1.GitRepo
		knownHosts     []byte
		expectedCommit string
		expectedErr    error
	}{
		"public repo": {
			gitrepo: &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:   "ssh://git@localhost:" + sshPort.Port() + "/test/" + "public-repo",
					Branch: "master",
				},
			},
			knownHosts:     gogsKnownHosts,
			expectedCommit: latestCommitPublicRepo,
			expectedErr:    nil,
		},
		"private repo with known hosts": {
			gitrepo: &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:   "ssh://git@localhost:" + sshPort.Port() + "/test/" + "private-repo",
					Branch: "master",
				},
			},
			knownHosts:     gogsKnownHosts,
			expectedCommit: latestCommitPrivateRepo,
			expectedErr:    nil,
		},
		"private repo without known hosts": {
			gitrepo: &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:   "ssh://git@localhost:" + sshPort.Port() + "/test/" + "private-repo",
					Branch: "master",
				},
			},
			knownHosts:     nil,
			expectedCommit: latestCommitPrivateRepo,
			expectedErr:    nil,
		},
		"private repo with known host with a wrong host url": {
			gitrepo: &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					Repo:   "ssh://git@localhost:" + sshPort.Port() + "/test/" + "private-repo",
					Branch: "master",
				},
			},
			knownHosts:     []byte("doesntexist " + gogsFingerPrint),
			expectedCommit: "",
			expectedErr:    fmt.Errorf("ssh: handshake failed: knownhosts: key is unknown"),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			secret := &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      git.DefaultSecretName,
					Namespace: test.gitrepo.Namespace,
				},
				Data: map[string][]byte{
					v1.SSHAuthPrivateKey: []byte(privateKey),
					"known_hosts":        test.knownHosts,
				},
				Type: v1.SecretTypeSSHAuth,
			}
			client := fake.NewClientBuilder().WithRuntimeObjects(secret).Build()
			f := git.Fetch{}
			latestCommit, err := f.LatestCommit(ctx, test.gitrepo, client)

			// works with nil and wrapped errors
			if test.expectedErr == nil {
				require.NoError(err)
			} else {
				require.Contains(test.expectedErr.Error(), err.Error())
			}

			if latestCommit != test.expectedCommit {
				t.Errorf("latestCommit doesn't match. got %s, expected %s", latestCommit, test.expectedCommit)
			}
		})
	}
}

func createGogsContainer(ctx context.Context, tmpDir string) (testcontainers.Container, string, error) {
	err := cp.Copy("../assets/gitserver", tmpDir)
	if err != nil {
		return nil, "", err
	}
	req := testcontainers.ContainerRequest{
		Image:        "gogs/gogs:0.13",
		ExposedPorts: []string{"3000/tcp", "22/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForHTTP("/").WithPort("3000/tcp").WithStartupTimeout(startupTimeout),
			wait.ForListeningPort("22/tcp").WithStartupTimeout(startupTimeout),
		),
		HostConfigModifier: func(hostConfig *dockercontainer.HostConfig) {
			hostConfig.Mounts = []dockermount.Mount{
				{
					Type:   dockermount.TypeBind,
					Source: tmpDir,
					Target: "/data",
				},
			}

		},
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})

	if err != nil {
		return nil, "", err
	}

	url, err := getURL(ctx, container)
	if err != nil {
		return nil, "", err
	}

	c := gogs.NewClient(url, "")
	token, err := c.CreateAccessToken(gogsUser, gogsPass, gogs.CreateAccessTokenOption{
		Name: "test",
	})
	if err != nil {
		return nil, "", err
	}

	gogsClient = gogs.NewClient(url, token.Sha1)
	latestCommitPublicRepo, err = initRepo(url, "public-repo", false)
	if err != nil {
		return nil, "", err
	}
	latestCommitPrivateRepo, err = initRepo(url, "private-repo", true)
	if err != nil {
		return nil, "", err
	}

	return container, url, nil
}

// initRepo creates a git repo and adds an initial commit.
func initRepo(url string, name string, private bool) (string, error) {
	// create repo
	_, err := gogsClient.CreateRepo(gogs.CreateRepoOption{
		Name:    name,
		Private: private,
	})
	if err != nil {
		return "", err
	}
	repoURL := url + "/" + gogsUser + "/" + name

	// add initial commit
	tmp, err := os.MkdirTemp("", name)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	if _, err = gogit.PlainInit(tmp, false); err != nil {
		return "", err
	}

	r, err := gogit.PlainOpen(tmp)
	if err != nil {
		return "", err
	}
	filename := filepath.Join(tmp, "example-git-file")
	err = os.WriteFile(filename, []byte("test"), 0600)
	if err != nil {
		return "", err
	}
	w, err := r.Worktree()
	if err != nil {
		return "", err
	}
	_, err = w.Add("example-git-file")
	if err != nil {
		return "", err
	}
	commit, err := w.Commit("test commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test user",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", err
	}
	cfg, err := r.Config()
	if err != nil {
		return "", err
	}
	cfg.Remotes["upstream"] = &config.RemoteConfig{
		Name: "upstream",
		URLs: []string{repoURL},
	}
	err = r.SetConfig(cfg)
	if err != nil {
		return "", err
	}
	err = r.Push(&gogit.PushOptions{
		RemoteName: "upstream",
		RemoteURL:  repoURL,
		Auth: &httpgit.BasicAuth{
			Username: gogsUser,
			Password: gogsPass,
		},
	})
	if err != nil {
		return "", err
	}

	return commit.String(), nil
}

func getURL(ctx context.Context, container testcontainers.Container) (string, error) {
	mappedPort, err := container.MappedPort(ctx, "3000")
	if err != nil {
		return "", err
	}
	host, err := container.Host(ctx)
	if err != nil {
		return "", err
	}
	url := "http://" + host + ":" + mappedPort.Port()

	return url, nil
}

// createTempFolder uses testing tempDir if running in local, which will cleanup the files at the end of the tests.
// cleanup fails in github actions, that's why we use os.MkdirTemp instead. Container will be removed at the end in
// github actions, so no resources are left orphaned.
func createTempFolder(t *testing.T) string {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		tmp, err := os.MkdirTemp("", "gogs")
		require.NoError(t, err)
		return tmp
	}

	return t.TempDir()
}

// createAndAddKeys creates a public private key pair. It adds the public key to gogs, and returns the private key.
func createAndAddKeys() (string, error) {
	publicKey, privateKey, err := makeSSHKeyPair()
	if err != nil {
		return "", err
	}

	_, err = gogsClient.CreatePublicKey(gogs.CreateKeyOption{
		Title: "test",
		Key:   publicKey,
	})
	if err != nil {
		return "", err
	}

	return privateKey, nil
}

func makeSSHKeyPair() (string, string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return "", "", err
	}

	var privKeyBuf strings.Builder
	privateKeyPEM := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}
	if err := pem.Encode(&privKeyBuf, privateKeyPEM); err != nil {
		return "", "", err
	}
	pub, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}
	var pubKeyBuf strings.Builder
	pubKeyBuf.Write(ssh.MarshalAuthorizedKey(pub))

	return pubKeyBuf.String(), privKeyBuf.String(), nil
}

func terminateContainer(ctx context.Context, container testcontainers.Container, t *testing.T) {
	if err := container.Terminate(ctx); err != nil {
		t.Fatalf("failed to terminate container: %s", err.Error())
	}
}
