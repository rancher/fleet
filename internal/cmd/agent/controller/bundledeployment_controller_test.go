package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/pkg/durations"

	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func downstreamResourcesScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	return scheme
}

func downstreamResourcesBundleDeployment() *fleetv1.BundleDeployment {
	return &fleetv1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "bd-1", Namespace: "cluster-ns"},
		Spec: fleetv1.BundleDeploymentSpec{
			Options: fleetv1.BundleDeploymentOptions{
				TargetNamespace: "target",
				DownstreamResources: []fleetv1.DownstreamResource{
					{Kind: "Secret", Name: "src-secret"},
					{Kind: "ConfigMap", Name: "src-cm"},
				},
			},
		},
	}
}

// TestCopyResourcesFromUpstream_CopiesUnderDeploymentClient verifies that the copy
// reads the source from the upstream reader and writes the copied objects (and the
// target namespace) through the deployment client resolved by the Deployer. With no
// service account and a nil helm deployer, that resolves to the agent downstream
// client, preserving the pre-impersonation behaviour.
func TestCopyResourcesFromUpstream_CopiesUnderDeploymentClient(t *testing.T) {
	scheme := downstreamResourcesScheme(t)
	bd := downstreamResourcesBundleDeployment()

	upstream := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "src-secret", Namespace: "cluster-ns"},
			Data:       map[string][]byte{"key": []byte("value")},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "src-cm", Namespace: "cluster-ns"},
			Data:       map[string]string{"key": "value"},
		},
	).Build()

	downstream := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &BundleDeploymentReconciler{
		Reader:           upstream,
		LocalClient:      downstream,
		Deployer:         deployer.New(downstream, upstream, nil, nil),
		DefaultNamespace: "cattle-fleet-system",
	}

	_, err := r.copyResourcesFromUpstream(context.Background(), bd, logr.Discard())
	require.NoError(t, err)

	ns := &corev1.Namespace{}
	require.NoError(t,
		downstream.Get(context.Background(), types.NamespacedName{Name: "target"}, ns),
		"expected target namespace to be created downstream")

	secret := &corev1.Secret{}
	require.NoError(t,
		downstream.Get(context.Background(), types.NamespacedName{Name: "src-secret", Namespace: "target"}, secret),
		"expected secret to be copied downstream")
	assert.Equal(t, "value", string(secret.Data["key"]), "copied secret has wrong data")
	assert.Equal(t, bd.Name, secret.Labels[fleetv1.BundleDeploymentOwnershipLabel], "copied secret missing ownership label")

	cm := &corev1.ConfigMap{}
	require.NoError(t,
		downstream.Get(context.Background(), types.NamespacedName{Name: "src-cm", Namespace: "target"}, cm),
		"expected configmap to be copied downstream")
	assert.Equal(t, "value", cm.Data["key"], "copied configmap has wrong data")
}

// TestCopyResourcesFromUpstream_ForbiddenSurfaces verifies that a denied downstream
// write surfaces as a Forbidden error, so requeueIfCopyForbidden can detect it and do a
// controlled requeue rather than tight-looping.
func TestCopyResourcesFromUpstream_ForbiddenSurfaces(t *testing.T) {
	scheme := downstreamResourcesScheme(t)
	bd := downstreamResourcesBundleDeployment()

	upstream := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "src-secret", Namespace: "cluster-ns"},
			Data:       map[string][]byte{"key": []byte("value")},
		},
	).Build()

	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "secrets"}, "src-secret", errors.New("nope"),
	)
	downstream := fake.NewClientBuilder().
		WithScheme(scheme).
		// Pre-create the target namespace so the Forbidden is hit on the resource write.
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "target"}}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*corev1.Secret); ok {
					return forbidden
				}
				return c.Create(ctx, obj, opts...)
			},
		}).
		Build()

	r := &BundleDeploymentReconciler{
		Reader:           upstream,
		LocalClient:      downstream,
		Deployer:         deployer.New(downstream, upstream, nil, nil),
		DefaultNamespace: "cattle-fleet-system",
	}

	_, err := r.copyResourcesFromUpstream(context.Background(), bd, logr.Discard())
	require.Error(t, err)
	assert.True(t, apierrors.IsForbidden(err), "expected error to be detectable as Forbidden")

	var copyForbiddenError *CopyForbiddenError
	assert.ErrorAs(t, err, &copyForbiddenError, "expected a denied downstream write to be marked as a copy denial")
}

// TestCopyResourcesFromUpstream_UpstreamForbiddenNotMarked verifies that a Forbidden
// from reading the sources on the management cluster is not mistaken for a denial of
// the deployment's service account. That read runs as the agent, so no downstream grant
// would resolve it and it must surface as a reconcile error instead of requeuing
// indefinitely.
func TestCopyResourcesFromUpstream_UpstreamForbiddenNotMarked(t *testing.T) {
	scheme := downstreamResourcesScheme(t)
	bd := downstreamResourcesBundleDeployment()

	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "secrets"}, "src-secret", errors.New("nope"),
	)
	upstream := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.Secret); ok {
					return forbidden
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	downstream := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "target"}}).
		Build()

	r := &BundleDeploymentReconciler{
		Reader:           upstream,
		LocalClient:      downstream,
		Deployer:         deployer.New(downstream, upstream, nil, nil),
		DefaultNamespace: "cattle-fleet-system",
	}

	_, err := r.copyResourcesFromUpstream(context.Background(), bd, logr.Discard())
	require.Error(t, err)

	var copyForbiddenError *CopyForbiddenError
	assert.NotErrorAs(t, err, &copyForbiddenError, "expected an upstream read denial not to be marked as a copy denial")
}

// TestCopyResourcesFromUpstream_MissingNamespaceForbidden verifies that a missing
// deployment namespace the service account may not create is reported as such,
// rather than as a "namespace not found" failure of the first resource write, and
// that it stays detectable as a Forbidden so the caller requeues.
func TestCopyResourcesFromUpstream_MissingNamespaceForbidden(t *testing.T) {
	scheme := downstreamResourcesScheme(t)
	bd := downstreamResourcesBundleDeployment()

	upstream := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "src-secret", Namespace: "cluster-ns"},
			Data:       map[string][]byte{"key": []byte("value")},
		},
	).Build()

	// The namespace does not exist and the deployment's identity may neither read
	// nor create it, which is what a tenant service account without cluster-scoped
	// namespace access sees.
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "namespaces"}, "target", errors.New("nope"),
	)
	downstream := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.Namespace); ok {
					return forbidden
				}
				return c.Get(ctx, key, obj, opts...)
			},
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*corev1.Namespace); ok {
					return forbidden
				}
				return c.Create(ctx, obj, opts...)
			},
		}).
		Build()

	// The agent client can see that the namespace is absent, even though the
	// deployment's identity cannot.
	agent := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &BundleDeploymentReconciler{
		Reader:           upstream,
		LocalClient:      agent,
		Deployer:         deployer.New(downstream, upstream, nil, nil),
		DefaultNamespace: "cattle-fleet-system",
	}

	_, err := r.copyResourcesFromUpstream(context.Background(), bd, logr.Discard())
	require.Error(t, err)
	assert.True(t, apierrors.IsForbidden(err), "expected error to stay detectable as Forbidden")

	var copyForbiddenError *CopyForbiddenError
	require.ErrorAs(t, err, &copyForbiddenError, "expected a denied namespace create to be marked as a copy denial")
	assert.Contains(t, err.Error(), "target", "expected the error to name the deployment namespace")
	assert.Contains(t, err.Error(), "does not exist", "expected the error to report the missing namespace")
}

// TestRequeueIfCopyForbidden_Forbidden verifies that a denied downstream copy write is
// handled as a controlled requeue: the status is persisted as not-ready and the
// result requeues after the namespace-permission interval, rather than being
// returned as a reconcile error.
func TestRequeueIfCopyForbidden_Forbidden(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fleetv1.AddToScheme(scheme))

	bd := &fleetv1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "bd-1", Namespace: "cluster-ns"},
		Status:     fleetv1.BundleDeploymentStatus{Ready: true},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&fleetv1.BundleDeployment{}).
		WithObjects(bd).
		Build()

	r := &BundleDeploymentReconciler{Client: c}

	orig := bd.DeepCopy()
	forbidden := copyForbidden(apierrors.NewForbidden(
		schema.GroupResource{Resource: "namespaces"}, "target", errors.New("nope"),
	))

	handled, res, err := r.requeueIfCopyForbidden(context.Background(), orig, bd, forbidden)
	require.True(t, handled, "expected the forbidden error to be handled")
	require.NoError(t, err, "expected no error from a handled requeue")
	assert.Equal(t, durations.NamespacePermissionRequeueInterval, res.RequeueAfter)

	persisted := &fleetv1.BundleDeployment{}
	require.NoError(t,
		c.Get(context.Background(), types.NamespacedName{Namespace: "cluster-ns", Name: "bd-1"}, persisted),
		"failed to fetch persisted bundle deployment")
	assert.False(t, persisted.Status.Ready, "expected persisted status Ready=false")
	assert.True(t, hasFalseCondition(persisted.Status, fleetv1.BundleDeploymentConditionReady),
		"expected a false %q condition", fleetv1.BundleDeploymentConditionReady)
	assert.True(t, hasFalseCondition(persisted.Status, fleetv1.BundleDeploymentConditionInstalled),
		"expected a false %q condition", fleetv1.BundleDeploymentConditionInstalled)
}

// TestRequeueIfCopyForbidden_NotForbidden verifies that a non-Forbidden error is
// left for the caller to return as a reconcile error, without touching status.
func TestRequeueIfCopyForbidden_NotForbidden(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fleetv1.AddToScheme(scheme))

	bd := &fleetv1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "bd-1", Namespace: "cluster-ns"},
		Status:     fleetv1.BundleDeploymentStatus{Ready: true},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&fleetv1.BundleDeployment{}).
		WithObjects(bd).
		Build()

	r := &BundleDeploymentReconciler{Client: c}

	handled, _, err := r.requeueIfCopyForbidden(context.Background(), bd.DeepCopy(), bd, errors.New("boom"))
	require.False(t, handled, "expected a non-forbidden error not to be handled")
	require.NoError(t, err, "expected no error when not handling")

	persisted := &fleetv1.BundleDeployment{}
	require.NoError(t,
		c.Get(context.Background(), types.NamespacedName{Namespace: "cluster-ns", Name: "bd-1"}, persisted),
		"failed to fetch bundle deployment")
	assert.True(t, persisted.Status.Ready, "expected status to be untouched (Ready=true)")
}

// TestRequeueIfCopyForbidden_UnmarkedForbidden verifies that a Forbidden which did not
// come from one of the downstream copy writes is left for the caller to return. Only
// those writes run as the deployment's service account, so only they converge once a
// downstream grant is added; requeuing anything else would loop forever on a status
// that names the wrong cause.
func TestRequeueIfCopyForbidden_UnmarkedForbidden(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fleetv1.AddToScheme(scheme))

	bd := &fleetv1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "bd-1", Namespace: "cluster-ns"},
		Status:     fleetv1.BundleDeploymentStatus{Ready: true},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&fleetv1.BundleDeployment{}).
		WithObjects(bd).
		Build()

	r := &BundleDeploymentReconciler{Client: c}

	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "secrets"}, "src-secret", errors.New("nope"),
	)

	handled, _, err := r.requeueIfCopyForbidden(context.Background(), bd.DeepCopy(), bd, forbidden)
	require.False(t, handled, "expected an unmarked forbidden error not to be handled")
	require.NoError(t, err, "expected no error when not handling")

	persisted := &fleetv1.BundleDeployment{}
	require.NoError(t,
		c.Get(context.Background(), types.NamespacedName{Namespace: "cluster-ns", Name: "bd-1"}, persisted),
		"failed to fetch bundle deployment")
	assert.True(t, persisted.Status.Ready, "expected status to be untouched (Ready=true)")
}

func hasFalseCondition(status fleetv1.BundleDeploymentStatus, condType string) bool {
	for _, c := range status.Conditions {
		if c.Type == condType {
			return c.Status == corev1.ConditionFalse
		}
	}
	return false
}

// TestStaleCacheRequeueAfter checks that repeated stale cache observations back
// off, and that the wait stops growing at the cap: a cache which never catches
// up must not keep the agent polling the API server every few seconds.
func TestStaleCacheRequeueAfter(t *testing.T) {
	cases := []struct {
		hits     int
		expected time.Duration
	}{
		{hits: 1, expected: durations.StaleCacheRequeue},
		{hits: 2, expected: 2 * durations.StaleCacheRequeue},
		{hits: 3, expected: 4 * durations.StaleCacheRequeue},
		{hits: 100, expected: durations.StaleCacheRequeueMax},
	}

	for _, c := range cases {
		if got := staleCacheRequeueAfter(c.hits); got != c.expected {
			t.Errorf("staleCacheRequeueAfter(%d) = %s, expected %s", c.hits, got, c.expected)
		}
	}
}

// TestStaleCacheHits checks that the counter for a BundleDeployment starts at
// one, grows by one every time a stale cache hit is recorded, and is reset to 0
// when stale cache hits are forgotten.
func TestStaleCacheHits(t *testing.T) {
	r := &BundleDeploymentReconciler{}
	key := "cluster-ns/bd"

	for expected := 1; expected <= 3; expected++ {
		if got := r.recordStaleCacheHit(key); got != expected {
			t.Errorf("recordStaleCacheHit() = %d, expected %d", got, expected)
		}
	}

	// Another BundleDeployment is counted separately.
	if got := r.recordStaleCacheHit("cluster-ns/other-bd"); got != 1 {
		t.Errorf("recordStaleCacheHit() for a second key = %d, expected 1", got)
	}

	r.forgetStaleCacheHits(key)

	if got := r.recordStaleCacheHit(key); got != 1 {
		t.Errorf("recordStaleCacheHit() after forgetting = %d, expected 1", got)
	}
}
