package agentmanagement_test

import (
	. "github.com/onsi/gomega"

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
