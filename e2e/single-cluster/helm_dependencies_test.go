package singlecluster_test

import (
	"fmt"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/infra/cmd"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	cp "github.com/otiai10/copy"
)

func getChartMuseumExternalAddr() string {
	username := os.Getenv("GIT_HTTP_USER")
	passwd := os.Getenv("GIT_HTTP_PASSWORD")
	Expect(username).ToNot(Equal(""))
	Expect(passwd).ToNot(Equal(""))
	return fmt.Sprintf("https://%s:%s@chartmuseum-service.%s.svc.cluster.local:8081", username, passwd, cmd.InfraNamespace)
}

func setupChartDepsInTmpDir(chartDir string, tmpDir string, namespace string, disableDependencyUpdate bool) {
	err := cp.Copy(chartDir, tmpDir)
	Expect(err).ToNot(HaveOccurred())
	// replace the helm repo url
	helmRepoUrl := getChartMuseumExternalAddr()
	out := filepath.Join(tmpDir, "Chart.yaml")
	in := filepath.Join(chartDir, "Chart.yaml")
	err = testenv.Template(out, in, struct {
		HelmRepoUrl string
	}{
		helmRepoUrl,
	})
	Expect(err).ToNot(HaveOccurred())

	if _, err = os.Stat(filepath.Join(chartDir, "fleet.yaml")); !os.IsNotExist(err) {
		out = filepath.Join(tmpDir, "fleet.yaml")
		in = filepath.Join(chartDir, "fleet.yaml")
		err = testenv.Template(out, in, struct {
			TestNamespace           string
			DisableDependencyUpdate bool
		}{
			namespace,
			disableDependencyUpdate,
		})
		Expect(err).ToNot(HaveOccurred())
	}
}

var _ = Describe("Helm dependency update tests", Label("infra-setup", "helm-registry"), func() {
	var (
		asset                   string
		k                       kubectl.Command
		gh                      *githelper.Git
		clonedir                string
		inClusterRepoURL        string
		tmpDir                  string
		gitrepoName             string
		r                       = rand.New(rand.NewSource(GinkgoRandomSeed()))
		namespace               string
		disableDependencyUpdate bool
	)

	JustBeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host := githelper.BuildGitHostname()

		addr, err := githelper.GetExternalRepoAddr(env, port, repoName)
		Expect(err).ToNot(HaveOccurred())
		gh = githelper.NewHTTP(addr)

		inClusterRepoURL = gh.GetInClusterURL(host, port, repoName)

		tmpDir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpDir, repoName)

		gitrepoName = testenv.RandomFilename("gitrepo-test", r)

		// setup the tmp chart dir.
		// we use a tmp dir because dependencies are downloaded to the directory
		tmpChart := GinkgoT().TempDir()
		setupChartDepsInTmpDir(testenv.AssetPath(asset), tmpChart, namespace, disableDependencyUpdate)

		_, err = gh.Create(clonedir, tmpChart, "examples")
		Expect(err).ToNot(HaveOccurred())

		err = testenv.ApplyTemplate(k, testenv.AssetPath("deps-charts/gitrepo.yaml"), struct {
			Name            string
			Repo            string
			Branch          string
			TargetNamespace string
		}{
			gitrepoName,
			inClusterRepoURL,
			gh.Branch,
			namespace, // to avoid conflicts with other tests
		})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		out, err := k.Delete("gitrepo", gitrepoName)
		Expect(err).ToNot(HaveOccurred(), out)
		out, err = k.Delete("ns", namespace)
		Expect(err).ToNot(HaveOccurred(), out)
		os.RemoveAll(tmpDir)
	})

	When("applying a gitrepo resource", func() {
		Context("containing a helm chart with dependencies and no fleet.yaml", func() {
			BeforeEach(func() {
				namespace = "no-fleet-yaml"
				asset = "deps-charts/" + namespace
				disableDependencyUpdate = false
			})
			It("deploys the chart plus its dependencies", func() {
				Eventually(func() bool {
					outConfigMaps, _ := k.Namespace(namespace).Get("configmaps")
					outPods, _ := k.Namespace(namespace).Get("pods")
					return strings.Contains(outConfigMaps, "test-simple-deps-chart") && strings.Contains(outPods, "sleeper-")
				}).Should(BeTrue())
			})
		})
		Context("containing a helm chart with dependencies and fleet.yaml with disableDependencyUpdate=false", func() {
			BeforeEach(func() {
				namespace = "with-fleet-yaml"
				asset = "deps-charts/" + namespace
				disableDependencyUpdate = false
			})
			It("deploys the chart plus its dependencies", func() {
				Eventually(func() bool {
					outConfigMaps, _ := k.Namespace(namespace).Get("configmaps")
					outPods, _ := k.Namespace(namespace).Get("pods")
					return strings.Contains(outConfigMaps, "test-simple-deps-chart") && strings.Contains(outPods, "sleeper-")
				}).Should(BeTrue())
			})
		})
		Context("containing a helm chart with dependencies and fleet.yaml with disableDependencyUpdate=true", func() {
			BeforeEach(func() {
				namespace = "with-fleet-yaml"
				asset = "deps-charts/" + namespace
				disableDependencyUpdate = true
			})
			It("deploys the chart, but not its dependencies", func() {
				Eventually(func() bool {
					outConfigMaps, _ := k.Namespace(namespace).Get("configmaps")
					outPods, _ := k.Namespace(namespace).Get("pods")
					return strings.Contains(outConfigMaps, "test-simple-deps-chart") && !strings.Contains(outPods, "sleeper-")
				}).Should(BeTrue())
			})
		})
	})
})
