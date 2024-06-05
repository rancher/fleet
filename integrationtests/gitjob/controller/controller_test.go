package controller

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v2/pkg/name"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

const (
	gitRepoNamespace   = "default"
	repo               = "https://www.github.com/rancher/fleet"
	commit             = "9ca3a0ad308ed8bffa6602572e2a1343af9c3d2e"
	stableCommitBranch = "renovate/golang.org-x-crypto-0.x"
	stableCommit       = "7b4c2b25a2da2160604bde2773ae8aa44ed481dd"
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

			// it should create RBAC resources for that gitRepo
			Eventually(func() bool {
				saName := name.SafeConcatName("git", gitRepo.Name)
				ns := types.NamespacedName{Name: saName, Namespace: gitRepo.Namespace}

				if err := k8sClient.Get(ctx, ns, &corev1.ServiceAccount{}); err != nil {
					return false
				}
				if err := k8sClient.Get(ctx, ns, &rbacv1.Role{}); err != nil {
					return false
				}
				if err := k8sClient.Get(ctx, ns, &rbacv1.RoleBinding{}); err != nil {
					return false
				}

				return true
			}).Should(BeTrue())
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

	When("a new GitRepo is created with DisablePolling set to true", func() {
		var (
			gitRepo     v1alpha1.GitRepo
			gitRepoName string
			job         batchv1.Job
		)

		JustBeforeEach(func() {
			gitRepo = createGitRepoWithDisablePolling(gitRepoName)
			Expect(k8sClient.Create(ctx, &gitRepo)).To(Succeed())

			By("Creating a job")
			Eventually(func() error {
				jobName := name.SafeConcatName(gitRepoName, name.Hex(repo+stableCommit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
			}).Should(Not(HaveOccurred()))
		})

		When("a job completes successfully", func() {
			BeforeEach(func() {
				gitRepoName = "disable-polling"
			})

			It("updates the commit from the actual repo", func() {
				job.Status.Succeeded = 1
				job.Status.Conditions = []batchv1.JobCondition{
					{
						Type:   "Complete",
						Status: "True",
					},
				}
				Expect(k8sClient.Status().Update(ctx, &job)).ToNot(HaveOccurred())

				By("verifying the commit is updated")
				Eventually(func() string {
					Expect(k8sClient.Get(ctx, types.NamespacedName{Name: gitRepoName, Namespace: gitRepoNamespace}, &gitRepo)).To(Succeed())
					return gitRepo.Status.Commit
				}, "30s", "1s").Should(Equal(stableCommit))
			})
		})
	})

	When("creating a gitRepo that references a nonexistent helm secret", func() {
		var (
			gitRepo                v1alpha1.GitRepo
			gitRepoName            string
			helmSecretNameForPaths string
			helmSecretName         string
		)

		JustBeforeEach(func() {
			gitRepoName = "test-no-for-paths-secret"
			gitRepo = createGitRepo(gitRepoName)
			gitRepo.Spec.HelmSecretNameForPaths = helmSecretNameForPaths
			gitRepo.Spec.HelmSecretName = helmSecretName
			// Create should return an error
			err := k8sClient.Create(ctx, &gitRepo)
			Expect(err).ToNot(HaveOccurred())
			Expect(simulateGitPollerUpdatingCommitInStatus(gitRepo, commit)).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			err := k8sClient.Delete(ctx, &gitRepo)
			Expect(err).ToNot(HaveOccurred())
			// reset the logs buffer so we don't read logs from previous tests
			logsBuffer.Reset()
		})

		Context("helmSecretNameForPaths secret does not exist", func() {
			BeforeEach(func() {
				helmSecretNameForPaths = "secret-does-not-exist"
				helmSecretName = ""
			})
			It("logs an error about HelmSecretNameForPaths not being found", func() {
				Eventually(func() bool {
					strLogs := logsBuffer.String()
					return strings.Contains(strLogs, `failed to look up HelmSecretNameForPaths, error: Secret \"secret-does-not-exist\" not found`)
				}).Should(BeTrue())
			})

			It("doesn't create RBAC resources", func() {
				Consistently(func() bool {
					saName := name.SafeConcatName("git", gitRepo.Name)
					ns := types.NamespacedName{Name: saName, Namespace: gitRepo.Namespace}

					if err := k8sClient.Get(ctx, ns, &corev1.ServiceAccount{}); !errors.IsNotFound(err) {
						return false
					}
					if err := k8sClient.Get(ctx, ns, &rbacv1.Role{}); !errors.IsNotFound(err) {
						return false
					}
					if err := k8sClient.Get(ctx, ns, &rbacv1.RoleBinding{}); !errors.IsNotFound(err) {
						return false
					}
					return true
				}, time.Second*5, time.Second*1).Should(BeTrue())
			})

			It("doesn't create the job", func() {
				Consistently(func() bool {
					jobName := name.SafeConcatName(gitRepoName, name.Hex(repo+commit, 5))
					newJob := &batchv1.Job{}
					err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepo.Namespace}, newJob)
					return errors.IsNotFound(err)
				}, time.Second*5, time.Second*1).Should(BeTrue())
			})
		})
		Context("helmSecretName secret does not exist", func() {
			BeforeEach(func() {
				helmSecretNameForPaths = ""
				helmSecretName = "secret-does-not-exist"
			})
			It("logs an error about HelmSecretName not being found", func() {
				Eventually(func() bool {
					strLogs := logsBuffer.String()
					return strings.Contains(strLogs, `failed to look up helmSecretName, error: Secret \"secret-does-not-exist\" not found`)
				}).Should(BeTrue())
			})

			It("doesn't create RBAC resources", func() {
				Consistently(func() bool {
					saName := name.SafeConcatName("git", gitRepo.Name)
					ns := types.NamespacedName{Name: saName, Namespace: gitRepo.Namespace}

					if err := k8sClient.Get(ctx, ns, &corev1.ServiceAccount{}); !errors.IsNotFound(err) {
						return false
					}
					if err := k8sClient.Get(ctx, ns, &rbacv1.Role{}); !errors.IsNotFound(err) {
						return false
					}
					if err := k8sClient.Get(ctx, ns, &rbacv1.RoleBinding{}); !errors.IsNotFound(err) {
						return false
					}
					return true
				}, time.Second*5, time.Second*1).Should(BeTrue())
			})

			It("doesn't create the job", func() {
				Consistently(func() bool {
					jobName := name.SafeConcatName(gitRepoName, name.Hex(repo+commit, 5))
					newJob := &batchv1.Job{}
					err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepo.Namespace}, newJob)
					return errors.IsNotFound(err)
				}, time.Second*5, time.Second*1).Should(BeTrue())
			})
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

func createGitRepoWithDisablePolling(gitRepoName string) v1alpha1.GitRepo {
	return v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gitRepoName,
			Namespace: gitRepoNamespace,
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo:           repo,
			DisablePolling: true,
			Branch:         stableCommitBranch,
		},
	}
}
