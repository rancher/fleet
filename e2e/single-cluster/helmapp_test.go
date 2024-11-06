package singlecluster_test

import (
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	helmOpsSecretName = "secret-helmops"
)

var _ = Describe("HelmApp resource tests", Label("infra-setup", "helm-registry"), func() {
	var (
		namespace string
		name      string
		k         kubectl.Command
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
	})

	JustBeforeEach(func() {
		namespace = testenv.NewNamespaceName(
			name,
			rand.New(rand.NewSource(time.Now().UnixNano())),
		)

		out, err := k.Create(
			"secret", "generic", helmOpsSecretName,
			"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
			"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
		)
		Expect(err).ToNot(HaveOccurred(), out)

		err = testenv.ApplyTemplate(k, testenv.AssetPath("helmapp/helmapp.yaml"), struct {
			Name           string
			Namespace      string
			Repo           string
			Chart          string
			HelmSecretName string
		}{
			name,
			namespace,
			getChartMuseumExternalAddr(env),
			"sleeper-chart",
			helmOpsSecretName,
		})
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		out, err := k.Delete("helmapp", name)
		Expect(err).ToNot(HaveOccurred(), out)
		out, err = k.Delete("secret", helmOpsSecretName)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("applying a helmapp resource", func() {
		Context("containing a valid helmapp description", func() {
			BeforeEach(func() {
				namespace = "helmapp-ns"
				name = "basic"
			})
			It("deploys the chart", func() {
				Eventually(func() bool {
					outPods, _ := k.Namespace(namespace).Get("pods")
					return strings.Contains(outPods, "sleeper-")
				}).Should(BeTrue())
				Eventually(func() bool {
					outDeployments, _ := k.Namespace(namespace).Get("deployments")
					return strings.Contains(outDeployments, "sleeper")
				}).Should(BeTrue())
			})
		})
	})
})
