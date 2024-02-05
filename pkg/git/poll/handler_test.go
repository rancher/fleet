//go:generate mockgen --build_flags=--mod=mod -destination=../mocks/watch_mock.go -package=mocks github.com/rancher/fleet/pkg/git/poll Watcher
package poll

import (
	"context"
	"testing"
	"time"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git/mocks"

	"github.com/google/go-cmp/cmp"
	"go.uber.org/mock/gomock"
	"golang.org/x/exp/maps"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestAddOrModifyWatchGitRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.TODO()
	gitRepo := v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gitjob",
			Namespace: "test",
		},
	}

	tests := map[string]struct {
		watches         func(mockWatcher *mocks.MockWatcher) map[string]Watcher
		pollingInterval time.Duration
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
		"gitrepo present with same pollingInterval": {
			watches: func(mockWatcher *mocks.MockWatcher) map[string]Watcher {
				return map[string]Watcher{"gitjob-test": mockWatcher}
			},
			pollingInterval: 10 * time.Second,
			expectedWatches: []string{"gitjob-test"},
			expectedCalls: func(mockWatcher *mocks.MockWatcher) {
				mockWatcher.EXPECT().GetSyncInterval().Return(10.0).Times(1)
				mockWatcher.EXPECT().UpdateGitRepo(gitRepo)
			},
		},
		"gitrepo present with different pollingInterval": {
			watches: func(mockWatcher *mocks.MockWatcher) map[string]Watcher {
				return map[string]Watcher{"gitjob-test": mockWatcher}
			},
			pollingInterval: 1 * time.Second,
			expectedWatches: []string{"gitjob-test"},
			expectedCalls: func(mockWatcher *mocks.MockWatcher) {
				mockWatcher.EXPECT().GetSyncInterval().Return(10.0).Times(1)
				mockWatcher.EXPECT().UpdateGitRepo(gitRepo)
				mockWatcher.EXPECT().Restart(ctx)
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			watcher := mocks.NewMockWatcher(ctrl)
			h := Handler{
				watches: test.watches(watcher),
				createWatch: func(_ v1alpha1.GitRepo, _ client.Client) Watcher {
					return watcher
				},
			}
			gitRepo.Spec.PollingInterval = &metav1.Duration{Duration: test.pollingInterval}

			test.expectedCalls(watcher)
			h.AddOrModifyGitRepoWatch(ctx, gitRepo)

			if !cmp.Equal(maps.Keys(h.watches), test.expectedWatches) {
				t.Errorf("expected %v, but got %v", test.expectedWatches, maps.Keys(h.watches))
			}
		})
	}
}
