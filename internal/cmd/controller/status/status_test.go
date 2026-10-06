package status

import (
	"context"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func newPatchClient(t *testing.T, patches *int) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := fleet.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&fleet.HelmOp{}).
		WithObjects(&fleet.HelmOp{ObjectMeta: metav1.ObjectMeta{Name: "helmop", Namespace: "ns"}}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(
				ctx context.Context,
				c client.Client,
				subResourceName string,
				obj client.Object,
				patch client.Patch,
				opts ...client.SubResourcePatchOption,
			) error {
				*patches++
				return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()
}

func getHelmOp(ctx context.Context, t *testing.T, c client.Client) *fleet.HelmOp {
	t.Helper()

	h := &fleet.HelmOp{}
	if err := c.Get(ctx, client.ObjectKey{Name: "helmop", Namespace: "ns"}, h); err != nil {
		t.Fatal(err)
	}

	return h
}

func TestPatchStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("skips the request when nothing changed", func(t *testing.T) {
		var patches int
		c := newPatchClient(t, &patches)

		orig := getHelmOp(ctx, t, c)
		if err := PatchStatus(ctx, c, orig, orig.DeepCopy()); err != nil {
			t.Fatal(err)
		}

		if patches != 0 {
			t.Errorf("expected no patch, got %d", patches)
		}
	})

	t.Run("patches the status when it changed", func(t *testing.T) {
		var patches int
		c := newPatchClient(t, &patches)

		orig := getHelmOp(ctx, t, c)
		obj := orig.DeepCopy()
		obj.Status.Version = "1.0.0"
		obj.Status.Conditions = append(obj.Status.Conditions, cond("Ready", corev1.ConditionTrue, ""))

		if err := PatchStatus(ctx, c, orig, obj); err != nil {
			t.Fatal(err)
		}

		if patches != 1 {
			t.Errorf("expected one patch, got %d", patches)
		}
		live := getHelmOp(ctx, t, c)
		if live.Status.Version != "1.0.0" || len(live.Status.Conditions) != 1 {
			t.Errorf("status not patched: %+v", live.Status)
		}
	})

	t.Run("returns a conflict when patching from a stale read", func(t *testing.T) {
		var patches int
		c := newPatchClient(t, &patches)

		orig := getHelmOp(ctx, t, c)

		// Another writer updates the status after orig was read.
		concurrent := orig.DeepCopy()
		concurrent.Status.Conditions = append(concurrent.Status.Conditions, cond("Accepted", corev1.ConditionTrue, ""))
		if err := c.Status().Update(ctx, concurrent); err != nil {
			t.Fatal(err)
		}

		obj := orig.DeepCopy()
		obj.Status.Conditions = append(obj.Status.Conditions, cond("Ready", corev1.ConditionTrue, ""))

		err := PatchStatus(ctx, c, orig, obj)
		if !apierrors.IsConflict(err) {
			t.Fatalf("expected a conflict error, got %v", err)
		}

		live := getHelmOp(ctx, t, c)
		if !slices.Contains(types(live.Status.Conditions), "Accepted") {
			t.Errorf("concurrently written condition was dropped: %+v", live.Status.Conditions)
		}
	})
}
