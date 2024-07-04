package singlecluster_test

import (
	"encoding/json"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BundleDiffs", func() {
	var (
		name            string
		targetNamespace string

		k        kubectl.Command
		kw       kubectl.Command
		interval = 1 * time.Second
		duration = 15 * time.Second
	)

	bundleDeploymentStatus := func(repo string) (*v1alpha1.BundleDeploymentStatus, error) {
		out, err := k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/repo-name="+repo, "-o=jsonpath={.items[*].status}")
		if err != nil {
			return nil, err
		}

		if out == "" {
			return nil, apierrors.NewNotFound(v1alpha1.Resource("bundledeployment"), repo)
		}

		status := &v1alpha1.BundleDeploymentStatus{}
		err = json.Unmarshal([]byte(out), status)
		if err != nil {
			return nil, err
		}

		return status, nil
	}

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)

		asset := "single-cluster/bundle-diffs.yaml"

		// name matches name from gitrepo for use in label selectors
		name = "bundle-diffs-test"

		// namespace needs to match diff.comparePatches in fleet.yaml
		targetNamespace = "bundle-diffs-example"
		kw = k.Namespace(targetNamespace)

		out, err := k.Apply("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)

		By("waiting for resources to be ready", func() {
			Eventually(func() bool {
				status, err := bundleDeploymentStatus(name)
				if err != nil || status == nil {
					return false
				}
				GinkgoWriter.Printf("bundledeployment status: %v", status)
				return status.Ready && status.NonModified
			}).Should(BeTrue())

			Eventually(func() string {
				out, _ := kw.Get("services")

				return out
			}).Should(ContainSubstring("app-service"))

			// wait for potential redeploys to finish
			time.Sleep(5 * time.Second)
		})

		DeferCleanup(func() {
			out, err := k.Delete("-f", testenv.AssetPath(asset))
			Expect(err).ToNot(HaveOccurred(), out)

			// test cases use the same namespace, so we have to
			// make sure resources are cleaned up
			Eventually(func() bool {
				_, err := bundleDeploymentStatus(name)
				if err != nil && apierrors.IsNotFound(err) {
					return true
				}

				return false
			}).Should(BeTrue(), "bundledeployment should be deleted")

			_, _ = k.Delete("namespace", targetNamespace)
		})

	})

	When("fleet.yaml contains bundle-diff patches", func() {
		Context("adding new values", func() {
			BeforeEach(func() {
				out, err := kw.Patch(
					"service", "app-service",
					"--type=json",
					"-p", `[{"op": "add", "path": "/spec/ports/0", "value": {"name":"test","port":1023,"protocol":"TCP"}}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)

				// adds a value
				out, err = kw.Patch(
					"configmap", "app-config",
					"--type=merge",
					"-p", `{"data":{"newvalue":"by-test-code"}}`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("ignores changes", func() {
				Consistently(func() bool {
					status, err := bundleDeploymentStatus(name)
					if err != nil || status == nil {
						return false
					}

					return status.NonModified
				}, duration, interval).Should(BeTrue(), "ignores modification")
			})
		})

		Context("modifying existing values, that are ignored", func() {
			BeforeEach(func() {
				out, err := kw.Patch(
					"configmap", "app-config",
					"--type=merge",
					"-p", `{"data":{"test":"by-test-code"}}`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("ignores changes", func() {
				Consistently(func() bool {
					status, err := bundleDeploymentStatus(name)
					if err != nil || status == nil {
						return false
					}

					return status.NonModified
				}, duration, interval).Should(BeTrue(), "ignores modification")
			})
		})

		Context("modifying existing values, that are not ignored", func() {
			BeforeEach(func() {
				out, err := kw.Patch(
					"service", "app-service",
					"--type=json",
					"-p", `[{"op": "add", "path": "/spec/selector", "value": {"env": "modification"}}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("detects modifications", func() {
				Eventually(func() bool {
					status, err := bundleDeploymentStatus(name)
					if err != nil || status == nil {
						return false
					}

					GinkgoWriter.Printf("bundledeployment status: %v", status)
					return status.NonModified
				}, duration*2, interval).Should(BeFalse(), "detects modification")
			})
		})
	})
})
