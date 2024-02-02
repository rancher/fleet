package apply

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	cp "github.com/otiai10/copy"

	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Fleet apply", Ordered, func() {

	var (
		dirs    []string
		name    string
		options apply.Options
	)

	JustBeforeEach(func() {
		err := fleetApply(name, dirs, options)
		Expect(err).NotTo(HaveOccurred())
	})

	When("folder contains simple resources", func() {
		BeforeEach(func() {
			name = "simple"
			dirs = []string{cli.AssetsPath + "simple"}
		})

		It("then a Bundle is created with all the resources and keepResources is false", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle.Spec.Resources)).To(Equal(2))
				isSvcPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"simple/svc.yaml", bundle.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isDeploymentPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"simple/deployment.yaml", bundle.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())

				return isSvcPresent && isDeploymentPresent && !bundle.Spec.KeepResources
			}).Should(BeTrue())
		})
	})

	When("simple resources in a nested folder", func() {
		BeforeEach(func() {
			name = "nested_simple"
			dirs = []string{cli.AssetsPath + "nested_simple"}
		})

		It("then a Bundle is created with all the resources", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle.Spec.Resources)).To(Equal(3))
				isSvcPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_simple/simple/svc.yaml", bundle.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isDeploymentPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_simple/simple/deployment.yaml", bundle.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isREADMEPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_simple/README.md", bundle.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())

				return isSvcPresent && isDeploymentPresent && isREADMEPresent
			}).Should(BeTrue())
		})
	})

	When("simple resources in a nested folder with two levels", func() {
		BeforeEach(func() {
			name = "nested_two_levels"
			dirs = []string{cli.AssetsPath + "nested_two_levels"}
		})

		It("then a Bundle is created with all the resources", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle.Spec.Resources)).To(Equal(2))
				isSvcPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_two_levels/nested/svc/svc.yaml", bundle.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isDeploymentPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_two_levels/nested/deployment/deployment.yaml", bundle.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())

				return isSvcPresent && isDeploymentPresent
			}).Should(BeTrue())
		})
	})

	When("multiple fleet.yaml in a nested folder", func() {
		BeforeEach(func() {
			name = "nested_multiple"
			dirs = []string{cli.AssetsPath + "nested_multiple"}
		})

		It("then 3 Bundles are created with the relevant resources", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleListFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle)).To(Equal(3))
				deploymentA := bundle[0]
				deploymentB := bundle[1]
				deploymentC := bundle[2]

				Expect(len(deploymentA.Spec.Resources)).To(Equal(2))
				Expect(len(deploymentB.Spec.Resources)).To(Equal(2))
				Expect(len(deploymentC.Spec.Resources)).To(Equal(2))

				isFleetAPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_multiple/deploymentA/fleet.yaml", deploymentA.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isSvcAPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_multiple/deploymentA/svc/svc.yaml", deploymentA.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isFleetBPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_multiple/deploymentB/fleet.yaml", deploymentB.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isSvcBPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_multiple/deploymentB/svc/nested/svc.yaml", deploymentB.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isFleetCPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_multiple/deploymentC/fleet.yaml", deploymentC.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isDeploymentCPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_multiple/deploymentC/deployment.yaml", deploymentC.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())

				return isFleetAPresent && isSvcAPresent && isFleetBPresent && isSvcBPresent && isFleetCPresent && isDeploymentCPresent
			}).Should(BeTrue())
		})
	})

	When("multiple fleet.yaml mixed with simple resources in a nested folder", func() {
		BeforeEach(func() {
			name = "nested_mixed_two_levels"
			dirs = []string{cli.AssetsPath + "nested_mixed_two_levels"}
		})

		It("then Bundles are created with all the resources", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleListFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle)).To(Equal(3))
				root := bundle[0]
				deploymentA := bundle[1]
				deploymentC := bundle[2]

				Expect(len(deploymentA.Spec.Resources)).To(Equal(2))
				Expect(len(deploymentC.Spec.Resources)).To(Equal(1))
				Expect(len(root.Spec.Resources)).To(Equal(5))

				isFleetAPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_mixed_two_levels/nested/deploymentA/fleet.yaml", deploymentA.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isDeploymentAPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_mixed_two_levels/nested/deploymentA/fleet.yaml", deploymentA.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isFleetCPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_mixed_two_levels/nested/deploymentC/fleet.yaml", deploymentC.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isRootDeploymentAPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_mixed_two_levels/nested/deploymentA/fleet.yaml", root.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isRootFleetAPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_mixed_two_levels/nested/deploymentA/fleet.yaml", root.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isRootSvcBPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_mixed_two_levels/nested/deploymentB/svc.yaml", root.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isRootFleetCPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_mixed_two_levels/nested/deploymentC/fleet.yaml", root.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())
				isRootDeploymentDPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"nested_mixed_two_levels/nested/deploymentD/deployment.yaml", root.Spec.Resources)
				Expect(err).NotTo(HaveOccurred())

				return isFleetAPresent && isDeploymentAPresent && isFleetCPresent && isRootDeploymentAPresent && isRootFleetAPresent && isRootSvcBPresent && isRootFleetCPresent && isRootDeploymentDPresent
			}).Should(BeTrue())
		})
	})

	When("containing keepResources in the fleet.yaml", func() {
		BeforeEach(func() {
			name = "keep_resources"
			dirs = []string{cli.AssetsPath + "keep_resources"}
		})

		It("then a Bundle is created with keepResources", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Spec.KeepResources
			}).Should(BeTrue())
		})
	})

	When("non-helm type bundle uses helm options in fleet.yaml", func() {
		When("passes along enabled helm options", func() {
			BeforeEach(func() {
				name = "helm_options_enabled"
				dirs = []string{cli.AssetsPath + name}
			})

			It("publishes the flag in the bundle options", func() {
				Eventually(func() bool {
					bundle, err := cli.GetBundleFromOutput(buf)
					Expect(err).NotTo(HaveOccurred())
					return bundle.Spec.Helm.TakeOwnership &&
						bundle.Spec.Helm.Atomic &&
						bundle.Spec.Helm.Force &&
						bundle.Spec.Helm.WaitForJobs &&
						bundle.Spec.Helm.DisablePreProcess &&
						bundle.Spec.Helm.ReleaseName == "enabled"
				}).Should(BeTrue())
			})
		})

		When("passes along disabled helm options", func() {
			BeforeEach(func() {
				name = "helm_options_disabled"
				dirs = []string{cli.AssetsPath + name}
			})

			It("publishes the flag in the bundle options", func() {
				Eventually(func() bool {
					bundle, err := cli.GetBundleFromOutput(buf)
					Expect(err).NotTo(HaveOccurred())
					return bundle.Spec.Helm.TakeOwnership == false &&
						bundle.Spec.Helm.Atomic == false &&
						bundle.Spec.Helm.Force == false &&
						bundle.Spec.Helm.WaitForJobs == false &&
						bundle.Spec.Helm.DisablePreProcess == false &&
						bundle.Spec.Helm.ReleaseName == "disabled"
				}).Should(BeTrue())
			})
		})

		When("passes along helm options with a kustomize bundle", func() {
			BeforeEach(func() {
				name = "helm_options_kustomize"
				dirs = []string{cli.AssetsPath + name}
			})

			It("publishes the flag in the bundle options", func() {
				Eventually(func() bool {
					bundle, err := cli.GetBundleFromOutput(buf)
					Expect(err).NotTo(HaveOccurred())
					return bundle.Spec.Helm.TakeOwnership &&
						bundle.Spec.Helm.ReleaseName == "kustomize"
				}).Should(BeTrue())
			})
		})
	})
})

var _ = Describe("Fleet apply with helm charts with dependencies", Ordered, func() {

	var (
		dirs      []string
		name      string
		options   apply.Options
		tmpDirRel string
		tmpDir    string
		repo      = repository{
			port: port,
		}
	)

	JustBeforeEach(func() {
		// start a fake helm repository
		repo.startRepository(false)
		tmpDir = GinkgoT().TempDir()
		err := cp.Copy(path.Join(cli.AssetsPath, "deps-charts", name), tmpDir)
		Expect(err).NotTo(HaveOccurred())
		// get the relative path because fleet apply needs a relative path
		pwd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		tmpDirRel, err = filepath.Rel(pwd, tmpDir)
		Expect(err).NotTo(HaveOccurred())
		dirs = []string{tmpDirRel}
		err = fleetApply(name, dirs, options)
		Expect(err).NotTo(HaveOccurred())
	})

	When("folder contains helm chart with no fleet.yaml", func() {
		BeforeEach(func() {
			name = "no-fleet-yaml"
		})

		It("then a Bundle is created with all the resources, including the dependencies, and keepResources is false", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle.Spec.Resources)).To(Equal(5))
				files, err := getAllFilesInDir(tmpDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(files)).To(Equal(len(bundle.Spec.Resources)))
				for _, file := range files {
					presentInBundleResources(file, bundle.Spec.Resources)
				}
				// explicitly check for dependency files
				presentInBundleResources(path.Join(tmpDir, "Chart.lock"), bundle.Spec.Resources)
				presentInBundleResources(path.Join(tmpDir, "charts/config-chart-0.1.0.tgz"), bundle.Spec.Resources)

				return !bundle.Spec.KeepResources
			}).Should(BeTrue())
		})
	})

	When("folder contains helm chart with fleet.yaml, disableDependencyUpdate is not set", func() {
		BeforeEach(func() {
			name = "simple-with-fleet-yaml"
		})

		It("then a Bundle is created with all the resources, including the dependencies, and keepResources is false", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle.Spec.Resources)).To(Equal(6))
				files, err := getAllFilesInDir(tmpDirRel)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(files)).To(Equal(len(bundle.Spec.Resources)))
				for _, file := range files {
					presentInBundleResources(file, bundle.Spec.Resources)
				}
				// explicitly check for dependency files
				presentInBundleResources(path.Join(tmpDirRel, "Chart.lock"), bundle.Spec.Resources)
				presentInBundleResources(path.Join(tmpDirRel, "charts/config-chart-0.1.0.tgz"), bundle.Spec.Resources)
				return !bundle.Spec.KeepResources
			}).Should(BeTrue())
		})
	})

	When("folder contains helm chart with fleet.yaml, disableDependencyUpdate is set to true", func() {
		BeforeEach(func() {
			name = "simple-with-fleet-yaml-no-deps"
		})

		It("then a Bundle is created with all the resources, dependencies should not be in the bundle", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle.Spec.Resources)).To(Equal(4))
				files, err := getAllFilesInDir(tmpDirRel)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(files)).To(Equal(len(bundle.Spec.Resources)))
				for _, file := range files {
					presentInBundleResources(file, bundle.Spec.Resources)
				}
				// explicitly check for dependency files (they should not exist)
				notPresentInBundleResources(path.Join(tmpDirRel, "Chart.lock"), bundle.Spec.Resources)
				notPresentInBundleResources(path.Join(tmpDirRel, "charts/config-chart-0.1.0.tgz"), bundle.Spec.Resources)
				return !bundle.Spec.KeepResources
			}).Should(BeTrue())
		})
	})

	When("folder contains fleet.yaml defining a remote chart which has dependencies", func() {
		BeforeEach(func() {
			name = "remote-chart-with-deps"
		})

		It("then a Bundle is created with all the resources, dependencies should be in the bundle", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle.Spec.Resources)).To(Equal(6))
				presentInBundleResources(path.Join(tmpDirRel, "fleet.yaml"), bundle.Spec.Resources)
				// as files were unpacked from the downloaded chart we can't just
				// list the files in the original folder and compare.
				// Files are only located in the bundle resources
				onlyPresentInBundleResources("Chart.yaml", bundle.Spec.Resources)
				onlyPresentInBundleResources("values.yaml", bundle.Spec.Resources)
				onlyPresentInBundleResources("templates/configmap.yaml", bundle.Spec.Resources)
				onlyPresentInBundleResources("Chart.lock", bundle.Spec.Resources)
				onlyPresentInBundleResources("charts/config-chart-0.1.0.tgz", bundle.Spec.Resources)
				return !bundle.Spec.KeepResources
			}).Should(BeTrue())
		})
	})

	When("folder contains fleet.yaml defining a remote chart which has dependencies, and disableDependencyUpdate is set", func() {
		BeforeEach(func() {
			name = "remote-chart-with-deps-disabled"
		})

		It("then a Bundle is created with all the resources, dependencies should not be in the bundle", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle.Spec.Resources)).To(Equal(4))
				presentInBundleResources(path.Join(tmpDirRel, "fleet.yaml"), bundle.Spec.Resources)
				// as files were unpacked from the downloaded chart we can't just
				// list the files in the original folder and compare.
				// Files are only located in the bundle resources
				onlyPresentInBundleResources("Chart.yaml", bundle.Spec.Resources)
				onlyPresentInBundleResources("values.yaml", bundle.Spec.Resources)
				onlyPresentInBundleResources("templates/configmap.yaml", bundle.Spec.Resources)
				return !bundle.Spec.KeepResources
			}).Should(BeTrue())
		})
	})

	When("folder contains multiple charts with different options", func() {
		BeforeEach(func() {
			name = "multi-chart"
		})

		It("then Bundles are created with the corresponding resources, depending if they should update dependencies", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleListFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(bundle)).To(Equal(3))
				deploymentA := bundle[0]
				deploymentB := bundle[1]
				deploymentC := bundle[2]

				// deploymentA corresponds to multi-chart/remote-chart-with-deps
				Expect(len(deploymentA.Spec.Resources)).To(Equal(6))
				presentInBundleResources(path.Join(tmpDirRel, "remote-chart-with-deps", "fleet.yaml"), deploymentA.Spec.Resources)
				// as files were unpacked from the downloaded chart we can't just
				// list the files in the original folder and compare.
				// Files are only located in the bundle resources
				onlyPresentInBundleResources("Chart.yaml", deploymentA.Spec.Resources)
				onlyPresentInBundleResources("values.yaml", deploymentA.Spec.Resources)
				onlyPresentInBundleResources("templates/configmap.yaml", deploymentA.Spec.Resources)
				onlyPresentInBundleResources("Chart.lock", deploymentA.Spec.Resources)
				onlyPresentInBundleResources("charts/config-chart-0.1.0.tgz", deploymentA.Spec.Resources)

				// deploymentB corresponds to multi-chart/simple-with-fleet-yaml
				Expect(len(deploymentB.Spec.Resources)).To(Equal(6))
				files, err := getAllFilesInDir(path.Join(tmpDirRel, "simple-with-fleet-yaml"))
				Expect(err).NotTo(HaveOccurred())
				Expect(len(files)).To(Equal(len(deploymentB.Spec.Resources)))
				for _, file := range files {
					presentInBundleResources(file, deploymentB.Spec.Resources)
				}
				// explicitly check for dependency files
				presentInBundleResources(path.Join(tmpDirRel, "simple-with-fleet-yaml", "Chart.lock"), deploymentB.Spec.Resources)
				presentInBundleResources(path.Join(tmpDirRel, "simple-with-fleet-yaml", "charts/config-chart-0.1.0.tgz"), deploymentB.Spec.Resources)

				// deploymentC corresponds to multi-char/simple-with-fleet-yaml-no-deps
				Expect(len(deploymentC.Spec.Resources)).To(Equal(4))
				files, err = getAllFilesInDir(path.Join(tmpDirRel, "simple-with-fleet-yaml-no-deps"))
				Expect(err).NotTo(HaveOccurred())
				Expect(len(files)).To(Equal(len(deploymentC.Spec.Resources)))
				for _, file := range files {
					presentInBundleResources(file, deploymentC.Spec.Resources)
				}
				// explicitly check for dependency files (they should not exist)
				notPresentInBundleResources(path.Join(tmpDirRel, "simple-with-fleet-yaml-no-deps", "Chart.lock"), deploymentC.Spec.Resources)
				notPresentInBundleResources(path.Join(tmpDirRel, "simple-with-fleet-yaml-no-deps", "charts/config-chart-0.1.0.tgz"), deploymentC.Spec.Resources)
				return !deploymentA.Spec.KeepResources && !deploymentB.Spec.KeepResources && !deploymentC.Spec.KeepResources
			}).Should(BeTrue())
		})
	})

	AfterEach(func() {
		err := repo.stopRepository()
		Expect(err).NotTo(HaveOccurred())
	})
})

func presentInBundleResources(path string, resources []v1alpha1.BundleResource) {
	isPresent, err := cli.IsResourcePresentInBundle(path, resources)
	Expect(err).NotTo(HaveOccurred())
	Expect(isPresent).Should(BeTrue())
}

func onlyPresentInBundleResources(path string, resources []v1alpha1.BundleResource) {
	found := false
	for _, resource := range resources {
		if strings.HasSuffix(resource.Name, path) {
			found = true
		}
	}
	Expect(found).Should(BeTrue())
}

func notPresentInBundleResources(path string, resources []v1alpha1.BundleResource) {
	isPresent, err := cli.IsResourcePresentInBundle(path, resources)
	Expect(err).To(HaveOccurred())
	Expect(isPresent).Should(BeFalse())
}

func getAllFilesInDir(chartPath string) ([]string, error) {
	var files []string
	err := filepath.Walk(chartPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
