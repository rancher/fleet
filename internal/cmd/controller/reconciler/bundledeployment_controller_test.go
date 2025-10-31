package reconciler

import (
	"testing"

	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

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
