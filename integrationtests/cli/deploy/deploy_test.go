package deploy_test

import (
	"github.com/onsi/gomega/gbytes"

	clihelper "github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/cli"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Fleet CLI Deploy", func() {
	var args []string

	act := func(args []string) (*gbytes.Buffer, error) {
		cmd := cli.NewDeploy()
		args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
		cmd.SetArgs(args)

		buf := gbytes.NewBuffer()
		cmd.SetOutput(buf)
		err := cmd.Execute()
		return buf, err
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
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf).To(gbytes.Say("Usage:"))
		})
	})

	When("Input file is missing", func() {
		BeforeEach(func() {
			args = []string{"--input-file", "/tmp/does-not-exist-bundle.yaml"}
		})

		It("prints an error", func() {
			errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("no such file or directory"))
		})
	})

	When("Input file is invalid", func() {
		BeforeEach(func() {
			args = []string{"--input-file", clihelper.AssetsPath + "helmrepository/config-chart-0.1.0.tgz"}
		})

		It("prints an error", func() {
			errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("yaml: control characters are not allowed"))
		})
	})

	When("Input file does not contain a content resource", func() {
		BeforeEach(func() {
			args = []string{"--input-file", clihelper.AssetsPath + "bundledeployment/bd-only.yaml"}
		})

		It("prints an error", func() {
			errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("failed to read content resource from file"))
		})
	})

	When("Input file does not contain a bundledeployment resource", func() {
		BeforeEach(func() {
			args = []string{"--input-file", clihelper.AssetsPath + "bundledeployment/content.yaml"}
		})

		It("prints an error", func() {
			errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("failed to read bundledeployment"))
		})
	})

	When("Deploying to a cluster", func() {
		BeforeEach(func() {
			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd.yaml",
			}
		})

		It("creates resources", func() {
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf).To(gbytes.Say("- apiVersion: v1"))
			Expect(buf).To(gbytes.Say("  data:"))
			Expect(buf).To(gbytes.Say("    name: example-value"))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "test-simple-chart-config"}, cm)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	When("Specifying a namespace", func() {
		BeforeEach(func() {
			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd.yaml",
				"--namespace", namespace,
			}
		})

		It("creates resources", func() {
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf).To(gbytes.Say("- apiVersion: v1"))
			Expect(buf).To(gbytes.Say("  data:"))
			Expect(buf).To(gbytes.Say("    name: example-value"))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "test-simple-chart-config"}, cm)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	When("Printing results with --dry-run", func() {
		BeforeEach(func() {
			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd.yaml",
				"--dry-run",
			}
		})

		It("prints a manifest and bundledeployment", func() {
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf.Contents()).To(And(
				ContainSubstring("- apiVersion: v1"),
				ContainSubstring("ConfigMap"),
				ContainSubstring("  data:"),
				ContainSubstring("    name: example-value"),
				ContainSubstring("ServiceAccount"),
				ContainSubstring("helm.sh/hook"),
				ContainSubstring("some-operator"),
			))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "test-simple-chart-config"}, cm)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	When("Printing results with --dry-run where the chart specifies kubeVersion >= 1.26.0", func() {
		BeforeEach(func() {
			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd-with-kube-version.yaml",
				"--dry-run",
				"--kube-version", "v1.27.0",
			}
		})

		It("prints a manifest and bundledeployment", func() {
			buf, err := act(args)
			Expect(err).ToNot(HaveOccurred())
			Expect(buf).To(gbytes.Say("- apiVersion: v1"))
			Expect(buf).To(gbytes.Say("  data:"))
			Expect(buf).To(gbytes.Say("    name: example-value"))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "test-simple-chart-config"}, cm)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
})
