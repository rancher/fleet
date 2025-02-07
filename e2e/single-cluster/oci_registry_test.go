package singlecluster_test

import (
	"fmt"
	"net"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const experimentalEnvVar = "EXPERIMENTAL_OCI_STORAGE"

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

// getActualEnvVariable returns the value of the given env variable
// in the gitjob and fleet-controller deployments.
// In order to be considered correct, both values should match
func getActualEnvVariable(k kubectl.Command, env string) bool {
	actualValueController, err := checkEnvVariable(k, "deployment/fleet-controller", env)
	Expect(err).ToNot(HaveOccurred())
	actualValueGitjob, err := checkEnvVariable(k, "deployment/gitjob", env)
	Expect(err).ToNot(HaveOccurred())
	// both values should be the same, otherwise is a clear symptom that something went wrong
	Expect(actualValueController).To(Equal(actualValueGitjob))
	GinkgoWriter.Printf("Actual experimental env variable values: CONTROLLER: %t, GITJOB: %t\n", actualValueController, actualValueGitjob)
	return actualValueController
}

// checkEnvVariable runs a kubectl set env --list command for the given component
// and returns the value of the given env variable
func checkEnvVariable(k kubectl.Command, component string, env string) (bool, error) {
	ns := "cattle-fleet-system"
	out, err := k.Namespace(ns).Run("set", "env", component, "--list")
	Expect(err).ToNot(HaveOccurred())
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, env) {
			keyValue := strings.Split(line, "=")
			Expect(keyValue).To(HaveLen(2))
			return strconv.ParseBool(keyValue[1])
		}
	}
	return false, fmt.Errorf("Environment variable was not found")
}

// updateExperimentalFlagValue updates the oci storage experimental flag to the given value.
// It compares the actual value vs the one given and, if they are different, updates the
// fleet-controller and gitjob deployments to set the given one.
// It waits until the pods related to both deployments are restarted.
func updateExperimentalFlagValue(k kubectl.Command, value bool) {
	actualEnvVal := getActualEnvVariable(k, experimentalEnvVar)
	GinkgoWriter.Printf("Env variable value to be used in this test: %t\n", value)
	if actualEnvVal == value {
		return
	}
	ns := "cattle-fleet-system"
	// get the actual fleet-controller and gitjob pods
	// When we change the environments variables of the deployment the pods will restart
	var controllerPod string
	var err error
	Eventually(func() string {
		controllerPod, err = k.Namespace(ns).Get("pod", "-l", "app=fleet-controller", "-l", "fleet.cattle.io/shard-default=true", "-o", "name")
		Expect(err).ToNot(HaveOccurred())
		return controllerPod
	}).WithTimeout(time.Second * 60).Should(ContainSubstring("pod/fleet-controller-"))

	var gitjobPod string
	Eventually(func() string {
		gitjobPod, err = k.Namespace(ns).Get("pod", "-l", "app=gitjob", "-o", "name")
		Expect(err).ToNot(HaveOccurred())
		return gitjobPod
	}).WithTimeout(time.Second * 60).Should(ContainSubstring("pod/gitjob-"))

	// set the experimental env value to the deployments
	strEnvValue := fmt.Sprintf("%s=%t", experimentalEnvVar, value)
	_, _ = k.Namespace(ns).Run("set", "env", "deployment/fleet-controller", strEnvValue)
	_, _ = k.Namespace(ns).Run("set", "env", "deployment/gitjob", strEnvValue)

	// wait for both pods to restart
	Eventually(func() bool {
		controllerPodNow, _ := k.Namespace(ns).Get("pod", "-l", "app=fleet-controller", "-l", "fleet.cattle.io/shard-default=true", "-o", "name")
		return controllerPodNow != "" &&
			strings.Contains(controllerPodNow, "pod/fleet-controller-") &&
			!strings.Contains(controllerPodNow, controllerPod)
	}).WithTimeout(time.Second * 30).Should(BeTrue())
	Eventually(func() bool {
		gitjobPodNow, _ := k.Namespace(ns).Get("pod", "-l", "app=gitjob", "-o", "name")
		return gitjobPodNow != "" &&
			strings.Contains(gitjobPodNow, "pod/gitjob-") &&
			!strings.Contains(gitjobPodNow, gitjobPod)
	}).WithTimeout(time.Second * 30).Should(BeTrue())
}

var _ = Describe("Single Cluster Deployments using OCI registry", Label("oci-registry", "infra-setup"), func() {
	var (
		asset                  string
		insecureSkipTLS        bool
		forceGitRepoURL        string
		k                      kubectl.Command
		tempDir                string
		contentsID             string
		downstreamNamespace    string
		ociRegistry            string
		experimentalFlagBefore bool
		experimentalValue      bool
		contentIDToPurge       string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		tempDir = GinkgoT().TempDir()
		externalIP, err := k.Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
		Expect(err).ToNot(HaveOccurred(), externalIP)
		Expect(net.ParseIP(externalIP)).ShouldNot(BeNil())
		ociRegistry = fmt.Sprintf("%s:8082", externalIP)

		// store the actual value of the experimental env value
		// we'll restore in the AfterEach statement if needed
		experimentalFlagBefore = getActualEnvVariable(k, experimentalEnvVar)

		// reset the value of the contents resource to purge
		contentIDToPurge = ""
	})

	JustBeforeEach(func() {
		updateExperimentalFlagValue(k, experimentalValue)
		Expect(os.Getenv("CI_OCI_USERNAME")).NotTo(BeEmpty())
		Expect(os.Getenv("CI_OCI_PASSWORD")).NotTo(BeEmpty())
		out, err := k.Create(
			"secret", "generic", "oci-secret",
			"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
			"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
		)
		Expect(err).ToNot(HaveOccurred(), out)

		gitrepo := path.Join(tempDir, "gitrepo.yaml")
		if forceGitRepoURL != "" {
			ociRegistry = forceGitRepoURL
		}

		err = testenv.Template(gitrepo, testenv.AssetPath(asset), struct {
			OCIReference       string
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
			out, _ := k.Namespace("fleet-local").Get("secrets", "-o", "name")
			return out
		}).ShouldNot(ContainSubstring("secret/s-"))

		Eventually(func() string {
			out, _ := k.Namespace(downstreamNamespace).Get("secrets", "-o", "name")
			return out
		}).ShouldNot(ContainSubstring("secret/s-"))

		// reset back experimental flag value if needed
		if experimentalValue != experimentalFlagBefore {
			GinkgoWriter.Printf("Restoring experimental env variable back to %t n", experimentalFlagBefore)
			updateExperimentalFlagValue(k, experimentalFlagBefore)
		}

		// check that contents have been purged when deleting the gitrepo
		if contentIDToPurge != "" {
			out, err := k.Delete("contents", contentIDToPurge)
			Expect(out).To(ContainSubstring("not found"))
			Expect(err).To(HaveOccurred())
		}
	})

	When("creating a gitrepo resource with ociRegistry info", func() {
		Context("containing a valid helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/test-oci.yaml"
				insecureSkipTLS = true
				forceGitRepoURL = ""
				experimentalValue = true
			})

			It("deploys the bundle", func() {
				By("creating the bundle", func() {
					Eventually(func() string {
						out, _ := k.Namespace("fleet-local").Get("bundles")
						return out
					}).Should(ContainSubstring("sample-simple-chart-oci"))
				})
				By("setting the ContentsID field in the bundle", func() {
					Eventually(func() string {
						contentsID, _ = k.Namespace("fleet-local").Get("bundle", "sample-simple-chart-oci", `-o=jsonpath={.spec.contentsId}`)
						return contentsID
					}).Should(ContainSubstring("s-"))
				})

				By("setting the OCI reference status field key to the OCI path of the manifest", func() {
					Eventually(func() bool {
						out, _ := k.Namespace("fleet-local").Get("bundle", "sample-simple-chart-oci", `-o=jsonpath={.status.ociReference}`)
						return out == fmt.Sprintf("oci://%s/%s:latest", ociRegistry, contentsID)
					}).Should(BeTrue())
				})
				By("creating a bundle secret", func() {
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
				By("creating a bundledeployment secret", func() {
					Eventually(func() string {
						out, err := k.Namespace(downstreamNamespace).Get("secret", contentsID)
						if err != nil {
							// return nothing in case of error
							// This avoids false positives when kubectl returns "secrets "XXXXX" not found
							return ""
						}
						GinkgoWriter.Printf("BundleDeployment secret: %s\n", out)
						return out
					}).Should(ContainSubstring(contentsID))
				})
				By("not creating a contents resource for this chart", func() {
					out, _ := k.Get("contents")
					Expect(out).NotTo(ContainSubstring(contentsID))
				})
				By("deploying the helm chart", func() {
					Eventually(func() string {
						out, _ := k.Namespace("fleet-local").Get("configmaps")
						return out
					}).Should(ContainSubstring("sample-config"))
				})
			})
		})
	})

	When("creating a gitrepo resource with invalid ociRegistry info and experimental flag is false", func() {
		Context("containing a public oci based helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/test-oci.yaml"
				insecureSkipTLS = true
				forceGitRepoURL = "not-valid-oci-registry.com"
				experimentalValue = false
			})

			It("creates the bundle with no OCI storage", func() {
				Eventually(func(g Gomega) {
					// check for the bundle
					out, _ := k.Namespace("fleet-local").Get("bundles")
					g.Expect(out).To(ContainSubstring("sample-simple-chart-oci"))

					// check for contentsID
					contentsID, err := k.Namespace("fleet-local").Get("bundle", "sample-simple-chart-oci", `-o=jsonpath={.spec.contentsId}`)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(contentsID).To(BeEmpty())

					GinkgoWriter.Printf("ContentsID: %s\n", contentsID)

					// bundle secret should not be created
					secrets, _ := k.Namespace("fleet-local").Get("secrets", "-o", "custom-columns=NAME:metadata.name", "--no-headers")
					g.Expect(secrets).ToNot(ContainSubstring("s-"))

					// gets the bundles sha256 for the resources
					// We'll use that to identify the contents resource for this bundle
					resourcesSha256, _ := k.Namespace("fleet-local").Get("bundle", "sample-simple-chart-oci", `-o=jsonpath={.status.resourcesSha256Sum}`)
					g.Expect(resourcesSha256).ToNot(BeEmpty())

					// delete the last 3 chars from the sha256 so it matches the contents ID
					resourcesSha256 = resourcesSha256[:len(resourcesSha256)-3]

					// check that it created a content resource with the above sha256
					contents, _ := k.Get("contents")
					g.Expect(contents).To(ContainSubstring(resourcesSha256))

					// save the contents id to purge after the test
					// this will prevent interference with the previous test
					// as the resources sha256 is the same
					contentIDToPurge = "s-" + resourcesSha256

					// finally check that the helm chart was deployed
					configmaps, _ := k.Namespace("fleet-local").Get("configmaps")
					g.Expect(configmaps).To(ContainSubstring("sample-config"))
				}).Should(Succeed())
			})
		})
	})

	When("creating a gitrepo resource with invalid ociRegistry info", func() {
		Context("containing a public oci based helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/test-oci.yaml"
				insecureSkipTLS = true
				forceGitRepoURL = "not-valid-oci-registry.com"
				experimentalValue = true
			})

			It("does not create the bundle", func() {
				// look for failed pods with the name sample-xxxx-xxxx
				r, _ := regexp.Compile("sample-([a-z0-9]+)-([a-z0-9]+)")
				var failedJob string
				Eventually(func(g Gomega) {
					failedPods, err := getFailedPodNames(k, "fleet-local")
					failedJob = ""

					g.Expect(err).ToNot(HaveOccurred())
					for _, pod := range failedPods {
						if r.MatchString(pod) {
							failedJob = pod
							break
						}
					}
					g.Expect(failedJob).ToNot(BeEmpty())
				}).Should(Succeed())

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
				forceGitRepoURL = ""
				experimentalValue = true
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
