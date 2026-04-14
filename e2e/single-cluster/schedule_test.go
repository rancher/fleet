package singlecluster_test

import (
	"fmt"
	"math/rand"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Schedules", Label("infra-setup"), func() {
	var (
		scheduleAsset string
		k             kubectl.Command
		env           *testenv.Env
		cluster       *fleet.Cluster
		namespace     string

		tmpDir           string
		gh               *githelper.Git
		clone            *git.Repository
		clonedir         string
		inClusterRepoURL string
		gitrepoName      string
		r                = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace  = testenv.NewNamespaceName("target", r)
	)

	BeforeEach(func() {
		env = testenv.New()
		k = env.Kubectl.Namespace(env.Namespace)
		scheduleAsset = "schedules/local-schedule.yaml"

		// Get the downstream cluster
		var err error
		out, err := k.Get("clusters", "-o", "json")
		Expect(err).ToNot(HaveOccurred())

		clusterList := &fleet.ClusterList{}
		err = env.Unmarshal(out, clusterList)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterList.Items).To(HaveLen(1))
		cluster = &clusterList.Items[0]
		namespace = cluster.Status.Namespace

		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host := githelper.BuildGitHostname()

		addr, err := githelper.GetExternalRepoAddr(env, 4343, "repo")
		Expect(err).ToNot(HaveOccurred())
		addr = strings.Replace(addr, "http://", "https://", 1)

		gh = githelper.NewHTTP(addr)

		inClusterRepoURL = gh.GetInClusterURL(host, 4343, "repo")

		tmpDir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpDir, "test-repo")

		gitrepoName = testenv.RandomFilename("gitjob-test", r)

		// wait for the beginning of the next minute so we are sure
		// that the cycle checked in the tests is correct.
		// Note that the schedule starts at 0 seconds.
		waitForBeginningOfNextMinute()
	})

	It("deploys and pauses based on the schedule", func() {
		err := testenv.ApplyTemplate(k, testenv.AssetPath(scheduleAsset), struct {
			Schedule string
			Duration string
		}{
			"0 */1 * * * *",
			"30s",
		})
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			_, _ = k.Namespace("fleet-local").Delete("schedule", "schedule1")
		})

		By("checking for the cluster to be scheduled")
		Eventually(func() bool {
			cluster, err = env.GetCluster(cluster.Name, cluster.Namespace)
			if err != nil {
				return false
			}
			return cluster.Status.Scheduled
		}).Should(BeTrue())

		// Wait for the schedule to become active.
		// The schedule is set to run every minute for 30 seconds.
		// We wait for the cluster to have ActiveSchedule=true.
		By("waiting for the schedule to be active")
		Eventually(func() bool {
			cluster, err = env.GetCluster(cluster.Name, cluster.Namespace)
			if err != nil {
				return false
			}
			return cluster.Status.ActiveSchedule
		}).Should(BeTrue())

		// Apply a GitRepo
		err = testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo.yaml"), gitRepoTestValues{
			Name:            gitrepoName,
			Repo:            inClusterRepoURL,
			Branch:          gh.Branch,
			PollingInterval: "15s",           // default
			TargetNamespace: targetNamespace, // to avoid conflicts with other tests
		})
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			_, _ = k.Namespace("fleet-local").Delete("gitrepo", gitrepoName)
		})

		clone, err = gh.Create(clonedir, testenv.AssetPath("gitrepo/sleeper-chart"), "examples")
		Expect(err).ToNot(HaveOccurred())

		// When the schedule is active, the bundledeployment should be created and not paused.
		By("checking the bundle deployment is active")
		var bd fleet.BundleDeployment
		Eventually(func(g Gomega) {
			label := fmt.Sprintf("fleet.cattle.io/bundle-name=%s-examples", gitrepoName)
			out, err := k.Namespace(namespace).Get("bundledeployments", "-l", label, "-o", "json")
			g.Expect(err).ToNot(HaveOccurred())

			bdList := &fleet.BundleDeploymentList{}
			err = env.Unmarshal(out, bdList)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(bdList.Items).To(HaveLen(1))

			bd = bdList.Items[0]
			g.Expect(bd.Spec.OffSchedule).To(BeFalse())
		}).Should(Succeed())

		By("checking the pod exists")
		Eventually(func() string {
			out, _ := k.Namespace(targetNamespace).Get("pods")
			return out
		}).Should(ContainSubstring("sleeper-"))

		// Wait for the schedule's duration to pass.
		// We wait for the cluster to have ActiveSchedule=false.
		By("waiting for the schedule to be inactive")
		Eventually(func() bool {
			cluster, err = env.GetCluster(cluster.Name, cluster.Namespace)
			if err != nil {
				return false
			}
			return !cluster.Status.ActiveSchedule
		}).Should(BeTrue())

		// When the schedule is inactive, the bundledeployment should be marked as OffSchedule.
		By("checking the bundle deployment is paused")
		Eventually(func(g Gomega) {
			label := fmt.Sprintf("fleet.cattle.io/bundle-name=%s-examples", gitrepoName)
			out, err := k.Namespace(namespace).Get("bundledeployments", "-l", label, "-o", "json")
			g.Expect(err).ToNot(HaveOccurred())

			bdList := &fleet.BundleDeploymentList{}
			err = env.Unmarshal(out, bdList)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(bdList.Items).To(HaveLen(1))

			bd = bdList.Items[0]
			g.Expect(bd.Spec.OffSchedule).To(BeTrue())
		}).Should(Succeed())

		By("updating the git repository")
		replace(path.Join(clonedir, "examples", "Chart.yaml"), "0.1.0", "0.2.0")
		replace(path.Join(clonedir, "examples", "templates", "deployment.yaml"), "name: sleeper", "name: newsleep")

		commit, err := gh.Update(clone)
		Expect(err).ToNot(HaveOccurred())

		By("updating the gitrepo's status")
		Eventually(func(g Gomega) {
			status := getGitRepoStatus(g, k, gitrepoName)
			g.Expect(status.Commit).To(Equal(commit))
		}, testenv.MediumTimeout, testenv.ShortTimeout).Should(Succeed())

		By("verifying the deployment is not updated while the schedule is inactive")
		Eventually(func(g Gomega) {
			label := fmt.Sprintf("fleet.cattle.io/bundle-name=%s-examples", gitrepoName)
			out, err := k.Namespace(namespace).Get("bundledeployments", "-l", label, "-o", "json")
			g.Expect(err).ToNot(HaveOccurred())

			bdList := &fleet.BundleDeploymentList{}
			err = env.Unmarshal(out, bdList)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(bdList.Items).To(HaveLen(1))

			bd = bdList.Items[0]
			g.Expect(bd.Spec.OffSchedule).To(BeTrue())

			cluster, err = env.GetCluster(cluster.Name, cluster.Namespace)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cluster.Status.ActiveSchedule).To(BeFalse())

			out, _ = k.Namespace(targetNamespace).Get("deployments")
			g.Expect(out).ToNot(ContainSubstring("newsleep"))
			g.Expect(out).To(ContainSubstring("sleep"))
		}).Should(Succeed())

		By("verifying the deployment is updated when the schedule becomes active again")
		Eventually(func(g Gomega) {
			label := fmt.Sprintf("fleet.cattle.io/bundle-name=%s-examples", gitrepoName)
			out, err := k.Namespace(namespace).Get("bundledeployments", "-l", label, "-o", "json")
			g.Expect(err).ToNot(HaveOccurred())

			bdList := &fleet.BundleDeploymentList{}
			err = env.Unmarshal(out, bdList)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(bdList.Items).To(HaveLen(1))

			bd = bdList.Items[0]
			g.Expect(bd.Spec.OffSchedule).To(BeFalse())

			cluster, err = env.GetCluster(cluster.Name, cluster.Namespace)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cluster.Status.ActiveSchedule).To(BeTrue())

			out, _ = k.Namespace(targetNamespace).Get("deployments")
			g.Expect(out).To(ContainSubstring("newsleep"))
		}).Should(Succeed())
	})
})

func waitForBeginningOfNextMinute() {
	for {
		now := time.Now()
		if now.Second() == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
}
