package sharding

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// ShardingIDLabel is the label key used to identify the shard ID of a controller pod
	ShardingIDLabel string = "fleet.cattle.io/shard-id"
	// ShardingRefLabel is the label key used by resources to reference the shard ID of a controller
	ShardingRefLabel string = "fleet.cattle.io/shard-ref"
	// ShardingDefaultLabel is the label key which is set to true on the controller handling unlabeled resources
	ShardingDefaultLabel string = "fleet.cattle.io/shard-default"
)

// ShouldProcess returns true if the given object should be processed by the shard
// identified by shardID.
//
// Behavior:
//   - If shardID == "", the shard is "unassigned" and should only handle objects
//     *without* a shard label.
//   - Otherwise, only objects with ShardingRefLabel == shardID are processed.
func ShouldProcess(obj client.Object, shardID string) bool {
	labels := obj.GetLabels()
	if labels == nil {
		// No labels at all
		return shardID == ""
	}

	label, hasLabel := labels[ShardingRefLabel]

	if shardID == "" {
		// The "default" (unsharded) controller handles only unlabeled objects
		return !hasLabel
	}

	// Otherwise, process only if the shard matches exactly
	return hasLabel && label == shardID
}

// TypedFilterByShardID returns a predicate function that filters objects by the shard ID they reference
func TypedFilterByShardID[T client.Object](shardID string) predicate.TypedFuncs[T] {
	matchesLabel := func(o metav1.Object) bool {
		label, hasLabel := o.GetLabels()[ShardingRefLabel]

		if shardID == "" {
			return !hasLabel
		}

		return label == shardID
	}

	return predicate.TypedFuncs[T]{
		CreateFunc: func(e event.TypedCreateEvent[T]) bool {
			return matchesLabel(e.Object)
		},
		UpdateFunc: func(e event.TypedUpdateEvent[T]) bool {
			return matchesLabel(e.ObjectNew)
		},
		DeleteFunc: func(e event.TypedDeleteEvent[T]) bool {
			return matchesLabel(e.Object)
		},
	}
}

// FilterByShardID returns a predicate function that filters objects by the shard ID they reference
func FilterByShardID(shardID string) predicate.Funcs {
	return TypedFilterByShardID[client.Object](shardID)
}
