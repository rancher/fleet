package apply

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
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
			options = apply.Options{Output: gbytes.NewBuffer()}
			dirs = []string{cli.AssetsPath + "simple"}
		})

		It("then a Bundle is created with all the resources and keepResources is false", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(options.Output)
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
			options = apply.Options{Output: gbytes.NewBuffer()}
			dirs = []string{cli.AssetsPath + "nested_simple"}
		})

		It("then a Bundle is created with all the resources", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(options.Output)
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
			options = apply.Options{Output: gbytes.NewBuffer()}
			dirs = []string{cli.AssetsPath + "nested_two_levels"}
		})

		It("then a Bundle is created with all the resources", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(options.Output)
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
			options = apply.Options{Output: gbytes.NewBuffer()}
			dirs = []string{cli.AssetsPath + "nested_multiple"}
		})

		It("then 3 Bundles are created with the relevant resources", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleListFromOutput(options.Output)
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
			options = apply.Options{Output: gbytes.NewBuffer()}
			dirs = []string{cli.AssetsPath + "nested_mixed_two_levels"}
		})

		It("then Bundles are created with all the resources", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleListFromOutput(options.Output)
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
			options = apply.Options{Output: gbytes.NewBuffer()}
			dirs = []string{cli.AssetsPath + "keep_resources"}
		})

		It("then a Bundle is created with keepResources", func() {
			Eventually(func() bool {
				bundle, err := cli.GetBundleFromOutput(options.Output)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Spec.KeepResources
			}).Should(BeTrue())
		})
	})

	When("non-helm type bundle uses helm options in fleet.yaml", func() {
		When("passes along enabled helm options", func() {
			BeforeEach(func() {
				name = "helm_options_enabled"
				options = apply.Options{Output: gbytes.NewBuffer()}
				dirs = []string{cli.AssetsPath + name}
			})

			It("publishes the flag in the bundle options", func() {
				Eventually(func() bool {
					bundle, err := cli.GetBundleFromOutput(options.Output)
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
				options = apply.Options{Output: gbytes.NewBuffer()}
				dirs = []string{cli.AssetsPath + name}
			})

			It("publishes the flag in the bundle options", func() {
				Eventually(func() bool {
					bundle, err := cli.GetBundleFromOutput(options.Output)
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
				options = apply.Options{Output: gbytes.NewBuffer()}
				dirs = []string{cli.AssetsPath + name}
			})

			It("publishes the flag in the bundle options", func() {
				Eventually(func() bool {
					bundle, err := cli.GetBundleFromOutput(options.Output)
					Expect(err).NotTo(HaveOccurred())
					return bundle.Spec.Helm.TakeOwnership &&
						bundle.Spec.Helm.ReleaseName == "kustomize"
				}).Should(BeTrue())
			})
		})
	})
})
