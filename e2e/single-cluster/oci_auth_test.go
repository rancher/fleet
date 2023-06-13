package singlecluster_test

import (
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"helm.sh/helm/v3/pkg/registry"

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

		var wgGit, wgHelm sync.WaitGroup
		wgGit.Add(1)
		wgHelm.Add(1)

		spinUpGitServer := func() {
			defer wgGit.Done()

			out, err := k.Apply("-f", testenv.AssetPath("gitrepo/nginx_deployment.yaml"))
			Expect(err).ToNot(HaveOccurred(), out)

			out, err = k.Apply("-f", testenv.AssetPath("gitrepo/nginx_service.yaml"))
			Expect(err).ToNot(HaveOccurred(), out)

			time.Sleep(5 * time.Second)
		}

		spinUpHelmRegistry := func() {
			defer wgHelm.Done()

			out, err := k.Create(
				"secret", "tls", "zot-tls",
				"--cert", path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "zot.crt"),
				"--key", path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "zot.key"),
			)
			Expect(err).ToNot(HaveOccurred(), out)

			out, err = k.Apply("-f", testenv.AssetPath("oci/zot_secret.yaml"))
			Expect(err).ToNot(HaveOccurred(), out)

			out, err = k.Apply("-f", testenv.AssetPath("oci/zot_configmap.yaml"))
			Expect(err).ToNot(HaveOccurred(), out)

			out, err = k.Apply("-f", testenv.AssetPath("oci/zot_deployment.yaml"))
			Expect(err).ToNot(HaveOccurred(), out)

			out, err = k.Apply("-f", testenv.AssetPath("oci/zot_service.yaml"))
			Expect(err).ToNot(HaveOccurred(), out)

			time.Sleep(5 * time.Second)
		}

		go spinUpGitServer()
		go spinUpHelmRegistry()

		wgHelm.Wait()

		// Login and push a Helm chart to our local Helm registry
		helmClient, err := registry.NewClient()
		Expect(err).ToNot(HaveOccurred())

		externalIP, err := k.Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
		Expect(err).ToNot(HaveOccurred())

		helmHost := fmt.Sprintf("%s:5000", externalIP)
		err = helmClient.Login(helmHost, registry.LoginOptBasicAuth("fleet-ci", "foo"))
		Expect(err).ToNot(HaveOccurred())

		chartArchive, err := os.ReadFile("../../sleeper-chart-0.1.0.tgz")
		Expect(err).ToNot(HaveOccurred())

		_, err = helmClient.Push(chartArchive, fmt.Sprintf("%s/sleeper-chart:0.1.0", helmHost))
		Expect(err).ToNot(HaveOccurred())

		wgGit.Wait()

		// Prepare git repo
		ip, err := githelper.GetExternalRepoIP(env, port, repoName)
		Expect(err).ToNot(HaveOccurred())
		Expect(ip).ToNot(HaveLen(0))

		gh = githelper.NewHTTP(ip)

		tmpdir, _ = os.MkdirTemp("", "fleet-")
		clonedir = path.Join(tmpdir, "clone")
		_, err = gh.Create(clonedir, testenv.AssetPath("oci/repo"), "helm-oci-with-auth")
		Expect(err).ToNot(HaveOccurred())

		out, err := k.Create(
			"secret", "generic", "helm-oci-secret",
			"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
			"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
			"--from-file=cacerts="+path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "root.crt"),
		)
		Expect(err).ToNot(HaveOccurred(), out)
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

	AfterEach(func() {
		_, _ = k.Delete("gitrepo", "helm")
		_, _ = k.Delete("deployment", "git-server")
		_, _ = k.Delete("service", "git-service")

		_, _ = k.Delete("configmap", "zot-config")
		_, _ = k.Delete("deployment", "zot")
		_, _ = k.Delete("service", "zot-service")
		_, _ = k.Delete("secret", "zot-tls")
	})

	When("creating a gitrepo resource", func() {
		Context("containing a private oci based helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm-oci-with-auth.yaml"
				k = env.Kubectl.Namespace(env.Namespace)
			})

			AfterEach(func() {
				k = env.Kubectl.Namespace(env.Namespace)

				out, err := k.Delete(
					"secret", "helm-oci-secret",
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-helm-oci-with-auth-example").Get("pods")
					return out
				}).Should(ContainSubstring("sleeper-"))
			})
		})
	})
})
