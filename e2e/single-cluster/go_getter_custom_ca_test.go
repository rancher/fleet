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
)

var _ = Describe("Testing go-getter CA bundles", Label("infra-setup"), func() {
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
		addr = strings.Replace(addr, "http://", fmt.Sprintf("%s://", "https"), 1)
		return addr
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
		// Creates the content in the sleeperClusterName directory
		_, err = gh.Create(cloneDir, testenv.AssetPath("gitrepo/sleeper-chart"), sleeper)
		Expect(err).ToNot(HaveOccurred())

		// Create the second repository
		Expect(err).ToNot(HaveOccurred())
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

		// Check that the bundle deployment resource has been deleted
		Eventually(func(g Gomega) {
			out, _ := k.Get(
				"bundledeployments",
				"-A",
				"-l",
				fmt.Sprintf("fleet.cattle.io/repo-name=%s", gitrepoName),
			)
			g.Expect(out).To(ContainSubstring("No resources found"))
		}).Should(Succeed())

		_, _ = k.Delete("ns", targetNamespace)
	})

	When("testing InsecureSkipTLSVerify", func() {
		BeforeEach(func() {
			targetNamespace = testenv.NewNamespaceName("target", r)
		})

		It("should fail if InsecureSkipTLSVerify is false and an invalid certificate was provided", func() {
			// Create and apply GitRepo
			err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), gitRepoTestValues{
				Name:                  gitrepoName,
				Repo:                  gh.GetInClusterURL(host, HTTPSPort, "repo"),
				Branch:                gh.Branch,
				PollingInterval:       "15s",           // default
				TargetNamespace:       targetNamespace, // to avoid conflicts with other tests
				Path:                  entrypoint,
				InsecureSkipTLSVerify: false,
				CABundle:              base64.StdEncoding.EncodeToString([]byte("invalid-ca-bundle")), // prevents Rancher CA bundles from being used
			})
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				out, err := k.Get("gitrepo", gitrepoName, `-o=jsonpath={.status.conditions[?(@.type=="GitPolling")].message}`)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(ContainSubstring("certificate signed by unknown authority"))
			}).Should(Succeed())
		})

		It("should succeed if InsecureSkipTLSVerify is true and an invalid certificate was provided", func() {
			// Create and apply GitRepo
			err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), gitRepoTestValues{
				Name:                  gitrepoName,
				Repo:                  gh.GetInClusterURL(host, HTTPSPort, "repo"),
				Branch:                gh.Branch,
				PollingInterval:       "15s",           // default
				TargetNamespace:       targetNamespace, // to avoid conflicts with other tests
				Path:                  entrypoint,
				InsecureSkipTLSVerify: true,
				CABundle:              base64.StdEncoding.EncodeToString([]byte("invalid-ca-bundle")), // prevents Rancher CA bundles from being used
			})
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				out, err := k.Get("gitrepo", gitrepoName, `-o=jsonpath={.status.display.readyBundleDeployments}`)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(ContainSubstring("1/1"))
			}).Should(Succeed())
		})
	})

	When("testing custom CA bundles", func() {
		It("should succeed when using the Rancher CA bundles provided in ConfigMaps", func() {
			// Create and apply GitRepo
			err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), gitRepoTestValues{
				Name:                  gitrepoName,
				Repo:                  gh.GetInClusterURL(host, HTTPSPort, "repo"),
				Branch:                gh.Branch,
				PollingInterval:       "15s",           // default
				TargetNamespace:       targetNamespace, // to avoid conflicts with other tests
				Path:                  entrypoint,
				InsecureSkipTLSVerify: false,
				CABundle:              "", // use Rancher CA bundles
			})
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				out, err := k.Get("gitrepo", gitrepoName, `-o=jsonpath={.status.display.readyBundleDeployments}`)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(ContainSubstring("1/1"))
			}).Should(Succeed())
		})

		It("should succeed when using the correct CA bundle provided in GitRepo", func() {
			certsDir := os.Getenv("CI_OCI_CERTS_DIR")
			Expect(certsDir).ToNot(BeEmpty())
			helmCrtFile := path.Join(certsDir, "helm.crt")
			crt, err := os.ReadFile(helmCrtFile)
			Expect(err).ToNot(HaveOccurred(), "failed to open helm.crt file")
			encodedCert := base64.StdEncoding.EncodeToString(crt)
			// Create and apply GitRepo
			err = testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), gitRepoTestValues{
				Name:                  gitrepoName,
				Repo:                  gh.GetInClusterURL(host, HTTPSPort, "repo"),
				Branch:                gh.Branch,
				PollingInterval:       "15s",           // default
				TargetNamespace:       targetNamespace, // to avoid conflicts with other tests
				Path:                  entrypoint,
				InsecureSkipTLSVerify: false,
				CABundle:              encodedCert,
			})
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				out, err := k.Get("gitrepo", gitrepoName, `-o=jsonpath={.status.display.readyBundleDeployments}`)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(ContainSubstring("1/1"))
			}).Should(Succeed())
		})
	})
})
