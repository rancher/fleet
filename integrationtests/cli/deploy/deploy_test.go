package deploy_test

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"

	"github.com/onsi/gomega/gbytes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clihelper "github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/cli"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
		cmd.SetOut(buf)
		cmd.SetErr(buf)
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

	When("deploying on top of a release in `pending-install` status", func() {
		BeforeEach(func() {
			// Create release v1 (deployed)
			releaseV1 := map[string]interface{}{
				"name":      "testbundle-simple-chart",
				"version":   1,
				"namespace": namespace,
				"info": map[string]interface{}{
					"status": "deployed",
				},
				"chart": map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":    "testbundle-simple-chart",
						"version": "0.1.0",
					},
				},
				"config":   map[string]interface{}{},
				"manifest": "",
				"labels":   map[string]string{},
			}
			releaseV1JSON, err := json.Marshal(releaseV1)
			Expect(err).ToNot(HaveOccurred())

			var gzBufV1 bytes.Buffer
			gzWriterV1 := gzip.NewWriter(&gzBufV1)
			_, err = gzWriterV1.Write(releaseV1JSON)
			Expect(err).ToNot(HaveOccurred())
			Expect(gzWriterV1.Close()).ToNot(HaveOccurred())

			relV1 := make([]byte, base64.StdEncoding.EncodedLen(gzBufV1.Len()))
			base64.StdEncoding.Encode(relV1, gzBufV1.Bytes())

			releaseSecretV1 := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sh.helm.release.v1.testbundle-simple-chart.v1",
					Namespace: namespace,
					Labels: map[string]string{
						"name":    "testbundle-simple-chart",
						"owner":   "helm",
						"status":  "deployed",
						"version": "1",
					},
				},
				Type: "helm.sh/release.v1",
				Data: map[string][]byte{"release": relV1},
			}

			// Create release v2 (pending-install)
			releaseV2 := map[string]interface{}{
				"name":      "testbundle-simple-chart",
				"version":   2,
				"namespace": namespace,
				"info": map[string]interface{}{
					"status": "pending-install",
				},
				"chart": map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":    "testbundle-simple-chart",
						"version": "0.1.0",
					},
				},
				"config":   map[string]interface{}{},
				"manifest": "",
				"labels":   map[string]string{},
			}
			releaseV2JSON, err := json.Marshal(releaseV2)
			Expect(err).ToNot(HaveOccurred())

			var gzBufV2 bytes.Buffer
			gzWriterV2 := gzip.NewWriter(&gzBufV2)
			_, err = gzWriterV2.Write(releaseV2JSON)
			Expect(err).ToNot(HaveOccurred())
			Expect(gzWriterV2.Close()).ToNot(HaveOccurred())

			relV2 := make([]byte, base64.StdEncoding.EncodedLen(gzBufV2.Len()))
			base64.StdEncoding.Encode(relV2, gzBufV2.Bytes())

			releaseSecretV2 := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sh.helm.release.v1.testbundle-simple-chart.v2",
					Namespace: namespace,
					Labels: map[string]string{
						"name":    "testbundle-simple-chart",
						"owner":   "helm",
						"status":  "pending-install",
						"version": "2",
					},
				},
				Type: "helm.sh/release.v1",
				Data: map[string][]byte{"release": relV2},
			}

			Expect(k8sClient.Create(ctx, &releaseSecretV1)).ToNot(HaveOccurred())
			Expect(k8sClient.Create(ctx, &releaseSecretV2)).ToNot(HaveOccurred())

			// check that the secret was created using List with a label selector
			// that uses owner=helm and name=testbundle-simple-chart
			Eventually(func(g Gomega) {
				secrets := &corev1.SecretList{}
				err := k8sClient.List(ctx, secrets, &client.ListOptions{
					Namespace: namespace,
					LabelSelector: labels.SelectorFromSet(map[string]string{
						"name":  "testbundle-simple-chart",
						"owner": "helm",
					}),
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(len(secrets.Items)).To(BeNumerically(">=", 2))
			}, "5s", "500ms").Should(Succeed())

			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd.yaml",
				"--namespace", namespace,
			}

			DeferCleanup(func() {
				err := k8sClient.Delete(ctx, &releaseSecretV1)
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).ToNot(HaveOccurred())
				}
				err = k8sClient.Delete(ctx, &releaseSecretV2)
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).ToNot(HaveOccurred())
				}
			})
		})

		It("upgrades an orphaned pending-install release while preserving history", func() {
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())

			By("creating resources")
			Expect(buf).To(gbytes.Say("- apiVersion: v1"))
			Expect(buf).To(gbytes.Say("  data:"))
			Expect(buf).To(gbytes.Say("    name: example-value"))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "test-simple-chart-config"}, cm)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	When("deploying on top of a release in `pending-install` status with no previous version", func() {
		BeforeEach(func() {
			// Create ONLY release v1 (pending-install) - no v0 deployed version exists
			// This simulates a failed initial install or lost history scenario
			releaseV1 := map[string]interface{}{
				"name":      "testbundle-simple-chart",
				"version":   1,
				"namespace": namespace,
				"info": map[string]interface{}{
					"status": "pending-install",
				},
				"chart": map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":    "testbundle-simple-chart",
						"version": "0.1.0",
					},
				},
				"config":   map[string]interface{}{},
				"manifest": "",
				"labels":   map[string]string{},
			}
			releaseV1JSON, err := json.Marshal(releaseV1)
			Expect(err).ToNot(HaveOccurred())

			var gzBufV1 bytes.Buffer
			gzWriterV1 := gzip.NewWriter(&gzBufV1)
			_, err = gzWriterV1.Write(releaseV1JSON)
			Expect(err).ToNot(HaveOccurred())
			Expect(gzWriterV1.Close()).ToNot(HaveOccurred())

			relV1 := make([]byte, base64.StdEncoding.EncodedLen(gzBufV1.Len()))
			base64.StdEncoding.Encode(relV1, gzBufV1.Bytes())

			releaseSecretV1 := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sh.helm.release.v1.testbundle-simple-chart.v1",
					Namespace: namespace,
					Labels: map[string]string{
						"name":    "testbundle-simple-chart",
						"owner":   "helm",
						"status":  "pending-install",
						"version": "1",
					},
				},
				Type: "helm.sh/release.v1",
				Data: map[string][]byte{"release": relV1},
			}

			// Clean up any existing secrets from previous tests to prevent conflicts
			// Previous tests might have created multiple versions (v1, v2, v3, etc.)
			secrets := &corev1.SecretList{}
			err = k8sClient.List(ctx, secrets, &client.ListOptions{
				Namespace: namespace,
				LabelSelector: labels.SelectorFromSet(map[string]string{
					"name":  "testbundle-simple-chart",
					"owner": "helm",
				}),
			})
			Expect(err).ToNot(HaveOccurred())
			for _, secret := range secrets.Items {
				_ = k8sClient.Delete(ctx, &secret)
			}

			// Wait for all secrets to be deleted before creating the new one
			Eventually(func(g Gomega) {
				secrets := &corev1.SecretList{}
				err := k8sClient.List(ctx, secrets, &client.ListOptions{
					Namespace: namespace,
					LabelSelector: labels.SelectorFromSet(map[string]string{
						"name":  "testbundle-simple-chart",
						"owner": "helm",
					}),
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secrets.Items).To(BeEmpty())
			}, "5s", "500ms").Should(Succeed())

			// Create only the pending-install secret
			Expect(k8sClient.Create(ctx, &releaseSecretV1)).ToNot(HaveOccurred())

			// Verify the secret was created
			Eventually(func(g Gomega) {
				secrets := &corev1.SecretList{}
				err := k8sClient.List(ctx, secrets, &client.ListOptions{
					Namespace: namespace,
					LabelSelector: labels.SelectorFromSet(map[string]string{
						"name":  "testbundle-simple-chart",
						"owner": "helm",
					}),
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secrets.Items).To(HaveLen(1))
			}, "5s", "500ms").Should(Succeed())

			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd.yaml",
				"--namespace", namespace,
			}

			DeferCleanup(func() {
				// Clean up all helm release secrets created during this test
				secrets := &corev1.SecretList{}
				err := k8sClient.List(ctx, secrets, &client.ListOptions{
					Namespace: namespace,
					LabelSelector: labels.SelectorFromSet(map[string]string{
						"name":  "testbundle-simple-chart",
						"owner": "helm",
					}),
				})
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).ToNot(HaveOccurred())
				}
				for _, secret := range secrets.Items {
					_ = k8sClient.Delete(ctx, &secret)
				}
			})
		})

		It("upgrades an orphaned pending-install release while preserving history", func() {
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())

			By("creating resources")
			Expect(buf).To(gbytes.Say("- apiVersion: v1"))
			Expect(buf).To(gbytes.Say("  data:"))
			Expect(buf).To(gbytes.Say("    name: example-value"))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "test-simple-chart-config"}, cm)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the release was upgraded (not deleted and reinstalled)")
			secrets := &corev1.SecretList{}
			err = k8sClient.List(ctx, secrets, &client.ListOptions{
				Namespace: namespace,
				LabelSelector: labels.SelectorFromSet(map[string]string{
					"name":  "testbundle-simple-chart",
					"owner": "helm",
				}),
			})
			Expect(err).NotTo(HaveOccurred())
			// Should have v1 (original pending-install) + v2 (successful upgrade)
			Expect(len(secrets.Items)).To(BeNumerically(">=", 2))

			// Verify the latest release is deployed
			var latestVersion int
			for _, secret := range secrets.Items {
				if secret.Labels["status"] == "deployed" {
					latestVersion++
				}
			}
			Expect(latestVersion).To(Equal(1), "should have exactly one deployed release")
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
