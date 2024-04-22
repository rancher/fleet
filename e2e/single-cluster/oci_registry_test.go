package singlecluster_test

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func getFailedPodNames(k kubectl.Command, namespace string) ([]string, error) {
	return getPodNamesByFieldSelector(k, namespace, "status.phase=Failed")
}

func getPodNamesByFieldSelector(k kubectl.Command, namespace, selector string) ([]string, error) {
	strSelector := fmt.Sprintf("--field-selector=%s", selector)
	out, err := k.Namespace(namespace).Get("pods", strSelector, "-o", "custom-columns=NAME:metadata.name", "--no-headers")
	if err != nil {
		return []string{}, err
	}
	return strings.Split(out, "\n"), nil
}

var _ = Describe("Single Cluster Deployments using OCI registry", Label("oci-registry", "infra-setup"), func() {
	var (
		asset               string
		insecureSkipTLS     bool
		forceGitRepoUrl     string
		k                   kubectl.Command
		tempDir             string
		contentsID          string
		downstreamNamespace string
		ociRegistry         string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		tempDir = GinkgoT().TempDir()
		externalIP, err := k.Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
		Expect(err).ToNot(HaveOccurred(), externalIP)
		Expect(net.ParseIP(externalIP)).ShouldNot(BeNil())
		ociRegistry = fmt.Sprintf("%s:5000", externalIP)
	})

	JustBeforeEach(func() {
		out, err := k.Create(
			"secret", "generic", "oci-secret",
			"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
			"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
		)
		Expect(err).ToNot(HaveOccurred(), out)

		gitrepo := path.Join(tempDir, "gitrepo.yaml")
		if forceGitRepoUrl != "" {
			ociRegistry = forceGitRepoUrl
		}

		err = testenv.Template(gitrepo, testenv.AssetPath(asset), struct {
			OCIUrl             string
			OCIInsecureSkipTLS bool
		}{
			ociRegistry,
			insecureSkipTLS,
		})
		Expect(err).ToNot(HaveOccurred())

		out, err = k.Apply("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)

		downstreamNamespace, err = k.Namespace("fleet-local").Get("cluster", "local", `-o=jsonpath={.status.namespace}`)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		out, err := k.Delete("secret", "oci-secret")
		Expect(err).ToNot(HaveOccurred(), out)

		gitrepo := path.Join(tempDir, "gitrepo.yaml")
		out, err = k.Delete("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)

		// secrets for bundle and bundledeployment should be gone
		// secret always begin with "s-"
		Eventually(func() string {
			out, _ := k.Namespace("fleet-local").Get("secrets")
			return out
		}).ShouldNot(ContainSubstring("s-"))

		Eventually(func() string {
			out, _ := k.Namespace(downstreamNamespace).Get("secrets")
			return out
		}).ShouldNot(ContainSubstring("s-"))
	})

	When("creating a gitrepo resource with ociRegistry info", func() {
		Context("containing a valid helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/test-oci.yaml"
				insecureSkipTLS = true
				forceGitRepoUrl = ""
			})

			It("creates the bundle", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-local").Get("bundles")
					return out
				}).Should(ContainSubstring("sample-simple-chart-oci"))
			})
			It("sets the ContentsID field in the bundle", func() {
				Eventually(func() string {
					contentsID, _ = k.Namespace("fleet-local").Get("bundle", "sample-simple-chart-oci", `-o=jsonpath={.spec.contentsId}`)
					return contentsID
				}).Should(ContainSubstring("s-"))
			})
			It("sets Resource key with the oci path to the manifest", func() {
				Eventually(func() bool {
					out, _ := k.Namespace("fleet-local").Get("bundle", "sample-simple-chart-oci", `-o=jsonpath={.status.resourceKey}`)
					var existingResourceKeys []fleetv1.ResourceKey
					err := json.Unmarshal([]byte(out), &existingResourceKeys)
					if err != nil {
						return false
					}
					// checks that it only creates 1 resource key
					if len(existingResourceKeys) != 1 {
						return false
					}
					if existingResourceKeys[0].Name != fmt.Sprintf("oci://%s/%s:latest", ociRegistry, contentsID) {
						return false
					}
					return true
				}).Should(BeTrue())
			})
			It("creates a bundle secret", func() {
				Eventually(func() string {
					out, err := k.Namespace("fleet-local").Get("secret", contentsID)
					if err != nil {
						// return nothing in case of error
						// This avoids false positives when kubectl returns "secrets "XXXXX" not found
						return ""
					}
					return out
				}).Should(ContainSubstring(contentsID))
			})
			It("creates a bundledeployment secret", func() {
				Eventually(func() string {
					out, err := k.Namespace(downstreamNamespace).Get("secret", contentsID)
					if err != nil {
						// return nothing in case of error
						// This avoids false positives when kubectl returns "secrets "XXXXX" not found
						return ""
					}
					return out
				}).Should(ContainSubstring(contentsID))
			})
			It("does not deploy a contents resource for this chart", func() {
				out, _ := k.Get("contents")
				Expect(out).NotTo(ContainSubstring(contentsID))
			})
			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-local").Get("configmaps")
					return out
				}).Should(ContainSubstring("sample-config"))
			})
		})
	})

	When("creating a gitrepo resource with invalid ociRegistry info", func() {
		Context("containing a public oci based helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/test-oci.yaml"
				insecureSkipTLS = true
				forceGitRepoUrl = "not-valid-oci-registry.com"
			})

			It("does not create the bundle", func() {
				// look for failed pods with the name sample-xxxx-xxxx
				r, _ := regexp.Compile("sample-([a-z0-9]+)-([a-z0-9]+)")
				var failedJob string
				Eventually(func() bool {
					failedPods, err := getFailedPodNames(k, "fleet-local")
					Expect(err).ToNot(HaveOccurred())
					for _, pod := range failedPods {
						if r.MatchString(pod) {
							failedJob = pod
							return true
						}
					}
					return false
				}).Should(BeTrue())

				Expect(failedJob).ShouldNot(BeEmpty())
				// check that the logs of the job reflect the bad OCI registry
				logs, err := k.Namespace("fleet-local").Logs(failedJob)
				Expect(err).ToNot(HaveOccurred())
				Expect(logs).Should(ContainSubstring("no such host"))

				// we don't fallback to content resources, so the bundle should not be created
				// at this point
				bundles, _ := k.Namespace("fleet-local").Get("bundles")
				Expect(bundles).ShouldNot(ContainSubstring("sample-simple-chart"))

				// bundle secret should not be created either
				secrets, _ := k.Namespace("fleet-local").Get("secrets", "-o", "custom-columns=NAME:metadata.name", "--no-headers")
				Expect(secrets).ShouldNot(ContainSubstring("s-"))
			})
		})
	})

	When("creating a gitrepo resource with valid ociRegistry but not ignoring certs", func() {
		Context("containing a public oci based helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/test-oci.yaml"
				insecureSkipTLS = false
				forceGitRepoUrl = ""
			})

			It("does not create the bundle", func() {
				// look for failed pods with the name sample-xxxx-xxxx
				r, _ := regexp.Compile("sample-([a-z0-9]+)-([a-z0-9]+)")
				var failedJob string
				Eventually(func() bool {
					failedPods, err := getFailedPodNames(k, "fleet-local")
					Expect(err).ToNot(HaveOccurred())
					for _, pod := range failedPods {
						if r.MatchString(pod) {
							failedJob = pod
							return true
						}
					}
					return false
				}).Should(BeTrue())

				Expect(failedJob).ShouldNot(BeEmpty())
				// check that the logs of the job reflect the bad OCI registry
				logs, err := k.Namespace("fleet-local").Logs(failedJob)
				Expect(err).ToNot(HaveOccurred())
				Expect(logs).Should(ContainSubstring("tls: failed to verify certificate: x509"))

				// we don't fallback to content resources, so the bundle should not be created
				// at this point
				bundles, _ := k.Namespace("fleet-local").Get("bundles")
				Expect(bundles).ShouldNot(ContainSubstring("sample-simple-chart"))

				// bundle secret should not be created either
				secrets, _ := k.Namespace("fleet-local").Get("secrets", "-o", "custom-columns=NAME:metadata.name", "--no-headers")
				Expect(secrets).ShouldNot(ContainSubstring("s-"))
			})
		})
	})
})
