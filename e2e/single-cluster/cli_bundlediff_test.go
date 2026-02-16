package singlecluster_test

import (
	"fmt"
	"math/rand"
	"os"
	"path"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	"github.com/go-git/go-git/v5"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/githelper"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/rancher/fleet/internal/cmd/cli"
	"sigs.k8s.io/yaml"
)

var _ = Describe("Fleet bundlediff CLI", func() {
	When("a bundle deployment has modified resources", func() {
		var (
			k                    kubectl.Command
			bundleName           string
			targetNs             string
			bundleDeploymentName string
			bdNamespace          string
		)

		BeforeEach(func() {
			r := rand.New(rand.NewSource(GinkgoRandomSeed()))
			k = env.Kubectl.Namespace(env.Namespace)
			bundleName = testenv.RandomFilename("test-bundlediff", r)
			targetNs = testenv.RandomFilename("bundlediff-target", r)

			out, err := k.Create("ns", targetNs)
			Expect(err).ToNot(HaveOccurred(), out)

			// Deploy a Bundle with inline content
			err = testenv.ApplyTemplate(k, testenv.AssetPath("single-cluster/bundle-test-modified.yaml"),
				map[string]string{
					"Name":            bundleName,
					"Namespace":       env.Namespace,
					"TargetNamespace": targetNs,
				})
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				out, err := k.Get("bundle", bundleName, "-o", "jsonpath={.status.display.readyClusters}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(Equal("1/1"))
			}, testenv.Timeout).Should(Succeed())

			Eventually(func(g Gomega) {
				out, err := k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/bundle-name="+bundleName, "-o", "jsonpath={.items[0]['metadata.name','metadata.namespace']}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).ToNot(BeEmpty())

				data := strings.Split(out, " ")
				g.Expect(data).To(HaveLen(2))
				bundleDeploymentName, bdNamespace = data[0], data[1]
			}, testenv.Timeout).Should(Succeed())

			Eventually(func(g Gomega) {
				_, err := k.Namespace(targetNs).Get("configmap", "test-bundlediff-cm")
				g.Expect(err).ToNot(HaveOccurred())
			}, testenv.Timeout).Should(Succeed())

			_, err = k.Namespace(targetNs).Run(
				"patch", "configmap", "test-bundlediff-cm",
				"--type=merge", "-p", `{"data":{"key":"modified-value"}}`,
			)
			Expect(err).ToNot(HaveOccurred())

			// Wait for the Bundle to show as modified
			Eventually(func(g Gomega) {
				out, err := k.Get("bundle", bundleName, "-o", "jsonpath={.status.display.state}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).To(Equal("Modified"))
			}, testenv.Timeout).Should(Succeed())
		})

		AfterEach(func() {
			_, _ = k.Delete("bundle", bundleName)
			_, _ = k.Delete("ns", targetNs, "--wait=false")
		})

		It("generates comparePatches that can be added to fix drift (Bundle-based test)", func() {
			By("generating fleet.yaml diff snippet from the modified deployment")
			cmd := cli.NewBundleDiff()
			cmd.SetArgs([]string{
				"--bundle-deployment", bundleDeploymentName,
				"--fleet-yaml",
				"-n", bdNamespace,
			})

			buf := gbytes.NewBuffer()
			errBuf := gbytes.NewBuffer()
			cmd.SetOut(buf)
			cmd.SetErr(errBuf)

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred(), string(errBuf.Contents()))

			diffSnippet := string(buf.Contents())
			actualOutput := strings.TrimSpace(diffSnippet)
			Expect(actualOutput).To(ContainSubstring("diff:"))
			Expect(actualOutput).To(ContainSubstring("comparePatches:"))
			Expect(actualOutput).To(ContainSubstring("apiVersion: v1"))
			Expect(actualOutput).To(ContainSubstring("kind: ConfigMap"))
			Expect(actualOutput).To(ContainSubstring("name: test-bundlediff-cm"))
			Expect(actualOutput).To(ContainSubstring("operations:"))
			Expect(actualOutput).To(SatisfyAny(
				ContainSubstring("op: remove"),
				ContainSubstring("op: replace"),
			))
			Expect(actualOutput).To(ContainSubstring("path: /data/key"))
			Expect(actualOutput).To(SatisfyAny(
				ContainSubstring("namespace: "+targetNs),
				Not(ContainSubstring("namespace:")),
			))

			By("verifying comparePatches hide the drift when applied to Bundle")
			jsonBytes, err := yaml.YAMLToJSON([]byte(diffSnippet))
			Expect(err).ToNot(HaveOccurred())
			patchJSON := fmt.Sprintf(`{"spec":{"options":%s}}`, string(jsonBytes))
			_, err = k.Namespace(bdNamespace).Run("patch", "bundledeployment", bundleDeploymentName, "--type=merge", "-p", patchJSON)
			Expect(err).ToNot(HaveOccurred())

			forceSyncJSON := fmt.Sprintf(`{"spec":{"options":{"forceSyncGeneration":%d}}}`, time.Now().UnixNano())
			_, err = k.Namespace(bdNamespace).Run("patch", "bundledeployment", bundleDeploymentName, "--type=merge", "-p", forceSyncJSON)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				out, err := k.Namespace(bdNamespace).Get("bundledeployment", bundleDeploymentName, "-o", "jsonpath={.status.display.state}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).ToNot(Equal("Modified"))
			}, testenv.Timeout).Should(Succeed())

			By("verifying the ConfigMap still contains the modified value")
			out, err := k.Namespace(targetNs).Get("configmap", "test-bundlediff-cm", "-o", "jsonpath={.data.key}")
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal("modified-value"), "ComparePatches should hide drift, not revert it")
		})
	})
})

var _ = Describe("Fleet bundlediff CLI GitOps workflow", Label("gitrepo", "gitserver", "infra-setup"), func() {
	var (
		k           kubectl.Command
		gh          *githelper.Git
		tmpdir      string
		gitRepoName string
		repoPath    string
		bundleName  string
		targetNs    string
		clone       *git.Repository
	)

	BeforeEach(func() {
		r := rand.New(rand.NewSource(GinkgoRandomSeed() + 1000))
		k = env.Kubectl.Namespace(env.Namespace)
		gitRepoName = testenv.RandomFilename("bundlediff-gitops", r)
		repoPath = "repo" // Use fixed repo path
		bundleName = gitRepoName + "-" + repoPath
		targetNs = testenv.RandomFilename("bundlediff-target", r)

		out, err := k.Create("ns", targetNs)
		Expect(err).ToNot(HaveOccurred(), out)

		host := githelper.BuildGitHostname()
		addr, err := githelper.GetExternalRepoAddr(env, 4343, repoPath)
		Expect(err).ToNot(HaveOccurred())
		addr = strings.Replace(addr, "http://", "https://", 1)
		gh = githelper.NewHTTP(addr)

		tmpdir, _ = os.MkdirTemp("", "fleet-")
		clonedir := path.Join(tmpdir, "test-repo")

		clone, err = gh.Create(clonedir, testenv.AssetPath("gitrepo/sleeper-chart"), repoPath)
		Expect(err).ToNot(HaveOccurred())

		fleetYAMLPath := path.Join(clonedir, repoPath, "fleet.yaml")
		err = testenv.Template(fleetYAMLPath, testenv.AssetPath("single-cluster/bundlediff-fleet.yaml"),
			map[string]string{"TargetNamespace": targetNs})
		Expect(err).ToNot(HaveOccurred())

		templatesDir := path.Join(clonedir, repoPath, "templates")
		err = os.MkdirAll(templatesDir, 0755)
		Expect(err).ToNot(HaveOccurred())
		cmPath := path.Join(templatesDir, "bundlediff-gitops-cm.yaml")
		cmContent := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: bundlediff-gitops-cm\ndata:\n  key: original-value\n"
		err = os.WriteFile(cmPath, []byte(cmContent), 0644)
		Expect(err).ToNot(HaveOccurred())

		_, err = gh.Update(clone)
		Expect(err).ToNot(HaveOccurred())

		inClusterRepoURL := gh.GetInClusterURL(host, 4343, repoPath)

		err = testenv.ApplyTemplate(k, testenv.AssetPath("single-cluster/gitrepo-bundlediff.yaml"),
			map[string]string{
				"Name":      gitRepoName,
				"Namespace": env.Namespace,
				"Repo":      inClusterRepoURL,
				"Branch":    "master",
				"Path":      repoPath,
			})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			out, err := k.Context("").Get("bundles", "-A", "-l",
				"fleet.cattle.io/repo-name="+gitRepoName,
				"-o", "jsonpath={.items[0].metadata.name}")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(out).ToNot(BeEmpty())
			bundleName = out
		}, testenv.Timeout).Should(Succeed())

		Eventually(func(g Gomega) {
			out, err := k.Get("bundle", bundleName, "-o", "jsonpath={.status.display.readyClusters}")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(out).To(Equal("1/1"))
		}, testenv.Timeout).Should(Succeed())

		Eventually(func(g Gomega) {
			_, err := k.Namespace(targetNs).Get("configmap", "bundlediff-gitops-cm")
			g.Expect(err).ToNot(HaveOccurred())
		}, testenv.Timeout).Should(Succeed())
	})

	AfterEach(func() {
		if tmpdir != "" {
			_ = os.RemoveAll(tmpdir)
		}
		_, _ = k.Delete("gitrepo", gitRepoName)
		_, _ = k.Delete("bundle", bundleName)
		_, _ = k.Delete("ns", targetNs, "--wait=false")
	})

	It("detects drift, generates fleet.yaml snippet, and resolves drift via Git commit", func() {
		By("modifying the deployed configmap to trigger drift")
		_, err := k.Namespace(targetNs).Run(
			"patch", "configmap", "bundlediff-gitops-cm",
			"--type=merge", "-p", `{"data":{"key":"modified-value"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			out, err := k.Get("bundle", bundleName, "-o", "jsonpath={.status.display.state}")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(out).To(Equal("Modified"))
		}, testenv.Timeout).Should(Succeed())

		By("finding the BundleDeployment")
		var bundleDeploymentName, bdNamespace string
		Eventually(func(g Gomega) {
			out, err := k.Get("bundledeployments", "-A", "-l",
				"fleet.cattle.io/repo-name="+gitRepoName,
				"-o", "jsonpath={.items[0]['metadata.name','metadata.namespace']}")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(out).ToNot(BeEmpty())

			data := strings.Split(out, " ")
			g.Expect(data).To(HaveLen(2))
			bundleDeploymentName, bdNamespace = data[0], data[1]
		}, testenv.Timeout).Should(Succeed())

		By("generating fleet.yaml diff snippet using bundlediff CLI")
		cmd := cli.NewBundleDiff()
		cmd.SetArgs([]string{
			"--bundle-deployment", bundleDeploymentName,
			"--fleet-yaml",
			"-n", bdNamespace,
		})

		buf := gbytes.NewBuffer()
		errBuf := gbytes.NewBuffer()
		cmd.SetOut(buf)
		cmd.SetErr(errBuf)

		err = cmd.Execute()
		Expect(err).ToNot(HaveOccurred(), string(errBuf.Contents()))

		diffSnippet := string(buf.Contents())
		Expect(diffSnippet).To(ContainSubstring("diff:"))
		Expect(diffSnippet).To(ContainSubstring("comparePatches"))
		Expect(diffSnippet).To(ContainSubstring("bundlediff-gitops-cm"))

		By("updating fleet.yaml in Git with the diff snippet")
		clonedir := path.Join(tmpdir, "test-repo")
		fleetYAMLPath := path.Join(clonedir, repoPath, "fleet.yaml")

		existingContent, err := os.ReadFile(fleetYAMLPath)
		Expect(err).ToNot(HaveOccurred())

		updatedContent := string(existingContent) + "\n" + diffSnippet
		err = os.WriteFile(fleetYAMLPath, []byte(updatedContent), 0644)
		Expect(err).ToNot(HaveOccurred())

		_, err = gh.Update(clone)
		Expect(err).ToNot(HaveOccurred())

		By("verifying the comparePatches are applied to the BundleDeployment")
		Eventually(func(g Gomega) {
			out, err := k.Namespace(bdNamespace).Get("bundledeployment", bundleDeploymentName, "-o", "jsonpath={.spec.options.diff.comparePatches[0].operations[0].op}")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(out).ToNot(BeEmpty())
		}, testenv.Timeout).Should(Succeed())

		forceSyncJSON := fmt.Sprintf(`{"spec":{"options":{"forceSyncGeneration":%d}}}`, time.Now().UnixNano())
		_, err = k.Namespace(bdNamespace).Run("patch", "bundledeployment", bundleDeploymentName, "--type=merge", "-p", forceSyncJSON)
		Expect(err).ToNot(HaveOccurred())

		By("verifying the Bundle is no longer in Modified state after GitOps sync")
		Eventually(func(g Gomega) {
			out, err := k.Namespace(bdNamespace).Get("bundledeployment", bundleDeploymentName, "-o", "jsonpath={.status.display.state}")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(out).ToNot(Equal("Modified"))
		}, testenv.Timeout).Should(Succeed())

		By("verifying the ConfigMap contains the modified value (not reverted)")
		out, err := k.Namespace(targetNs).Get("configmap", "bundlediff-gitops-cm", "-o", "jsonpath={.data.key}")
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal("modified-value"), "ComparePatches accept drift, don't revert it")
	})
})
