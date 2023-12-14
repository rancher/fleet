package target

//go:generate mockgen --build_flags=--mod=mod -destination=../mocks/cluster_group_cache_mock.go -package=mocks github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1 ClusterGroupCache

import (
	"fmt"
	"testing"

	"go.uber.org/mock/gomock"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/fleet/internal/cmd/controller/mocks"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// from fib_test.go
func BenchmarkStore(b *testing.B) {
	ctrl := gomock.NewController(b)
	store := newClusterGroupStore()
	cache := mocks.NewMockClusterGroupCache(ctrl)
	m := Manager{
		ClusterGroupStore: store,
		clusterGroups:     cache,
	}

	list := make([]*fleet.ClusterGroup, 0, b.N)
	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("cg%0000d", i)
		cg := &fleet.ClusterGroup{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: fleet.ClusterGroupSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"foo": name,
					},
				},
			},
		}
		store.Store("default/"+name, cg)
		list = append(list, cg)
	}
	cache.EXPECT().List(gomock.Any(), gomock.Any()).Return(list, nil).AnyTimes()

	cluster := &fleet.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "acluster", Labels: map[string]string{"foo": "bar"}}}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			m.clusterGroupsForCluster(cluster)
		}
	})
}
