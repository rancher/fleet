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
