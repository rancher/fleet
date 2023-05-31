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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"helm.sh/helm/v3/pkg/registry"
)

// These tests use the examples from https://github.com/rancher/fleet-examples/tree/master/single-cluster
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

		//
		// 1. Deploy git server
		// 2. Deploy Helm registry (apply config map + secret, then depl + service) -> do this from CI/script?
		// 3. Push data taken from https://github.com/rancher/fleet-test-data (path: helm-oci-with-auth) to the repo
		// 4. Deploy GitRepo pointing to served repo (helm-oci-with-auth.yaml)
		// 5. Check that connection to registry happens (does root cert need to be installed somewhere in
		// cluster, ie for Fleet to use and log into the Helm registry?)
		var wg sync.WaitGroup

		spinUpGitServer := func() {
			wg.Add(1)
			defer wg.Done()

			out, err := k.Apply("-f", testenv.AssetPath("gitrepo/nginx_deployment.yaml"))
			Expect(err).ToNot(HaveOccurred(), out)

			out, err = k.Apply("-f", testenv.AssetPath("gitrepo/nginx_service.yaml"))
			Expect(err).ToNot(HaveOccurred(), out)

			time.Sleep(5 * time.Second)
		}

		//spinUpHelmRegistry := func() {
		//wg.Add(1)
		//defer wg.Done()

		out, err := k.Apply("-f", testenv.AssetPath("oci/zot_secret.yaml"))
		Expect(err).ToNot(HaveOccurred(), out)

		// XXX: ensure certs have been generated: can this be taken care of here?
		out, err = k.Apply("-f", testenv.AssetPath("oci/zot_configmap.yaml"))
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Apply("-f", testenv.AssetPath("oci/zot_deployment.yaml"))
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Apply("-f", testenv.AssetPath("oci/zot_service.yaml"))
		Expect(err).ToNot(HaveOccurred(), out)

		time.Sleep(5 * time.Second)
		//}

		go spinUpGitServer()
		//go spinUpHelmRegistry()

		// TODO log into repo and push chart
		helmClient, err := registry.NewClient()
		Expect(err).ToNot(HaveOccurred())

		externalIP, err := k.Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
		Expect(err).ToNot(HaveOccurred())

		helmHost := fmt.Sprintf("%s:5000", externalIP)
		err = helmClient.Login(helmHost, registry.LoginOptBasicAuth("fleet-ci", "foo"))
		Expect(err).ToNot(HaveOccurred())

		chartArchive, err := os.ReadFile("../../sleeper-chart-0.1.0.tgz")
		Expect(err).ToNot(HaveOccurred())

		result, err := helmClient.Push(chartArchive, path.Join(helmHost, "sleeper-chart"), registry.PushOptStrictMode(false))
		Expect(err).ToNot(HaveOccurred())

		fmt.Println(result) // DEBUG

		wg.Wait()

		// Prepare git repo
		ip, err := githelper.GetExternalRepoIP(env, port, repoName)
		Expect(err).ToNot(HaveOccurred())
		gh = githelper.New(ip, false)

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

	AfterEach(func() {
		_, _ = k.Delete("gitrepo", "helm")
		_, _ = k.Delete("deployment", "git-server")
		_, _ = k.Delete("service", "git-service")

		_, _ = k.Delete("configmap", "zot-config")
		_, _ = k.Delete("deployment", "zot")
		_, _ = k.Delete("service", "zot-service")

		// Let's not delete the secret, which prevents us from having to regenerate it and install the root cert
		// on the host for each test run
	})

	When("creating a gitrepo resource", func() {
		Context("containing a private oci based helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm-oci-with-auth.yaml"
				k = env.Kubectl.Namespace(env.Namespace)

				out, err := k.Create(
					"secret", "generic", "helm-oci-secret",
					//"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
					"--from-literal=username=fleet-ci",
					//"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
					"--from-literal=password=foo",
					"--from-file=cacerts=../../FleetCI-RootCA/FleetCI-RootCA.crt",
				)
				Expect(err).ToNot(HaveOccurred(), out)
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
