package helmdeployer

import (
	"context"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestDeleteResourcesCopiedFromUpstream_CollectsCopiesInAnyNamespace verifies that the
// cleanup deletes the copies owned by the bundle deployment wherever they were placed,
// including a namespace the deployment no longer targets, and leaves alone copies owned
// by another deployment as well as unlabeled resources.
func TestDeleteResourcesCopiedFromUpstream_CollectsCopiesInAnyNamespace(t *testing.T) {
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
		// owned by bd-1, left behind in a namespace it targeted earlier: must be
		// deleted too, a namespace-scoped list would orphan it
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "stale-secret", Namespace: "previous",
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

	require.NoError(t, deleteResourcesCopiedFromUpstream(context.Background(), c, bdName))

	deleted := func(obj client.Object, ns, name string) bool {
		err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, obj)
		return apierrors.IsNotFound(err)
	}

	assert.True(t, deleted(&corev1.Secret{}, releaseNS, "copied-secret"),
		"expected owned secret in release namespace to be deleted")
	assert.True(t, deleted(&corev1.ConfigMap{}, releaseNS, "copied-cm"),
		"expected owned configmap in release namespace to be deleted")
	assert.True(t, deleted(&corev1.Secret{}, "previous", "stale-secret"),
		"expected owned secret left in a previously targeted namespace to be deleted")
	assert.False(t, deleted(&corev1.Secret{}, releaseNS, "foreign-secret"),
		"secret owned by a different bundle deployment must not be deleted")
	assert.False(t, deleted(&corev1.ConfigMap{}, releaseNS, "unrelated-cm"),
		"unlabeled configmap must not be deleted")
}
