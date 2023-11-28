package singlecluster_test

import (
	"encoding/json"
	"os"
	"path"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BundleDiffs", func() {
	var (
		asset           string
		tmpdir          string
		name            string
		targetNamespace string

		k        kubectl.Command
		interval = 2 * time.Second
		duration = 30 * time.Second
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

		var err error
		targetNamespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		name = "bundle-diff-" + targetNamespace
	})

	JustBeforeEach(func() {
		tmpdir, _ = os.MkdirTemp("", "fleet-")
		gitrepo := path.Join(tmpdir, "gitrepo.yaml")
		err := testenv.Template(gitrepo, testenv.AssetPath(asset), struct {
			Name            string
			TargetNamespace string
		}{
			name,
			targetNamespace,
		})
		Expect(err).ToNot(HaveOccurred())

		out, err := k.Apply("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)

		DeferCleanup(func() {
			out, err := k.Delete("-f", gitrepo)
			Expect(err).ToNot(HaveOccurred(), out)

			os.RemoveAll(tmpdir)

			_, _ = k.Delete("namespace", targetNamespace)

			// test cases use the same namespace, so we have to
			// make sure resources are cleaned up
			Eventually(func() bool {
				_, err := bundleDeploymentStatus(name)
				if err != nil && apierrors.IsNotFound(err) {
					return true
				}

				return false
			}).Should(BeTrue(), "bundledeployment should be deleted")
		})
	})

	When("fleet.yaml contains bundle-diff patches", func() {
		BeforeEach(func() {
			asset = "single-cluster/bundle-diffs.yaml"
		})

		JustBeforeEach(func() {
			By("waiting for resources to be ready", func() {
				Eventually(func() bool {
					status, err := bundleDeploymentStatus(name)
					if err != nil || status == nil {
						return false
					}
					return status.Ready && status.NonModified
				}).Should(BeTrue())

				Eventually(func() string {
					out, _ := k.Namespace(targetNamespace).Get("services")

					return out
				}).Should(ContainSubstring("app-service"))
			})
		})

		Context("modifying a ignored resource", func() {
			JustBeforeEach(func() {
				By("modifying the workload resources", func() {
					kw := k.Namespace(targetNamespace)
					out, err := kw.Patch(
						"service", "app-service",
						"--type=json",
						"-p", `[{"op": "add", "path": "/spec/ports/0", "value": {"name":"test","port":1023,"protocol":"TCP"}}]`,
					)
					Expect(err).ToNot(HaveOccurred(), out)

					out, err = kw.Patch(
						"configmap", "app-config",
						"--type=merge",
						"-p", `{"data":{"value":"by-test-code"}}`,
					)
					Expect(err).ToNot(HaveOccurred(), out)
				})
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

		Context("modifying a monitored resource", func() {
			JustBeforeEach(func() {
				By("modifying a monitored value", func() {
					kw := k.Namespace(targetNamespace)
					out, err := kw.Patch(
						"service", "app-service",
						"--type=json",
						"-p", `[{"op": "add", "path": "/spec/selector", "value": {"name": "app", "value": "modification"}}]`,
					)
					Expect(err).ToNot(HaveOccurred(), out)
				})
			})

			It("detects modifications", func() {
				Eventually(func() bool {
					status, err := bundleDeploymentStatus(name)
					if err != nil || status == nil {
						return false
					}

					return status.NonModified
				}, duration, interval).Should(BeFalse(), "detects modification")
			})
		})
	})
})
