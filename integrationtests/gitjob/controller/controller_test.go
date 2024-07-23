package controller

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/integrationtests/utils"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"github.com/rancher/wrangler/v3/pkg/name"

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
	stableCommitBranch = "release/v0.6"
	stableCommit       = "26bdd9326b0238bb2fb743f863d9380c3c5d43e0"
)

func getCondition(gitrepo *v1alpha1.GitRepo, condType string) (genericcondition.GenericCondition, bool) {
	for _, cond := range gitrepo.Status.Conditions {
		if cond.Type == condType {
			return cond, true
		}
	}
	return genericcondition.GenericCondition{}, false
}

func checkCondition(gitrepo *v1alpha1.GitRepo, condType string, status corev1.ConditionStatus) bool {
	cond, found := getCondition(gitrepo, condType)
	if !found {
		return false
	}
	return cond.Type == condType && cond.Status == status
}

var _ = Describe("GitJob controller", func() {

	When("a new GitRepo is created", func() {
		var (
			gitRepo     v1alpha1.GitRepo
			gitRepoName string
			job         batchv1.Job
			jobName     string
		)

		JustBeforeEach(func() {
			expectedCommit = commit
			gitRepo = createGitRepo(gitRepoName)
			Expect(k8sClient.Create(ctx, &gitRepo)).ToNot(HaveOccurred())
			Eventually(func() string {
				var gitRepoFromCluster v1alpha1.GitRepo
				err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &gitRepoFromCluster)
				if err != nil {
					// maybe the resource gitrepo is not created yet
					return ""
				}
				return gitRepoFromCluster.Status.Display.ReadyBundleDeployments
			}).Should(ContainSubstring("0/0"))

			By("Creating a job")
			Eventually(func() error {
				var gitRepoFromCluster v1alpha1.GitRepo
				err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &gitRepoFromCluster)
				if err != nil {
					return err
				}
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
					// Job Status should be failed and commit should be as expected
					return gitRepo.Status.GitJobStatus == "Failed" && gitRepo.Status.Commit == commit
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
			expectedCommit = commit
			gitRepo = createGitRepo(gitRepoName)
			Expect(k8sClient.Create(ctx, &gitRepo)).ToNot(HaveOccurred())

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
				expectedCommit = newCommit
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
			Eventually(func() error {
				jobName = name.SafeConcatName(gitRepoName, name.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
			}).Should(Not(HaveOccurred()))
			// store the generation value to compare against later
			generationValue = gitRepo.Spec.ForceSyncGeneration
			Expect(simulateIncreaseForceSyncGeneration(gitRepo)).ToNot(HaveOccurred())
		})
		BeforeEach(func() {
			expectedCommit = commit
			gitRepoName = "force-deletion"
		})
		AfterEach(func() {
			// delete the gitrepo and wait until it is deleted
			waitDeleteGitrepo(gitRepo)
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
			expectedCommit = commit
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
				expectedCommit = stableCommit
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
			expectedCommit = commit
			gitRepoName = "test-no-for-paths-secret"
			gitRepo = createGitRepo(gitRepoName)
			gitRepo.Spec.HelmSecretNameForPaths = helmSecretNameForPaths
			gitRepo.Spec.HelmSecretName = helmSecretName
			// Create should return an error
			err := k8sClient.Create(ctx, &gitRepo)
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			// delete the gitrepo and wait until it is deleted
			waitDeleteGitrepo(gitRepo)
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

var _ = Describe("GitRepo", func() {
	var (
		gitrepo     *v1alpha1.GitRepo
		gitrepoName string
	)

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		p := make([]byte, 12)
		s := rand.New(rand.NewSource(GinkgoRandomSeed())) // nolint:gosec // non-crypto usage
		if _, err := s.Read(p); err != nil {
			panic(err)
		}
		gitrepoName = fmt.Sprintf("test-gitrepo-%.12s", hex.EncodeToString(p))

		gitrepo = &v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      gitrepoName,
				Namespace: namespace,
			},
			Spec: v1alpha1.GitRepoSpec{
				Repo: "https://github.com/rancher/fleet-test-data/not-found",
			},
		}

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, gitrepo)).ToNot(HaveOccurred())
		})
	})

	When("creating a gitrepo", func() {
		JustBeforeEach(func() {
			expectedCommit = commit
			err := k8sClient.Create(ctx, gitrepo)
			Expect(err).NotTo(HaveOccurred())
		})

		It("updates the gitrepo status", func() {
			org := gitrepo.ResourceVersion
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: gitrepoName, Namespace: namespace}, gitrepo)
				g.Expect(gitrepo.ResourceVersion > org).To(BeTrue())
				g.Expect(gitrepo.Status.Display.ReadyBundleDeployments).To(Equal("0/0"))
				g.Expect(gitrepo.Status.Display.Error).To(BeFalse())
				g.Expect(len(gitrepo.Status.Conditions)).To(Equal(5))
				g.Expect(checkCondition(gitrepo, "GitPolling", corev1.ConditionTrue)).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Reconciling", corev1.ConditionTrue)).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Stalled", corev1.ConditionFalse)).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Ready", corev1.ConditionTrue)).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Accepted", corev1.ConditionTrue)).To(BeTrue())
			}).Should(Succeed())
		})
	})
})

var _ = Describe("GitRepo Status Fields", func() {

	var (
		gitrepo *v1alpha1.GitRepo
		bd      *v1alpha1.BundleDeployment
	)

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	When("Bundle changes", func() {
		BeforeEach(func() {
			cluster, err := utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))
			targets := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						TargetNamespace: "targetNs",
					},
					Name:        "cluster",
					ClusterName: "cluster",
				},
			}
			bundle, err := utils.CreateBundle(ctx, k8sClient, "name", namespace, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))

			gitrepo = &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gitrepo",
					Namespace: namespace,
				},
				Spec: v1alpha1.GitRepoSpec{
					Repo: "https://github.com/rancher/fleet-test-data/not-found",
				},
			}
			err = k8sClient.Create(ctx, gitrepo)
			Expect(err).NotTo(HaveOccurred())

			bd = &v1alpha1.BundleDeployment{}
			Eventually(func() bool {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				return err == nil
			}).Should(BeTrue())
		})

		It("updates the status fields", func() {
			bundle := &v1alpha1.Bundle{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).ToNot(HaveOccurred())
				bundle.Labels["fleet.cattle.io/repo-name"] = gitrepo.Name
				return k8sClient.Update(ctx, bundle)
			}).ShouldNot(HaveOccurred())
			Expect(bundle.Status.Summary.Ready).ToNot(Equal(1))

			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gitrepo.Name}, gitrepo)
			Expect(err).ToNot(HaveOccurred())
			Expect(gitrepo.Status.Summary.Ready).To(Equal(0))

			bd := &v1alpha1.BundleDeployment{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				if err != nil {
					return err
				}
				bd.Status.Display.State = "Ready"
				bd.Status.AppliedDeploymentID = bd.Spec.DeploymentID
				bd.Status.Ready = true
				bd.Status.NonModified = true
				return k8sClient.Status().Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Status.Summary.Ready == 1
			}).Should(BeTrue())
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gitrepo.Name}, gitrepo)
			Expect(err).ToNot(HaveOccurred())
			Expect(gitrepo.Status.Summary.Ready).To(Equal(1))
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

func waitDeleteGitrepo(gitRepo v1alpha1.GitRepo) {
	err := k8sClient.Delete(ctx, &gitRepo)
	Expect(err).ToNot(HaveOccurred())
	Eventually(func() bool {
		var gitRepoFromCluster v1alpha1.GitRepo
		err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &gitRepoFromCluster)
		return errors.IsNotFound(err)
	}).Should(BeTrue())
}
