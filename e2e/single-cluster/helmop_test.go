package singlecluster_test

import (
	"crypto/tls"
	"encoding/json"
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
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"helm.sh/helm/v4/pkg/registry"

	"github.com/chartmuseum/helm-push/pkg/chartmuseum"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	helmOpsSecretName = "secret-helmops"
)

var _ = Describe("HelmOp resource with polling of repo index", Label("infra-setup", "helm-registry"), Ordered, func() {
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

				By("setting the expected version in the helmop Status")
				Eventually(func() string {
					out, _ := k.Get("helmop", name, "-o=jsonpath={.status.version}")
					return out
				}).Should(Equal(chartVersion))
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
			By("setting the expected version in the helmop Status")
			Eventually(func() string {
				out, _ := k.Get("helmop", name, "-o=jsonpath={.status.version}")
				return out
			}).Should(Equal("0.2.0"))
		})
	})
})

var _ = Describe("HelmOp resource with polling of OCI registry", Label("infra-setup", "oci-registry"), Ordered, func() {
	var (
		namespace    string
		name         string
		repo         string
		chartVersion string
		insecure     bool
		ociRef       = getZotInternalRef()
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
			repo,
			"",
			5 * time.Second,
			helmOpsSecretName,
			insecure,
			chartVersion,
		})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		out, err := k.Delete("helmop", name)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterAll(func() {
		out, err := k.Delete("secret", helmOpsSecretName)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("applying a helmop resource", func() {
		Context("containing a valid helmop description pointing to an oci registry and insecure TLS", func() {
			BeforeEach(func() {
				namespace = "helmop-ns"
				name = "basic-oci"
				insecure = true

				repo = fmt.Sprintf("%s/sleeper-chart", ociRef)
				chartVersion = "0.1.0" // no polling
			})
			It("deploys the chart", func() {
				Eventually(func(g Gomega) {
					outPods, _ := k.Namespace(namespace).Get("pods")
					g.Expect(outPods).To(ContainSubstring("sleeper-"))
				}).Should(Succeed())
				Eventually(func(g Gomega) {
					outDeployments, _ := k.Namespace(namespace).Get("deployments")
					g.Expect(outDeployments).To(ContainSubstring("sleeper"))
				}).Should(Succeed())

				By("setting the expected version in the helmop Status")
				Eventually(func() string {
					out, _ := k.Get("helmop", name, "-o=jsonpath={.status.version}")
					return out
				}).Should(Equal(chartVersion))
			})
		})

		Context("a new version of the referenced chart is available", func() {
			BeforeEach(func() {
				chartVersion = "< 1.0.0"
			})

			AfterEach(func() {
				addr, err := getExternalOCIAddr(k)
				Expect(err).ToNot(HaveOccurred())

				url := fmt.Sprintf("https://%s:8082/v2/sleeper-chart/manifests/0.2.0", addr)
				req, err := http.NewRequest(http.MethodDelete, url, nil)
				Expect(err).ToNot(HaveOccurred())

				req.SetBasicAuth(os.Getenv("CI_OCI_USERNAME"), os.Getenv("CI_OCI_PASSWORD"))

				cli := http.Client{
					Transport: &http.Transport{
						TLSClientConfig: &tls.Config{
							InsecureSkipVerify: true,
						},
					},
				}

				resp, err := cli.Do(req)
				Expect(err).ToNot(HaveOccurred())

				Expect(resp.StatusCode).To(Equal(http.StatusAccepted))
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
				Eventually(func() string {
					out, _ := k.Get("helmop", name, "-o=jsonpath={.status.version}")
					return out
				}).Should(Equal("0.1.0"))

				By("having a newer chart version available in the repository")
				cmd := exec.Command("helm", "package", testenv.AssetPath("helmop/sleeper-chart2/"))
				out, err := cmd.CombinedOutput()
				Expect(err).ToNot(HaveOccurred(), out)

				externalIP, err := getExternalOCIAddr(k)
				Expect(err).ToNot(HaveOccurred())

				chartArchive, err := os.ReadFile("sleeper-chart-0.2.0.tgz")
				Expect(err).ToNot(HaveOccurred())

				// Login and push a Helm chart to our local OCI registry
				tlsConf := &tls.Config{
					InsecureSkipVerify: true,
				}
				OCIClient, err := registry.NewClient(
					registry.ClientOptHTTPClient(&http.Client{
						Transport: &http.Transport{
							TLSClientConfig: tlsConf,
							Proxy:           http.ProxyFromEnvironment,
						},
					}),
					registry.ClientOptBasicAuth(os.Getenv("CI_OCI_USERNAME"), os.Getenv("CI_OCI_PASSWORD")),
				)
				Expect(err).ToNot(HaveOccurred())

				OCIHost := fmt.Sprintf("%s:8082", externalIP)
				_, err = OCIClient.Push(chartArchive, fmt.Sprintf("%s/sleeper-chart:0.2.0", OCIHost))
				Expect(err).ToNot(HaveOccurred())

				By("installing the newer chart version")
				Eventually(func() bool {
					outPods, _ := k.Namespace(namespace).Get("pods")
					return strings.Contains(outPods, "sleeper2-")
				}).Should(BeTrue())
				Eventually(func() bool {
					outDeployments, _ := k.Namespace(namespace).Get("deployments")
					return strings.Contains(outDeployments, "sleeper2")
				}).Should(BeTrue())
				By("setting the expected version in the helmop Status")
				Eventually(func() string {
					out, _ := k.Get("helmop", name, "-o=jsonpath={.status.version}")
					return out
				}).Should(Equal("0.2.0"))
			})
		})

		Context("containing a valid helmop description pointing to an oci registry and not TLS", func() {
			BeforeEach(func() {
				namespace = "helmop-ns2"
				name = "basic-oci-no-tls"
				insecure = false

				repo = fmt.Sprintf("%s/sleeper-chart", ociRef)
			})
			It("does not deploy the chart because of TLS", func() {
				Consistently(func() string {
					out, _ := k.Namespace(namespace).Get("pods")
					return out
				}, 5*time.Second, time.Second).ShouldNot(ContainSubstring("sleeper-"))
			})
		})
	})

	When("applying a helmop resource which cannot be deployed", func() {
		Context("containing a helmop description pointing to an OCI registry using the wrong field", func() {
			BeforeEach(func() {
				namespace = "helmop-ns"
				name = "basic-oci-invalid"
				insecure = true

				repo = fmt.Sprintf("%s/sleeper-chart-will-not-be-found", ociRef)
			})
			It("fails visibly", func() {
				By("not deploying the chart")
				Consistently(func(g Gomega) {
					outPods, _ := k.Namespace(namespace).Get("pods")
					g.Expect(outPods).NotTo(ContainSubstring("sleeper-"))

					outDeployments, _ := k.Namespace(namespace).Get("deployments")
					g.Expect(outDeployments).NotTo(ContainSubstring("sleeper"))
				}, 5*time.Second, time.Second).Should(Succeed())

				By("displaying the reason for the failure in the HelmOps' status")
				Eventually(func(g Gomega) {
					st, err := k.Get("helmop", name, "-o=jsonpath={.status}")
					g.Expect(err).ToNot(HaveOccurred())

					var status fleet.StatusBase
					err = json.Unmarshal([]byte(st), &status)
					g.Expect(err).ToNot(HaveOccurred())

					g.Expect(status.Conditions).ToNot(BeEmpty())

					var foundReady, foundAccepted bool
					for _, cond := range status.Conditions {
						if cond.Type == "Ready" {
							foundReady = true

							g.Expect(string(cond.Status)).To(Equal("True"))
						}

						if cond.Type == "Accepted" {
							foundAccepted = true
							g.Expect(string(cond.Status)).To(Equal("False"))
							g.Expect(cond.Message).To(ContainSubstring("not found"))
						}
					}
					g.Expect(foundAccepted).To(BeTrue())
					g.Expect(foundReady).To(BeTrue())
				}).Should(Succeed())
			})
		})
	})
})

var _ = Describe("HelmOp resource tests with tarball source", Label("infra-setup", "helm-registry"), Ordered, func() {
	var (
		namespace string
		name      string
		insecure  = true
		k         kubectl.Command
		version   string
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
			"",
			fmt.Sprintf("%s/charts/sleeper-chart-0.1.0.tgz", getChartMuseumExternalAddr()),
			0,
			helmOpsSecretName,
			insecure,
			version,
		})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		out, err := k.Delete("helmop", name)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterAll(func() {
		out, err := k.Delete("secret", helmOpsSecretName)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	// Other version combinations are tested in HelmOps controller unit test
	When("applying a helmop resource without a version", func() {
		BeforeEach(func() {
			namespace = "helmop-tarball-ns-no-version"
			name = "basic-helmop"
			version = ""
		})
		It("deploys the chart", func() {
			Eventually(func(g Gomega) {
				outPods, _ := k.Namespace(namespace).Get("pods")
				g.Expect(outPods).To(ContainSubstring("sleeper-"))
			}).Should(Succeed())
			Eventually(func(g Gomega) {
				outDeployments, _ := k.Namespace(namespace).Get("deployments")
				g.Expect(outDeployments).To(ContainSubstring("sleeper"))
			}).Should(Succeed())
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

// getExternalOCIAddr retrieves the external URL where our local OCI registry can be reached.
func getExternalOCIAddr(k kubectl.Command) (string, error) {
	if v := os.Getenv("external_ip"); v != "" {
		return v, nil
	}

	return k.Namespace(cmd.InfraNamespace).Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
}
