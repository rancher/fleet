package helmdeployer

import (
	"context"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func deleteScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	return scheme
}

// TestDeleteResourcesCopiedFromUpstream_ScopedToReleaseNamespace verifies that the
// cleanup deletes only the copies owned by the bundle deployment that live in the
// release namespace, and leaves alone copies of other bundle deployments as well as
// same-named copies that a different deployment placed in another namespace. The
// namespace scoping is what lets the List succeed under an impersonated service
// account that has no cluster-wide list access.
func TestDeleteResourcesCopiedFromUpstream_ScopedToReleaseNamespace(t *testing.T) {
	scheme := deleteScheme(t)

	const releaseNS = "target"
	const bdName = "bd-1"

	objs := []client.Object{
		// owned by bd-1 in the release namespace: must be deleted
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "copied-secret", Namespace: releaseNS,
			Labels: map[string]string{fleet.BundleDeploymentOwnershipLabel: bdName},
		}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: "copied-cm", Namespace: releaseNS,
			Labels: map[string]string{fleet.BundleDeploymentOwnershipLabel: bdName},
		}},
		// owned by bd-1 but in another namespace: out of scope, must survive
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "other-ns-secret", Namespace: "elsewhere",
			Labels: map[string]string{fleet.BundleDeploymentOwnershipLabel: bdName},
		}},
		// owned by a different bd in the release namespace: must survive
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "foreign-secret", Namespace: releaseNS,
			Labels: map[string]string{fleet.BundleDeploymentOwnershipLabel: "bd-2"},
		}},
		// unlabeled resource in the release namespace: must survive
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: "unrelated-cm", Namespace: releaseNS,
		}},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	if err := deleteResourcesCopiedFromUpstream(context.Background(), c, releaseNS, bdName); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deleted := func(obj client.Object, ns, name string) bool {
		err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, obj)
		return apierrors.IsNotFound(err)
	}

	if !deleted(&corev1.Secret{}, releaseNS, "copied-secret") {
		t.Errorf("expected owned secret in release namespace to be deleted")
	}
	if !deleted(&corev1.ConfigMap{}, releaseNS, "copied-cm") {
		t.Errorf("expected owned configmap in release namespace to be deleted")
	}
	if deleted(&corev1.Secret{}, "elsewhere", "other-ns-secret") {
		t.Errorf("owned secret in another namespace must not be deleted (out of scope)")
	}
	if deleted(&corev1.Secret{}, releaseNS, "foreign-secret") {
		t.Errorf("secret owned by a different bundle deployment must not be deleted")
	}
	if deleted(&corev1.ConfigMap{}, releaseNS, "unrelated-cm") {
		t.Errorf("unlabeled configmap must not be deleted")
	}
}
