package singlecluster_test

import (
	"os"
	"path"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Single Cluster Examples", func() {
	var (
		asset    string
		tmpdir   string
		clonedir string
		k        kubectl.Command
		gh       *githelper.Git
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)

		// Prepare git repo
		addr, err := githelper.GetExternalRepoAddr(env, port, caseName)
		Expect(err).ToNot(HaveOccurred())
		Expect(addr).ToNot(HaveLen(0))

		gh = githelper.NewHTTP(addr)

		tmpdir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpdir, "clone")
		_, err = gh.Create(clonedir, testenv.AssetPath("oci/repo"), "helm-oci-with-auth")
		Expect(err).ToNot(HaveOccurred())
	})

	JustBeforeEach(func() {
		// Build git repo URL reachable _within_ the cluster, for the GitRepo
		host, err := githelper.BuildGitHostname(env.Namespace)
		Expect(err).ToNot(HaveOccurred())

		inClusterRepoURL := gh.GetInClusterURL(host, port, repoName)

		gitrepo := path.Join(tmpdir, "gitrepo.yaml")
		err = testenv.Template(gitrepo, testenv.AssetPath(asset), struct {
			Repo string
		}{
			inClusterRepoURL,
		})
		Expect(err).ToNot(HaveOccurred())

		out, err := k.Apply("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("creating a gitrepo resource", func() {
		Context("containing a private oci based helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm-oci-with-auth.yaml"
				k = env.Kubectl.Namespace(env.Namespace)
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-helm-oci-with-auth-example").Get("pods", "--field-selector=status.phase==Running")
					return out
				}).Should(ContainSubstring("sleeper-"))
			})
		})
	})

	AfterEach(func() {
		_, _ = k.Delete("gitrepo", "helm")
	})
})
