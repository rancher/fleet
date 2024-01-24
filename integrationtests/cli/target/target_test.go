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

	When("No cluster in namespace", func() {
		BeforeEach(func() {
			args = []string{"--bundle-file", clihelper.AssetsPath + "bundle/bundle.yaml"}
		})

		It("prints a manifest only", func() {
			args = append(args, "--list-inputs")
			buf, errBuf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("null"))
			Expect(buf).To(gbytes.Say("kind: Content"))
			Expect(buf.Contents()).NotTo(ContainSubstring("kind: BundleDeployment"))
		})
	})

	When("Matching a cluster in namespace", func() {
		BeforeEach(func() {
			args = []string{
				"--bundle-file", clihelper.AssetsPath + "bundle/bundle.yaml",
				"--namespace", namespace,
				"--list-inputs",
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
			Expect(buf).To(gbytes.Say("kind: Content"))
			Expect(buf).To(gbytes.Say("kind: BundleDeployment"))
		})
	})
})
