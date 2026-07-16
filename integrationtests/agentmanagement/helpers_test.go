package agentmanagement_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

// clusterRole returns a ClusterRole with the given name (cluster-scoped).
func clusterRole(name string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

// objectExists returns an AsyncAssertion that succeeds when the object can be
// fetched from the API server.
func objectExists(obj client.Object) AsyncAssertion {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	return Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key, obj)).To(Succeed())
	})
}

// namespaceExists returns an AsyncAssertion that succeeds when a namespace with
// the given name exists.
func namespaceExists(name string) AsyncAssertion {
	return objectExists(newNamespace(name))
}

// namespaceIsGone returns an AsyncAssertion that succeeds when no namespace with
// the given name exists.
func namespaceIsGone(name string) AsyncAssertion {
	return Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &corev1.Namespace{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
}

// deleteNamespace removes a namespace for good. envtest runs no namespace
// controller, so a deleted namespace stays Terminating forever unless the
// kubernetes finalizer is cleared through the finalize subresource.
func deleteNamespace(name string) {
	GinkgoHelper()

	ns := newNamespace(name)
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ns))).To(Succeed())

	Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, ns)
		if apierrors.IsNotFound(err) {
			return
		}
		g.Expect(err).NotTo(HaveOccurred())

		ns.Spec.Finalizers = nil
		g.Expect(client.IgnoreNotFound(k8sClient.SubResource("finalize").Update(ctx, ns))).To(Succeed())
	}).Should(Succeed())

	namespaceIsGone(name).Should(Succeed())
}

// newConfigMap builds the ConfigMap representation of cfg, as expected by the
// global config's ConfigMap watch.
func newConfigMap(namespace, name string, cfg *config.Config) *corev1.ConfigMap {
	cm, err := config.ToConfigMap(namespace, name, cfg)
	Expect(err).NotTo(HaveOccurred())
	return cm
}

// newGeneratedNamespace returns a namespace object whose actual name is
// assigned by the API server on creation, using prefix as the GenerateName.
func newGeneratedNamespace(prefix string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: prefix},
	}
}

// newCluster returns a minimal Cluster in the given namespace.
func newCluster(namespace, name string) *fleet.Cluster {
	return &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
	}
}

// clusterNamespaceName predicts the generated cluster namespace name for a
// Cluster identified by its (namespace, name), following the documented
// format of Cluster.Status.Namespace: "cluster-<namespace>-<name>-<hash>".
func clusterNamespaceName(namespace, name string) string {
	return names.SafeConcatName("cluster", namespace, name,
		names.KeyHash(namespace+"::"+name))
}

// newBundleDeployment returns a minimal BundleDeployment in the given namespace.
func newBundleDeployment(namespace, name string) *fleet.BundleDeployment {
	return &fleet.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
	}
}
