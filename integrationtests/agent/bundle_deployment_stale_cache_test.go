package agent_test

import (
	"context"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// helmReleaseSecrets returns the helm release storage secrets living in the
// given namespace.
func helmReleaseSecrets(namespace string) []corev1.Secret {
	list := &corev1.SecretList{}
	err := k8sClient.List(ctx, list, client.InNamespace(namespace), client.MatchingLabels{"owner": "helm"})
	Expect(err).ToNot(HaveOccurred())

	return list.Items
}

// The agent used to delete the resources it manages whenever its cached 
// client reported a BundleDeployment as NotFound, 
// even though the BundleDeployment still existed upstream.
//
// The agent reads BundleDeployments through the manager's cache
// (mgr.GetClient()). controller-runtime's CacheReader.Get returns a plain
// NotFound whenever the object is missing from the local informer store, and
// makes no distinction between "the object was deleted" and "our store is
// empty or behind". The fix is to confirm with a live read
// (mgr.GetAPIReader()) before uninstalling anything.
//
// hideBundleDeployment() reproduces the stale cache exactly: the API server
// keeps the BundleDeployment, only the agent's cached view of it disappears.
var _ = Describe("BundleDeployment reported as NotFound by a stale agent cache", Ordered, func() {
	var (
		namespace string
		name      string
		env       *specEnv
		pokes     int
	)

	createBundleDeployment := func(name string) {
		bd := v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: clusterNS,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: "v1",
				Options: v1alpha1.BundleDeploymentOptions{
					DefaultNamespace: namespace,
				},
			},
		}

		Expect(k8sClient.Create(context.TODO(), &bd)).ToNot(HaveOccurred())
	}

	// triggerReconcile wakes up the agent for this BundleDeployment without
	// changing anything about it. A label change is enough to pass the
	// controller's event filter. In a real cluster the wake-up comes for free:
	// emptying the informer store makes the informer emit a delete event.
	triggerReconcile := func(name string) {
		bd := &v1alpha1.BundleDeployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)).ToNot(HaveOccurred())

		if bd.Labels == nil {
			bd.Labels = map[string]string{}
		}
		pokes++
		bd.Labels["poke"] = strconv.Itoa(pokes)

		Expect(k8sClient.Update(ctx, bd)).ToNot(HaveOccurred())
	}

	When("the BundleDeployment is missing from the cache but still exists upstream", func() {
		BeforeAll(func() {
			name = "stale-cache-keeps-resources"
			namespace = createNamespace()
			env = &specEnv{namespace: namespace}

			createBundleDeployment(name)
			DeferCleanup(func() {
				unhideBundleDeployment(clusterNS, name)
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		It("keeps the deployed resources", func() {
			By("Deploying the bundle's resources")
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			_, err := env.getConfigMap("cm-test")
			Expect(err).ToNot(HaveOccurred())
			Expect(helmReleaseSecrets(namespace)).ToNot(BeEmpty())

			By("Making the BundleDeployment invisible to the agent's cache only")
			hideBundleDeployment(clusterNS, name)

			By("Checking that the BundleDeployment is still there for anyone reading the API server")
			bd := &v1alpha1.BundleDeployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)).ToNot(HaveOccurred())

			By("Waking up the agent")
			triggerReconcile(name)

			By("Observing that the agent left the resources alone")
			// The window has to outlast a reconcile on a loaded CI runner:
			// a shorter one could close before the agent gets to delete
			// anything, and pass with the bug present.
			Consistently(func() error {
				_, err := env.getConfigMap("cm-test")
				return err
			}).WithTimeout(10*time.Second).WithPolling(250*time.Millisecond).
				Should(Succeed(), "cm-test was deleted, see rancher/fleet#5406")

			_, err = env.getService("svc-test")
			Expect(err).ToNot(HaveOccurred(), "svc-test was deleted, see rancher/fleet#5406")

			By("Checking that the helm release was left alone too")
			Expect(helmReleaseSecrets(namespace)).ToNot(BeEmpty())
		})
	})

	When("the BundleDeployment is really deleted", func() {
		BeforeAll(func() {
			name = "deleted-bundledeployment"
			namespace = createNamespace()
			env = &specEnv{namespace: namespace}

			createBundleDeployment(name)
		})

		It("still deletes the deployed resources", func() {
			By("Deploying the bundle's resources")
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			_, err := env.getConfigMap("cm-test")
			Expect(err).ToNot(HaveOccurred())

			By("Deleting the BundleDeployment for real")
			Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
			})).ToNot(HaveOccurred())

			By("Observing that the agent uninstalled the release")
			Eventually(func() bool {
				_, err := env.getConfigMap("cm-test")
				return err != nil
			}).Should(BeTrue(), "cm-test survived a genuine deletion")

			Eventually(func() bool {
				return len(helmReleaseSecrets(namespace)) == 0
			}).Should(BeTrue(), "the helm release survived a genuine deletion")
		})
	})
})
