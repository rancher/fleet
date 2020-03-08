package sharedindex

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
)

const (
	ClusterGroupByNamespace = "clusterGroupByNamespace"
)

func Register(_ context.Context,
	clusterGroups v1alpha1.ClusterGroupCache) {
	clusterGroups.AddIndexer(ClusterGroupByNamespace, indexGroupByClusterNamespace)
}

func indexGroupByClusterNamespace(obj *fleet.ClusterGroup) ([]string, error) {
	return []string{
		obj.Status.Namespace,
	}, nil
}
