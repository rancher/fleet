package bundlediff

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/cmd/cli"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Fleet bundlediff", func() {
	var (
		bundleDeploymentName string
		bundleName           string
	)

	act := func(args []string, ns string) (*gbytes.Buffer, *gbytes.Buffer, error) {
		cmd := cli.NewBundleDiff()
		fmt.Printf("Using kubeconfig: %s\n", kubeconfigPath)
		if ns != "" {
			args = append([]string{"--kubeconfig", kubeconfigPath, "-n", ns}, args...)
		} else {
			args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
		}
		cmd.SetArgs(args)

		buf := gbytes.NewBuffer()
		errBuf := gbytes.NewBuffer()
		cmd.SetOut(buf)
		cmd.SetErr(errBuf)

		err := cmd.Execute()
		return buf, errBuf, err
	}

	BeforeEach(func() {
		namespace = "default"
	})

	When("a BundleDeployment has modified resources", func() {
		BeforeEach(func() {
			bundleDeploymentName = "test-bd-modified"
			bundleName = "test-bundle-modified"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleDeploymentName,
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd)).ToNot(HaveOccurred())

			bd.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "Service",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "my-service",
					Patch:      `{"spec":{"selector":{"app":"modified"}}}`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd)
			})
		})

		It("should display the modified resources in text format", func() {
			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("BundleDeployment: default/test-bd-modified"))
			Expect(output).To(ContainSubstring("Bundle: test-bundle-modified"))
			Expect(output).To(ContainSubstring("Modified Resources:"))
			Expect(output).To(ContainSubstring("Service.v1 test-ns/my-service"))
			Expect(output).To(ContainSubstring("Status: Modified"))
			Expect(output).To(ContainSubstring(`"spec"`))
		})

		It("should display the modified resources in JSON format", func() {
			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName, "--json"}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var output cli.BundleDiffOutput
			err = json.Unmarshal(buf.Contents(), &output)
			Expect(err).NotTo(HaveOccurred())

			Expect(output.Namespace).To(Equal(namespace))
			Expect(output.BundleDeploymentDiffs).To(HaveLen(1))
			Expect(output.BundleDeploymentDiffs[0].BundleDeploymentName).To(Equal(bundleDeploymentName))
			Expect(output.BundleDeploymentDiffs[0].ModifiedResources).To(HaveLen(1))
			Expect(output.BundleDeploymentDiffs[0].ModifiedResources[0].Name).To(Equal("my-service"))
		})
	})

	When("a BundleDeployment has missing resources", func() {
		BeforeEach(func() {
			bundleDeploymentName = "test-bd-missing"
			bundleName = "test-bundle-missing"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleDeploymentName,
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd)).ToNot(HaveOccurred())

			bd.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "ConfigMap",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "missing-cm",
					Create:     true,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd)
			})
		})

		It("should indicate missing resources", func() {
			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("ConfigMap.v1 test-ns/missing-cm"))
			Expect(output).To(ContainSubstring("Status: Resource is missing (should be created)"))
		})
	})

	When("a BundleDeployment has extra resources", func() {
		BeforeEach(func() {
			bundleDeploymentName = "test-bd-extra"
			bundleName = "test-bundle-extra"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleDeploymentName,
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd)).ToNot(HaveOccurred())

			bd.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "Secret",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "extra-secret",
					Delete:     true,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd)
			})
		})

		It("should indicate extra resources", func() {
			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("Secret.v1 test-ns/extra-secret"))
			Expect(output).To(ContainSubstring("Status: Extra resource (should be deleted)"))
		})
	})

	When("a BundleDeployment has non-ready resources", func() {
		BeforeEach(func() {
			bundleDeploymentName = "test-bd-nonready"
			bundleName = "test-bundle-nonready"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleDeploymentName,
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd)).ToNot(HaveOccurred())

			bd.Status.NonReadyStatus = []fleet.NonReadyStatus{
				{
					Kind:       "Deployment",
					APIVersion: "apps/v1",
					Namespace:  "test-ns",
					Name:       "my-deployment",
					Summary: summary.Summary{
						State: "NotReady",
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd)
			})
		})

		It("should display non-ready resources", func() {
			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("Non-Ready Resources:"))
			Expect(output).To(ContainSubstring("Deployment.apps/v1 test-ns/my-deployment"))
		})

		It("should include in --bundle view when only non-ready resources exist", func() {
			buf, _, err := act([]string{"--bundle", bundleName}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring(bundleDeploymentName))
			Expect(output).To(ContainSubstring("Non-Ready Resources"))
			Expect(output).To(ContainSubstring("Deployment.apps/v1 test-ns/my-deployment"))
		})

		It("should include in default grouped view when only non-ready resources exist", func() {
			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("Bundle: " + bundleName))
			Expect(output).To(ContainSubstring(bundleDeploymentName))
			Expect(output).To(ContainSubstring("Non-Ready Resources"))
		})
	})

	When("multiple BundleDeployments have diffs for a Bundle", func() {
		BeforeEach(func() {
			bundleName = "test-bundle"

			bd1 := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bd-1",
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd1)).ToNot(HaveOccurred())

			bd1.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "Service",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "service-1",
					Patch:      `{"spec":{"type":"NodePort"}}`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd1)).ToNot(HaveOccurred())

			bd2 := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bd-2",
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd2)).ToNot(HaveOccurred())

			bd2.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "ConfigMap",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "config-2",
					Patch:      `{"data":{"key":"newvalue"}}`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd2)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd1)
				_ = k8sClient.Delete(ctx, bd2)
			})
		})

		It("should display diffs for all BundleDeployments of the Bundle", func() {
			buf, _, err := act([]string{"--bundle", bundleName}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("test-bd-1"))
			Expect(output).To(ContainSubstring("test-bd-2"))
			Expect(output).To(ContainSubstring("service-1"))
			Expect(output).To(ContainSubstring("config-2"))
		})

		It("should output bundle diffs in JSON format", func() {
			buf, _, err := act([]string{"--bundle", bundleName, "--json"}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var output cli.BundleDiffOutput
			err = json.Unmarshal(buf.Contents(), &output)
			Expect(err).NotTo(HaveOccurred())

			Expect(output.BundleName).To(Equal(bundleName))
			Expect(output.BundleDeploymentDiffs).To(HaveLen(2))
		})
	})

	When("no BundleDeployments have diffs", func() {
		BeforeEach(func() {
			bundleDeploymentName = "test-bd-clean"
			bundleName = "test-bundle-clean"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleDeploymentName,
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
				Status: fleet.BundleDeploymentStatus{
					// No modified or non-ready resources
				},
			}
			Expect(k8sClient.Create(ctx, bd)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd)
			})
		})

		It("should indicate no modified resources found", func() {
			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("No modified resources found"))
		})

		It("should output empty array in JSON format", func() {
			buf, _, err := act([]string{"--json"}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var output cli.BundleDiffOutput
			err = json.Unmarshal(buf.Contents(), &output)
			Expect(err).NotTo(HaveOccurred())

			Expect(output.BundleDeploymentDiffs).To(BeEmpty())
		})
	})

	When("default view with multiple bundles having diffs", func() {
		BeforeEach(func() {
			bd1 := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app-cluster1",
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": "my-app",
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd1)).ToNot(HaveOccurred())

			bd1.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "ConfigMap",
					APIVersion: "v1",
					Namespace:  "app-ns",
					Name:       "app-config",
					Patch:      `{"data":{"key":"value"}}`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd1)).ToNot(HaveOccurred())

			bd2 := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app-cluster2",
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": "my-app",
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd2)).ToNot(HaveOccurred())

			bd2.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "Service",
					APIVersion: "v1",
					Namespace:  "app-ns",
					Name:       "app-svc",
					Patch:      `{"spec":{"type":"LoadBalancer"}}`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd2)).ToNot(HaveOccurred())

			bd3 := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-cluster1",
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": "other-app",
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd3)).ToNot(HaveOccurred())

			bd3.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "Deployment",
					APIVersion: "apps/v1",
					Namespace:  "default",
					Name:       "web",
					Patch:      `{"spec":{"replicas":3}}`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd3)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd1)
				_ = k8sClient.Delete(ctx, bd2)
				_ = k8sClient.Delete(ctx, bd3)
			})
		})

		It("should group by bundle and show counts", func() {
			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("Bundle: my-app"))
			Expect(output).To(ContainSubstring("BundleDeployments with diffs: 2"))
			Expect(output).To(ContainSubstring("Bundle: other-app"))
			Expect(output).To(ContainSubstring("BundleDeployments with diffs: 1"))
			Expect(output).To(ContainSubstring("app-cluster1"))
			Expect(output).To(ContainSubstring("app-cluster2"))
			Expect(output).To(ContainSubstring("other-cluster1"))
		})

		It("should output grouped bundles in JSON format", func() {
			buf, _, err := act([]string{"--json"}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var output cli.BundleDiffOutput
			err = json.Unmarshal(buf.Contents(), &output)
			Expect(err).NotTo(HaveOccurred())

			Expect(output.BundleDeploymentDiffs).To(HaveLen(3))

			bundleNames := make(map[string]int)
			for _, diff := range output.BundleDeploymentDiffs {
				bundleNames[diff.BundleName]++
			}
			Expect(bundleNames["my-app"]).To(Equal(2))
			Expect(bundleNames["other-app"]).To(Equal(1))
		})
	})

	When("BundleDeployment has both modified and non-ready resources", func() {
		BeforeEach(func() {
			bundleDeploymentName = "test-bd-mixed"
			bundleName = "test-bundle-mixed"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleDeploymentName,
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd)).ToNot(HaveOccurred())

			bd.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "ConfigMap",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "mixed-cm",
					Patch:      `{"data":{"updated":"true"}}`,
				},
			}
			bd.Status.NonReadyStatus = []fleet.NonReadyStatus{
				{
					Kind:       "Pod",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "mixed-pod",
					Summary: summary.Summary{
						State: "NotReady",
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd)
			})
		})

		It("should display both modified and non-ready resources", func() {
			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("Modified Resources:"))
			Expect(output).To(ContainSubstring("ConfigMap.v1 test-ns/mixed-cm"))
			Expect(output).To(ContainSubstring("Non-Ready Resources:"))
			Expect(output).To(ContainSubstring("Pod.v1 test-ns/mixed-pod"))
		})

		It("should include both in JSON output", func() {
			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName, "--json"}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var output cli.BundleDiffOutput
			err = json.Unmarshal(buf.Contents(), &output)
			Expect(err).NotTo(HaveOccurred())

			Expect(output.BundleDeploymentDiffs).To(HaveLen(1))
			diff := output.BundleDeploymentDiffs[0]
			Expect(diff.ModifiedResources).To(HaveLen(1))
			Expect(diff.NonReadyResources).To(HaveLen(1))
		})
	})

	When("BundleDeployment has no bundle label", func() {
		BeforeEach(func() {
			bundleDeploymentName = "test-bd-unlabeled"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleDeploymentName,
					Namespace: namespace,
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd)).ToNot(HaveOccurred())

			bd.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "Secret",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "unlabeled-secret",
					Patch:      `{"data":{"key":"value"}}`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd)
			})
		})

		It("should group under unknown bundle in default view", func() {
			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("Bundle: (unknown)"))
			Expect(output).To(ContainSubstring("test-bd-unlabeled"))
		})

		It("should work with bundledeployment flag", func() {
			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("BundleDeployment: default/test-bd-unlabeled"))
			Expect(output).To(ContainSubstring("Secret.v1 test-ns/unlabeled-secret"))
		})
	})

	When("BundleDeployments exist in multiple namespaces", func() {
		var namespace2 string
		var bd1, bd2 *fleet.BundleDeployment
		var ns1, ns2 *corev1.Namespace

		BeforeEach(func() {
			namespace = fmt.Sprintf("namespace-1-%d", time.Now().UnixNano())
			namespace2 = fmt.Sprintf("namespace-2-%d", time.Now().UnixNano())
			bundleName = "cross-namespace-bundle"

			ns1 = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}
			Expect(k8sClient.Create(ctx, ns1)).ToNot(HaveOccurred())

			ns2 = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace2,
				},
			}
			Expect(k8sClient.Create(ctx, ns2)).ToNot(HaveOccurred())

			bd1 = &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd-in-ns1",
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd1)).ToNot(HaveOccurred())

			bd1.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "ConfigMap",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "config-ns1",
					Patch:      `{"data":{"key":"value1"}}`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd1)).ToNot(HaveOccurred())

			bd2 = &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd-in-ns2",
					Namespace: namespace2,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd2)).ToNot(HaveOccurred())

			bd2.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "ConfigMap",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "config-ns2",
					Patch:      `{"data":{"key":"value2"}}`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd2)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd1)
				_ = k8sClient.Delete(ctx, bd2)
				_ = k8sClient.Delete(ctx, ns1)
				_ = k8sClient.Delete(ctx, ns2)
			})
		})

		It("should find BundleDeployments across all namespaces when no namespace specified", func() {
			fmt.Println("\n=== TEST: Cross-Namespace Search (No -n flag) ===")
			buf, _, err := act([]string{}, "")
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("bd-in-ns1"))
			Expect(output).To(ContainSubstring("bd-in-ns2"))
			Expect(output).To(ContainSubstring("config-ns1"))
			Expect(output).To(ContainSubstring("config-ns2"))
		})

		It("should only find BundleDeployments in specified namespace", func() {
			fmt.Println("\n=== TEST: Namespace-Restricted Search ===")
			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("bd-in-ns1"))
			Expect(output).To(ContainSubstring("config-ns1"))
			Expect(output).NotTo(ContainSubstring("bd-in-ns2"))
			Expect(output).NotTo(ContainSubstring("config-ns2"))
		})

		It("should find BundleDeployments from both namespaces in JSON format", func() {
			fmt.Println("\n=== TEST: Cross-Namespace JSON Output ===")
			buf, _, err := act([]string{"--json"}, "")
			Expect(err).NotTo(HaveOccurred())

			var output cli.BundleDiffOutput
			err = json.Unmarshal(buf.Contents(), &output)
			Expect(err).NotTo(HaveOccurred())

			Expect(output.BundleDeploymentDiffs).To(HaveLen(2))

			namespaces := make(map[string]bool)
			for _, diff := range output.BundleDeploymentDiffs {
				namespaces[diff.Namespace] = true
			}
			Expect(namespaces[namespace]).To(BeTrue())
			Expect(namespaces[namespace2]).To(BeTrue())
		})

		It("should find all BundleDeployments for a bundle across namespaces", func() {
			fmt.Println("\n=== TEST: Bundle Filter Across Namespaces ===")
			buf, _, err := act([]string{"--bundle", bundleName}, "")
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			Expect(output).To(ContainSubstring("Bundle: " + bundleName))
			Expect(output).To(ContainSubstring("BundleDeployments with diffs: 2"))
			Expect(output).To(ContainSubstring("bd-in-ns1"))
			Expect(output).To(ContainSubstring("bd-in-ns2"))
		})
	})

	When("using fleet-yaml output format", func() {
		BeforeEach(func() {
			bundleDeploymentName = "test-bd-fleet-yaml"
			bundleName = "test-bundle-fleet-yaml"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleDeploymentName,
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/bundle-name": bundleName,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-content:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd)).ToNot(HaveOccurred())

			bd.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "ConfigMap",
					APIVersion: "v1",
					Namespace:  "bundle-diffs-example",
					Name:       "app-config",
					Patch:      `[{"op":"remove","path":"/data"}]`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd)
			})
		})

		It("should output in fleet.yaml format", func() {
			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName, "--fleet-yaml"}, namespace)
			Expect(err).NotTo(HaveOccurred())

			expected, err := os.ReadFile(filepath.Join("testdata", "fleet-yaml-single.yaml"))
			Expect(err).NotTo(HaveOccurred())

			Expect(strings.TrimSpace(string(buf.Contents()))).To(Equal(strings.TrimSpace(string(expected))))
		})

		It("should handle multiple operations in fleet.yaml format", func() {
			bd := &fleet.BundleDeployment{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: bundleDeploymentName}, bd)).ToNot(HaveOccurred())

			bd.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "Deployment",
					APIVersion: "apps/v1",
					Namespace:  "test-ns",
					Name:       "my-deployment",
					Patch:      `[{"op":"replace","path":"/spec/replicas","value":3},{"op":"remove","path":"/spec/template/metadata/labels/old-label"}]`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName, "--fleet-yaml"}, namespace)
			Expect(err).NotTo(HaveOccurred())

			expected, err := os.ReadFile(filepath.Join("testdata", "fleet-yaml-multiple-ops.yaml"))
			Expect(err).NotTo(HaveOccurred())

			Expect(strings.TrimSpace(string(buf.Contents()))).To(Equal(strings.TrimSpace(string(expected))))
		})

		It("should output multiple resources in fleet.yaml format", func() {
			bd := &fleet.BundleDeployment{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: bundleDeploymentName}, bd)).ToNot(HaveOccurred())

			bd.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "ConfigMap",
					APIVersion: "v1",
					Namespace:  "ns1",
					Name:       "cm1",
					Patch:      `[{"op":"add","path":"/data/key1","value":"val1"}]`,
				},
				{
					Kind:       "Secret",
					APIVersion: "v1",
					Namespace:  "ns2",
					Name:       "secret1",
					Patch:      `[{"op":"remove","path":"/data/password"}]`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName, "--fleet-yaml"}, namespace)
			Expect(err).NotTo(HaveOccurred())

			expected, err := os.ReadFile(filepath.Join("testdata", "fleet-yaml-multiple-resources.yaml"))
			Expect(err).NotTo(HaveOccurred())

			Expect(strings.TrimSpace(string(buf.Contents()))).To(Equal(strings.TrimSpace(string(expected))))
		})

		It("should output empty when no modified resources with patches", func() {
			bd := &fleet.BundleDeployment{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: bundleDeploymentName}, bd)).ToNot(HaveOccurred())

			bd.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "ConfigMap",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "missing-cm",
					Create:     true,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName, "--fleet-yaml"}, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf.Contents()).To(BeEmpty())
		})

		It("should skip operations with empty op field", func() {
			bd := &fleet.BundleDeployment{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: bundleDeploymentName}, bd)).ToNot(HaveOccurred())

			bd.Status.ModifiedStatus = []fleet.ModifiedStatus{
				{
					Kind:       "ConfigMap",
					APIVersion: "v1",
					Namespace:  "test-ns",
					Name:       "empty-op-cm",
					Patch:      `[{"op":"","path":"/data/key"},{"op":"remove","path":"/data/valid"}]`,
				},
			}
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			buf, _, err := act([]string{"--bundle-deployment", bundleDeploymentName, "--fleet-yaml"}, namespace)
			Expect(err).NotTo(HaveOccurred())

			output := string(buf.Contents())
			// Should contain only the valid remove operation
			Expect(output).To(ContainSubstring("op: remove"))
			Expect(output).To(ContainSubstring("/data/valid"))
			// Should have only one operation
			Expect(strings.Count(output, "op:")).To(Equal(1))
		})
	})
})
