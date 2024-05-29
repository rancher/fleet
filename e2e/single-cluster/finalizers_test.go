package singlecluster_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("Deleting a resource with finalizers", func() {
	var (
		k               kubectl.Command
		gitrepoName     string
		path            string
		r               = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		targetNamespace = testenv.NewNamespaceName("target", r)
	})

	JustBeforeEach(func() {
		gitrepoName = testenv.RandomFilename("finalizers-test", r)
		path = "simple-chart"
	})

	AfterEach(func() {
		_, err := k.Namespace("cattle-fleet-system").Run(
			"scale",
			"deployment",
			"fleet-controller",
			"--replicas=1",
			"--timeout=5s",
		)
		Expect(err).ToNot(HaveOccurred())

		_, _ = k.Delete("gitrepo", gitrepoName)
		_, _ = k.Delete("bundle", fmt.Sprintf("%s-%s", gitrepoName, path))
	})

	When("deleting an existing GitRepo", func() {
		JustBeforeEach(func() {
			By("creating a GitRepo")
			err := testenv.CreateGitRepo(k, targetNamespace, gitrepoName, "master", "", path)
			Expect(err).ToNot(HaveOccurred())
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
			_, err := k.Namespace("cattle-fleet-system").Run(
				"scale",
				"deployment",
				"fleet-controller",
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
				"fleet-controller",
				"--replicas=1",
				"--timeout=5s",
			)
			Expect(err).ToNot(HaveOccurred())

			_, err = k.Delete("gitrepo", gitrepoName)
			Expect(err).NotTo(HaveOccurred())

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
		JustBeforeEach(func() {
			By("creating a GitRepo")
			err := testenv.CreateGitRepo(k, targetNamespace, gitrepoName, "master", "", path)
			Expect(err).ToNot(HaveOccurred())
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
		JustBeforeEach(func() {
			By("creating a GitRepo")
			err := testenv.CreateGitRepo(k, targetNamespace, gitrepoName, "master", "", path)
			Expect(err).ToNot(HaveOccurred())
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
			_, err = k.Namespace(targetNamespace).Get("configmap", "test-simple-chart-config")
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
