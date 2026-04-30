// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestTypedResourceVersionUnchangedPredicate(t *testing.T) {
	p := TypedResourceVersionUnchangedPredicate[client.Object]{}

	t.Run("Create returns false", func(t *testing.T) {
		if p.Create(event.CreateEvent{Object: &fleet.Bundle{}}) {
			t.Error("expected false for Create")
		}
	})

	t.Run("Delete returns false", func(t *testing.T) {
		if p.Delete(event.DeleteEvent{Object: &fleet.Bundle{}}) {
			t.Error("expected false for Delete")
		}
	})

	t.Run("Generic returns false", func(t *testing.T) {
		if p.Generic(event.GenericEvent{Object: &fleet.Bundle{}}) {
			t.Error("expected false for Generic")
		}
	})

	t.Run("Update with same ResourceVersion returns true", func(t *testing.T) {
		old := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "42"}}
		new := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "42"}}
		e := event.TypedUpdateEvent[client.Object]{ObjectOld: old, ObjectNew: new}
		if !p.Update(e) {
			t.Error("expected true when ResourceVersion unchanged")
		}
	})

	t.Run("Update with changed ResourceVersion returns false", func(t *testing.T) {
		old := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"}}
		new := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "2"}}
		e := event.TypedUpdateEvent[client.Object]{ObjectOld: old, ObjectNew: new}
		if p.Update(e) {
			t.Error("expected false when ResourceVersion changed")
		}
	})

	t.Run("Update with nil old returns false", func(t *testing.T) {
		new := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"}}
		e := event.TypedUpdateEvent[client.Object]{ObjectOld: nil, ObjectNew: new}
		if p.Update(e) {
			t.Error("expected false for nil old object")
		}
	})

	t.Run("Update with nil new returns false", func(t *testing.T) {
		old := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"}}
		e := event.TypedUpdateEvent[client.Object]{ObjectOld: old, ObjectNew: nil}
		if p.Update(e) {
			t.Error("expected false for nil new object")
		}
	})
}

func TestClusterChangedPredicate(t *testing.T) {
	p := clusterChangedPredicate()

	t.Run("Create returns true", func(t *testing.T) {
		if !p.Create(event.CreateEvent{Object: &fleet.Cluster{}}) {
			t.Error("expected true for Create")
		}
	})

	t.Run("Delete returns true", func(t *testing.T) {
		if !p.Delete(event.DeleteEvent{Object: &fleet.Cluster{}}) {
			t.Error("expected true for Delete")
		}
	})

	t.Run("Update with no change returns false", func(t *testing.T) {
		cluster := &fleet.Cluster{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"env": "prod"}},
			Status:     fleet.ClusterStatus{Namespace: "test-ns"},
		}
		e := event.UpdateEvent{ObjectOld: cluster, ObjectNew: cluster.DeepCopy()}
		if p.Update(e) {
			t.Error("expected false when nothing changed")
		}
	})

	t.Run("Update with label change returns true", func(t *testing.T) {
		old := &fleet.Cluster{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"env": "prod"}}}
		new := old.DeepCopy()
		new.Labels["env"] = "staging"
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true for label change")
		}
	})

	t.Run("Update with annotation change returns true", func(t *testing.T) {
		old := &fleet.Cluster{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"key": "val"}}}
		new := old.DeepCopy()
		new.Annotations["key"] = "newval"
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true for annotation change")
		}
	})

	t.Run("Update with deletion timestamp set returns true", func(t *testing.T) {
		old := &fleet.Cluster{}
		new := old.DeepCopy()
		now := metav1.Now()
		new.DeletionTimestamp = &now
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true when deletion timestamp set")
		}
	})

	t.Run("Update with status namespace change returns true", func(t *testing.T) {
		old := &fleet.Cluster{Status: fleet.ClusterStatus{Namespace: "old-ns"}}
		new := old.DeepCopy()
		new.Status.Namespace = "new-ns"
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true for status namespace change")
		}
	})
}

func TestNonSecretAnnotationChangedPredicate(t *testing.T) {
	p := nonSecretAnnotationChangedPredicate()

	t.Run("Create returns false", func(t *testing.T) {
		if p.Create(event.CreateEvent{Object: &fleet.GitRepo{}}) {
			t.Error("expected false for Create")
		}
	})

	t.Run("Delete returns false", func(t *testing.T) {
		if p.Delete(event.DeleteEvent{Object: &fleet.GitRepo{}}) {
			t.Error("expected false for Delete")
		}
	})

	t.Run("Update with no annotation change returns false", func(t *testing.T) {
		old := &fleet.GitRepo{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"app": "v1"}}}
		new := old.DeepCopy()
		if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected false when annotations unchanged")
		}
	})

	t.Run("Update with regular annotation change returns true", func(t *testing.T) {
		old := &fleet.GitRepo{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"app": "v1"}}}
		new := old.DeepCopy()
		new.Annotations["app"] = "v2"
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true when regular annotation changed")
		}
	})

	t.Run("Update adding regular annotation returns true", func(t *testing.T) {
		old := &fleet.GitRepo{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
		new := old.DeepCopy()
		new.Annotations["new-key"] = "new-value"
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true when regular annotation added")
		}
	})

	t.Run("Update removing regular annotation returns true", func(t *testing.T) {
		old := &fleet.GitRepo{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"app": "v1"}}}
		new := old.DeepCopy()
		delete(new.Annotations, "app")
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true when regular annotation removed")
		}
	})

	t.Run("Update with only client-secret-hash change returns false", func(t *testing.T) {
		old := &fleet.GitRepo{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			"fleet.cattle.io/client-secret-hash": "old-hash",
		}}}
		new := old.DeepCopy()
		new.Annotations["fleet.cattle.io/client-secret-hash"] = "new-hash"
		if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected false when only client-secret-hash changed")
		}
	})

	t.Run("Update with only helm-secret-hash change returns false", func(t *testing.T) {
		old := &fleet.GitRepo{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			"fleet.cattle.io/helm-secret-hash": "old-hash",
		}}}
		new := old.DeepCopy()
		new.Annotations["fleet.cattle.io/helm-secret-hash"] = "new-hash"
		if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected false when only helm-secret-hash changed")
		}
	})

	t.Run("Update with only helm-secret-for-paths-hash change returns false", func(t *testing.T) {
		old := &fleet.GitRepo{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			"fleet.cattle.io/helm-secret-for-paths-hash": "old-hash",
		}}}
		new := old.DeepCopy()
		new.Annotations["fleet.cattle.io/helm-secret-for-paths-hash"] = "new-hash"
		if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected false when only helm-secret-for-paths-hash changed")
		}
	})

	t.Run("Update with secret hash and regular annotation change returns true", func(t *testing.T) {
		old := &fleet.GitRepo{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			"fleet.cattle.io/client-secret-hash": "old-hash",
			"app":                                "v1",
		}}}
		new := old.DeepCopy()
		new.Annotations["fleet.cattle.io/client-secret-hash"] = "new-hash"
		new.Annotations["app"] = "v2"
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true when regular annotation also changed")
		}
	})
}

func TestDataChangedPredicate(t *testing.T) {
	p := dataChangedPredicate()

	t.Run("Create returns true", func(t *testing.T) {
		if !p.Create(event.CreateEvent{Object: &corev1.Secret{}}) {
			t.Error("expected true for Secret Create")
		}
	})

	t.Run("Delete returns false", func(t *testing.T) {
		if p.Delete(event.DeleteEvent{Object: &corev1.Secret{}}) {
			t.Error("expected false for Secret Delete")
		}
	})

	t.Run("Secret Update with no data change returns false", func(t *testing.T) {
		old := &corev1.Secret{Data: map[string][]byte{"key": []byte("val")}}
		new := old.DeepCopy()
		if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected false when secret data unchanged")
		}
	})

	t.Run("Secret Update with data change returns true", func(t *testing.T) {
		old := &corev1.Secret{Data: map[string][]byte{"key": []byte("val")}}
		new := old.DeepCopy()
		new.Data["key"] = []byte("newval")
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true when secret data changed")
		}
	})

	t.Run("Secret Update adding key returns true", func(t *testing.T) {
		old := &corev1.Secret{Data: map[string][]byte{}}
		new := old.DeepCopy()
		new.Data["new-key"] = []byte("value")
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true when key added to secret")
		}
	})

	t.Run("ConfigMap Update with no data change returns false", func(t *testing.T) {
		old := &corev1.ConfigMap{Data: map[string]string{"key": "val"}}
		new := old.DeepCopy()
		if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected false when configmap data unchanged")
		}
	})

	t.Run("ConfigMap Update with data change returns true", func(t *testing.T) {
		old := &corev1.ConfigMap{Data: map[string]string{"key": "val"}}
		new := old.DeepCopy()
		new.Data["key"] = "newval"
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true when configmap data changed")
		}
	})

	t.Run("ConfigMap Update with BinaryData change returns true", func(t *testing.T) {
		old := &corev1.ConfigMap{BinaryData: map[string][]byte{"key": []byte("val")}}
		new := old.DeepCopy()
		new.BinaryData["key"] = []byte("newval")
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true when configmap binary data changed")
		}
	})

	t.Run("Update with wrong type returns false", func(t *testing.T) {
		// Pod is not a Secret or ConfigMap
		old := &corev1.Pod{}
		new := &corev1.Pod{}
		if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected false for unsupported object type")
		}
	})
}

func TestSecretDataChangedPredicate(t *testing.T) {
	p := secretDataChangedPredicate()

	t.Run("Create returns true", func(t *testing.T) {
		if !p.Create(event.CreateEvent{Object: &corev1.Secret{}}) {
			t.Error("expected true for Create")
		}
	})

	t.Run("Delete returns true", func(t *testing.T) {
		if !p.Delete(event.DeleteEvent{Object: &corev1.Secret{}}) {
			t.Error("expected true for Delete")
		}
	})

	t.Run("Update with no data change returns false", func(t *testing.T) {
		old := &corev1.Secret{Data: map[string][]byte{"key": []byte("val")}}
		new := old.DeepCopy()
		if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected false when secret data unchanged")
		}
	})

	t.Run("Update with data change returns true", func(t *testing.T) {
		old := &corev1.Secret{Data: map[string][]byte{"key": []byte("val")}}
		new := old.DeepCopy()
		new.Data["key"] = []byte("newval")
		if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected true when secret data changed")
		}
	})

	t.Run("Update with non-Secret objects returns false", func(t *testing.T) {
		old := &corev1.ConfigMap{}
		new := &corev1.ConfigMap{}
		if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Error("expected false when objects are not Secrets")
		}
	})
}
