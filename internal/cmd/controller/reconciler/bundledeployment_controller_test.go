package reconciler

import (
	"context"
	"testing"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestBundleDeploymentWithAnUnchangedDisplayIsReportedToo(t *testing.T) {
	// A deployment which starts reporting an error stays NotReady, so its
	// display does not change and nothing is patched. The agent changed what
	// the display is derived from, though, which events report.
	scheme := runtime.NewScheme()
	if err := fleetv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	bd := &fleetv1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-bd", Namespace: "cluster-ns"},
		Status: fleetv1.BundleDeploymentStatus{
			NonReadyStatus: []fleetv1.NonReadyStatus{{Kind: "Deployment", Name: "web"}},
		},
	}
	bd.Status.Display.State = string(summary.GetDeploymentState(bd))

	notifier := &observingNotifier{}
	r := &BundleDeploymentReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(bd).WithStatusSubresource(bd).Build(),
		Scheme: scheme,
		Events: notifier,
	}

	_, err := r.Reconcile(context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Name: bd.Name, Namespace: bd.Namespace}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if notifier.observedDeployments != 1 {
		t.Errorf("expected the bundle deployment to be reported once, got %d", notifier.observedDeployments)
	}
}

func TestBundleDeploymentStatusChangedPredicate(t *testing.T) {
	p := bundleDeploymentStatusChangedPredicate()

	t.Run("Create", func(t *testing.T) {
		e := event.CreateEvent{Object: &fleetv1.BundleDeployment{}}
		if !p.Create(e) {
			t.Error("expected true for create event")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		// Delete is not defined, so it should be false
		e := event.DeleteEvent{Object: &fleetv1.BundleDeployment{}}
		if !p.Delete(e) {
			t.Error("expected true for delete event")
		}
	})

	t.Run("Generic", func(t *testing.T) {
		// Generic is not defined, so it should be false
		e := event.GenericEvent{Object: &fleetv1.BundleDeployment{}}
		if p.Generic(e) {
			t.Error("expected false for generic event")
		}
	})

	t.Run("Update", func(t *testing.T) {
		oldBD := &fleetv1.BundleDeployment{
			Status: fleetv1.BundleDeploymentStatus{Ready: false},
		}
		newBD := oldBD.DeepCopy()

		// No change
		e := event.UpdateEvent{ObjectOld: oldBD, ObjectNew: newBD}
		if p.Update(e) {
			t.Error("should be false when status is identical")
		}

		// Status changed
		newBD.Status.Ready = true
		e = event.UpdateEvent{ObjectOld: oldBD, ObjectNew: newBD}
		if !p.Update(e) {
			t.Error("should be true when status changes")
		}

		// Deletion timestamp added
		newBD = oldBD.DeepCopy()
		now := metav1.Now()
		newBD.DeletionTimestamp = &now
		e = event.UpdateEvent{ObjectOld: oldBD, ObjectNew: newBD}
		if !p.Update(e) {
			t.Error("should be true when deletion timestamp is set")
		}

		// Nil objects
		e = event.UpdateEvent{ObjectOld: nil, ObjectNew: newBD}
		if p.Update(e) {
			t.Error("should be false for nil old object")
		}
		e = event.UpdateEvent{ObjectOld: oldBD, ObjectNew: nil}
		if p.Update(e) {
			t.Error("should be false for nil new object")
		}
	})
}
