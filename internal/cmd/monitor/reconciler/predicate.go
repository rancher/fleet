// Copyright (c) 2021-2026 SUSE LLC

package reconciler

import (
	"maps"
	"reflect"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// TypedResourceVersionUnchangedPredicate implements a update predicate to
// allow syncPeriod to trigger the reconciler
type TypedResourceVersionUnchangedPredicate[T metav1.Object] struct {
	predicate.TypedFuncs[T]
}

func isNil[T metav1.Object](arg T) bool {
	return any(arg) == nil
}

func (TypedResourceVersionUnchangedPredicate[T]) Create(e event.CreateEvent) bool {
	return false
}

func (TypedResourceVersionUnchangedPredicate[T]) Delete(e event.DeleteEvent) bool {
	return false
}

// Update implements default UpdateEvent filter for validating resource version change.
func (TypedResourceVersionUnchangedPredicate[T]) Update(e event.TypedUpdateEvent[T]) bool {
	if isNil(e.ObjectOld) {
		return false
	}
	if isNil(e.ObjectNew) {
		return false
	}

	return e.ObjectNew.GetResourceVersion() == e.ObjectOld.GetResourceVersion()
}

func (TypedResourceVersionUnchangedPredicate[T]) Generic(e event.GenericEvent) bool {
	return false
}

// bundleDeploymentStatusChangedPredicate returns true if the bundledeployment
// status has changed, or the bundledeployment was created
func bundleDeploymentStatusChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			n := e.ObjectNew.(*fleet.BundleDeployment)
			o := e.ObjectOld.(*fleet.BundleDeployment)
			if n == nil || o == nil {
				return false
			}
			return !n.DeletionTimestamp.IsZero() || !reflect.DeepEqual(n.Status, o.Status)
		},
	}
}

// jobUpdatedPredicate returns true if the job status has changed
func jobUpdatedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			n, isJob := e.ObjectNew.(*batchv1.Job)
			if !isJob {
				return false
			}
			o := e.ObjectOld.(*batchv1.Job)
			if n == nil || o == nil {
				return false
			}
			return !reflect.DeepEqual(n.Status, o.Status) ||
				(n.DeletionTimestamp != nil && o.DeletionTimestamp == nil)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
}

// commitChangedPredicate returns true if the webhook or polling commit has changed.
// Mirrors production gitjob_controller.go commitChangedPredicate.
func commitChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldGitRepo, ok := e.ObjectOld.(*fleet.GitRepo)
			if !ok {
				return true
			}
			newGitRepo, ok := e.ObjectNew.(*fleet.GitRepo)
			if !ok {
				return true
			}
			return (oldGitRepo.Status.WebhookCommit != newGitRepo.Status.WebhookCommit) ||
				(oldGitRepo.Status.PollingCommit != newGitRepo.Status.PollingCommit)
		},
	}
}

// clusterChangedPredicate filters cluster events that relate to bundle deployment creation.
// Mirrors production bundle_controller.go clusterChangedPredicate.
func clusterChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			n := e.ObjectNew.(*fleet.Cluster)
			o := e.ObjectOld.(*fleet.Cluster)
			// cluster deletion will eventually trigger a delete event
			if n == nil || !n.DeletionTimestamp.IsZero() {
				return true
			}
			// labels and annotations are used for templating and targeting
			if !maps.Equal(n.Labels, o.Labels) {
				return true
			}
			if !maps.Equal(n.Annotations, o.Annotations) {
				return true
			}
			// spec templateValues is used in templating
			if !reflect.DeepEqual(n.Spec, o.Spec) {
				return true
			}
			// this namespace contains the bundledeployments
			if n.Status.Namespace != o.Status.Namespace {
				return true
			}
			// this namespace indicates the agent is running
			if n.Status.Agent.Namespace != o.Status.Agent.Namespace {
				return true
			}
			if n.Status.Scheduled != o.Status.Scheduled {
				return true
			}
			if n.Status.ActiveSchedule != o.Status.ActiveSchedule {
				return true
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}
}

// nonSecretAnnotationChangedPredicate returns true if annotations changed,
// excluding changes to only the secret data hash tracking annotations.
// Mirrors production gitjob_controller.go nonSecretAnnotationChangedPredicate.
func nonSecretAnnotationChangedPredicate() predicate.Funcs {
	secretAnnotationKeys := map[string]struct{}{
		"fleet.cattle.io/client-secret-hash":         {},
		"fleet.cattle.io/helm-secret-hash":           {},
		"fleet.cattle.io/helm-secret-for-paths-hash": {},
	}

	annotationsChangedExcludingSecrets := func(oldAnnotations, newAnnotations map[string]string) bool {
		// Check if any non-secret annotation was added, removed, or changed
		for key, newVal := range newAnnotations {
			if _, isSecretAnnotation := secretAnnotationKeys[key]; isSecretAnnotation {
				continue
			}
			if oldVal, exists := oldAnnotations[key]; !exists || oldVal != newVal {
				return true
			}
		}
		// Check if any non-secret annotation was removed
		for key := range oldAnnotations {
			if _, isSecretAnnotation := secretAnnotationKeys[key]; isSecretAnnotation {
				continue
			}
			if _, exists := newAnnotations[key]; !exists {
				return true
			}
		}
		return false
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return annotationsChangedExcludingSecrets(
				e.ObjectOld.GetAnnotations(),
				e.ObjectNew.GetAnnotations(),
			)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
}

// dataChangedPredicate filters Secret and ConfigMap events to only trigger reconciliation
// when Data or BinaryData fields have changed.
// Mirrors production bundle_controller.go dataChangedPredicate.
func dataChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			switch new := e.ObjectNew.(type) {
			case *corev1.Secret:
				old, ok := e.ObjectOld.(*corev1.Secret)
				if !ok {
					return false
				}
				return !reflect.DeepEqual(new.Data, old.Data)
			case *corev1.ConfigMap:
				old, ok := e.ObjectOld.(*corev1.ConfigMap)
				if !ok {
					return false
				}
				return !maps.Equal(new.Data, old.Data) || !reflect.DeepEqual(new.BinaryData, old.BinaryData)
			default:
				return false
			}
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
}

// secretDataChangedPredicate filters Secret events to only trigger reconciliation
// when Data field has changed, or when the secret is created or deleted.
// Mirrors production gitjob_controller.go secretDataChangedPredicate.
func secretDataChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			newSecret, newOk := e.ObjectNew.(*corev1.Secret)
			oldSecret, oldOk := e.ObjectOld.(*corev1.Secret)
			if !newOk || !oldOk {
				return false
			}
			return !reflect.DeepEqual(newSecret.Data, oldSecret.Data)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}
}
