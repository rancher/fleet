//go:generate mockgen --build_flags=--mod=mod -destination=../mocks/watch_mock.go -package=mocks github.com/rancher/gitjob/pkg/git/poll Watcher
package poll

import (
	"context"
	"testing"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/git/mocks"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/exp/maps"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestAddOrModifyWatchGitRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.TODO()
	gitJob := v1.GitJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gitjob",
			Namespace: "test",
		},
	}

	tests := map[string]struct {
		watches         func(mockWatcher *mocks.MockWatcher) map[string]Watcher
		syncInterval    int
		expectedWatches []string
		expectedCalls   func(mockWatcher *mocks.MockWatcher)
	}{
		"gitrepo not present": {
			watches: func(mockWatcher *mocks.MockWatcher) map[string]Watcher {
				return make(map[string]Watcher)
			},
			expectedWatches: []string{"gitjob-test"},
			expectedCalls: func(mockWatcher *mocks.MockWatcher) {
				mockWatcher.EXPECT().StartBackgroundSync(ctx).Times(1)
			},
		},
		"gitrepo present with same syncInterval": {
			watches: func(mockWatcher *mocks.MockWatcher) map[string]Watcher {
				return map[string]Watcher{"gitjob-test": mockWatcher}
			},
			syncInterval:    10,
			expectedWatches: []string{"gitjob-test"},
			expectedCalls: func(mockWatcher *mocks.MockWatcher) {
				mockWatcher.EXPECT().GetSyncInterval().Return(10).Times(1)
				mockWatcher.EXPECT().UpdateGitJob(gitJob)
			},
		},
		"gitrepo present with different syncInterval": {
			watches: func(mockWatcher *mocks.MockWatcher) map[string]Watcher {
				return map[string]Watcher{"gitjob-test": mockWatcher}
			},
			syncInterval:    1,
			expectedWatches: []string{"gitjob-test"},
			expectedCalls: func(mockWatcher *mocks.MockWatcher) {
				mockWatcher.EXPECT().GetSyncInterval().Return(10).Times(1)
				mockWatcher.EXPECT().UpdateGitJob(gitJob)
				mockWatcher.EXPECT().Restart(ctx)
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			watcher := mocks.NewMockWatcher(ctrl)
			h := Handler{
				watches: test.watches(watcher),
				createWatch: func(_ v1.GitJob, _ client.Client) Watcher {
					return watcher
				},
			}
			gitJob.Spec.SyncInterval = test.syncInterval

			test.expectedCalls(watcher)
			h.AddOrModifyGitRepoWatch(ctx, gitJob)

			if !cmp.Equal(maps.Keys(h.watches), test.expectedWatches) {
				t.Errorf("expected %v, but got %v", test.expectedWatches, maps.Keys(h.watches))
			}
		})
	}
}
