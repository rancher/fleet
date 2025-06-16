package singlecluster_test

import (
	"crypto/tls"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/infra/cmd"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/rancher/fleet/e2e/testenv/zothelper"

	"github.com/chartmuseum/helm-push/pkg/chartmuseum"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	helmOpsSecretName = "secret-helmops"
)

var _ = Describe("HelmOp resource tests with polling", Label("infra-setup", "helm-registry"), Ordered, func() {
	var (
		namespace    = "helmop-ns"
		name         = "basic"
		chartVersion string
		k            kubectl.Command
	)
	BeforeAll(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		out, err := k.Create(
			"secret", "generic", helmOpsSecretName,
			"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
			"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
		)
		if strings.Contains(out, "already exists") {
			err = nil
		}

		Expect(err).ToNot(HaveOccurred(), out)
	})

	JustBeforeEach(func() {
		namespace = testenv.NewNamespaceName(
			name,
			rand.New(rand.NewSource(time.Now().UnixNano())),
		)

		err := testenv.ApplyTemplate(k, testenv.AssetPath("helmop/helmop.yaml"), struct {
			Name                  string
			Namespace             string
			Repo                  string
			Chart                 string
			PollingInterval       time.Duration
			HelmSecretName        string
			InsecureSkipTLSVerify bool
			Version               string
		}{
			name,
			namespace,
			getChartMuseumExternalAddr(),
			"sleeper-chart",
			5 * time.Second,
			helmOpsSecretName,
			true,
			chartVersion,
		})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterAll(func() {
		out, err := k.Delete("helmop", name)
		Expect(err).ToNot(HaveOccurred(), out)
		out, err = k.Delete("secret", helmOpsSecretName)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("applying a helmop resource", func() {
		BeforeEach(func() {
			chartVersion = "0.1.0" // No polling
		})
		Context("containing a valid helmop description", func() {
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

	When("a new version of the referenced chart is available", func() {
		BeforeEach(func() {
			chartVersion = "< 1.0.0"
		})

		AfterEach(func() {
			addr, err := getExternalHelmAddr(k)
			Expect(err).ToNot(HaveOccurred())

			url := fmt.Sprintf("https://%s:8081/api/charts/sleeper-chart/0.2.0", addr)
			req, err := http.NewRequest(http.MethodDelete, url, nil)
			Expect(err).ToNot(HaveOccurred())

			req.SetBasicAuth(os.Getenv("CI_OCI_USERNAME"), os.Getenv("CI_OCI_PASSWORD"))

			tlsConf := &tls.Config{
				InsecureSkipVerify: true,
			}

			cli := http.Client{
				Transport: &http.Transport{
					TLSClientConfig: tlsConf,
				},
			}

			resp, err := cli.Do(req)
			Expect(err).ToNot(HaveOccurred())

			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("polls the registry and installs a newer version when available", func() {
			By("installing the latest available version when the bundle is first created")
			Eventually(func() bool {
				outPods, _ := k.Namespace(namespace).Get("pods")
				return strings.Contains(outPods, "sleeper-")
			}).Should(BeTrue())
			Eventually(func() bool {
				outDeployments, _ := k.Namespace(namespace).Get("deployments")
				return strings.Contains(outDeployments, "sleeper")
			}).Should(BeTrue())

			By("having a newer chart version available in the repository")
			cmd := exec.Command("helm", "package", testenv.AssetPath("helmop/sleeper-chart2/"))
			out, err := cmd.CombinedOutput()
			Expect(err).ToNot(HaveOccurred(), out)

			externalIP, err := getExternalHelmAddr(k)
			Expect(err).ToNot(HaveOccurred())

			c, err := chartmuseum.NewClient(
				chartmuseum.URL(fmt.Sprintf("https://%s:8081", externalIP)),
				chartmuseum.Username(os.Getenv("CI_OCI_USERNAME")),
				chartmuseum.Password(os.Getenv("CI_OCI_PASSWORD")),
				chartmuseum.InsecureSkipVerify(true),
			)
			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("failed to create chartmuseum client: %v", err))

			resp, err := c.UploadChartPackage("sleeper-chart-0.2.0.tgz", true)
			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("failed to push new chart: %v", err))

			defer resp.Body.Close()

			Expect(resp.StatusCode).
				To(Equal(http.StatusCreated), fmt.Sprintf("POST response status code from ChartMuseum: %d", resp.StatusCode))

			By("installing the newer chart version")
			Eventually(func() bool {
				outPods, _ := k.Namespace(namespace).Get("pods")
				return strings.Contains(outPods, "sleeper2-")
			}).Should(BeTrue())
			Eventually(func() bool {
				outDeployments, _ := k.Namespace(namespace).Get("deployments")
				return strings.Contains(outDeployments, "sleeper2")
			}).Should(BeTrue())
		})
	})
})

var _ = Describe("HelmOp resource tests with oci registry", Label("infra-setup", "oci-registry"), func() {
	var (
		namespace string
		name      string
		insecure  bool
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
		if strings.Contains(out, "already exists") {
			err = nil
		}
		Expect(err).ToNot(HaveOccurred(), out)

		ociRef, err := zothelper.GetOCIReference(k)
		Expect(err).ToNot(HaveOccurred(), ociRef)

		err = testenv.ApplyTemplate(k, testenv.AssetPath("helmop/helmop.yaml"), struct {
			Name                  string
			Namespace             string
			Repo                  string
			Chart                 string
			PollingInterval       time.Duration
			HelmSecretName        string
			InsecureSkipTLSVerify bool
			Version               string
		}{
			name,
			namespace,
			"",
			fmt.Sprintf("%s/sleeper-chart", ociRef),
			0,
			helmOpsSecretName,
			insecure,
			"0.1.0",
		})
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		out, err := k.Delete("helmop", name)
		Expect(err).ToNot(HaveOccurred(), out)
		out, err = k.Delete("secret", helmOpsSecretName)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("applying a helmop resource", func() {
		Context("containing a valid helmop description pointing to an oci registry and insecure TLS", func() {
			BeforeEach(func() {
				namespace = "helmop-ns"
				name = "basic-oci"
				insecure = true
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
		Context("containing a valid helmop description pointing to an oci registry and not TLS", func() {
			BeforeEach(func() {
				namespace = "helmop-ns2"
				name = "basic-oci-no-tls"
				insecure = false
			})
			It("does not deploy the chart because of TLS", func() {
				Consistently(func() string {
					out, _ := k.Namespace(namespace).Get("pods")
					return out
				}, 5*time.Second, time.Second).ShouldNot(ContainSubstring("sleeper-"))
			})
		})
	})
})

var _ = Describe("HelmOp resource tests with tarball source", Label("infra-setup", "helm-registry"), func() {
	var (
		namespace string
		name      string
		insecure  bool
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

		err = testenv.ApplyTemplate(k, testenv.AssetPath("helmop/helmop.yaml"), struct {
			Name                  string
			Namespace             string
			Repo                  string
			Chart                 string
			PollingInterval       time.Duration
			HelmSecretName        string
			InsecureSkipTLSVerify bool
			Version               string
		}{
			name,
			namespace,
			"",
			fmt.Sprintf("%s/charts/sleeper-chart-0.1.0.tgz", getChartMuseumExternalAddr()),
			0,
			helmOpsSecretName,
			insecure,
			"0.1.0",
		})
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		out, err := k.Delete("helmop", name)
		Expect(err).ToNot(HaveOccurred(), out)
		out, err = k.Delete("secret", helmOpsSecretName)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("applying a helmop resource", func() {
		BeforeEach(func() {
			namespace = "helmop-ns"
			name = "basic-oci"
			insecure = true
		})
		It("deploys the chart", func() {
			Eventually(func() bool {
				outDeployments, _ := k.Namespace(namespace).Get("pods")
				return strings.Contains(outDeployments, "sleeper-")
			}).Should(BeTrue())
			Eventually(func() bool {
				outDeployments, _ := k.Namespace(namespace).Get("deployments")
				return strings.Contains(outDeployments, "sleeper")
			}).Should(BeTrue())
		})
	})
})

// getExternalHelmAddr retrieves the external URL where our local Helm registry can be reached.
func getExternalHelmAddr(k kubectl.Command) (string, error) {
	if v := os.Getenv("external_ip"); v != "" {
		return v, nil
	}

	return k.Namespace(cmd.InfraNamespace).Get("service", "chartmuseum-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
}
