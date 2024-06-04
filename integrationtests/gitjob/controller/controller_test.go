package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v2/pkg/name"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

const (
	gitRepoNamespace = "default"
	repo             = "https://www.github.com/rancher/fleet"
	commit           = "9ca3a0ad308ed8bffa6602572e2a1343af9c3d2e"
)

var _ = Describe("GitJob controller", func() {

	When("a new GitRepo is created", func() {
		var (
			gitRepo     v1alpha1.GitRepo
			gitRepoName string
			job         batchv1.Job
			jobName     string
		)

		JustBeforeEach(func() {
			gitRepo = createGitRepo(gitRepoName)
			Expect(k8sClient.Create(ctx, &gitRepo)).ToNot(HaveOccurred())
			Expect(simulateGitPollerUpdatingCommitInStatus(gitRepo, commit)).ToNot(HaveOccurred())

			By("Creating a job")
			Eventually(func() error {
				jobName = name.SafeConcatName(gitRepoName, name.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
			}).Should(Not(HaveOccurred()))

			Expect(job.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(job.Spec.Template.Spec.Containers[0].Args).To(ContainElements("fleet", "apply"))
		})

		When("a job completes successfully", func() {
			BeforeEach(func() {
				gitRepoName = "success"
			})

			It("sets LastExecutedCommit and JobStatus in GitRepo", func() {
				// simulate job was successful
				Eventually(func() error {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
					// We could be checking this when the job is still not created
					Expect(client.IgnoreNotFound(err)).ToNot(HaveOccurred())
					job.Status.Succeeded = 1
					job.Status.Conditions = []batchv1.JobCondition{
						{
							Type:   "Complete",
							Status: "True",
						},
					}
					return k8sClient.Status().Update(ctx, &job)
				}).Should(Not(HaveOccurred()))

				Eventually(func() bool {
					Expect(k8sClient.Get(
						ctx,
						types.NamespacedName{Name: gitRepoName, Namespace: gitRepoNamespace},
						&gitRepo,
					)).ToNot(HaveOccurred())

					return gitRepo.Status.Commit == commit && gitRepo.Status.GitJobStatus == "Current"
				}).Should(BeTrue())
			})
		})

		When("Job fails", func() {
			BeforeEach(func() {
				gitRepoName = "fail"
			})

			It("sets JobStatus in GitRepo", func() {
				// simulate job has failed
				Eventually(func() error {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
					Expect(err).ToNot(HaveOccurred())
					job.Status.Failed = 1
					job.Status.Conditions = []batchv1.JobCondition{
						{
							Type:    "Failed",
							Status:  "True",
							Reason:  "BackoffLimitExceeded",
							Message: "Job has reached the specified backoff limit",
						},
					}
					return k8sClient.Status().Update(ctx, &job)
				}).Should(Not(HaveOccurred()))

				Eventually(func() bool {
					Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gitRepoName, Namespace: gitRepoNamespace}, &gitRepo)).ToNot(HaveOccurred())
					// XXX: do we need an additional `LastExecutedCommit` or similar status field?
					//return gitRepo.Status.Commit != commit && gitRepo.Status.GitJobStatus == "Failed"
					return gitRepo.Status.GitJobStatus == "Failed"
				}).Should(BeTrue())

				By("verifying that the job is deleted if Spec.Generation changed")
				Expect(simulateIncreaseGitRepoGeneration(gitRepo)).ToNot(HaveOccurred())
				Eventually(func() bool {
					jobName = name.SafeConcatName(gitRepoName, name.Hex(repo+commit, 5))
					return errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job))
				}).Should(BeTrue())
			})
		})
	})

	When("A new commit is found for a git repo", func() {
		var (
			gitRepo     v1alpha1.GitRepo
			gitRepoName string
			job         batchv1.Job
		)

		JustBeforeEach(func() {
			gitRepo = createGitRepo(gitRepoName)
			Expect(k8sClient.Create(ctx, &gitRepo)).ToNot(HaveOccurred())
			Expect(simulateGitPollerUpdatingCommitInStatus(gitRepo, commit)).ToNot(HaveOccurred())

			By("creating a Job")
			Eventually(func() error {
				jobName := name.SafeConcatName(gitRepoName, name.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
			}).Should(Not(HaveOccurred()))
		})

		When("A new commit is set to the .Status.commit", func() {
			BeforeEach(func() {
				gitRepoName = "new-commit"
			})
			It("creates a new Job", func() {
				const newCommit = "9ca3a0adbbba32"
				Expect(simulateGitPollerUpdatingCommitInStatus(gitRepo, newCommit)).ToNot(HaveOccurred())
				Eventually(func() error {
					jobName := name.SafeConcatName(gitRepoName, name.Hex(repo+newCommit, 5))
					return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
				}).Should(Not(HaveOccurred()))
			})
		})
	})

	When("User wants to force a job deletion by increasing Spec.ForceUpdateGeneration", func() {
		var (
			gitRepo         v1alpha1.GitRepo
			gitRepoName     string
			job             batchv1.Job
			jobName         string
			generationValue int64
		)

		JustBeforeEach(func() {
			gitRepo = createGitRepo(gitRepoName)
			Expect(k8sClient.Create(ctx, &gitRepo)).ToNot(HaveOccurred())
			Expect(simulateGitPollerUpdatingCommitInStatus(gitRepo, commit)).ToNot(HaveOccurred())
			Eventually(func() error {
				jobName = name.SafeConcatName(gitRepoName, name.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
			}).Should(Not(HaveOccurred()))
			// store the generation value to compare against later
			generationValue = gitRepo.Spec.ForceSyncGeneration
			Expect(simulateIncreaseForceSyncGeneration(gitRepo)).ToNot(HaveOccurred())
		})
		BeforeEach(func() {
			gitRepoName = "force-deletion"
		})
		AfterEach(func() {
			err := k8sClient.Delete(ctx, &gitRepo)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Verifies that the Job is recreated", func() {
			Eventually(func() bool {
				newJob := &batchv1.Job{}
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, newJob)

				return string(job.UID) != string(newJob.UID)
			}).Should(BeTrue())
		})

		It("verifies that UpdateGeneration in Status is equal to ForceSyncGeneration", func() {
			Eventually(func() bool {
				gr := &v1alpha1.GitRepo{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepoName, Namespace: gitRepoNamespace}, gr)
				Expect(err).ToNot(HaveOccurred())
				// ForceSyncGeneration and UpdateGeneration should be equal and different to the initial value
				return gr.Spec.ForceSyncGeneration == gr.Status.UpdateGeneration && gr.Status.UpdateGeneration != generationValue
			}).Should(BeTrue())
		})
	})

	When("User performs an update in a gitrepo field", func() {
		var (
			gitRepo     v1alpha1.GitRepo
			gitRepoName string
			job         batchv1.Job
			jobName     string
		)

		JustBeforeEach(func() {
			gitRepo = createGitRepo(gitRepoName)
			Expect(k8sClient.Create(ctx, &gitRepo)).ToNot(HaveOccurred())
			Expect(simulateGitPollerUpdatingCommitInStatus(gitRepo, commit)).ToNot(HaveOccurred())
			Eventually(func() error {
				jobName = name.SafeConcatName(gitRepoName, name.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
			}).Should(Not(HaveOccurred()))

			// change a gitrepo field, this will change the Generation field. This simulates changing fleet apply parameters.
			Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
				var gitRepoFromCluster v1alpha1.GitRepo
				err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &gitRepoFromCluster)
				if err != nil {
					return err
				}
				gitRepoFromCluster.Spec.KeepResources = !gitRepoFromCluster.Spec.KeepResources

				return k8sClient.Update(ctx, &gitRepoFromCluster)
			})).ToNot(HaveOccurred())
		})

		BeforeEach(func() {
			gitRepoName = "simulate-arg-update"
		})

		It("Verifies that the Job is recreated", func() {
			Eventually(func() bool {
				newJob := &batchv1.Job{}
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, newJob)

				return string(job.UID) != string(newJob.UID)
			}).Should(BeTrue())
		})
	})
})

func simulateIncreaseForceSyncGeneration(gitRepo v1alpha1.GitRepo) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gitRepoFromCluster v1alpha1.GitRepo
		err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &gitRepoFromCluster)
		if err != nil {
			return err
		}
		gitRepoFromCluster.Spec.ForceSyncGeneration++
		return k8sClient.Update(ctx, &gitRepoFromCluster)
	})
}

func simulateIncreaseGitRepoGeneration(gitRepo v1alpha1.GitRepo) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gitRepoFromCluster v1alpha1.GitRepo
		err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &gitRepoFromCluster)
		if err != nil {
			return err
		}
		gitRepoFromCluster.Spec.ClientSecretName = "new"
		return k8sClient.Update(ctx, &gitRepoFromCluster)
	})
}

func simulateGitPollerUpdatingCommitInStatus(gitRepo v1alpha1.GitRepo, commit string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gitRepoFromCluster v1alpha1.GitRepo
		err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &gitRepoFromCluster)
		if err != nil {
			return err
		}
		gitRepoFromCluster.Status = v1alpha1.GitRepoStatus{
			Commit: commit,
		}
		return k8sClient.Status().Update(ctx, &gitRepoFromCluster)
	})
}

func createGitRepo(gitRepoName string) v1alpha1.GitRepo {
	return v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gitRepoName,
			Namespace: gitRepoNamespace,
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo: repo,
		},
	}
}
