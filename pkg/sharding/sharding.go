package sharding

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const ShardingLabel string = "fleet.cattle.io/shard"

func FilterByShardID(shardID string) predicate.Funcs {
	matchesLabel := func(o client.Object) bool {
		label, hasLabel := o.GetLabels()[ShardingLabel]

		if shardID == "" {
			return !hasLabel
		}

		return label == shardID
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return matchesLabel(e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return matchesLabel(e.ObjectNew)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return matchesLabel(e.Object)
		},
	}
}
