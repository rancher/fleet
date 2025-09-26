package reconciler

import (
	"reflect"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

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
			return !reflect.DeepEqual(n.Status, o.Status)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
}

func webhookCommitChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldGitRepo, ok := e.ObjectOld.(*v1alpha1.GitRepo)
			if !ok {
				return true
			}
			newGitRepo, ok := e.ObjectNew.(*v1alpha1.GitRepo)
			if !ok {
				return true
			}
			return oldGitRepo.Status.WebhookCommit != newGitRepo.Status.WebhookCommit
		},
	}
}
