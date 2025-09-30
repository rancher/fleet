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
		// sleeperInClusterAddr := gh.GetInClusterURL(host, HTTPSPort, sleeperClusterName)
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

		// _, err := k.Delete("ns", targetNamespace, "--wait=true")
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
			// Create and apply GitRepo
			err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), gitRepoTestValues{
				Name:                  gitrepoName,
				Repo:                  gh.GetInClusterURL(host, HTTPSPort, "repo"),
				Branch:                gh.Branch,
				PollingInterval:       "15s",           // default
				TargetNamespace:       targetNamespace, // to avoid conflicts with other tests
				Path:                  entrypoint,
				InsecureSkipTLSVerify: false,
				CABundle:              ` LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUZ5VENDQTdHZ0F3SUJBZ0lVU0dZTE5tazlwamN5TVFoQ2ticG9mUWJycmVNd0RRWUpLb1pJaHZjTkFRRUwKQlFBd2RERUxNQWtHQTFVRUJoTUNSRVV4RWpBUUJnTlZCQWdNQ1Vac1pXVjBiR0Z1WkRFU01CQUdBMVVFQnd3SgpSbXhsWlhSamFYUjVNUkF3RGdZRFZRUUtEQWRTWVc1amFHVnlNUTR3REFZRFZRUUxEQVZHYkdWbGRERWJNQmtHCkExVUVBd3dTUm14bFpYUXRWR1Z6ZENCU2IyOTBJRU5CTUI0WERUSTFNRGN3TkRBM01qazBNRm9YRFRJMk1EY3cKTkRBM01qazBNRm93ZERFTE1Ba0dBMVVFQmhNQ1JFVXhFakFRQmdOVkJBZ01DVVpzWldWMGJHRnVaREVTTUJBRwpBMVVFQnd3SlJteGxaWFJqYVhSNU1SQXdEZ1lEVlFRS0RBZFNZVzVqYUdWeU1RNHdEQVlEVlFRTERBVkdiR1ZsCmRERWJNQmtHQTFVRUF3d1NSbXhsWlhRdFZHVnpkQ0JTYjI5MElFTkJNSUlDSWpBTkJna3Foa2lHOXcwQkFRRUYKQUFPQ0FnOEFNSUlDQ2dLQ0FnRUF0S3pmK2dtSGJhR0g4SVlnZ2M0K1ErMFMwVmdUWnBsRDFoTlVkMndGRjRSMQo4M3VZTER3eGJVeFUwT1l2cjJydDNBcVV6c0FCM0hURkJySVQ2UDJXSTZyNnNRRGtwYzU1YVErWWlhb2daT2JGCnQ4M0l6M3ZsenJUbDR3dWJ6MWVuQkhJYmV0aXZBV3pvL2RPZzB1VlFWNldXaG42b2lTdnFYdlJmWnVRbTYwVWkKamZuLysveS9lVzZleWFIM1FKM3RpaXRLZktoTituSDU2YkFVT3VrOEEzS21EMDdreVBHeWpQRVRJMlVLbDF5Qwp1UE5FR1pNdFd5U0JsRTJVblFDLzM5ZVZkNDZ5SkpDRGZ3L2tGOGw2aW43S3g4WWw5aXhVRVA1NGFaOWFBU2E2Ci9VbFludm9kQktQNmdtMnEzTzhXSjRIWE5pdE9HWnlUQzZOZ2twWFFvUm9LZkdkOHlHbU1BUTk3ZnA2VjNBcGYKR0ttM2pCS2VPZ3NvdkRucGxDWGlnci9LL1NxNHhrMHpZbjVrRFpSc2l3OW4vcjRVNW9IOVloc08xQitYVnNHRgpDNUxxRklRZys3L1V6RjB5K1R1akovTkJJdlE0K0Mwa3pXZkYvaHlpL01XSnBLMkxxMzU2TG9jbHRSU0FjRTVrCjhkQXU1N2EyVG4vaXNhMTFyMkxIVU55TldydC9oc0l0TTBoQTdHMkZpZURmMnNZV0UraGN3TWZraHhtVTNBVmUKejdTc0hlUHlWTkNieERiRFE0Q2FvU1FZeTBoYzFRbjJZRHN5SE5hR0p4K1VhNVF3ODFKUXZ0aUtPUCsyaytHZgpBNVAwaGZiMW15UVRVcmFuKzlOci9tVXBnYmNrejNjbm1ZdURDQVdEanVnYU9IRzgydW1TcHlJbWtTQTlZR0VDCkF3RUFBYU5UTUZFd0hRWURWUjBPQkJZRUZCVlBjTjR2NTlNRkYzTlU2VCtCNVh0NDZUTW5NQjhHQTFVZEl3UVkKTUJhQUZCVlBjTjR2NTlNRkYzTlU2VCtCNVh0NDZUTW5NQThHQTFVZEV3RUIvd1FGTUFNQkFmOHdEUVlKS29aSQpodmNOQVFFTEJRQURnZ0lCQUc1bE5aMTAwK3ZtSWVmT2pxZGpVdXlCLzVjK0dDcmlNZER6TWdVN29pbzFMa1FwCjlrZXBGS2ZXNzQ1SHdRQTE2ZHRKTHdHQ1ZCTGQ0aVczcTErYWt5QUtselhEYVdIZGNVSzkrM3cyYVR2ajdjU1gKeUJIdVRISnlSL3R5TVBaY1l5RzRyUEpZc1dZZ0s2a1VYK3prZXVqTnBuRUx2U1dvYW5LY1FVell5dzVId3hDSQp0OG9kcHRxc1ZyZmJJSFd6SEdkMW92SWpQQngyMkxDSEF1UW5MaGdzLzY0QVN2UDVYbm1yR0kyRXczeTlPMFNqCkxjWUsxTTBubWpmL1YzZGVuM3RMVXFLQmxPd1BLZ0VHS0VJUFBJOFpJeVEvM1hDNGVDSGdvVjBhckdlWWxndnAKNzlhQis2ZEkvTzhPQ1NPNGlMRjB0T0FEblo4NjBYSmRoQjhNZHFxN25tWk5oemZjZndta3pMRkVMNHI4OTgregpPbkVNZ3FmdkExeG13OUFsZGZyWFlLRDY2TmhIUjI4cE42Uk1CRmdZeDM5WlpERVhPc2RPa3cwRkRvUlE4enpBCk9jQUdHOCs1ZHRvdVhwTkZzNk04KzU0aGtKVXdPK1YvOFNKcGY3bEo1K3pNM1BEUWRsTkdybmt4RUtGRm9WSjQKb2grSGI0N2h3V3lwdDhjNWdFR2xaNmJXMWs1S0puTVk3b21DOVdMd2xQQThTdVlFK084UEV0RTkyeTdVbEhTYQplMTFhK1BPUURzTVRROFkrbHdUcDRORU5vOWNnUXhIN0p5Njd4ak5idU0vNUkvNzZqNmhrTWNQUGFmZVozTGpjClF3Q2NlYWU5cld2N05FSGpoRGNvckYwTi9zcWRiTGRBWTdHZnl3RnN4SlVKK1o1RmZwTndobDNoY2E4bAotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==`,
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
