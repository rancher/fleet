package apply

import (
	"io/fs"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
)

const (
	numberOfFilesInHelmConfigChart = 3
	port                           = ":3000"
)

var _ = Describe("Fleet apply helm release", Serial, func() {
	When("applying a folder with fleet.yaml that contains a helm release in the repo field", func() {
		testHelmRepo("helm_repo_url", port)
	})

	When("applying a folder with fleet.yaml that contains a helm release in the chart field", func() {
		testHelmRepo("helm_chart_url", port)
	})

	When("applying a folder with fleet.yaml that contains a sub folder with another fleet.yaml", func() {
		var repo = repository{
			port: port,
		}

		BeforeEach(func() {
			repo.startRepository(true)
		})

		AfterEach(func() {
			err := repo.stopRepository()
			Expect(err).NotTo(HaveOccurred())
		})

		When("path credentials are provided just for root folder", func() {
			It("fleet apply fails for sub folder", func() {
				Eventually(func() string {
					err := fleetApply("helm", []string{cli.AssetsPath + "helm_path_credentials"}, apply.Options{
						AuthByPath: map[string]bundlereader.Auth{cli.AssetsPath + "helm_path_credentials": {Username: username, Password: password}},
					})
					Expect(err).To(HaveOccurred())
					return err.Error()
				}).Should(ContainSubstring("401"))
			})
		})

		When("path credentials are provided for both root and sub folder", func() {
			It("fleet apply works fine", func() {
				Eventually(func() error {
					return fleetApply("helm", []string{cli.AssetsPath + "helm_path_credentials"}, apply.Options{
						AuthByPath: map[string]bundlereader.Auth{
							cli.AssetsPath + "helm_path_credentials":           {Username: username, Password: password},
							cli.AssetsPath + "helm_path_credentials/subfolder": {Username: username, Password: password},
						},
					})
				}).Should(Not(HaveOccurred()))
				By("verifying Bundle is created with all the resources inside of the helm release", func() {
					Eventually(verifyResourcesArePresent).Should(BeTrue())
				})
			})
		})
	})
})

func testHelmRepo(path, port string) {
	var authEnabled bool
	var repo = repository{
		port: port,
	}

	JustBeforeEach(func() {
		repo.startRepository(authEnabled)
	})

	AfterEach(func() {
		err := repo.stopRepository()
		Expect(err).NotTo(HaveOccurred())
	})

	When("no auth is required", func() {
		BeforeEach(func() {
			authEnabled = false
		})
		It("fleet apply success", func() {
			Eventually(func() error {
				return fleetApply("helm", []string{cli.AssetsPath + path}, apply.Options{})
			}).Should(Not(HaveOccurred()))
			By("verifying Bundle is created with all the resources inside of the helm release", func() {
				Eventually(verifyResourcesArePresent).Should(BeTrue())
			})
		})
	})

	When("auth is required, and it is not provided", func() {
		BeforeEach(func() {
			authEnabled = true
		})
		It("fleet apply fails when no auth provided", func() {
			Eventually(func() string {
				err := fleetApply("helm", []string{cli.AssetsPath + path}, apply.Options{})
				Expect(err).To(HaveOccurred())
				return err.Error()
			}).Should(ContainSubstring("401"))
		})
	})

	When("auth is required, and it is provided without --helm-repo-url-regex", func() {
		BeforeEach(func() {
			authEnabled = true
		})
		It("fleet apply success", func() {
			Eventually(func() error {
				return fleetApply("helm", []string{cli.AssetsPath + path}, apply.Options{Auth: bundlereader.Auth{Username: username, Password: password}})
			}).Should(Not(HaveOccurred()))
			By("verifying Bundle is created with all the resources inside of the helm release", func() {
				Eventually(verifyResourcesArePresent).Should(BeTrue())
			})
		})
	})

	When("auth is required, it is provided and --helm-repo-url-regex matches the repo url", func() {
		BeforeEach(func() {
			authEnabled = true
		})
		It("fleet apply success", func() {
			Eventually(func() error {
				return fleetApply("helm", []string{cli.AssetsPath + path}, apply.Options{
					Auth:             bundlereader.Auth{Username: username, Password: password},
					HelmRepoURLRegex: "http://localhost/*",
				})
			}).Should(Not(HaveOccurred()))
			By("verifying Bundle is created with all the resources inside of the helm release", func() {
				Eventually(verifyResourcesArePresent).Should(BeTrue())
			})
		})
	})

	When("auth is required, and it is provided but --helm-repo-url-regex doesn't match", func() {
		BeforeEach(func() {
			authEnabled = true
		})
		It("fleet apply fails when --helm-repo-url-regex doesn't match the helm repo url", func() {
			Eventually(func() string {
				err := fleetApply("helm", []string{cli.AssetsPath + path}, apply.Options{
					Auth:             bundlereader.Auth{Username: username, Password: password},
					HelmRepoURLRegex: "nomatch",
				})
				Expect(err).To(HaveOccurred())
				return err.Error()
			}).Should(ContainSubstring("401"))
		})
	})

	When("auth is required, and it is provided but --helm-repo-url-regex is not valid", func() {
		BeforeEach(func() {
			authEnabled = true
		})
		It("fleet apply fails when --helm-repo-url-regex is not valid", func() {
			Eventually(func() string {
				err := fleetApply("helm", []string{cli.AssetsPath + path}, apply.Options{
					Auth:             bundlereader.Auth{Username: username, Password: password},
					HelmRepoURLRegex: "a(b",
				})
				Expect(err).To(HaveOccurred())
				return err.Error()
			}).Should(Equal("error parsing regexp: missing closing ): `a(b`"))
		})
	})

	When("Auth is required, and it is provided in HelmSecretNameForPaths", func() {
		BeforeEach(func() {
			authEnabled = true
		})
		It("fleet apply uses credentials from HelmSecretNameForPaths", func() {
			Eventually(func() error {
				return fleetApply("helm", []string{cli.AssetsPath + path}, apply.Options{AuthByPath: map[string]bundlereader.Auth{cli.AssetsPath + path: {Username: username, Password: password}}})
			}).Should(Not(HaveOccurred()))
			By("verify Bundle is created with all the resources inside of the helm release", func() {
				Eventually(verifyResourcesArePresent).Should(BeTrue())
			})
		})
	})

	When("Auth is required, and it provided in both HelmSecretNameForPaths and HelmSecret", func() {
		BeforeEach(func() {
			authEnabled = true
		})
		It("fleet apply uses credentials from HelmSecretNameForPaths", func() {
			Eventually(func() error {
				return fleetApply("helm", []string{cli.AssetsPath + path}, apply.Options{Auth: bundlereader.Auth{Username: "wrong", Password: "wrong"}, AuthByPath: map[string]bundlereader.Auth{cli.AssetsPath + path: {Username: username, Password: password}}})
			}).Should(Not(HaveOccurred()))
			By("verify Bundle is created with all the resources inside of the helm release", func() {
				Eventually(verifyResourcesArePresent).Should(BeTrue())
			})
		})
	})
}

func verifyResourcesArePresent() bool {
	bundle, err := cli.GetBundleFromOutput(buf)
	Expect(err).NotTo(HaveOccurred())
	paths, err := getAllResourcesPathFromTheHelmRelease()
	Expect(err).NotTo(HaveOccurred())
	Expect(paths).Should(HaveLen(numberOfFilesInHelmConfigChart))
	// should contain all resources plus the fleet.yaml
	Expect(bundle.Spec.Resources).Should(HaveLen(numberOfFilesInHelmConfigChart + 1))
	for _, path := range paths {
		present, err := cli.IsResourcePresentInBundle(path, bundle.Spec.Resources)
		Expect(err).NotTo(HaveOccurred(), "validating resource: "+path)
		Expect(present).Should(BeTrue(), "validating resource: "+path)
	}
	return true
}

// returns path for all resources in the assets/helmrepository/config-chart folder
func getAllResourcesPathFromTheHelmRelease() ([]string, error) {
	paths := []string{}
	err := filepath.Walk(cli.AssetsPath+"helmrepository/config-chart", func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}
