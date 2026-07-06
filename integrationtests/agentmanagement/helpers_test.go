package agentmanagement_test

import (
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
