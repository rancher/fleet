package singlecluster_test

// These test cases rely on a local git server, so that they can be run locally and against PRs.
// For tests monitoring external git hosting providers, see `e2e/require-secrets`.

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"path"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	"sigs.k8s.io/yaml"
)

// Those tests are specifically targeting one feature of go-getter, namely cloning of git
// repositories using HTTPS. That is, because TLS certificates are only used in HTTPS URLs, not if
// SSH keys are used to clone those repositories. Since go-getter shells out to the `git` CLI for
// cloning git repositories, the configuration of TLS certificates or ignoring those needs to be
// configured with `git` typical environment variables. Those are the tests for that implementation.
// For go-getter to be used, the `helm.chart` field in `fleet.yaml` needs to point to a URL that
// tells go-getter to use the git protocol over HTTPS. Such an URL is prefixed with `git::https://`.
// The contents fetched from those repositories are expected to be helm charts.
var _ = Describe("Testing go-getter CA bundles and insecureSkipVerify for cloning git repositories", Label("infra-setup"), func() {
	const (
		sleeper    = "sleeper"
		entrypoint = "entrypoint"
	)

	var (
		tmpDir          string
		cloneDir        string
		k               kubectl.Command
		gh              *githelper.Git
		gitrepoName     string
		r               = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace string
		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host = githelper.BuildGitHostname()
	)

	getExternalRepoURL := func(repoName string) string {
		GinkgoHelper()
		addr, err := githelper.GetExternalRepoAddr(env, HTTPSPort, repoName)
		Expect(err).ToNot(HaveOccurred())
		addr = strings.Replace(addr, "http://", "https://", 1)
		return addr
	}

	mustReadFileAsBase64 := func(filePath string) string {
		GinkgoHelper()
		data, err := os.ReadFile(filePath)
		Expect(err).ToNot(HaveOccurred(), "failed to read file: %s", filePath)
		return base64.StdEncoding.EncodeToString(data)
	}

	// getCertificateFilePath returns the path to the CA certificate file used to set up the local
	// git server.
	getCertificateFilePath := func() string {
		GinkgoHelper()
		certsDir := os.Getenv("CI_OCI_CERTS_DIR")
		Expect(certsDir).ToNot(BeEmpty())
		return path.Join(certsDir, "helm.crt")
	}

	createInvalidCACertFile := func() *os.File {
		tmpFile, err := os.CreateTemp("", "invalid-ca-*.crt")
		Expect(err).ToNot(HaveOccurred(), "failed to create temp file for invalid CA bundle")
		_, err = tmpFile.Write([]byte("invalid-ca-bundle"))
		Expect(err).ToNot(HaveOccurred(), "failed to write invalid CA bundle to temp file")
		err = tmpFile.Close()
		Expect(err).ToNot(HaveOccurred(), "failed to close temp file for invalid CA bundle")
		return tmpFile
	}

	expectGitRepoToNotBeReady := func(g Gomega) {
		out, err := k.Get("gitrepo", gitrepoName, `-o=jsonpath={.status.display.readyBundleDeployments}`)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(out).To(ContainSubstring("0/0"))
	}

	expectGitRepoToBeReady := func(g Gomega) {
		out, err := k.Get("gitrepo", gitrepoName, `-o=jsonpath={.status.display.readyBundleDeployments}`)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(out).To(ContainSubstring("1/1"))
	}

	type Auth struct {
		Username           string `json:"username,omitempty"`
		Password           string `json:"password,omitempty"`
		CABundle           string `json:"caBundle,omitempty"`
		SSHPrivateKey      string `json:"sshPrivateKey,omitempty"`
		InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
		BasicHTTP          bool   `json:"basicHTTP,omitempty"`
	}
	type AuthConfigByPath map[string]Auth

	createHelmSecretForPaths := func(secretName string, config AuthConfigByPath) {
		GinkgoHelper()
		data, err := yaml.Marshal(config)
		Expect(err).ToNot(HaveOccurred())

		file, err := os.CreateTemp("", "helm-auth-*.yaml")
		Expect(err).ToNot(HaveOccurred(), "failed to create temp file for helm auth config")
		defer os.Remove(file.Name())
		_, err = file.Write(data)
		Expect(err).ToNot(HaveOccurred(), "failed to write helm auth config to temp file")

		_, err = k.Create("secret", "generic", secretName, "--from-file=secrets-path.yaml="+file.Name(), "-n", env.Namespace)
		Expect(err).ToNot(HaveOccurred())
	}

	type gitRepoOptions struct {
		CABundle               string
		InsecureSkipTLSVerify  bool
		HelmSecretName         string
		HelmSecretNameForPaths string
	}

	createGitRepo := func(options gitRepoOptions) {
		GinkgoHelper()
		values := gitRepoTestValues{
			// Defaults
			Name:            gitrepoName,
			Repo:            gh.GetInClusterURL(host, HTTPSPort, "repo"),
			Branch:          gh.Branch,
			PollingInterval: "15s",           // default
			TargetNamespace: targetNamespace, // to avoid conflicts with other tests
			Path:            entrypoint,
			// Customizations
			CABundle:               options.CABundle,
			InsecureSkipTLSVerify:  options.InsecureSkipTLSVerify,
			HelmSecretName:         options.HelmSecretName,
			HelmSecretNameForPaths: options.HelmSecretNameForPaths,
		}

		err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), values)
		Expect(err).ToNot(HaveOccurred())
	}

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
	})

	JustBeforeEach(func() {
		// Create the first repository
		addr := getExternalRepoURL("repo")
		gh = githelper.NewHTTP(addr)
		tmpDir, err := os.MkdirTemp("", "fleet-")
		Expect(err).ToNot(HaveOccurred())
		cloneDir = path.Join(tmpDir, "repo") // Fixed and built into the container image.
		gitrepoName = testenv.RandomFilename("gitjob-test", r)
		// Creates the content in the sleeper directory
		_, err = gh.Create(cloneDir, testenv.AssetPath("gitrepo/sleeper-chart"), sleeper)
		Expect(err).ToNot(HaveOccurred())

		// Create the second repository, referencing the Helm chart from the
		// first repository through a fleet.yaml
		tmpAssetDir := path.Join(tmpDir, "entryPoint")
		err = os.Mkdir(tmpAssetDir, 0755)
		Expect(err).ToNot(HaveOccurred())
		url := "git::" + gh.GetInClusterURL(host, HTTPSPort, "repo?ref="+sleeper)
		err = os.WriteFile(
			path.Join(tmpAssetDir, "fleet.yaml"),
			fmt.Appendf([]byte{}, "helm:\n  chart: %s\n", url),
			0755,
		)
		Expect(err).NotTo(HaveOccurred())

		_, err = gh.Add(cloneDir, tmpAssetDir, entrypoint)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir)
		_, _ = k.Delete("gitrepo", gitrepoName)
		_, _ = k.Delete("ns", targetNamespace)
	})

	When("testing custom CA bundles for cloning git with HTTPS using go-getter (fleet.yaml)", func() {
		It("should succeed when not configuring any CA", func() {
			// Create and apply GitRepo, don't configure CABundle, InsecureSkipTLSVerify,
			// helmSecretName, or helmSecretNameForPath in GitRepo.spec which makes it fall back to
			// Rancher certificates that work.
			createGitRepo(gitRepoOptions{})
			Eventually(expectGitRepoToBeReady).Should(Succeed())
		})

		It("should succeed when using the correct CA bundle provided in GitRepo's CABundle field", func() {
			encodedCert := mustReadFileAsBase64(getCertificateFilePath())
			createGitRepo(gitRepoOptions{CABundle: encodedCert})
			Eventually(expectGitRepoToBeReady).Should(Succeed())
		})

		It("should fail when using the incorrect CA bundle provided in GitRepo's CABundle field", func() {
			// But it should fail in gitcloner already, not later in fleet apply.
			encodedCert := base64.StdEncoding.EncodeToString([]byte("invalid-ca-bundle"))
			createGitRepo(gitRepoOptions{CABundle: encodedCert})
			Eventually(expectGitRepoToNotBeReady).Should(Succeed())
			Consistently(expectGitRepoToNotBeReady).Should(Succeed())
		})

		It("should succeed when using the correct CA bundle provided in helmSecretName", func() {
			helmCrtFile := getCertificateFilePath()

			// Create secret with CA bundle
			secretName := testenv.RandomFilename("helm-ca-bundle", r)
			out, err := k.Create("secret", "generic", secretName, "--from-file=cacerts="+helmCrtFile, "-n", env.Namespace)
			Expect(err).ToNot(HaveOccurred(), out)

			createGitRepo(gitRepoOptions{HelmSecretName: secretName})
			Eventually(expectGitRepoToBeReady).Should(Succeed())
		})

		// That's the one that produces an error in the gitjob, but we provoked it by setting a
		// certificate that it can't use. That's basically what the resulting error says.
		It("should fail when using an incorrect CA bundle provided in helmSecretName", func() {
			invalidCACertsFile := createInvalidCACertFile()
			defer os.Remove(invalidCACertsFile.Name())

			// Create secret with CA bundle
			secretName := testenv.RandomFilename("helm-ca-bundle", r)
			out, err := k.Create("secret", "generic", secretName, "--from-file=cacerts="+invalidCACertsFile.Name(), "-n", env.Namespace)
			Expect(err).ToNot(HaveOccurred(), out)

			createGitRepo(gitRepoOptions{HelmSecretName: secretName})
			Eventually(expectGitRepoToNotBeReady).Should(Succeed())
			Consistently(expectGitRepoToNotBeReady).Should(Succeed())
		})

		It("should succeed when using the correct CA bundle provided in helmSecretNameForPath", func() {
			secretName := "helm-ca-bundle-by-path-" + gitrepoName
			createHelmSecretForPaths(
				secretName,
				AuthConfigByPath{
					entrypoint: {
						CABundle: mustReadFileAsBase64(getCertificateFilePath()),
					},
				},
			)

			createGitRepo(gitRepoOptions{HelmSecretNameForPaths: secretName})
			Eventually(expectGitRepoToBeReady).Should(Succeed())
		})

		It("should not succeed when using an incorrect CA bundle provided in helmSecretNameForPath", func() {
			secretName := "helm-ca-bundle-by-path-" + gitrepoName
			createHelmSecretForPaths(
				secretName,
				AuthConfigByPath{
					entrypoint: {
						CABundle: "asdf", // invalid
					},
				},
			)

			createGitRepo(gitRepoOptions{HelmSecretNameForPaths: secretName})
			Consistently(expectGitRepoToNotBeReady).Should(Succeed())
		})
	})

	When("testing ignoring insecure certificates for cloning git repositories with HTTPS using go-getter (fleet.yaml)", func() {
		It("should succeed when using the incorrect CA bundle provided in GitRepo's CABundle field but setting InsecureSkipTLSVerify to true", func() {
			// But it should fail in gitcloner already, not later in fleet apply.
			encodedCert := base64.StdEncoding.EncodeToString([]byte("invalid-ca-bundle"))
			createGitRepo(gitRepoOptions{CABundle: encodedCert, InsecureSkipTLSVerify: true})
			Eventually(expectGitRepoToBeReady).Should(Succeed())
		})

		It("should succeed when using the incorrect CA bundle provided in helmSecretName but setting InsecureSkipVerify to true", func() {
			invalidCertFile := createInvalidCACertFile()
			defer os.Remove(invalidCertFile.Name())

			secretName := testenv.RandomFilename("helm-ca-bundle", r)
			out, err := k.Create("secret", "generic", secretName,
				"--from-file=cacerts="+invalidCertFile.Name(),
				"--from-literal=insecureSkipVerify=true",
				"-n", env.Namespace)
			Expect(err).ToNot(HaveOccurred(), out)

			createGitRepo(gitRepoOptions{HelmSecretName: secretName, InsecureSkipTLSVerify: true})
			Eventually(expectGitRepoToBeReady).Should(Succeed())
		})

		It("should succeed when using the incorrect CA bundle provided in helmSecretNameForPath but setting InsecureSkipVerify to true", func() {
			secretName := "helm-ca-bundle-by-path-" + gitrepoName
			createHelmSecretForPaths(
				secretName,
				AuthConfigByPath{
					entrypoint: {
						CABundle:           "asdf", // invalid
						InsecureSkipVerify: true,
					},
				},
			)

			createGitRepo(gitRepoOptions{HelmSecretNameForPaths: secretName})
			Eventually(expectGitRepoToBeReady).Should(Succeed())
		})
	})
})
