//go:generate mockgen --build_flags=--mod=mod -destination=../mocks/fetch_mock.go -package=mocks github.com/rancher/gitjob/pkg/git/poll GitFetcher

package poll

import (
	"context"
	"sync"
	"testing"
	"time"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/git/mocks"

	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFetchBySyncInterval(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	gitJob := v1.GitJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gitjob",
		},
	}
	fetcher := mocks.NewMockGitFetcher(ctrl)
	scheme := runtime.NewScheme()
	err := v1.AddToScheme(scheme)
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&gitJob).WithStatusSubresource(&gitJob).Build()
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
				gitJob:  gitJob,
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
					tickerC <- time.Now() // simulate syncInterval has passed
				}
				w.Finish()
			}()

			w.fetchBySyncInterval(ctx, ticker)

			updatedGitJob := v1.GitJob{}
			err = client.Get(ctx, types.NamespacedName{Name: gitJob.Name, Namespace: gitJob.Namespace}, &updatedGitJob)
			if err != nil {
				t.Errorf("unexpected error %v", err)
			}
			if updatedGitJob.Status.Commit != commit {
				t.Errorf("expected .Status.Commit %v, but got %v", commit, updatedGitJob.Status.Commit)
			}
		})
	}
}
