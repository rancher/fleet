package singlecluster_test

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path"
	"strings"

	"github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
)

var _ = Describe("Checks status updates happen for a simple deployment", Ordered, func() {
	var (
		k               kubectl.Command
		targetNamespace string
		deleteNamespace bool
	)

	type TemplateData struct {
		TargetNamespace string
		DeleteNamespace bool
	}

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		deleteNamespace = false
	})

	JustBeforeEach(func() {
		err := testenv.ApplyTemplate(k, testenv.AssetPath("single-cluster/delete-namespace/gitrepo.yaml"),
			TemplateData{targetNamespace, deleteNamespace})

		Expect(err).ToNot(HaveOccurred())
		Eventually(func() error {
			out, err := k.Namespace(targetNamespace).Get("configmaps")
			if err != nil {
				return err
			}

			if !strings.Contains(out, "app-config") {
				return errors.New("expected configmap is not found")
			}

			return nil
		}).ShouldNot(HaveOccurred())
	})

	AfterAll(func() {
		_, _ = k.Delete("gitrepo", "my-gitrepo")
		_, _ = k.Delete("ns", "my-custom-namespace", "--wait=false")
	})

	When("deployment is successful", func() {
		BeforeEach(func() {
			targetNamespace = "my-custom-namespace"
		})

		It("correctly sets the status values for GitRepos", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get("gitrepo", "my-gitrepo", "-n", "fleet-local", "-o", "jsonpath='{.status.summary}'")
				g.Expect(err).ToNot(HaveOccurred(), out)

				g.Expect(out).Should(ContainSubstring("\"desiredReady\":1"))
				g.Expect(out).Should(ContainSubstring("\"ready\":1"))

				out, err = k.Get("gitrepo", "my-gitrepo", "-n", "fleet-local", "-o", "jsonpath='{.status.display}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(ContainSubstring("\"readyBundleDeployments\":\"1/1\""))
			}).Should(Succeed())
		})

		It("correctly sets the status values for Clusters", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get("cluster", "local", "-n", "fleet-local", "-o", "jsonpath='{.status.display.readyBundles}'")
				g.Expect(err).ToNot(HaveOccurred(), out)

				g.Expect(out).Should(Equal("'2/2'"))
			}).Should(Succeed())
		})

		It("correctly sets the status values for ClusterGroups", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get("clustergroup", "default", "-n", "fleet-local", "-o", "jsonpath='{.status.display.readyBundles}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(Equal("'2/2'"))

				out, err = k.Get("clustergroup", "default", "-n", "fleet-local", "-o", "jsonpath='{.status.display.readyClusters}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(Equal("'1/1'"))
			}).Should(Succeed())
		})

		It("correctly sets the status values for bundle", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get("bundle", "my-gitrepo-helm-verify", "-n", "fleet-local", "-o", "jsonpath='{.status.summary}'")
				g.Expect(err).ToNot(HaveOccurred(), out)

				g.Expect(out).Should(ContainSubstring("\"desiredReady\":1"))
				g.Expect(out).Should(ContainSubstring("\"ready\":1"))

				out, err = k.Get("bundle", "my-gitrepo-helm-verify", "-n", "fleet-local", "-o", "jsonpath='{.status.display}'")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).Should(ContainSubstring("\"readyClusters\":\"1/1\""))
			}).Should(Succeed())

		})
	})
})

var _ = FDescribe("Checks that template errors are shown in bundles and gitrepos", Label("infra-setup"), func() {
	var (
		tmpDir           string
		clonedir         string
		k                kubectl.Command
		gh               *githelper.Git
		clone            *git.Repository
		repoName         string
		inClusterRepoURL string
		gitrepoName      string
		r                = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace  string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
	})

	JustBeforeEach(func() {
		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host, err := githelper.BuildGitHostname(env.Namespace)
		Expect(err).ToNot(HaveOccurred())

		addr, err := githelper.GetExternalRepoAddr(env, port, repoName)
		Expect(err).ToNot(HaveOccurred())
		gh = githelper.NewHTTP(addr)

		inClusterRepoURL = gh.GetInClusterURL(host, port, repoName)

		tmpDir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpDir, repoName)

		gitrepoName = testenv.RandomFilename("status-test", r)
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir)

		_, err := k.Delete("gitrepo", gitrepoName)
		Expect(err).ToNot(HaveOccurred())

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

		out, err := k.Namespace("cattle-fleet-system").Logs(
			"-l",
			"app=fleet-controller",
			"-c",
			"fleet-controller",
		)
		Expect(err).ToNot(HaveOccurred())

		// Errors about resources other than bundles or bundle deployments not being found at deletion time
		// should be ignored, as they may result from other test suites.
		Expect(out).ToNot(MatchRegexp(
			`ERROR.*Reconciler error.*Bundle(Deployment)?.fleet.cattle.io \\".*\\" not found`,
		))

		_, err = k.Delete("ns", targetNamespace, "--wait=false")
		Expect(err).ToNot(HaveOccurred())
	})

	When("a git repository is created that contains a template error", func() {
		BeforeEach(func() {
			repoName = "repo"
			targetNamespace = testenv.NewNamespaceName("target", r)
		})
		JustBeforeEach(func() {
			err := testenv.ApplyTemplate(k, testenv.AssetPath("single-cluster/gitrepo-with-template-vars.yaml"), struct {
				Name            string
				Repo            string
				Branch          string
				PollingInterval string
				TargetNamespace string
			}{
				gitrepoName,
				inClusterRepoURL,
				gh.Branch,
				"15s",           // default
				targetNamespace, // to avoid conflicts with other tests
			})
			Expect(err).ToNot(HaveOccurred())

			clone, err = gh.Create(clonedir, testenv.AssetPath("gitrepo/sleeper-chart"), "examples")
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
