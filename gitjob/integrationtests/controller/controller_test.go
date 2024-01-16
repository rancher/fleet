package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/wrangler/v2/pkg/name"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

const (
	gitJobNamespace = "default"
	repo            = "https://www.github.com/rancher/gitjob"
	commit          = "9ca3a0ad308ed8bffa6602572e2a1343af9c3d2e"
)

var _ = Describe("GitJob controller", func() {

	When("a new GitJob is created", func() {
		var (
			gitJob     v1.GitJob
			gitJobName string
			job        batchv1.Job
			jobName    string
		)

		JustBeforeEach(func() {
			gitJob = createGitJob(gitJobName)
			Expect(k8sClient.Create(ctx, &gitJob)).ToNot(HaveOccurred())
			Expect(simulateGitPollerUpdatingCommitInStatus(gitJob, commit)).ToNot(HaveOccurred())

			By("Creating a job")
			Eventually(func() error {
				jobName = name.SafeConcatName(gitJobName, name.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitJobNamespace}, &job)
			}).Should(Not(HaveOccurred()))
		})

		When("a job completes successfully", func() {
			BeforeEach(func() {
				gitJobName = "success"
			})

			It("sets LastExecutedCommit and JobStatus in GitJob", func() {
				// simulate job was successful
				job.Status.Succeeded = 1
				job.Status.Conditions = []batchv1.JobCondition{
					{
						Type:   "Complete",
						Status: "True",
					},
				}
				Expect(k8sClient.Status().Update(ctx, &job)).ToNot(HaveOccurred())

				Eventually(func() bool {
					Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gitJobName, Namespace: gitJobNamespace}, &gitJob)).ToNot(HaveOccurred())
					return gitJob.Status.LastExecutedCommit == commit && gitJob.Status.JobStatus == "Current"
				}).Should(BeTrue())
			})
		})

		When("Job fails", func() {
			BeforeEach(func() {
				gitJobName = "fail"
			})

			It("sets JobStatus in GitJob", func() {
				// simulate job has failed
				job.Status.Failed = 1
				job.Status.Conditions = []batchv1.JobCondition{
					{
						Type:    "Failed",
						Status:  "True",
						Reason:  "BackoffLimitExceeded",
						Message: "Job has reached the specified backoff limit",
					},
				}
				Expect(k8sClient.Status().Update(ctx, &job)).ToNot(HaveOccurred())
				Eventually(func() bool {
					Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gitJobName, Namespace: gitJobNamespace}, &gitJob)).ToNot(HaveOccurred())
					return gitJob.Status.LastExecutedCommit != commit && gitJob.Status.JobStatus == "Failed"
				}).Should(BeTrue())

				By("verifying that the job is deleted if Spec.Generation changed")
				Expect(simulateIncreaseGitJobGeneration(gitJob)).ToNot(HaveOccurred())
				Eventually(func() bool {
					jobName = name.SafeConcatName(gitJobName, name.Hex(repo+commit, 5))
					return errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitJobNamespace}, &job))
				}).Should(BeTrue())
			})
		})
	})

	When("A new commit is found for a git repo", func() {
		var (
			gitJob     v1.GitJob
			gitJobName string
			job        batchv1.Job
		)

		JustBeforeEach(func() {
			gitJob = createGitJob(gitJobName)
			Expect(k8sClient.Create(ctx, &gitJob)).ToNot(HaveOccurred())
			Expect(simulateGitPollerUpdatingCommitInStatus(gitJob, commit)).ToNot(HaveOccurred())

			By("creating a Job")
			Eventually(func() error {
				jobName := name.SafeConcatName(gitJobName, name.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitJobNamespace}, &job)
			}).Should(Not(HaveOccurred()))
		})

		When("A new commit is set to the .Status.commit", func() {
			BeforeEach(func() {
				gitJobName = "new-commit"
			})
			It("creates a new Job", func() {
				const newCommit = "9ca3a0adbbba32"
				Expect(simulateGitPollerUpdatingCommitInStatus(gitJob, newCommit)).ToNot(HaveOccurred())
				Eventually(func() error {
					jobName := name.SafeConcatName(gitJobName, name.Hex(repo+newCommit, 5))
					return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitJobNamespace}, &job)
				}).Should(Not(HaveOccurred()))
			})
		})
	})

	When("User wants to force a job deletion by increasing Spec.ForceUpdateGeneration", func() {
		var (
			gitJob     v1.GitJob
			gitJobName string
			job        batchv1.Job
			jobName    string
		)

		JustBeforeEach(func() {
			gitJob = createGitJob(gitJobName)
			Expect(k8sClient.Create(ctx, &gitJob)).ToNot(HaveOccurred())
			Expect(simulateGitPollerUpdatingCommitInStatus(gitJob, commit)).ToNot(HaveOccurred())
			Eventually(func() error {
				jobName = name.SafeConcatName(gitJobName, name.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitJobNamespace}, &job)
			}).Should(Not(HaveOccurred()))

			Expect(simulateIncreaseForceUpdateGeneration(gitJob)).ToNot(HaveOccurred())
		})
		BeforeEach(func() {
			gitJobName = "force-deletion"
		})

		It("Verifies that the Job is recreated", func() {
			Eventually(func() bool {
				newJob := &batchv1.Job{}
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitJobNamespace}, newJob)

				return string(job.UID) != string(newJob.UID)
			}).Should(BeTrue())
		})
	})

	When("User performs an update in a Job argument", func() {
		var (
			gitJob     v1.GitJob
			gitJobName string
			job        batchv1.Job
			jobName    string
		)

		JustBeforeEach(func() {
			gitJob = createGitJob(gitJobName)
			Expect(k8sClient.Create(ctx, &gitJob)).ToNot(HaveOccurred())
			Expect(simulateGitPollerUpdatingCommitInStatus(gitJob, commit)).ToNot(HaveOccurred())
			Eventually(func() error {
				jobName = name.SafeConcatName(gitJobName, name.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitJobNamespace}, &job)
			}).Should(Not(HaveOccurred()))

			// change args parameter, this will change the Generation field. This simulates changing fleet apply parameters.
			Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
				var gitJobFomCluster v1.GitJob
				err := k8sClient.Get(ctx, types.NamespacedName{Name: gitJob.Name, Namespace: gitJob.Namespace}, &gitJobFomCluster)
				if err != nil {
					return err
				}
				gitJobFomCluster.Spec.JobSpec.Template.Spec.Containers[0].Args = []string{"-v"}

				return k8sClient.Update(ctx, &gitJobFomCluster)
			})).ToNot(HaveOccurred())
		})

		BeforeEach(func() {
			gitJobName = "simulate-arg-update"
		})

		It("Verifies that the Job is recreated", func() {
			Eventually(func() bool {
				newJob := &batchv1.Job{}
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitJobNamespace}, newJob)

				return string(job.UID) != string(newJob.UID)
			}).Should(BeTrue())
		})
	})
})

func simulateIncreaseForceUpdateGeneration(gitJob v1.GitJob) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gitJobFomCluster v1.GitJob
		err := k8sClient.Get(ctx, types.NamespacedName{Name: gitJob.Name, Namespace: gitJob.Namespace}, &gitJobFomCluster)
		if err != nil {
			return err
		}
		gitJobFomCluster.Spec.ForceUpdateGeneration++
		return k8sClient.Update(ctx, &gitJobFomCluster)
	})
}

func simulateIncreaseGitJobGeneration(gitJob v1.GitJob) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gitJobFomCluster v1.GitJob
		err := k8sClient.Get(ctx, types.NamespacedName{Name: gitJob.Name, Namespace: gitJob.Namespace}, &gitJobFomCluster)
		if err != nil {
			return err
		}
		gitJobFomCluster.Spec.Git.ClientSecretName = "new"
		return k8sClient.Update(ctx, &gitJobFomCluster)
	})
}

func simulateGitPollerUpdatingCommitInStatus(gitJob v1.GitJob, commit string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gitJobFomCluster v1.GitJob
		err := k8sClient.Get(ctx, types.NamespacedName{Name: gitJob.Name, Namespace: gitJob.Namespace}, &gitJobFomCluster)
		if err != nil {
			return err
		}
		gitJobFomCluster.Status = v1.GitJobStatus{
			GitEvent: v1.GitEvent{
				Commit: commit,
			},
		}
		return k8sClient.Status().Update(ctx, &gitJobFomCluster)
	})
}

func createGitJob(gitJobName string) v1.GitJob {
	return v1.GitJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gitJobName,
			Namespace: gitJobNamespace,
		},
		Spec: v1.GitJobSpec{
			Git: v1.GitInfo{
				Repo: repo,
			},
			JobSpec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers: []corev1.Container{
							{
								Image: "nginx",
								Name:  "nginx",
							},
						},
					},
				},
			},
		},
	}
}
