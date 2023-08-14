package singlecluster_test

// These test cases rely on a local git server, so that they can be run locally and against PRs.
// For tests monitoring external git hosting providers, see `e2e/require-secrets`.

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

const (
	port     = 8080
	repoName = "repo"
)

var _ = Describe("Monitoring Git repos via HTTP for change", Label("infra-setup"), func() {
	var (
		tmpdir           string
		clonedir         string
		k                kubectl.Command
		gh               *githelper.Git
		clone            *git.Repository
		repoName         string
		inClusterRepoURL string
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

		tmpdir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpdir, repoName)
	})

	AfterEach(func() {
		os.RemoveAll(tmpdir)
		_, _ = k.Delete("gitrepo", "gitrepo-test")
	})

	When("updating a git repository monitored via polling", func() {
		BeforeEach(func() {
			repoName = "repo"
		})

		JustBeforeEach(func() {
			err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), struct {
				Repo            string
				Branch          string
				PollingInterval string
			}{
				inClusterRepoURL,
				gh.Branch,
				"15s", // default
			})
			Expect(err).ToNot(HaveOccurred())

			clone, err = gh.Create(clonedir, testenv.AssetPath("gitrepo/sleeper-chart"), "examples")
			Expect(err).ToNot(HaveOccurred())
		})

		It("updates the deployment", func() {
			By("checking the pod exists")
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("pods")
				return out
			}).Should(ContainSubstring("sleeper-"))

			By("updating the git repository")
			replace(path.Join(clonedir, "examples", "Chart.yaml"), "0.1.0", "0.2.0")
			replace(path.Join(clonedir, "examples", "templates", "deployment.yaml"), "name: sleeper", "name: newsleep")

			commit, err := gh.Update(clone)
			Expect(err).ToNot(HaveOccurred())

			By("checking for the updated commit hash in gitrepo")
			Eventually(func() string {
				out, _ := k.Get("gitrepo", "gitrepo-test", "-o", "yaml")
				return out
			}).Should(ContainSubstring("commit: " + commit))

			By("checking the deployment's new name")
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("deployments")
				return out
			}).Should(ContainSubstring("newsleep"))
		})
	})

	When("updating a git repository monitored via webhook", func() {
		BeforeEach(func() {
			repoName = "webhook-test"
		})

		JustBeforeEach(func() {
			// Get git server pod name and create post-receive hook script from template
			var (
				out string
				err error
			)
			Eventually(func() string {
				out, err = k.Get("pod", "-l", "app=git-server", "-o", "name")
				if err != nil {
					fmt.Printf("%v\n", err)
					return ""
				}
				return out
			}).Should(ContainSubstring("pod/git-server-"))
			Expect(err).ToNot(HaveOccurred(), out)

			gitServerPod := strings.TrimPrefix(strings.TrimSpace(out), "pod/")

			hookScript := path.Join(tmpdir, "hook_script")

			err = testenv.Template(hookScript, testenv.AssetPath("gitrepo/post-receive.sh"), struct {
				RepoURL string
			}{
				inClusterRepoURL,
			})
			Expect(err).ToNot(HaveOccurred())

			// Create a git repo, erasing a previous repo with the same name if any
			out, err = k.Run(
				"exec",
				gitServerPod,
				"--",
				"/bin/sh",
				"-c",
				fmt.Sprintf(
					`dir=/srv/git/%s; rm -rf "$dir"; mkdir -p "$dir"; git init "$dir" --bare; GIT_DIR="$dir" git update-server-info`,
					repoName,
				),
			)
			Expect(err).ToNot(HaveOccurred(), out)

			// Copy the script into the repo on the server pod
			hookPathInRepo := fmt.Sprintf("/srv/git/%s/hooks/post-receive", repoName)

			out, err = k.Run("cp", hookScript, fmt.Sprintf("%s:%s", gitServerPod, hookPathInRepo))
			Expect(err).ToNot(HaveOccurred(), out)

			// Make hook script executable
			out, err = k.Run("exec", gitServerPod, "--", "chmod", "+x", hookPathInRepo)
			Expect(err).ToNot(HaveOccurred(), out)

			err = testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), struct {
				Repo            string
				Branch          string
				PollingInterval string
			}{
				inClusterRepoURL,
				gh.Branch,
				"24h", // prevent polling
			})
			Expect(err).ToNot(HaveOccurred())

			// Clone previously created repo
			clone, err = gh.Create(clonedir, testenv.AssetPath("gitrepo/sleeper-chart"), "examples")
			Expect(err).ToNot(HaveOccurred())
		})

		It("updates the deployment", func() {
			By("checking the pod exists")
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("pods")
				return out
			}).Should(ContainSubstring("sleeper-"))

			By("updating the git repository")
			replace(path.Join(clonedir, "examples", "Chart.yaml"), "0.1.0", "0.2.0")
			replace(path.Join(clonedir, "examples", "templates", "deployment.yaml"), "name: sleeper", "name: newsleep")

			commit, err := gh.Update(clone)
			Expect(err).ToNot(HaveOccurred())

			By("checking for the updated commit hash in gitrepo")
			Eventually(func() string {
				out, _ := k.Get("gitrepo", "gitrepo-test", "-o", "yaml")
				return out
			}).Should(ContainSubstring("commit: " + commit))

			By("checking the deployment's new name")
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("deployments")
				return out
			}).Should(ContainSubstring("newsleep"))
		})
	})
})

// replace replaces string s with r in the file located at path. That file must exist and be writable.
func replace(path string, s string, r string) {
	b, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())

	b = bytes.ReplaceAll(b, []byte(s), []byte(r))

	err = os.WriteFile(path, b, 0644)
	Expect(err).ToNot(HaveOccurred())
}
