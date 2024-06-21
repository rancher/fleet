package poll

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"golang.org/x/sync/semaphore"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	// . "github.com/onsi/gomega"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git/mocks"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("GitRepoPollJob tests", func() {
	var (
		expectedCalls func(fetcher *mocks.MockGitFetcher)
		gitRepo       v1alpha1.GitRepo
		job           GitRepoPollJob
		fetcher       *mocks.MockGitFetcher
		client        client.WithWatch
		ctx           context.Context
		commit        string
	)

	JustBeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())

		gitRepo = v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gitrepo",
			},
		}
		fetcher = mocks.NewMockGitFetcher(ctrl)
		scheme := runtime.NewScheme()
		err := v1alpha1.AddToScheme(scheme)
		Expect(err).ToNot(HaveOccurred())
		client = fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&gitRepo).WithStatusSubresource(&gitRepo).Build()
		ctx = context.TODO()

		job = GitRepoPollJob{
			sem:     semaphore.NewWeighted(1),
			client:  client,
			GitRepo: gitRepo,
			fetcher: fetcher,
		}

		expectedCalls(fetcher)

		err = job.Execute(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	When("Running the job with a commit different to the one set in the gitRepo", func() {
		BeforeEach(func() {
			commit = "fakeCommit"
			expectedCalls = func(fetcher *mocks.MockGitFetcher) {
				fetcher.EXPECT().LatestCommit(ctx, gomock.Any(), client).Return(commit, nil).Times(1)
			}
			gitRepo.Status.Commit = "9b0380f535d4c428d5b18f2efb5fddfe52b9dbf1"
		})
		It("Should update the gitRepo commit", func() {
			updatedGitRepo := v1alpha1.GitRepo{}
			err := client.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &updatedGitRepo)
			Expect(err).ToNot(HaveOccurred())
			Expect(updatedGitRepo.Status.Commit).To(Equal(commit))
		})
	})
	When("Running the job and LatestCommit returns an error", func() {
		BeforeEach(func() {
			commit = "fakeCommit"
			expectedCalls = func(fetcher *mocks.MockGitFetcher) {
				fetcher.EXPECT().LatestCommit(ctx, gomock.Any(), client).Return("", fmt.Errorf("Some error")).Times(1)
			}
			gitRepo.Status.Commit = "9b0380f535d4c428d5b18f2efb5fddfe52b9dbf1"
		})
		It("Should not update the original gitRepo commit", func() {
			updatedGitRepo := v1alpha1.GitRepo{}
			err := client.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &updatedGitRepo)
			Expect(err).ToNot(HaveOccurred())
			Expect(updatedGitRepo.Status.Commit).To(Equal(gitRepo.Status.Commit))
			errorFound := false
			for _, c := range updatedGitRepo.Status.Conditions {
				if c.Message == "Some error" {
					errorFound = true
				}
			}
			Expect(errorFound).To(BeTrue())
		})
	})
})
