package apply

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gcustom"
	"github.com/onsi/gomega/types"
	cp "github.com/otiai10/copy"

	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/internal/bundlereader"
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
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle.Spec.Resources).To(HaveLen(2))
			Expect(cli.AssetsPath + "simple/svc.yaml").To(bePresentInBundleResources(bundle.Spec.Resources))
			Expect(cli.AssetsPath + "simple/deployment.yaml").To(bePresentInBundleResources(bundle.Spec.Resources))
			Expect(bundle.Spec.KeepResources).Should(BeFalse())
		})
	})

	When("simple resources in a nested folder", func() {
		BeforeEach(func() {
			name = "nested_simple"
			dirs = []string{cli.AssetsPath + "nested_simple"}
		})

		It("then a Bundle is created with all the resources", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle.Spec.Resources).To(HaveLen(3))
			Expect(cli.AssetsPath + "nested_simple/simple/svc.yaml").To(bePresentInBundleResources(bundle.Spec.Resources))
			Expect(cli.AssetsPath + "nested_simple/simple/deployment.yaml").To(bePresentInBundleResources(bundle.Spec.Resources))
			Expect(cli.AssetsPath + "nested_simple/README.md").To(bePresentInBundleResources(bundle.Spec.Resources))
		})
	})

	When("simple resources in a nested folder with two levels", func() {
		BeforeEach(func() {
			name = "nested_two_levels"
			dirs = []string{cli.AssetsPath + "nested_two_levels"}
		})

		It("then a Bundle is created with all the resources", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle.Spec.Resources).To(HaveLen(2))
			Expect(cli.AssetsPath + "nested_two_levels/nested/svc/svc.yaml").To(bePresentInBundleResources(bundle.Spec.Resources))
			Expect(cli.AssetsPath + "nested_two_levels/nested/deployment/deployment.yaml").To(bePresentInBundleResources(bundle.Spec.Resources))
		})
	})

	When("multiple fleet.yaml in a nested folder", func() {
		BeforeEach(func() {
			name = "nested_multiple"
			dirs = []string{cli.AssetsPath + "nested_multiple"}
		})

		It("then 3 Bundles are created with the relevant resources", func() {
			bundle, err := cli.GetBundleListFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(HaveLen(3))
			deploymentA := bundle[0]
			deploymentB := bundle[1]
			deploymentC := bundle[2]

			Expect(deploymentA.Spec.Resources).To(HaveLen(1))
			Expect(deploymentB.Spec.Resources).To(HaveLen(1))
			Expect(deploymentC.Spec.Resources).To(HaveLen(1))

			Expect(cli.AssetsPath + "nested_multiple/deploymentA/svc/svc.yaml").To(bePresentInBundleResources(deploymentA.Spec.Resources))
			Expect(cli.AssetsPath + "nested_multiple/deploymentB/svc/nested/svc.yaml").To(bePresentInBundleResources(deploymentB.Spec.Resources))
			Expect(cli.AssetsPath + "nested_multiple/deploymentC/deployment.yaml").To(bePresentInBundleResources(deploymentC.Spec.Resources))
		})
	})

	When("multiple fleet.yaml mixed with simple resources in a nested folder", func() {
		BeforeEach(func() {
			name = "nested_mixed_two_levels"
			dirs = []string{cli.AssetsPath + "nested_mixed_two_levels"}
		})

		It("then Bundles are created with all the resources", func() {
			bundle, err := cli.GetBundleListFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(HaveLen(3))
			root := bundle[0]
			deploymentA := bundle[1]
			deploymentC := bundle[2]

			Expect(deploymentA.Spec.Resources).To(HaveLen(1))
			Expect(deploymentC.Spec.Resources).To(HaveLen(1))
			Expect(root.Spec.Resources).To(HaveLen(6))

			Expect(cli.AssetsPath + "nested_mixed_two_levels/nested/deploymentA/deployment.yaml").To(bePresentInBundleResources(deploymentA.Spec.Resources))
			Expect(cli.AssetsPath + "nested_mixed_two_levels/nested/deploymentA/deployment.yaml").To(bePresentInBundleResources(root.Spec.Resources))
			Expect(cli.AssetsPath + "nested_mixed_two_levels/nested/deploymentB/svc.yaml").To(bePresentInBundleResources(root.Spec.Resources))
			Expect(cli.AssetsPath + "nested_mixed_two_levels/nested/deploymentC/cm.yaml").To(bePresentInBundleResources(deploymentC.Spec.Resources))
			Expect(cli.AssetsPath + "nested_mixed_two_levels/nested/deploymentD/deployment.yaml").To(bePresentInBundleResources(root.Spec.Resources))
		})
	})

	When("containing keepResources in the fleet.yaml", func() {
		BeforeEach(func() {
			name = "keep_resources"
			dirs = []string{cli.AssetsPath + "keep_resources"}
		})

		It("then a Bundle is created with keepResources", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle.Spec.KeepResources).To(BeTrue())
		})
	})

	When("non-helm type bundle uses helm options in fleet.yaml", func() {
		When("passes along enabled helm options", func() {
			BeforeEach(func() {
				name = "helm_options_enabled"
				dirs = []string{cli.AssetsPath + name}
			})

			It("publishes the flag in the bundle options", func() {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(bundle.Spec.Helm.TakeOwnership).To(BeTrue())
				Expect(bundle.Spec.Helm.Atomic).To(BeTrue())
				Expect(bundle.Spec.Helm.Force).To(BeTrue())
				Expect(bundle.Spec.Helm.WaitForJobs).To(BeTrue())
				Expect(bundle.Spec.Helm.DisablePreProcess).To(BeTrue())
				Expect(bundle.Spec.Helm.ReleaseName).To(Equal("enabled"))
			})
		})

		When("passes along disabled helm options", func() {
			BeforeEach(func() {
				name = "helm_options_disabled"
				dirs = []string{cli.AssetsPath + name}
			})

			It("publishes the flag in the bundle options", func() {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(bundle.Spec.Helm.TakeOwnership).To(BeFalse())
				Expect(bundle.Spec.Helm.Atomic).To(BeFalse())
				Expect(bundle.Spec.Helm.Force).To(BeFalse())
				Expect(bundle.Spec.Helm.WaitForJobs).To(BeFalse())
				Expect(bundle.Spec.Helm.DisablePreProcess).To(BeFalse())
				Expect(bundle.Spec.Helm.ReleaseName).To(Equal("disabled"))
			})
		})

		When("passes along helm options with a kustomize bundle", func() {
			BeforeEach(func() {
				name = "helm_options_kustomize"
				dirs = []string{cli.AssetsPath + name}
			})

			It("publishes the flag in the bundle options", func() {
				bundle, err := cli.GetBundleFromOutput(buf)
				Expect(err).NotTo(HaveOccurred())
				Expect(bundle.Spec.Helm.TakeOwnership).To(BeTrue())
				Expect(bundle.Spec.Helm.ReleaseName).To(Equal("kustomize"))
			})
		})
	})
})

var _ = Describe("Fleet apply driven", Ordered, func() {

	var (
		dirs    []string
		name    string
		options apply.Options
		repo    = repository{
			port: port,
		}
	)

	JustBeforeEach(func() {
		err := fleetApplyDriven(name, dirs, options)
		Expect(err).NotTo(HaveOccurred())
	})

	When("using driven scan with multiple paths and options", func() {
		BeforeEach(func() {
			name = "multiple_paths"
			dirs = []string{
				cli.AssetsPath + "driven/helm",
				cli.AssetsPath + "driven/simple",
				cli.AssetsPath + "driven/kustomize" + ":dev.yaml",
				cli.AssetsPath + "driven/kustomize" + ":prod.yaml",
			}
			// set credentials to download helm chart for mock repository
			options.AuthByPath = map[string]bundlereader.Auth{
				cli.AssetsPath + "driven/helm": {Username: username, Password: password},
			}
			repo.startRepository(true)
		})

		AfterEach(func() {
			err := repo.stopRepository()
			Expect(err).NotTo(HaveOccurred())
		})

		It("creates the expected 4 bundles of different types", func() {
			bundles, err := cli.GetBundleListFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundles).To(HaveLen(4))

			// helm bundle
			helmBundle := bundles[0]
			Expect(helmBundle.Name).To(Equal("assets-driven-helm"))
			Expect(helmBundle.Spec.Resources).To(HaveLen(3))
			// as files were unpacked from the downloaded chart we can't just
			// list the files in the original folder and compare.
			// Files are only located in the bundle resources
			Expect("Chart.yaml").To(bePresentOnlyInBundleResources(helmBundle.Spec.Resources))
			Expect("values.yaml").To(bePresentOnlyInBundleResources(helmBundle.Spec.Resources))
			Expect("templates/configmap.yaml").To(bePresentOnlyInBundleResources(helmBundle.Spec.Resources))
			// check for helm options defined in the fleet.yaml file
			Expect(helmBundle.Spec.Helm).ToNot(BeNil())
			Expect(helmBundle.Spec.Helm.ReleaseName).To(Equal("config-chart"))
			Expect(helmBundle.Spec.Helm.Chart).To(Equal("http://localhost:3000/config-chart-0.1.0.tgz"))

			// simple bundle
			simpleBundle := bundles[1]
			Expect(simpleBundle.Name).To(Equal("assets-driven-simple"))
			Expect(simpleBundle.Spec.Resources).To(HaveLen(2))
			expectedResources := []string{
				cli.AssetsPath + "driven/simple/deployment.yaml",
				cli.AssetsPath + "driven/simple/svc.yaml",
			}
			for _, r := range expectedResources {
				Expect(r).To(bePresentInBundleResources(simpleBundle.Spec.Resources))
			}

			// kustomize dev bundle
			kDevBundle := bundles[2]
			Expect(kDevBundle.Name).To(Equal("assets-driven-kustomize-dev"))

			// kustomize prod bundle
			kProdBundle := bundles[3]
			Expect(kProdBundle.Name).To(Equal("assets-driven-kustomize-prod"))

			// both kustomize bundles have the same resources, but different config fleet.yaml
			kResources := []string{
				cli.AssetsPath + "driven/kustomize/base/kustomization.yaml",
				cli.AssetsPath + "driven/kustomize/base/secret.yaml",
				cli.AssetsPath + "driven/kustomize/overlays/dev/kustomization.yaml",
				cli.AssetsPath + "driven/kustomize/overlays/dev/secret.yaml",
				cli.AssetsPath + "driven/kustomize/overlays/prod/kustomization.yaml",
				cli.AssetsPath + "driven/kustomize/overlays/prod/secret.yaml",
				cli.AssetsPath + "driven/kustomize/dev.yaml",
				cli.AssetsPath + "driven/kustomize/prod.yaml",
			}
			Expect(kDevBundle.Spec.Resources).To(HaveLen(8))
			Expect(kProdBundle.Spec.Resources).To(HaveLen(8))
			for _, r := range kResources {
				Expect(r).To(bePresentInBundleResources(kDevBundle.Spec.Resources))
				Expect(r).To(bePresentInBundleResources(kProdBundle.Spec.Resources))
			}

			// they should have different Kustomize options
			Expect(kDevBundle.Spec.Kustomize).ToNot(BeNil())
			Expect(kProdBundle.Spec.Kustomize).ToNot(BeNil())
			Expect(kDevBundle.Spec.Kustomize.Dir).To(Equal("overlays/dev"))
			Expect(kProdBundle.Spec.Kustomize.Dir).To(Equal("overlays/prod"))
		})
	})

	When("deploying folder with fleet files in camelCase", func() {
		BeforeEach(func() {
			name = "camelCase_config_filenames"
			dirs = []string{
				cli.AssetsPath + "driven2/kustomize" + ":fleetDev.yaml",
				cli.AssetsPath + "driven2/kustomize" + ":fleetProd.yaml",
			}
		})

		It("creates the bundles with names in lower case and having a suffix", func() {
			bundles, err := cli.GetBundleListFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundles).To(HaveLen(2))

			// kustomize dev bundle
			kDevBundle := bundles[0]
			Expect(kDevBundle.Name).To(Equal("assets-driven2-kustomize-fleetdev-2946f474"))

			// kustomize prod bundle
			kProdBundle := bundles[1]
			Expect(kProdBundle.Name).To(Equal("assets-driven2-kustomize-fleetprod-99f597b0"))

			// both kustomize bundles have the same resources, but different config fleet.yaml
			kResources := []string{
				cli.AssetsPath + "driven2/kustomize/base/kustomization.yaml",
				cli.AssetsPath + "driven2/kustomize/base/secret.yaml",
				cli.AssetsPath + "driven2/kustomize/overlays/dev/kustomization.yaml",
				cli.AssetsPath + "driven2/kustomize/overlays/dev/secret.yaml",
				cli.AssetsPath + "driven2/kustomize/overlays/prod/kustomization.yaml",
				cli.AssetsPath + "driven2/kustomize/overlays/prod/secret.yaml",
				cli.AssetsPath + "driven2/kustomize/fleetDev.yaml",
				cli.AssetsPath + "driven2/kustomize/fleetProd.yaml",
			}
			Expect(kDevBundle.Spec.Resources).To(HaveLen(8))
			Expect(kProdBundle.Spec.Resources).To(HaveLen(8))
			for _, r := range kResources {
				Expect(r).To(bePresentInBundleResources(kDevBundle.Spec.Resources))
				Expect(r).To(bePresentInBundleResources(kProdBundle.Spec.Resources))
			}

			// they should have different Kustomize options
			Expect(kDevBundle.Spec.Kustomize).ToNot(BeNil())
			Expect(kProdBundle.Spec.Kustomize).ToNot(BeNil())
			Expect(kDevBundle.Spec.Kustomize.Dir).To(Equal("overlays/dev"))
			Expect(kProdBundle.Spec.Kustomize.Dir).To(Equal("overlays/prod"))
		})
	})

	When("deploying folder with fleet file in a subfolder and no specified config path", func() {
		BeforeEach(func() {
			name = "single_path_no_config_specified"
			dirs = []string{
				cli.AssetsPath + "driven_fleet_yaml_subfolder/helm",
			}
		})

		It("creates 1 raw bundle, that has the fleet.yaml only and no helm options", func() {
			bundles, err := cli.GetBundleListFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundles).To(HaveLen(1))

			// kustomize dev bundle
			b := bundles[0]

			// name will be truncated
			Expect(b.Name).To(Equal("assets-driven-fleet-yaml-subfolder-helm-f49dc35c"))
			Expect(b.Spec.Resources).To(HaveLen(1))
			resPath := cli.AssetsPath + "driven_fleet_yaml_subfolder/helm/test/fleet.yaml"
			Expect(resPath).To(bePresentInBundleResources(b.Spec.Resources))

			// as the fleet.yaml was not parsed as a fleet.yaml the helm options are not set
			Expect(b.Spec.Helm).To(BeNil())
		})
	})

	When("deploying folder with fleet file in a subfolder and specifying the path", func() {
		BeforeEach(func() {
			name = "single_path_config_specified"
			dirs = []string{
				cli.AssetsPath + "driven_fleet_yaml_subfolder/helm:test/fleet.yaml",
			}
			// set credentials to download helm chart for mock repository
			options.AuthByPath = map[string]bundlereader.Auth{
				cli.AssetsPath + "driven_fleet_yaml_subfolder/helm": {Username: username, Password: password},
			}
			repo.startRepository(true)
		})

		AfterEach(func() {
			err := repo.stopRepository()
			Expect(err).NotTo(HaveOccurred())
		})

		It("creates 1 raw bundle, that has chart resources and has helm options", func() {
			bundles, err := cli.GetBundleListFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundles).To(HaveLen(1))

			// helm bundle
			helmBundle := bundles[0]
			Expect(helmBundle.Name).To(Equal("assets-driven-fleet-yaml-subfolder-helm-test-fl-b676f"))
			Expect(helmBundle.Spec.Resources).To(HaveLen(4))
			// as files were unpacked from the downloaded chart we can't just
			// list the files in the original folder and compare.
			// Files are only located in the bundle resources
			Expect("Chart.yaml").To(bePresentOnlyInBundleResources(helmBundle.Spec.Resources))
			Expect("values.yaml").To(bePresentOnlyInBundleResources(helmBundle.Spec.Resources))
			Expect("templates/configmap.yaml").To(bePresentOnlyInBundleResources(helmBundle.Spec.Resources))
			resPath := cli.AssetsPath + "driven_fleet_yaml_subfolder/helm/test/fleet.yaml"
			Expect(resPath).To(bePresentInBundleResources(helmBundle.Spec.Resources))
			// check for helm options defined in the fleet.yaml file
			Expect(helmBundle.Spec.Helm).ToNot(BeNil())
			Expect(helmBundle.Spec.Helm.ReleaseName).To(Equal("config-chart"))
			Expect(helmBundle.Spec.Helm.Chart).To(Equal("http://localhost:3000/config-chart-0.1.0.tgz"))
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

		It("creates a Bundle  with all the resources, including the dependencies", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			// files expected are:
			// Chart.yaml + values.yaml + templates/configmap.yaml +
			// Chart.lock + charts/config-chart-0.1.0.tgz
			Expect(bundle.Spec.Resources).To(HaveLen(5))
			files, err := getAllFilesInDir(tmpDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveLen(len(bundle.Spec.Resources)))
			for _, file := range files {
				Expect(file).To(bePresentInBundleResources(bundle.Spec.Resources))
			}
			// explicitly check for dependency files
			Expect(path.Join(tmpDir, "Chart.lock")).To(bePresentInBundleResources(bundle.Spec.Resources))
			Expect(path.Join(tmpDir, "charts/config-chart-0.1.0.tgz")).To(bePresentInBundleResources(bundle.Spec.Resources))
		})
	})

	When("folder contains helm chart with fleet.yaml, disableDependencyUpdate is not set", func() {
		BeforeEach(func() {
			name = "simple-with-fleet-yaml"
		})

		It("creates a Bundle with all the resources, including the dependencies", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			// files expected are:
			// Chart.yaml + values.yaml + templates/configmap.yaml +
			// Chart.lock + charts/config-chart-0.1.0.tgz
			Expect(bundle.Spec.Resources).To(HaveLen(5))
			files, err := getAllFilesInDir(tmpDirRel)
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveLen(len(bundle.Spec.Resources) + 1))
			for _, file := range files {
				if strings.HasSuffix(file, "fleet.yaml") {
					continue
				}
				Expect(file).To(bePresentInBundleResources(bundle.Spec.Resources))
			}
			// explicitly check for dependency files
			Expect(path.Join(tmpDirRel, "Chart.lock")).To(bePresentInBundleResources(bundle.Spec.Resources))
			Expect(path.Join(tmpDirRel, "charts/config-chart-0.1.0.tgz")).To(bePresentInBundleResources(bundle.Spec.Resources))
		})
	})

	When("folder contains helm chart with fleet.yaml, disableDependencyUpdate is set to true", func() {
		BeforeEach(func() {
			name = "simple-with-fleet-yaml-no-deps"
		})

		It("creates a Bundle with all the resources, dependencies should not be in the bundle", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			// files expected are:
			// Chart.yaml + values.yaml + templates/configmap.yaml
			Expect(bundle.Spec.Resources).To(HaveLen(3))
			files, err := getAllFilesInDir(tmpDirRel)
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveLen(len(bundle.Spec.Resources) + 1))
			for _, file := range files {
				if strings.HasSuffix(file, "fleet.yaml") {
					continue
				}
				Expect(file).To(bePresentInBundleResources(bundle.Spec.Resources))
			}
			// explicitly check for dependency files (they should not exist in the file system nor in bundle resources)
			Expect(path.Join(tmpDirRel, "Chart.lock")).NotTo(BeAnExistingFile())
			Expect(path.Join(tmpDirRel, "Chart.lock")).NotTo(bePresentOnlyInBundleResources(bundle.Spec.Resources))
			Expect(path.Join(tmpDirRel, "charts/config-chart-0.1.0.tgz")).NotTo(BeAnExistingFile())
			Expect(path.Join(tmpDirRel, "charts/config-chart-0.1.0.tgz")).NotTo(bePresentOnlyInBundleResources(bundle.Spec.Resources))
		})
	})

	When("folder contains fleet.yaml defining a remote chart which has dependencies", func() {
		BeforeEach(func() {
			name = "remote-chart-with-deps"
		})

		It("creates a Bundle with all the resources, dependencies should be in the bundle", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			// expected files are:
			// Chart.yaml + values.yaml + templates/configmap.yaml +
			// Chart.lock + charts/config-chart-0.1.0.tgz
			Expect(bundle.Spec.Resources).To(HaveLen(5))
			Expect(path.Join(tmpDirRel, "fleet.yaml")).NotTo(bePresentInBundleResources(bundle.Spec.Resources))
			// as files were unpacked from the downloaded chart we can't just
			// list the files in the original folder and compare.
			// Files are only located in the bundle resources
			Expect("Chart.yaml").To(bePresentOnlyInBundleResources(bundle.Spec.Resources))
			Expect("values.yaml").To(bePresentOnlyInBundleResources(bundle.Spec.Resources))
			Expect("templates/configmap.yaml").To(bePresentOnlyInBundleResources(bundle.Spec.Resources))
			Expect("Chart.lock").To(bePresentOnlyInBundleResources(bundle.Spec.Resources))
			Expect("charts/config-chart-0.1.0.tgz").To(bePresentOnlyInBundleResources(bundle.Spec.Resources))
		})
	})

	When("folder contains fleet.yaml defining a remote chart which has dependencies, and disableDependencyUpdate is set", func() {
		BeforeEach(func() {
			name = "remote-chart-with-deps-disabled"
		})

		It("creates a Bundle with all the resources, dependencies should not be in the bundle", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			// expected files are:
			// Chart.yaml + values.yaml + templates/configmap.yaml
			Expect(bundle.Spec.Resources).To(HaveLen(3))
			Expect(path.Join(tmpDirRel, "fleet.yaml")).NotTo(bePresentInBundleResources(bundle.Spec.Resources))
			// as files were unpacked from the downloaded chart we can't just
			// list the files in the original folder and compare.
			// Files are only located in the bundle resources
			Expect("Chart.yaml").To(bePresentOnlyInBundleResources(bundle.Spec.Resources))
			Expect("values.yaml").To(bePresentOnlyInBundleResources(bundle.Spec.Resources))
			Expect("templates/configmap.yaml").To(bePresentOnlyInBundleResources(bundle.Spec.Resources))

			// explicitly check for dependency files (they should not exist in the file system nor in bundle resources)
			Expect(path.Join(tmpDirRel, "Chart.lock")).NotTo(BeAnExistingFile())
			Expect(path.Join(tmpDirRel, "Chart.lock")).NotTo(bePresentOnlyInBundleResources(bundle.Spec.Resources))
			Expect(path.Join(tmpDirRel, "charts/config-chart-0.1.0.tgz")).NotTo(BeAnExistingFile())
			Expect(path.Join(tmpDirRel, "charts/config-chart-0.1.0.tgz")).NotTo(bePresentOnlyInBundleResources(bundle.Spec.Resources))
		})
	})

	When("folder contains multiple charts with different options", func() {
		BeforeEach(func() {
			name = "multi-chart"
		})

		It("creates Bundles with the corresponding resources, depending if they should update dependencies", func() {
			bundle, err := cli.GetBundleListFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(HaveLen(3))
			remoteDepl := bundle[0]
			simpleDepl := bundle[1]
			noDepsDepl := bundle[2]

			// remoteDepl corresponds to multi-chart/remote-chart-with-deps
			// expected files are:
			// Chart.yaml + values.yaml + templates/configmap.yaml + Chart.lock + charts/config-chart-0.1.0.tgz
			Expect(remoteDepl.Spec.Resources).To(HaveLen(5))
			Expect(path.Join(tmpDirRel, "remote-chart-with-deps", "fleet.yaml")).NotTo(bePresentInBundleResources(remoteDepl.Spec.Resources))
			// as files were unpacked from the downloaded chart we can't just
			// list the files in the original folder and compare.
			// Files are only located in the bundle resources
			Expect("Chart.yaml").To(bePresentOnlyInBundleResources(remoteDepl.Spec.Resources))
			Expect("values.yaml").To(bePresentOnlyInBundleResources(remoteDepl.Spec.Resources))
			Expect("templates/configmap.yaml").To(bePresentOnlyInBundleResources(remoteDepl.Spec.Resources))
			Expect("Chart.lock").To(bePresentOnlyInBundleResources(remoteDepl.Spec.Resources))
			Expect("charts/config-chart-0.1.0.tgz").To(bePresentOnlyInBundleResources(remoteDepl.Spec.Resources))

			// simpleDepl corresponds to multi-chart/simple-with-fleet-yaml
			// expected files are:
			// Chart.yaml + values.yaml + templates/configmap.yaml +
			// Chart.lock + charts/config-chart-0.1.0.tgz
			Expect(simpleDepl.Spec.Resources).To(HaveLen(5))
			files, err := getAllFilesInDir(path.Join(tmpDirRel, "simple-with-fleet-yaml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveLen(len(simpleDepl.Spec.Resources) + 1))
			for _, file := range files {
				if strings.HasSuffix(file, "fleet.yaml") {
					continue
				}
				Expect(file).To(bePresentInBundleResources(simpleDepl.Spec.Resources))
			}
			// explicitly check for dependency files
			Expect(path.Join(tmpDirRel, "simple-with-fleet-yaml", "Chart.lock")).To(bePresentInBundleResources(simpleDepl.Spec.Resources))
			Expect(path.Join(tmpDirRel, "simple-with-fleet-yaml", "charts/config-chart-0.1.0.tgz")).To(bePresentInBundleResources(simpleDepl.Spec.Resources))

			// noDepsDepl corresponds to multi-char/simple-with-fleet-yaml-no-deps
			// expected files are:
			// Chart.yaml +  values.yaml + templates/configmap.yaml
			Expect(noDepsDepl.Spec.Resources).To(HaveLen(3))
			files, err = getAllFilesInDir(path.Join(tmpDirRel, "simple-with-fleet-yaml-no-deps"))
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveLen(len(noDepsDepl.Spec.Resources) + 1))
			for _, file := range files {
				if strings.HasSuffix(file, "fleet.yaml") {
					continue
				}
				Expect(file).To(bePresentInBundleResources(noDepsDepl.Spec.Resources))
			}
			// explicitly check for dependency files (they should not exist in the file system nor in bundle resources)
			Expect(path.Join(tmpDirRel, "simple-with-fleet-yaml-no-deps", "Chart.lock")).NotTo(BeAnExistingFile())
			Expect(path.Join(tmpDirRel, "simple-with-fleet-yaml-no-deps", "Chart.lock")).NotTo(bePresentOnlyInBundleResources(noDepsDepl.Spec.Resources))
			Expect(path.Join(tmpDirRel, "simple-with-fleet-yaml-no-deps", "charts/config-chart-0.1.0.tgz")).NotTo(BeAnExistingFile())
			Expect(path.Join(tmpDirRel, "simple-with-fleet-yaml-no-deps", "charts/config-chart-0.1.0.tgz")).NotTo(bePresentOnlyInBundleResources(noDepsDepl.Spec.Resources))
		})
	})

	AfterEach(func() {
		err := repo.stopRepository()
		Expect(err).NotTo(HaveOccurred())
	})
})

func bePresentInBundleResources(expected interface{}) types.GomegaMatcher {
	return gcustom.MakeMatcher(func(path string) (bool, error) {
		resources, ok := expected.([]v1alpha1.BundleResource)
		if !ok {
			return false, fmt.Errorf("BePresentInBundleResources matcher expects []v1alpha1.BundleResource")
		}
		isPresent, err := cli.IsResourcePresentInBundle(path, resources)
		if err != nil {
			return false, fmt.Errorf("Failed to check for path in resources: %s", err.Error())
		}
		return isPresent, nil
	}).WithTemplate("Expected:\n{{.FormattedActual}}\n{{.To}} be present in \n{{format .Data 1}}").WithTemplateData(expected)
}

func bePresentOnlyInBundleResources(expected interface{}) types.GomegaMatcher {
	return gcustom.MakeMatcher(func(path string) (bool, error) {
		resources, ok := expected.([]v1alpha1.BundleResource)
		if !ok {
			return false, fmt.Errorf("bePresentOnlyInBundleResources matcher expects []v1alpha1.BundleResource")
		}
		found := false
		for _, resource := range resources {
			if strings.HasSuffix(resource.Name, path) {
				found = true
			}
		}
		return found, nil
	}).WithTemplate("Expected:\n{{.FormattedActual}}\n{{.To}} be present in \n{{format .Data 1}}").WithTemplateData(expected)
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
