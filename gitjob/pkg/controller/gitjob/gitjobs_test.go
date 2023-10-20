package gitjob

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"

	"github.com/golang/mock/gomock"
	"github.com/rancher/wrangler/v2/pkg/generic/fake"
	"github.com/rancher/wrangler/v2/pkg/kstatus"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Gitjob Controller", func() {
	var (
		h          Handler
		gitjob     *v1.GitJob
		jobmock    *fake.MockClientInterface[*batchv1.Job, *batchv1.JobList]
		gitjobmock *fake.MockControllerInterface[*v1.GitJob, *v1.GitJobList]
		background = metav1.DeletePropagationBackground
	)

	defaultGitjob := func() *v1.GitJob {
		return &v1.GitJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "testns",
			},
			Spec: v1.GitJobSpec{
				Git: v1.GitInfo{
					Repo:     "testrepo",
					Revision: "1234",
				},
			},
			Status: v1.GitJobStatus{
				GitEvent: v1.GitEvent{Commit: "1234"},
			},
		}
	}

	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())

		jobmock = fake.NewMockClientInterface[*batchv1.Job, *batchv1.JobList](ctrl)
		gitjobmock = fake.NewMockControllerInterface[*v1.GitJob, *v1.GitJobList](ctrl)
		gitjobmock.EXPECT().EnqueueAfter(gomock.Any(), gomock.Any(), gomock.Any())

		h = Handler{
			batch:   jobmock,
			gitjobs: gitjobmock,
		}
	})

	When("no change", func() {
		BeforeEach(func() {
			gitjob = defaultGitjob()
		})

		It("enqueues after sync intervall", func() {
			objs, status, err := h.generate(gitjob, gitjob.Status)
			Expect(err).ToNot(HaveOccurred())
			Expect(status).To(Equal(gitjob.Status))
			Expect(objs).To(HaveLen(1))

		})
	})

	When("previous sync error from git", func() {
		BeforeEach(func() {
			gitjob = defaultGitjob()
			gitjob.Spec.Git.Revision = ""
			gitjob.Status.LastSyncedTime = metav1.Now()
			kstatus.SetError(gitjob, "test")
		})

		It("enqueues after sync intervall", func() {
			objs, status, err := h.generate(gitjob, gitjob.Status)
			Expect(err).ToNot(HaveOccurred())
			Expect(status).To(Equal(gitjob.Status))
			Expect(objs).To(HaveLen(1))

		})
	})

	When("force is set", func() {
		BeforeEach(func() {
			gitjob = defaultGitjob()
			gitjob.Spec.ForceUpdateGeneration = 234
			gitjob.Status.UpdateGeneration = 1
			jobmock.EXPECT().Delete(gitjob.Namespace, jobName(gitjob), &metav1.DeleteOptions{PropagationPolicy: &background}).Return(nil)
		})

		It("deletes the job and updates update generation in status", func() {
			objs, status, err := h.generate(gitjob, gitjob.Status)
			Expect(err).ToNot(HaveOccurred())
			Expect(status.UpdateGeneration).To(Equal(int64(234)))
			Expect(objs).To(HaveLen(1))

		})
	})

	When("the job execution failed", func() {
		BeforeEach(func() {
			gitjob = defaultGitjob()
			kstatus.SetError(gitjob, `time="2023-07-19T10:48:12Z" level=fatal msg="Helm chart download: failed to authorize: failed to fetch anonymous token: unexpected status: 403 Forbidden"`)
			jobmock.EXPECT().Delete(gitjob.Namespace, jobName(gitjob), &metav1.DeleteOptions{PropagationPolicy: &background}).Return(nil)
		})

		It("deletes the job", func() {
			objs, status, err := h.generate(gitjob, gitjob.Status)
			Expect(err).ToNot(HaveOccurred())
			Expect(status).To(Equal(gitjob.Status))
			Expect(objs).To(HaveLen(1))
		})
	})
})
