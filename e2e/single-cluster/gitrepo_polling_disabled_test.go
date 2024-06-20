package singlecluster_test

import (
	"math/rand"
	"os"
	"path"
	"regexp"

	"github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("GitRepoPollingDisabled", Label("infra-setup"), func() {
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

		gitrepoName = testenv.RandomFilename("gitjob-test", r)
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir)

		_, err := k.Delete("gitrepo", gitrepoName)
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() string {
			out, _ := k.Get("bundledeployments", "-A")
			return out
		}).ShouldNot(ContainSubstring(gitrepoName))

		out, err := k.Namespace("cattle-fleet-system").Logs(
			"-l",
			"app=fleet-controller",
			"-c",
			"fleet-controller",
		)
		Expect(err).ToNot(HaveOccurred())

		// Errors about bundles or bundle deployments not being found at deletion time should be ignored.
		isError, err := regexp.MatchString(
			`ERROR.*Reconciler error.*Bundle(Deployment)?.fleet.cattle.io \\".*\\" not found`,
			out,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(isError).To(BeFalse())

		_, err = k.Delete("ns", targetNamespace)
		Expect(err).ToNot(HaveOccurred())
	})

	When("applying a gitrepo with disable polling", func() {
		BeforeEach(func() {
			repoName = "repo"
			targetNamespace = testenv.NewNamespaceName("disable-polling", r)
		})

		JustBeforeEach(func() {
			err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo-polling-disabled.yaml"), struct {
				Name            string
				Repo            string
				Branch          string
				TargetNamespace string
			}{
				gitrepoName,
				inClusterRepoURL,
				gh.Branch,
				targetNamespace,
			})
			Expect(err).ToNot(HaveOccurred())

			clone, err = gh.Create(clonedir, testenv.AssetPath("gitrepo/sleeper-chart"), "disable_polling")
			Expect(err).ToNot(HaveOccurred())
		})

		It("deploys the resources initially and updates them while force updating", func() {
			By("checking the pod exists")
			Eventually(func() string {
				out, _ := k.Namespace(targetNamespace).Get("pods")
				return out
			}).Should(ContainSubstring("sleeper-"))

			By("Updating the git repository")
			replace(path.Join(clonedir, "disable_polling", "templates", "deployment.yaml"), "name: sleeper", "name: newsleep")

			commit, err := gh.Update(clone)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying the pods aren't updated")
			Eventually(func() string {
				out, _ := k.Namespace(targetNamespace).Get("pods")
				return out
			}).ShouldNot(ContainSubstring("newsleep"))

			By("Force updating the GitRepo")
			patch := `{"spec": {"forceSyncGeneration": 1}}`
			out, err := k.Run("patch", "gitrepo", gitrepoName, "--type=merge", "--patch", patch)
			Expect(err).ToNot(HaveOccurred(), out)

			By("Verifying the pods are updated")
			Eventually(func() string {
				out, _ := k.Namespace(targetNamespace).Get("pods")
				return out
			}).Should(ContainSubstring("newsleep"))

			By("Verifying the commit hash is updated")
			Eventually(func() string {
				out, _ := k.Get("gitrepo", gitrepoName, "-o", "jsonpath={.status.commit}")
				return out
			}).Should(Equal(commit))
		})
	})
})
