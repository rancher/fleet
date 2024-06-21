package poll

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/reugn/go-quartz/quartz"
	gomock "go.uber.org/mock/gomock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git/mocks"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("Gitrepo pooling tests", func() {
	var (
		scheduler *mocks.MockScheduler
		fetcher   *mocks.MockGitFetcher
		gitRepo   v1alpha1.GitRepo
		client    client.Client
	)
	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		scheduler = mocks.NewMockScheduler(ctrl)
		fetcher = mocks.NewMockGitFetcher(ctrl)
		gitRepo = v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gitjob",
				Namespace: "test",
			},
		}
		scheme := runtime.NewScheme()
		err := v1alpha1.AddToScheme(scheme)
		Expect(err).ToNot(HaveOccurred())
		client = fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&gitRepo).WithStatusSubresource(&gitRepo).Build()
	})
	DescribeTable("Gitrepo pooling tests",
		func(pollingInterval time.Duration, disablePolling bool, changeSpec bool,
			schedulerCalls func(gitRepo v1alpha1.GitRepo, pollingInterval time.Duration),
			fetcherCalls func()) {
			if pollingInterval != 0 {
				gitRepo.Spec.PollingInterval = &metav1.Duration{Duration: pollingInterval}
			} else {
				gitRepo.Spec.PollingInterval = nil
			}
			gitRepo.Spec.DisablePolling = disablePolling
			if changeSpec {
				gitRepo.Generation = 2
			}

			handler := Handler{
				scheduler: scheduler,
				fetcher:   fetcher,
				client:    client,
			}

			schedulerCalls(gitRepo, pollingInterval)
			fetcherCalls()

			handler.AddOrModifyGitRepoPollJob(context.TODO(), gitRepo)
		},

		Entry("GitRepo is not present and disablePolling = true",
			1*time.Second, true, false, func(gitRepo v1alpha1.GitRepo, pollingInterval time.Duration) {
				key := GitRepoPollKey(gitRepo)
				scheduler.EXPECT().GetScheduledJob(key).Return(nil, quartz.ErrJobNotFound).Times(1)
			}, func() {}),

		Entry("Gitrepo is not present, not setting pollingInterval and disablePolling=false",
			0*time.Second, false, false, func(gitRepo v1alpha1.GitRepo, pollingInterval time.Duration) {
				key := GitRepoPollKey(gitRepo)
				scheduler.EXPECT().GetScheduledJob(key).Return(nil, quartz.ErrJobNotFound).Times(1)

				job := NewGitRepoPollJob(client, fetcher, gitRepo)
				jobDetail := quartz.NewJobDetail(job, key)
				// we're not specifying an internal, so the default (15 secs) is set
				trigger := quartz.NewSimpleTrigger(15 * time.Second)
				scheduler.EXPECT().ScheduleJob(jobDetail, trigger).Return(nil).Times(1)
			}, func() {
				fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("commit", nil).Times(1)
			},
		),

		Entry("Gitrepo is not present, setting pollingInterval to a specific value and disablePolling=false",
			1999*time.Second, false, false, func(gitRepo v1alpha1.GitRepo, pollingInterval time.Duration) {
				key := GitRepoPollKey(gitRepo)
				scheduler.EXPECT().GetScheduledJob(key).Return(nil, quartz.ErrJobNotFound).Times(1)

				job := NewGitRepoPollJob(client, fetcher, gitRepo)
				jobDetail := quartz.NewJobDetail(job, key)
				// trigger should be pollingInterval seconds
				trigger := quartz.NewSimpleTrigger(pollingInterval)
				scheduler.EXPECT().ScheduleJob(jobDetail, trigger).Return(nil).Times(1)
			}, func() {
				fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("commit", nil).Times(1)
			},
		),

		Entry("gitrepo present, same polling interval, disablePolling=true",
			10*time.Second, true, false, func(gitRepo v1alpha1.GitRepo, pollingInterval time.Duration) {
				key := GitRepoPollKey(gitRepo)
				job := NewGitRepoPollJob(client, fetcher, gitRepo)
				jobDetail := quartz.NewJobDetail(job, key)
				schedJob := &mocks.MockScheduledJob{
					Detail:          jobDetail,
					TriggerDuration: 10 * time.Second,
				}
				// job exists
				scheduler.EXPECT().GetScheduledJob(key).Return(schedJob, nil).Times(1)
				// but we delete it because disablePolling is true
				scheduler.EXPECT().DeleteJob(key).Return(nil).Times(1)
			}, func() {},
		),

		Entry("gitrepo present, different polling interval, disablePolling=true",
			10*time.Second, true, false, func(gitRepo v1alpha1.GitRepo, pollingInterval time.Duration) {
				key := GitRepoPollKey(gitRepo)
				job := NewGitRepoPollJob(client, fetcher, gitRepo)
				jobDetail := quartz.NewJobDetail(job, key)
				schedJob := &mocks.MockScheduledJob{
					Detail:          jobDetail,
					TriggerDuration: 10 * time.Second,
				}
				// job exists with a polling interval of 10 seconds
				scheduler.EXPECT().GetScheduledJob(key).Return(schedJob, nil).Times(1)
				// we delete it because disablePolling is true
				scheduler.EXPECT().DeleteJob(key).Return(nil).Times(1)
			}, func() {},
		),

		Entry("gitrepo present, same polling interval, disablePolling=false",
			10*time.Second, false, false, func(gitRepo v1alpha1.GitRepo, pollingInterval time.Duration) {
				key := GitRepoPollKey(gitRepo)
				job := NewGitRepoPollJob(client, fetcher, gitRepo)
				jobDetail := quartz.NewJobDetail(job, key)
				schedJob := &mocks.MockScheduledJob{
					Detail:          jobDetail,
					TriggerDuration: 10 * time.Second,
				}
				// gets the job and does nothing else
				scheduler.EXPECT().GetScheduledJob(key).Return(schedJob, nil).Times(1)
			}, func() {},
		),

		Entry("gitrepo present, different polling interval, disablePolling=false",
			1999*time.Second, false, false, func(gitRepo v1alpha1.GitRepo, pollingInterval time.Duration) {
				gitRepoCopy := gitRepo
				gitRepoCopy.Spec.PollingInterval = &metav1.Duration{Duration: 10 * time.Second}
				key := GitRepoPollKey(gitRepoCopy)
				job := NewGitRepoPollJob(client, fetcher, gitRepoCopy)
				jobDetail := quartz.NewJobDetail(job, key)
				schedJob := &mocks.MockScheduledJob{
					Detail:          jobDetail,
					TriggerDuration: 10 * time.Second,
				}
				// gets the job and does nothing else
				scheduler.EXPECT().GetScheduledJob(key).Return(schedJob, nil).Times(1)
				scheduler.EXPECT().DeleteJob(key).Return(nil).Times(1)
				trigger := quartz.NewSimpleTrigger(1999 * time.Second)
				scheduler.EXPECT().ScheduleJob(jobDetail, trigger).Return(nil).Times(1)
			}, func() {
				fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("commit", nil).Times(1)
			},
		),

		Entry("gitrepo present, same polling interval, disablePolling=false, generation changed",
			10*time.Second, false, true, func(gitRepo v1alpha1.GitRepo, pollingInterval time.Duration) {
				gitRepoCopy := gitRepo
				gitRepoCopy.Generation = 1
				key := GitRepoPollKey(gitRepoCopy)
				job := NewGitRepoPollJob(client, fetcher, gitRepoCopy)
				jobDetail := quartz.NewJobDetail(job, key)
				schedJob := &mocks.MockScheduledJob{
					Detail:          jobDetail,
					TriggerDuration: 10 * time.Second,
				}
				// gets the job and rechedules
				scheduler.EXPECT().GetScheduledJob(key).Return(schedJob, nil).Times(1)
				scheduler.EXPECT().DeleteJob(key).Return(nil).Times(1)
				trigger := quartz.NewSimpleTrigger(10 * time.Second)
				scheduler.EXPECT().ScheduleJob(jobDetail, trigger).Return(nil).Times(1)
			}, func() {
				fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("commit", nil).Times(1)
			},
		),
	)
})

var _ = Describe("Gitrepo cleanup tests", func() {
	var (
		scheduler              *mocks.MockScheduler
		fetcher                *mocks.MockGitFetcher
		gitRepo                v1alpha1.GitRepo
		client                 client.Client
		handler                Handler
		expectedSchedulerCalls func(sched *mocks.MockScheduler)
	)
	JustBeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		scheduler = mocks.NewMockScheduler(ctrl)
		fetcher = mocks.NewMockGitFetcher(ctrl)
		handler = Handler{
			scheduler: scheduler,
			fetcher:   fetcher,
			client:    client,
		}
	})
	When("All gitRepos are found", func() {
		BeforeEach(func() {
			scheme := runtime.NewScheme()
			err := v1alpha1.AddToScheme(scheme)
			Expect(err).ToNot(HaveOccurred())
			gitRepo1 := v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitjob1",
					Namespace: "test",
				},
			}

			gitRepo2 := v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitjob2",
					Namespace: "test",
				},
			}

			gitRepo3 := v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitjob3",
					Namespace: "test",
				},
			}
			client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(&gitRepo1, &gitRepo2, &gitRepo3).
				WithStatusSubresource(&gitRepo, &gitRepo2, &gitRepo3).
				Build()

			expectedSchedulerCalls = func(sched *mocks.MockScheduler) {
				key1 := GitRepoPollKey(gitRepo1)
				key2 := GitRepoPollKey(gitRepo2)
				key3 := GitRepoPollKey(gitRepo3)
				sched.EXPECT().GetJobKeys().Return([]*quartz.JobKey{key1, key2, key3}).Times(1)
			}

		})
		It("Does nothing", func() {
			expectedSchedulerCalls(scheduler)
			ctx := context.TODO()
			handler.CleanUpGitRepoPollJobs(ctx)
		})
	})
	When("A gitRepo is not found", func() {
		BeforeEach(func() {
			scheme := runtime.NewScheme()
			err := v1alpha1.AddToScheme(scheme)
			Expect(err).ToNot(HaveOccurred())
			gitRepo1 := v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitjob1",
					Namespace: "test",
				},
			}

			gitRepo2 := v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitjob2",
					Namespace: "test",
				},
			}

			gitRepo3 := v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitjob3",
					Namespace: "test",
				},
			}
			client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(&gitRepo1, &gitRepo2).
				WithStatusSubresource(&gitRepo, &gitRepo2).
				Build()

			expectedSchedulerCalls = func(sched *mocks.MockScheduler) {
				key1 := GitRepoPollKey(gitRepo1)
				key2 := GitRepoPollKey(gitRepo2)
				// gitRepo3 was not added to the client
				key3 := GitRepoPollKey(gitRepo3)
				sched.EXPECT().GetJobKeys().Return([]*quartz.JobKey{key1, key2, key3}).Times(1)
				sched.EXPECT().DeleteJob(key3).Return(nil).Times(1)
			}
		})
		It("Deletes the job that was not found from the schedule", func() {
			expectedSchedulerCalls(scheduler)
			ctx := context.TODO()
			handler.CleanUpGitRepoPollJobs(ctx)
		})
	})
})
