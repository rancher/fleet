package reconciler

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// TypedResourceVersionUnchangedPredicate implements a update predicate to
// allow syncPeriod to trigger the reconciler
type TypedResourceVersionUnchangedPredicate[T metav1.Object] struct {
	predicate.TypedFuncs[T]
}

func isNil(arg any) bool {
	if v := reflect.ValueOf(arg); !v.IsValid() || ((v.Kind() == reflect.Ptr ||
		v.Kind() == reflect.Interface ||
		v.Kind() == reflect.Slice ||
		v.Kind() == reflect.Map ||
		v.Kind() == reflect.Chan ||
		v.Kind() == reflect.Func) && v.IsNil()) {
		return true
	}
	return false
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
