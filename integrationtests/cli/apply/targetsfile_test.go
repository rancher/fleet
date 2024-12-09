package apply

import (
	"encoding/json"
	"os"

	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Fleet apply targets", func() {
	var (
		dirs    []string
		options apply.Options
	)

	JustBeforeEach(func() {
		err := fleetApply("targets", dirs, options)
		Expect(err).NotTo(HaveOccurred())
	})

	When("Targets file is empty, and overrideTargets is not provided", func() {
		BeforeEach(func() {
			dirs = []string{cli.AssetsPath + "targets/simple"}
		})

		It("Bundle contains the default target", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(bundle.Spec.Targets)).To(Equal(1))
			Expect(bundle.Spec.Targets[0].ClusterGroup).To(Equal("default"))
			Expect(len(bundle.Spec.TargetRestrictions)).To(Equal(0))
		})
	})

	When("Targets file is empty, and overrideTargets is provided", func() {
		BeforeEach(func() {
			dirs = []string{cli.AssetsPath + "targets/override"}
		})

		It("Bundle contains targets and targetRestrictions from override", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(bundle.Spec.Targets)).To(Equal(1))
			Expect(bundle.Spec.Targets[0].ClusterName).To(Equal("overridden"))
			Expect(len(bundle.Spec.TargetRestrictions)).To(Equal(1))
			Expect(bundle.Spec.TargetRestrictions[0].ClusterName).To(Equal("overridden"))
		})
	})

	When("Targets file contains one target, and overrideTargets is not provided", func() {
		var (
			targets            []fleet.BundleTarget
			targetRestrictions []fleet.BundleTargetRestriction
		)

		BeforeEach(func() {
			targets = []fleet.BundleTarget{{Name: "target1", ClusterName: "test1"}}
			targetRestrictions = []fleet.BundleTargetRestriction{{Name: "target1", ClusterName: "test1"}}
			file := createTargetsFile(targets, targetRestrictions)
			options = apply.Options{TargetsFile: file.Name()}
			dirs = []string{cli.AssetsPath + "targets/simple"}
		})

		It("Bundle contains targets and targetRestrictions from the targets file", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle.Spec.Targets).To(Equal(targets))
			Expect(bundle.Spec.TargetRestrictions).To(Equal(targetRestrictions))
		})
	})

	When("Targets file contains one target, and overrideTargets is provided", func() {
		var (
			targets            []fleet.BundleTarget
			targetRestrictions []fleet.BundleTargetRestriction
		)

		BeforeEach(func() {
			targets = []fleet.BundleTarget{{Name: "target1", ClusterName: "test1"}}
			targetRestrictions = []fleet.BundleTargetRestriction{{Name: "target1", ClusterName: "test1"}}
			file := createTargetsFile(targets, targetRestrictions)
			options = apply.Options{TargetsFile: file.Name()}
			dirs = []string{cli.AssetsPath + "targets/override"}
		})

		It("Bundle contains targets and targetRestrictions from override", func() {
			bundle, err := cli.GetBundleFromOutput(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle.Spec.Targets).To(Not(Equal(targets)))
			Expect(bundle.Spec.TargetRestrictions).To(Not(Equal(targetRestrictions)))
			Expect(len(bundle.Spec.Targets)).To(Equal(1))
			Expect(bundle.Spec.Targets[0].ClusterName).To(Equal("overridden"))
			Expect(len(bundle.Spec.TargetRestrictions)).To(Equal(1))
			Expect(bundle.Spec.TargetRestrictions[0].ClusterName).To(Equal("overridden"))
		})
	})
})

func createTargetsFile(targets []fleet.BundleTarget, targetRestrictions []fleet.BundleTargetRestriction) *os.File {
	tmpDir := GinkgoT().TempDir()
	file, err := os.CreateTemp(tmpDir, "targets")
	Expect(err).NotTo(HaveOccurred())
	spec := &fleet.BundleSpec{
		BundleSpecBase: fleet.BundleSpecBase{
			Targets:            targets,
			TargetRestrictions: targetRestrictions,
		},
	}

	data, err := json.Marshal(spec)
	Expect(err).NotTo(HaveOccurred())

	_, err = file.Write(data)
	Expect(err).NotTo(HaveOccurred())

	return file
}
