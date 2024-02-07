package target_test

import (
	"github.com/onsi/gomega/gbytes"

	clihelper "github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/cli"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Fleet CLI target", func() {
	var args []string

	act := func(args []string) (*gbytes.Buffer, *gbytes.Buffer, error) {
		cmd := cli.NewTarget()
		args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
		cmd.SetArgs(args)

		buf := gbytes.NewBuffer()
		errBuf := gbytes.NewBuffer()
		cmd.SetOut(buf)
		cmd.SetErr(errBuf)
		err := cmd.Execute()
		return buf, errBuf, err
	}

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		})).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			})).ToNot(HaveOccurred())
		})
	})

	When("input file parameter is missing", func() {
		It("prints the help", func() {
			buf, _, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf).To(gbytes.Say("Usage:"))
		})
	})

	When("Input file is missing", func() {
		BeforeEach(func() {
			args = []string{"--bundle-file", "/tmp/does-not-exist-bundle.yaml"}
		})

		It("prints an error", func() {
			_, errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("no such file or directory"))
		})
	})

	When("Input file is invalid", func() {
		BeforeEach(func() {
			args = []string{"--bundle-file", clihelper.AssetsPath + "helmrepository/config-chart-0.1.0.tgz"}
		})

		It("prints an error", func() {
			_, errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("error converting YAML to JSON"))
		})
	})

	When("Input file does not contain a bundle", func() {
		BeforeEach(func() {
			args = []string{"--bundle-file", clihelper.AssetsPath + "helmrepository/index.yaml"}
		})

		It("prints an error", func() {
			_, errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("bundle is empty"))
		})
	})

	When("No cluster in namespace", func() {
		BeforeEach(func() {
			args = []string{"--bundle-file", clihelper.AssetsPath + "bundle/bundle.yaml"}
		})

		It("prints a manifest only", func() {
			args = append(args, "--dump-input-list")
			buf, errBuf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("null"))
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: Content"))
			Expect(buf.Contents()).NotTo(ContainSubstring("kind: BundleDeployment"))
		})
	})

	When("Matching a cluster in namespace", func() {
		BeforeEach(func() {
			args = []string{
				"--bundle-file", clihelper.AssetsPath + "bundle/bundle.yaml",
				"--namespace", namespace,
				"--dump-input-list",
			}

			err := k8sClient.Create(ctx, &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local",
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("prints a manifest and bundledeployment", func() {
			buf, errBuf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(errBuf.Contents())).To(ContainSubstring("- Bundle:"), "cannot list inputs")
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: Content"))
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: BundleDeployment"))
		})
	})

	When("Matching multiple clusters in namespace", func() {
		BeforeEach(func() {
			args = []string{
				"--bundle-file", clihelper.AssetsPath + "bundle/bundle-all.yaml",
				"--namespace", namespace,
				"--dump-input-list",
			}

			err := k8sClient.Create(ctx, &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local",
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Create(ctx, &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("prints a manifest and bundledeployment", func() {
			buf, errBuf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(errBuf.Contents())).To(ContainSubstring("- Bundle:"), "cannot list inputs")
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: Content"))
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: BundleDeployment"))
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: BundleDeployment"))
		})
	})
})
