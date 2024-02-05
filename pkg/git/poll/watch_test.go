//go:generate mockgen --build_flags=--mod=mod -destination=../mocks/fetch_mock.go -package=mocks github.com/rancher/fleet/pkg/git/poll GitFetcher

package poll

import (
	"context"
	"sync"
	"testing"
	"time"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git/mocks"

	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFetchBySyncInterval(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	gitRepo := v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gitrepo",
		},
	}
	fetcher := mocks.NewMockGitFetcher(ctrl)
	scheme := runtime.NewScheme()
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&gitRepo).WithStatusSubresource(&gitRepo).Build()
	ctx := context.TODO()
	commit := "fakeCommit"

	tests := map[string]struct {
		numTimeTickMock int
	}{
		".Status.LastPulledCommit is updated before syncPeriod": {
			0,
		},
		"Latest commit is fetched 10 times": {
			10,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			w := Watch{
				gitRepo: gitRepo,
				client:  client,
				mu:      new(sync.Mutex),
				fetcher: fetcher,
			}
			tickerC := make(chan time.Time)
			ticker := &time.Ticker{
				C: tickerC,
			}
			fetcher.EXPECT().LatestCommit(ctx, gomock.Any(), client).Return(commit, nil).Times(test.numTimeTickMock + 1)
			go func() {
				for i := 0; i < test.numTimeTickMock; i++ {
					tickerC <- time.Now() // simulate pollingInterval has passed
				}
				w.Finish()
			}()

			w.fetchBySyncInterval(ctx, ticker)

			updatedGitRepo := v1alpha1.GitRepo{}
			err = client.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &updatedGitRepo)
			if err != nil {
				t.Errorf("unexpected error %v", err)
			}
			if updatedGitRepo.Status.Commit != commit {
				t.Errorf("expected .Status.Commit %v, but got %v", commit, updatedGitRepo.Status.Commit)
			}
		})
	}
}
