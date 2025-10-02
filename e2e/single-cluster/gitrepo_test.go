package singlecluster_test

// These test cases rely on a local git server, so that they can be run locally and against PRs.
// For tests monitoring external git hosting providers, see `e2e/require-secrets`.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path"
	"reflect"
	"strings"

	"github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/infra/cmd"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
)

const (
	port      = 8080
	HTTPSPort = 4343
)

type gitRepoTestValues struct {
	Name                  string
	Repo                  string
	Branch                string
	PollingInterval       string
	TargetNamespace       string
	Path                  string
	InsecureSkipTLSVerify bool
	CABundle              string
}

var _ = Describe("Monitoring Git repos via HTTP for change", Label("infra-setup"), func() {
	var (
		tmpDir           string
		clonedir         string
		k                kubectl.Command
		gh               *githelper.Git
		clone            *git.Repository
		localRepoName    string
		inClusterRepoURL string
		gitrepoName      string
		r                = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace  string

		gitServerPort int
		gitProtocol   string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
	})

	JustBeforeEach(func() {

		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host := githelper.BuildGitHostname()

		addr, err := githelper.GetExternalRepoAddr(env, gitServerPort, localRepoName)
		Expect(err).ToNot(HaveOccurred())
		addr = strings.Replace(addr, "http://", fmt.Sprintf("%s://", gitProtocol), 1)
		gh = githelper.NewHTTP(addr)

		inClusterRepoURL = gh.GetInClusterURL(host, gitServerPort, localRepoName)

		tmpDir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpDir, localRepoName)

		gitrepoName = testenv.RandomFilename("gitjob-test", r)
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

		_, err := k.Delete("ns", targetNamespace, "--wait=false")
		Expect(err).ToNot(HaveOccurred())
	})

	When("updating a git repository monitored via polling with HTTP for change", func() {
		BeforeEach(func() {
			localRepoName = "repo"
			targetNamespace = testenv.NewNamespaceName("target", r)

			gitServerPort = port
			gitProtocol = "http"
		})

		JustBeforeEach(func() {
			err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), gitRepoTestValues{
				Name:            gitrepoName,
				Repo:            inClusterRepoURL,
				Branch:          gh.Branch,
				PollingInterval: "15s",           // default
				TargetNamespace: targetNamespace, // to avoid conflicts with other tests
			})
			Expect(err).ToNot(HaveOccurred())

			clone, err = gh.Create(clonedir, testenv.AssetPath("gitrepo/sleeper-chart"), "examples")
			Expect(err).ToNot(HaveOccurred())
		})

		It("updates the deployment", func() {
			By("checking the pod exists")
			Eventually(func() string {
				out, _ := k.Namespace(targetNamespace).Get("pods")
				return out
			}).Should(ContainSubstring("sleeper-"))

			By("updating the git repository")
			replace(path.Join(clonedir, "examples", "Chart.yaml"), "0.1.0", "0.2.0")
			replace(path.Join(clonedir, "examples", "templates", "deployment.yaml"), "name: sleeper", "name: newsleep")

			commit, err := gh.Update(clone)
			Expect(err).ToNot(HaveOccurred())

			By("updating the gitrepo's status")
			Eventually(func(g Gomega) {
				status := getGitRepoStatus(g, k, gitrepoName)
				g.Expect(status).To(matchGitRepoStatus(expectedStatus(commit, "")))
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(Succeed())

			By("checking the deployment's new name")
			Eventually(func() string {
				out, _ := k.Namespace(targetNamespace).Get("deployments")
				return out
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(ContainSubstring("newsleep"))
		})
	})

	When("updating a git repository monitored via polling with HTTP with a custom CA for change", func() {
		BeforeEach(func() {
			localRepoName = "repo"
			targetNamespace = testenv.NewNamespaceName("target", r)

			gitServerPort = HTTPSPort
			gitProtocol = "https"
		})

		JustBeforeEach(func() {
			err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), gitRepoTestValues{
				Name:            gitrepoName,
				Repo:            inClusterRepoURL,
				Branch:          gh.Branch,
				PollingInterval: "15s",           // default
				TargetNamespace: targetNamespace, // to avoid conflicts with other tests
			})
			Expect(err).ToNot(HaveOccurred())

			clone, err = gh.Create(clonedir, testenv.AssetPath("gitrepo/sleeper-chart"), "examples")
			Expect(err).ToNot(HaveOccurred())
		})

		It("updates the deployment", func() {
			By("checking the pod exists")
			Eventually(func() string {
				out, _ := k.Namespace(targetNamespace).Get("pods")
				return out
			}).Should(ContainSubstring("sleeper-"))

			By("updating the git repository")
			replace(path.Join(clonedir, "examples", "Chart.yaml"), "0.1.0", "0.2.0")
			replace(path.Join(clonedir, "examples", "templates", "deployment.yaml"), "name: sleeper", "name: newsleep")

			commit, err := gh.Update(clone)
			Expect(err).ToNot(HaveOccurred())

			By("updating the gitrepo's status")
			Eventually(func(g Gomega) {
				status := getGitRepoStatus(g, k, gitrepoName)
				g.Expect(status).To(matchGitRepoStatus(expectedStatus(commit, "")))
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(Succeed())

			By("checking the deployment's new name")
			Eventually(func() string {
				out, _ := k.Namespace(targetNamespace).Get("deployments")
				return out
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(ContainSubstring("newsleep"))
		})
	})

	When("updating a git repository monitored via webhook", func() {
		BeforeEach(func() {
			localRepoName = "webhook-test"
			targetNamespace = testenv.NewNamespaceName("target", r)
		})

		JustBeforeEach(func() {
			// Get git server pod name and create post-receive hook script from template
			var (
				out string
				err error
				kg  = k.Namespace(cmd.InfraNamespace)
			)
			Eventually(func() string {
				out, err = kg.Get("pod", "-l", "app=git-server", "-o", "name")
				if err != nil {
					fmt.Printf("%v\n", err)
					return ""
				}
				return out
			}).Should(ContainSubstring("pod/git-server-"))
			Expect(err).ToNot(HaveOccurred(), out)

			gitServerPod := strings.TrimPrefix(strings.TrimSpace(out), "pod/")

			hookScript := path.Join(tmpDir, "hook_script")

			err = testenv.Template(hookScript, testenv.AssetPath("gitrepo/post-receive.sh"), struct {
				RepoURL string
			}{
				inClusterRepoURL,
			})
			Expect(err).ToNot(HaveOccurred())

			// Create a git repo, erasing a previous repo with the same name if any
			out, err = kg.Run(
				"exec",
				gitServerPod,
				"--",
				"/bin/sh",
				"-c",
				fmt.Sprintf(
					`dir=/srv/git/%s; rm -rf "$dir"; mkdir -p "$dir"; git init "$dir" --bare; GIT_DIR="$dir" git update-server-info`,
					localRepoName,
				),
			)
			Expect(err).ToNot(HaveOccurred(), out)

			// Copy the script into the repo on the server pod
			hookPathInRepo := fmt.Sprintf("/srv/git/%s/hooks/post-receive", localRepoName)

			Eventually(func() error {
				out, err = kg.Run("cp", hookScript, fmt.Sprintf("%s:%s", gitServerPod, hookPathInRepo))
				return err
			}).Should(Not(HaveOccurred()), out)

			// Make hook script executable
			Eventually(func() error {
				out, err = kg.Run("exec", gitServerPod, "--", "chmod", "+x", hookPathInRepo)
				return err
			}).ShouldNot(HaveOccurred(), out)

			// Clone previously created repo
			clone, err = gh.Create(clonedir, testenv.AssetPath("gitrepo/sleeper-chart"), "examples")
			Expect(err).ToNot(HaveOccurred())

			err = testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), gitRepoTestValues{
				Name:            gitrepoName,
				Repo:            inClusterRepoURL,
				Branch:          gh.Branch,
				PollingInterval: "24h",           // prevent polling
				TargetNamespace: targetNamespace, // to avoid conflicts with other tests
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("updates the deployment", func() {
			By("checking the pod exists")
			Eventually(func() string {
				out, _ := k.Namespace(targetNamespace).Get("pods")
				return out
			}).Should(ContainSubstring("sleeper-"))

			By("updating the git repository")
			replace(path.Join(clonedir, "examples", "Chart.yaml"), "0.1.0", "0.2.0")
			replace(path.Join(clonedir, "examples", "templates", "deployment.yaml"), "name: sleeper", "name: newsleep")

			commit, err := gh.Update(clone)
			Expect(err).ToNot(HaveOccurred())

			By("updating the gitrepo's status")
			Eventually(func(g Gomega) {
				status := getGitRepoStatus(g, k, gitrepoName)
				g.Expect(status).To(matchGitRepoStatus(expectedStatus(commit, commit)))
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(Succeed())

			By("checking the deployment's new name")
			Eventually(func() string {
				out, _ := k.Namespace(targetNamespace).Get("deployments")
				return out
			}, testenv.MediumTimeout, testenv.ShortTimeout).Should(ContainSubstring("newsleep"))
		}, Label("webhook"))
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

// getGitRepoStatus retrieves the status of the gitrepo with the provided name.
func getGitRepoStatus(g Gomega, k kubectl.Command, name string) fleet.GitRepoStatus {
	gr, err := k.Get("gitrepo", name, "-o=json")

	g.Expect(err).ToNot(HaveOccurred())

	var gitrepo fleet.GitRepo
	_ = json.Unmarshal([]byte(gr), &gitrepo)

	return gitrepo.Status
}

// expectedStatus builds an expected gitrepo status based on a commit SHA.
// Does not set the webhookCommit if not provided
func expectedStatus(commit string, webhookCommit string) fleet.GitRepoStatus {
	status := fleet.GitRepoStatus{
		Commit:       commit,
		GitJobStatus: "Current",
		StatusBase: fleet.StatusBase{
			ReadyClusters:        1,
			DesiredReadyClusters: 1,
			Summary: fleet.BundleSummary{
				NotReady:          0,
				WaitApplied:       0,
				ErrApplied:        0,
				OutOfSync:         0,
				Modified:          0,
				Ready:             1,
				Pending:           0,
				DesiredReady:      1,
				NonReadyResources: []fleet.NonReadyResource(nil),
			},
			Display: fleet.StatusDisplay{
				ReadyBundleDeployments: "1/1",
				// XXX: add state and message?
			},
			Conditions: []genericcondition.GenericCondition{
				{
					Type:   "Ready",
					Status: "True",
				},
				{
					Type:   "Accepted",
					Status: "True",
				},
				{
					Type:   "Reconciling",
					Status: "False",
				},
				{
					Type:   "Stalled",
					Status: "False",
				},
			},
			ResourceCounts: fleet.ResourceCounts{
				Ready:        1,
				DesiredReady: 1,
				WaitApplied:  0,
				Modified:     0,
				Orphaned:     0,
				Missing:      0,
				Unknown:      0,
				NotReady:     0,
			},
		},
	}
	if webhookCommit != "" {
		status.WebhookCommit = commit
	}

	return status
}

type gitRepoStatusMatcher struct {
	expected fleet.GitRepoStatus
}

func matchGitRepoStatus(expected fleet.GitRepoStatus) types.GomegaMatcher {
	return &gitRepoStatusMatcher{expected: expected}
}

func (matcher *gitRepoStatusMatcher) Match(actual interface{}) (success bool, err error) {
	got, ok := actual.(fleet.GitRepoStatus)
	if !ok {
		return false, fmt.Errorf("gitRepoStatusMatcher expects a GitRepoStatus")
	}

	want := matcher.expected

	// Conditions are tested using custom logic to avoid having to manipulate timestamps (last update and transition
	// times).
	for _, wantCond := range want.Conditions {
		found := false
		for _, gotCond := range got.Conditions {
			if gotCond.Type == wantCond.Type &&
				wantCond.Status == gotCond.Status &&
				wantCond.Reason == gotCond.Reason &&
				wantCond.Message == gotCond.Message {
				found = true
			}
		}
		if !found {
			return false, fmt.Errorf(
				"Condition %q with status %q not found",
				wantCond.Type,
				wantCond.Status,
			)
		}
	}

	return got.Commit == want.Commit &&
			got.WebhookCommit == want.WebhookCommit &&
			got.ReadyClusters == want.ReadyClusters &&
			got.DesiredReadyClusters == want.DesiredReadyClusters &&
			got.GitJobStatus == want.GitJobStatus &&
			reflect.DeepEqual(got.Summary, want.Summary) &&
			got.Display.ReadyBundleDeployments == want.Display.ReadyBundleDeployments &&
			got.ResourceCounts == want.ResourceCounts,
		nil
}

func (matcher *gitRepoStatusMatcher) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected\n\t%#v\nto match status\n\t%#v", actual, matcher.expected)
}

func (matcher *gitRepoStatusMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected\n\t%#v\nnot to match status\n\t%#v", actual, matcher.expected)
}
