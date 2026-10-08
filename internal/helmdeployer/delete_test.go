package helmdeployer

import (
	"context"
	"errors"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"

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
	"sigs.k8s.io/controller-runtime/pkg/log"
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

type mockDriver struct {
	releases               []release.Releaser
	simulatedListFailures  int
	simulatedFailuresCount int
}

func (md *mockDriver) Create(key string, rls release.Releaser) error {
	return nil // implementation not needed
}

func (md *mockDriver) Update(key string, rls release.Releaser) error {
	return nil // implementation not needed
}

func (md *mockDriver) Delete(key string) (release.Releaser, error) {
	return nil, nil // implementation not needed
}

func (md *mockDriver) Get(key string) (release.Releaser, error) {
	return nil, nil // implementation not needed
}

func (md *mockDriver) List(filter func(release.Releaser) bool) ([]release.Releaser, error) {
	if md.simulatedFailuresCount < md.simulatedListFailures {
		md.simulatedFailuresCount++

		return nil, errors.New("simulated release listing failure")
	}

	return md.releases, nil
}

func (md *mockDriver) listCalls() int {
	return md.simulatedFailuresCount
}

func (md *mockDriver) Query(labels map[string]string) ([]release.Releaser, error) {
	return nil, nil // implementation not needed
}
func (md *mockDriver) Name() string {
	return "test driver"
}

func TestDeleteNamespace(t *testing.T) {
	testCases := []struct {
		name                       string
		releases                   []releasev1.Release // left _after_ deleting a release
		failingReleaseListAttempts int
		releaseNS                  string
		expectDeletedNS            bool
	}{
		{
			name:            "happy case: target namespace should be deleted",
			releaseNS:       "my-target-namespace",
			expectDeletedNS: true,
		},
		{
			name:                       "target namespace can be deleted even if the first attempts fail",
			releaseNS:                  "my-target-namespace",
			failingReleaseListAttempts: 3,
			expectDeletedNS:            true,
		},
		{
			name:            "target namespace is kube-system",
			releaseNS:       "kube-system",
			expectDeletedNS: false,
		},
		{
			name:            "target namespace is default",
			releaseNS:       "default",
			expectDeletedNS: false,
		},
		{
			name: "more than one releases share the target namespace",
			releases: []releasev1.Release{
				{
					Name:      "foo",
					Namespace: "common-release-ns",
				},
			},
			releaseNS:       "common-release-ns",
			expectDeletedNS: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := deleteScheme(t)

			ns := corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: tc.releaseNS,
				},
			}

			objs := []client.Object{&ns}

			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			ctx := context.Background()
			logger := log.FromContext(ctx).WithName("test")

			releases := make([]release.Releaser, len(tc.releases))
			for i, r := range tc.releases {
				releases[i] = r
			}

			storageDriver := mockDriver{
				releases:              releases,
				simulatedListFailures: tc.failingReleaseListAttempts,
			}
			cfg := action.Configuration{Releases: &storage.Storage{Driver: &storageDriver}}

			purgeReleaseNamespace(ctx, &cfg, c, logger, tc.releaseNS)

			err := c.Get(ctx, types.NamespacedName{Name: tc.releaseNS}, &corev1.Namespace{})
			if tc.expectDeletedNS {
				assert.True(t, apierrors.IsNotFound(err))
			} else {
				require.NoError(t, err) // The common release namespace still exists
			}

			assert.Equal(t, tc.failingReleaseListAttempts, storageDriver.listCalls())
		})
	}
}
