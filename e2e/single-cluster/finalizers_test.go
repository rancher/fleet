package singlecluster_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/wrangler/v3/pkg/kv"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("Deleting a resource with finalizers", Ordered, func() {
	var (
		k               kubectl.Command
		gitrepoName     string
		path            string
		r               = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace string
		contentID       string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		targetNamespace = testenv.NewNamespaceName("target", r)
	})

	JustBeforeEach(func() {
		path = "simple-chart"

		err := testenv.CreateGitRepo(k, targetNamespace, gitrepoName, "master", "", path)
		Expect(err).ToNot(HaveOccurred())
		GinkgoWriter.Printf("created GitRepo %q in %q", gitrepoName, targetNamespace)

		Eventually(func(g Gomega) {
			deployID, err := k.Get(
				"bundledeployments",
				"-A",
				"-l",
				fmt.Sprintf("fleet.cattle.io/repo-name=%s", gitrepoName),
				"-o=jsonpath={.items[*].spec.deploymentID}",
			)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(deployID).ToNot(BeEmpty())

			// Check existence of content resource
			contentID, _ = kv.Split(deployID, ":")

			out, err := k.Get("content", contentID)
			g.Expect(err).ToNot(HaveOccurred(), out)

			out, err = k.Namespace(targetNamespace).Get("configmap", "test-simple-chart-config")
			g.Expect(err).ToNot(HaveOccurred(), out)
		}).Should(Succeed())
	})

	AfterEach(func() {
		out, err := k.Namespace("cattle-fleet-system").Run(
			"scale",
			"deployment",
			"fleet-controller",
			"--replicas=1",
			"--timeout=5s",
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(ContainSubstring("deployment.apps/fleet-controller scaled"))
		GinkgoWriter.Print("Scaled up the fleet-controller to 1 replicas")

		_, err = k.Namespace("cattle-fleet-system").Run(
			"scale",
			"deployment",
			"gitjob",
			"--replicas=1",
			"--timeout=5s",
		)
		Expect(err).ToNot(HaveOccurred())

		_, _ = k.Delete("gitrepo", gitrepoName, "--wait=false")
		_, _ = k.Delete("bundle", fmt.Sprintf("%s-%s", gitrepoName, path), "--wait=false")

		_, _ = k.Delete("ns", targetNamespace, "--wait=false")

		// Check that the content resource has been deleted, once all GitRepos and bundle deployments
		// referencing it have also been deleted.
		// We do not run this test when directly deleting a bundle deployment deletion, because this leads to
		// recreation of the corresponding content resource as the bundle still exists.
		if !strings.Contains(gitrepoName, "bundledeployment-test") {
			Eventually(func(g Gomega) {
				// Check that the last known content resource for the gitrepo has been deleted
				out, _ := k.Get("content", contentID)
				g.Expect(out).To(ContainSubstring("not found"))

				// Check that no content resource is left for the gitrepo's bundledeployment(s)
				out, err := k.Get(
					"contents",
					`-o=jsonpath=jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.finalizers}`+
						`{"\n"}{end}'`,
				)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).ToNot(ContainSubstring(gitrepoName))
			}).Should(Succeed())
		}
	})

	When("deleting an existing GitRepo", func() {
		BeforeEach(func() {
			gitrepoName = testenv.RandomFilename("finalizers-gitrepo-test", r)
		})
		It("updates the deployment", func() {
			By("checking the bundle and bundle deployment exist")
			Eventually(func() string {
				out, _ := k.Get("bundles")
				return out
			}).Should(ContainSubstring(gitrepoName))

			Eventually(func() string {
				out, _ := k.Get("bundledeployments", "-A")
				return out
			}).Should(ContainSubstring(gitrepoName))

			By("scaling down the gitjob controller to 0 replicas")
			GinkgoWriter.Print("Scaling down the fleet-controller to 0 replicas")
			_, err := k.Namespace("cattle-fleet-system").Run(
				"scale",
				"deployment",
				"gitjob",
				"--replicas=0",
				"--timeout=5s",
			)
			Expect(err).ToNot(HaveOccurred())

			By("deleting the GitRepo")
			out, err := k.Delete("gitrepo", gitrepoName, "--timeout=2s")
			Expect(err).To(HaveOccurred())
			Expect(out).To(ContainSubstring("timed out"))

			By("checking that the gitrepo still exists and has a deletion timestamp")
			out, err = k.Get(
				"gitrepo",
				gitrepoName,
				"-o=jsonpath={range .items[*]}{.metadata.deletionTimestamp}",
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).ToNot(BeZero())

			By("checking that the bundle and bundle deployment still exist")
			out, err = k.Get("bundles")
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(ContainSubstring(gitrepoName))

			out, err = k.Get("bundledeployments", "-A")
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(ContainSubstring(gitrepoName))

			By("checking that the auxiliary resources still exist")
			serviceAccountName := fmt.Sprintf("git-%s", gitrepoName)
			Consistently(func() error {
				out, _ := k.Get("configmaps")
				if !strings.Contains(out, fmt.Sprintf("%s-config", gitrepoName)) {
					return errors.New("configmap not found")
				}

				out, _ = k.Get("serviceaccounts")
				if !strings.Contains(out, serviceAccountName) {
					return errors.New("serviceaccount not found")
				}

				out, _ = k.Get("roles")
				if !strings.Contains(out, serviceAccountName) {
					return errors.New("role not found")
				}

				out, _ = k.Get("rolebindings")
				if !strings.Contains(out, serviceAccountName) {
					return errors.New("rolebinding not found")
				}

				return nil
			}, 2*time.Second, 100*time.Millisecond).ShouldNot(HaveOccurred())

			By("deleting the GitRepo once the controller runs again")
			_, err = k.Namespace("cattle-fleet-system").Run(
				"scale",
				"deployment",
				"gitjob",
				"--replicas=1",
				"--timeout=5s",
			)
			Expect(err).ToNot(HaveOccurred())

			// As soon as the controller is back, it deletes the gitrepo
			// as its delete timestamp was already set

			// These resources should be deleted when the GitRepo is deleted.
			By("checking that the auxiliary resources don't exist anymore")
			Eventually(func() error {
				out, _ := k.Get("configmaps")
				if strings.Contains(out, fmt.Sprintf("%s-config", gitrepoName)) {
					return errors.New("configmap not expected")
				}

				out, _ = k.Get("serviceaccounts")
				if strings.Contains(out, serviceAccountName) {
					return errors.New("serviceaccount not expected")
				}

				out, _ = k.Get("roles")
				if strings.Contains(out, serviceAccountName) {
					return errors.New("role not expected")
				}

				out, _ = k.Get("rolebindings")
				if strings.Contains(out, serviceAccountName) {
					return errors.New("rolebinding not expected")
				}

				return nil
			}).ShouldNot(HaveOccurred())
		})
	})

	When("deleting an existing bundle", func() {
		BeforeEach(func() {
			gitrepoName = testenv.RandomFilename("finalizers-bundle-test", r)
		})
		It("updates the deployment", func() {
			By("checking the bundle and bundle deployment exist")
			Eventually(func() string {
				out, _ := k.Get("bundles")
				return out
			}).Should(ContainSubstring(gitrepoName))

			Eventually(func() string {
				out, _ := k.Get("bundledeployments", "-A")
				return out
			}).Should(ContainSubstring(gitrepoName))

			By("scaling down the Fleet controller to 0 replicas")
			GinkgoWriter.Print("Scaling down the fleet-controller to 0 replicas")
			_, err := k.Namespace("cattle-fleet-system").Run(
				"scale",
				"deployment",
				"fleet-controller",
				"--replicas=0",
				"--timeout=5s",
			)
			Expect(err).ToNot(HaveOccurred())

			By("deleting the bundle")
			out, err := k.Delete(
				"bundle",
				fmt.Sprintf("%s-%s", gitrepoName, path),
				"--timeout=2s",
			)
			Expect(err).To(HaveOccurred())
			Expect(out).To(ContainSubstring("timed out"))

			By("checking that the bundle still exists and has a deletion timestamp")
			out, err = k.Get(
				"bundle",
				fmt.Sprintf("%s-%s", gitrepoName, path),
				"-o=jsonpath={range .items[*]}{.metadata.deletionTimestamp}",
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).ToNot(BeZero())

			By("checking that the bundle deployment still exists")
			out, err = k.Get("bundledeployments", "-A")
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(ContainSubstring(gitrepoName))
		})
	})

	When("deleting an existing bundledeployment", func() {
		BeforeEach(func() {
			gitrepoName = testenv.RandomFilename("finalizers-bundledeployment-test", r)
		})
		It("updates the deployment", func() {
			By("checking the bundle and bundle deployment exist")
			Eventually(func() string {
				out, _ := k.Get("bundles")
				return out
			}).Should(ContainSubstring(gitrepoName))

			Eventually(func() string {
				out, _ := k.Get("bundledeployments", "-A")
				return out
			}).Should(ContainSubstring(gitrepoName))

			By("scaling down the Fleet controller to 0 replicas")
			GinkgoWriter.Print("Scaling down the fleet-controller to 0 replicas")
			_, err := k.Namespace("cattle-fleet-system").Run(
				"scale",
				"deployment",
				"fleet-controller",
				"--replicas=0",
				"--timeout=5s",
			)
			Expect(err).ToNot(HaveOccurred())

			By("deleting the bundledeployment")
			bdNamespace, err := k.Get(
				"ns",
				"-o=jsonpath={.items[?(@."+
					`metadata.annotations.fleet\.cattle\.io/cluster-namespace=="fleet-local"`+
					")].metadata.name}",
			)
			Expect(err).ToNot(HaveOccurred())

			// Deleting a bundle deployment should hang for as long as it has a finalizer
			out, err := env.Kubectl.Namespace(bdNamespace).Delete(
				"bundledeployment",
				fmt.Sprintf("%s-%s", gitrepoName, path),
				"--timeout=2s",
			)
			Expect(err).To(HaveOccurred())
			Expect(out).To(ContainSubstring("timed out"))

			By("checking that the bundledeployment still exists and has a deletion timestamp")
			out, err = env.Kubectl.Namespace(bdNamespace).Get(
				"bundledeployment",
				fmt.Sprintf("%s-%s", gitrepoName, path),
				"-o=jsonpath={range .items[*]}{.metadata.deletionTimestamp}",
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).ToNot(BeZero())

			By("checking that the configmap created by the bundle deployment still exists")
			out, err = k.Namespace(targetNamespace).Get("configmap", "test-simple-chart-config")
			Expect(err).ToNot(HaveOccurred(), out)
		})
	})
})
