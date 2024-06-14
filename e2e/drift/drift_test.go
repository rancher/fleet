package examples_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Drift", func() {
	var (
		asset      string
		k          kubectl.Command
		namespace  string
		bundleName string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		namespace = "drift"
	})

	JustBeforeEach(func() {
		out, err := k.Namespace(env.Namespace).Apply("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
		Eventually(func() bool {
			b := getBundle(bundleName, k)
			return b.Status.Summary.Ready == 1
		}).Should(BeTrue())
	})

	AfterEach(func() {
		out, err := k.Namespace(env.Namespace).Delete("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("Drift correction is not enabled", func() {
		BeforeEach(func() {
			asset = "drift/correction-disabled/gitrepo.yaml"
			bundleName = "drift-test-drift"
		})

		Context("Modifying externalName in service resource", func() {
			JustBeforeEach(func() {
				kw := k.Namespace(namespace)
				out, err := kw.Patch(
					"service", "nginx-service",
					"--type=json",
					"-p", `[{"op": "replace", "path": "/spec/externalName", "value": "modified"}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("Bundle is modified", func() {
				Eventually(func() bool {
					b := getBundle(bundleName, k)
					return b.Status.Summary.Modified == 1
				}).Should(BeTrue())
				By("Changes haven't been rolled back")
				kw := k.Namespace(namespace)
				out, _ := kw.Get("services", "nginx-service", "-o=json")
				var service corev1.Service
				_ = json.Unmarshal([]byte(out), &service)
				Expect(service.Spec.ExternalName).Should(Equal("modified"))
			})
		})
	})

	When("Drift correction is enabled without force", func() {
		BeforeEach(func() {
			asset = "drift/correction-enabled/gitrepo.yaml"
			bundleName = "drift-correction-test-drift"
		})
		Context("Modifying externalName in service", func() {
			JustBeforeEach(func() {
				kw := k.Namespace(namespace)
				out, err := kw.Patch(
					"service", "nginx-service",
					"--type=json",
					"-p", `[{"op": "replace", "path": "/spec/externalName", "value": "modified"}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("Drift is corrected", func() {
				Eventually(func() bool {
					b := getBundle(bundleName, k)
					return b.Status.Summary.Ready == 1
				}).Should(BeTrue())
				Eventually(func() bool {
					kw := k.Namespace(namespace)
					out, _ := kw.Get("services", "nginx-service", "-o=json")
					var service corev1.Service
					_ = json.Unmarshal([]byte(out), &service)
					return service.Spec.ExternalName == "nginx"
				}).Should(BeTrue())
			})
		})

		Context("Modifying image in deployment", func() {
			JustBeforeEach(func() {
				kw := k.Namespace(namespace)
				out, err := kw.Patch(
					"deployment", "nginx-deployment",
					"--type=json",
					"-p", `[{"op": "replace", "path": "/spec/template/spec/containers/0/image", "value": "nginx:modified"}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("Drift is corrected", func() {
				Eventually(func() bool {
					b := getBundle(bundleName, k)
					return b.Status.Summary.Ready == 1
				}).Should(BeTrue())
				Eventually(func() bool {
					kw := k.Namespace(namespace)
					out, _ := kw.Get("deployment", "nginx-deployment", "-o=json")
					var deployment appsv1.Deployment
					_ = json.Unmarshal([]byte(out), &deployment)
					return deployment.Spec.Template.Spec.Containers[0].Image == "nginx:1.14.2"
				}).Should(BeTrue())
			})
		})

		Context("Modifying data in configmap", func() {
			JustBeforeEach(func() {
				kw := k.Namespace(namespace)
				out, err := kw.Patch(
					"configmap", "configmap",
					"--type=json",
					"-p", `[{"op": "replace", "path": "data/foo", "value": "modified"}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("Drift is corrected", func() {
				Eventually(func() bool {
					b := getBundle(bundleName, k)
					return b.Status.Summary.Ready == 1
				}).Should(BeTrue())
				Eventually(func() bool {
					kw := k.Namespace(namespace)
					out, _ := kw.Get("configmap", "configmap", "-o=json")
					var configMap corev1.ConfigMap
					_ = json.Unmarshal([]byte(out), &configMap)
					return configMap.Data["foo"] == "bar"
				}).Should(BeTrue())
				Expect(func() string {
					kw := k.Namespace(namespace)
					n, _ := kw.Get("secrets", "--field-selector=type=helm.sh/release.v1", "-o=go-template='{{printf \"%d\\n\" (len  .items)}}'")
					return n
				}).Should(Equal("2"))
			})
		})

		// Helm rollback uses three-way merge by default (without force), which fails when trying to rollback a change made on an item in the ports array.
		Context("Modifying port in service", func() {
			JustBeforeEach(func() {
				kw := k.Namespace(namespace)
				out, err := kw.Patch(
					"service", "nginx-service",
					"--type=json",
					"-p", `[{"op": "replace", "path": "/spec/ports/0/port", "value": 1234}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("Status is modified", func() {
				Eventually(func() string {
					out, _ := k.Namespace(env.Namespace).Get("bundles", bundleName, "-o=jsonpath={.status.conditions[*].message}")
					return out
				}).Should(ContainSubstring(`error reconciling drift: cannot patch "nginx-service" with kind Service: Service "nginx-service" is invalid: spec.ports[1].name: Duplicate value: "http"]; service.v1 drift/nginx-service modified {"spec":{"ports":[{"name":"http","port":80,"protocol":"TCP","targetPort":"http-web-svc"},{"name":"http","port":1234,"protocol":"TCP","targetPort":"http-web-svc"}]}}`))
			})
		})
	})

	When("Drift correction is enabled with force", func() {
		BeforeEach(func() {
			asset = "drift/force/gitrepo.yaml"
			bundleName = "drift-force-test-drift"
		})

		//Helm rollback does a PUT to override all resources when --force is used.
		Context("Modifying port in service", func() {
			JustBeforeEach(func() {
				kw := k.Namespace(namespace)
				out, err := kw.Patch(
					"service", "nginx-service",
					"--type=json",
					"-p", `[{"op": "replace", "path": "/spec/ports/0/port", "value": 1234}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("Bundle Status is Ready, and changes are rolled back", func() {
				Eventually(func() bool {
					b := getBundle(bundleName, k)
					return b.Status.Summary.Ready == 1
				}).Should(BeTrue())
				Eventually(func() bool {
					kw := k.Namespace(namespace)
					out, _ := kw.Get("services", "nginx-service", "-o=json")
					var service corev1.Service
					_ = json.Unmarshal([]byte(out), &service)
					return service.Spec.Ports[0].Port == 80
				}).Should(BeTrue())
			})
		})
	})
})

func getBundle(bundleName string, k kubectl.Command) fleet.Bundle {
	out, _ := k.Namespace(env.Namespace).Get("bundles", bundleName, "-o=json")
	var bundle fleet.Bundle
	_ = json.Unmarshal([]byte(out), &bundle)

	return bundle
}
