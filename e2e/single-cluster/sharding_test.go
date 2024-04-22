package singlecluster_test

import (
	"fmt"
	"math/rand"
	"os"
	"path"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/matchers"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var shards = []string{"shard1", "shard2", "shard3"}

var _ = Describe("Filtering events by shard", Label("infra-setup"), Ordered, func() {
	var (
		tmpDir           string
		clonedir         string
		k                kubectl.Command
		gh               *githelper.Git
		inClusterRepoURL string
		gitrepoName      string
		r                = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace  string
	)

	BeforeAll(func() {
		// No sharded controller should have reconciled any GitRepo until this point.
		for _, shard := range shards {
			logs, err := k.Namespace("cattle-fleet-system").Logs(
				"-l",
				"app=fleet-controller",
				"-l",
				fmt.Sprintf("shard=%s", shard),
				"--tail=-1",
			)
			Expect(err).ToNot(HaveOccurred())
			regexMatcher := matchers.MatchRegexpMatcher{Regexp: "Reconciling GitRepo.*"}
			hasReconciledGitRepos, err := regexMatcher.Match(logs)
			Expect(err).ToNot(HaveOccurred())
			Expect(hasReconciledGitRepos).To(BeFalse())
		}

		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host, err := githelper.BuildGitHostname(env.Namespace)
		Expect(err).ToNot(HaveOccurred())

		addr, err := githelper.GetExternalRepoAddr(env, port, repoName)
		Expect(err).ToNot(HaveOccurred())
		gh = githelper.NewHTTP(addr)

		inClusterRepoURL = gh.GetInClusterURL(host, port, repoName)

		tmpDir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpDir, repoName)

		_, err = gh.Create(clonedir, testenv.AssetPath("gitrepo/sleeper-chart"), "examples")
		Expect(err).ToNot(HaveOccurred())

		k = env.Kubectl.Namespace(env.Namespace)
	})

	for _, shard := range shards {
		When(fmt.Sprintf("deploying a gitrepo labeled with shard ID %s", shard), func() {
			JustBeforeEach(func() {
				targetNamespace = testenv.NewNamespaceName("target", r)
				gitrepoName = testenv.RandomFilename("sharding-test", r)

				err := testenv.ApplyTemplate(
					k,
					testenv.AssetPath("gitrepo/gitrepo_sharded.yaml"),
					struct {
						Name            string
						Repo            string
						Branch          string
						PollingInterval string
						TargetNamespace string
						ShardID         string
					}{
						gitrepoName,
						inClusterRepoURL,
						gh.Branch,
						"15s",           // default
						targetNamespace, // to avoid conflicts with other tests
						shard,
					},
				)
				Expect(err).ToNot(HaveOccurred())
			})

			It(fmt.Sprintf("deploys the gitrepo via the controller labeled with shard ID %s", shard), func() {
				By("checking the pod exists")
				Eventually(func() string {
					out, _ := k.Namespace(targetNamespace).Get("pods")
					return out
				}).Should(ContainSubstring("sleeper-"))

				for _, s := range shards {
					logs, err := k.Namespace("cattle-fleet-system").Logs(
						"-l",
						"app=fleet-controller",
						"-l",
						fmt.Sprintf("shard=%s", s),
					)
					Expect(err).ToNot(HaveOccurred())
					regexMatcher := matchers.MatchRegexpMatcher{
						Regexp: fmt.Sprintf(`Reconciling GitRepo.*"name":"%s"`, gitrepoName),
					}
					hasReconciledGitRepo, err := regexMatcher.Match(logs)
					Expect(err).ToNot(HaveOccurred())
					if s == shard {
						Expect(hasReconciledGitRepo).To(BeTrueBecause(
							"GitRepo %q labeled with shard %q should have been"+
								" deployed by controller for shard %q in namespace %q",
							gitrepoName,
							shard,
							shard,
							env.Namespace,
						))
					} else {
						Expect(hasReconciledGitRepo).To(BeFalseBecause(
							"GitRepo %q labeled with shard %q should not have been"+
								" deployed by controller for shard %q",
							gitrepoName,
							shard,
							s,
						))
					}
				}
			})

			AfterEach(func() {
				_ = os.RemoveAll(tmpDir)
				_, _ = k.Delete("gitrepo", gitrepoName)
			})
		})
	}

	When("deploying a gitrepo labeled with an unknown shard ID", func() {
		JustBeforeEach(func() {
			targetNamespace = testenv.NewNamespaceName("target", r)
			gitrepoName = testenv.RandomFilename("sharding-test", r)

			err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo_sharded.yaml"), struct {
				Name            string
				Repo            string
				Branch          string
				PollingInterval string
				TargetNamespace string
				ShardID         string
			}{
				gitrepoName,
				inClusterRepoURL,
				gh.Branch,
				"15s",           // default
				targetNamespace, // to avoid conflicts with other tests
				"unknown",
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("does not deploy the gitrepo", func() {
			By("checking the pod does not exist")
			Eventually(func() string {
				out, _ := k.Namespace(targetNamespace).Get("pods")
				return out
			}).ShouldNot(ContainSubstring("sleeper-"))

			for _, s := range shards {
				logs, err := k.Namespace("cattle-fleet-system").Logs(
					"-l",
					"app=fleet-controller",
					"-l",
					fmt.Sprintf("shard=%s", s),
				)
				Expect(err).ToNot(HaveOccurred())
				regexMatcher := matchers.MatchRegexpMatcher{
					Regexp: fmt.Sprintf(
						`Reconciling GitRepo.*"GitRepo": {"name":"%s","namespace":"%s"}`,
						gitrepoName,
						env.Namespace,
					),
				}
				hasReconciledGitRepos, err := regexMatcher.Match(logs)
				Expect(err).ToNot(HaveOccurred())
				Expect(hasReconciledGitRepos).To(BeFalseBecause(
					"GitRepo labeled with shard %q should not have been deployed by"+
						" controller for shard %q",
					"unknown",
					s,
				))
			}
		})

		AfterEach(func() {
			_ = os.RemoveAll(tmpDir)
			_, _ = k.Delete("gitrepo", gitrepoName)
		})
	})
})
