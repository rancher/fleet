package controller

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gcustom"
	gomegatypes "github.com/onsi/gomega/types"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/names"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
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

func checkCondition(gitrepo *v1alpha1.GitRepo, condType string, status corev1.ConditionStatus, message string) bool {
	cond, found := getCondition(gitrepo, condType)
	if !found {
		return false
	}
	return cond.Type == condType && cond.Status == status && cond.Message == message
}

var _ = Describe("GitJob controller", func() {
	When("a new GitRepo is created", func() {
		var (
			gitRepo     v1alpha1.GitRepo
			gitRepoName string
			job         batchv1.Job
			jobName     string
			caBundle    []byte
		)

		JustBeforeEach(func() {
			expectedCommit = commit
			gitRepo = createGitRepo(gitRepoName)
			gitRepo.Spec.CABundle = caBundle

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
				jobName = names.SafeConcatName(gitRepoName, names.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
			}).Should(Not(HaveOccurred()))

			Expect(job.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(job.Spec.Template.Spec.Containers[0].Args).To(ContainElements("fleet", "apply"))

			gitRepoOwnerRef := metav1.OwnerReference{
				Kind:       "GitRepo",
				APIVersion: "fleet.cattle.io/v1alpha1",
				Name:       gitRepoName,
			}

			// it should create RBAC resources for that gitRepo
			Eventually(func(g Gomega) {
				saName := names.SafeConcatName("git", gitRepo.Name)
				ns := types.NamespacedName{Name: saName, Namespace: gitRepo.Namespace}

				var sa corev1.ServiceAccount
				g.Expect(k8sClient.Get(ctx, ns, &sa)).ToNot(HaveOccurred())
				Expect(sa.ObjectMeta).To(beOwnedBy(gitRepoOwnerRef))

				var ro rbacv1.Role
				g.Expect(k8sClient.Get(ctx, ns, &ro)).ToNot(HaveOccurred())
				Expect(ro.ObjectMeta).To(beOwnedBy(gitRepoOwnerRef))

				var rb rbacv1.RoleBinding
				g.Expect(k8sClient.Get(ctx, ns, &rb)).ToNot(HaveOccurred())
				Expect(rb.ObjectMeta).To(beOwnedBy(gitRepoOwnerRef))
			}).Should(Succeed())
		})

		When("a job is created without a specified CA bundle", func() {
			BeforeEach(func() {
				gitRepoName = "no-ca-bundle"
				caBundle = nil
			})

			It("does not create a secret for the CA bundle", func() {
				secretName := fmt.Sprintf("%s-cabundle", gitRepoName)
				ns := types.NamespacedName{Name: secretName, Namespace: gitRepo.Namespace}
				var secret corev1.Secret

				Consistently(func(g Gomega) {
					err := k8sClient.Get(ctx, ns, &secret)

					g.Expect(err).ToNot(BeNil())
					g.Expect(errors.IsNotFound(err)).To(BeTrue(), err)
				}, time.Second*5, time.Second*1).Should(Succeed())
			})
		})

		When("a job is created with a CA bundle", func() {
			BeforeEach(func() {
				gitRepoName = "with-ca-bundle"
				caBundle = []byte("LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tZm9vLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=")
			})

			It("Creates a secret for the CA bundle", func() {
				gitRepoOwnerRef := metav1.OwnerReference{
					Kind:       "GitRepo",
					APIVersion: "fleet.cattle.io/v1alpha1",
					Name:       gitRepoName,
				}

				secretName := fmt.Sprintf("%s-cabundle", gitRepoName)
				ns := types.NamespacedName{Name: secretName, Namespace: gitRepo.Namespace}
				var secret corev1.Secret

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, ns, &secret)
					g.Expect(err).ToNot(HaveOccurred())
					Expect(secret.ObjectMeta).To(beOwnedBy(gitRepoOwnerRef))

					data, ok := secret.Data["additional-ca.crt"]
					g.Expect(ok).To(BeTrue())
					g.Expect(data).To(Equal(gitRepo.Spec.CABundle))
				}).Should(Succeed())
			})
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

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(
						ctx,
						types.NamespacedName{Name: gitRepoName, Namespace: gitRepoNamespace},
						&gitRepo,
					)).ToNot(HaveOccurred())
					g.Expect(gitRepo.Status.Commit).To(Equal(commit))
					g.Expect(gitRepo.Status.GitJobStatus).To(Equal("Current"))

					// check the conditions related to the job
					// Current.... Stalled=false Reconcilling=false
					g.Expect(checkCondition(&gitRepo, "Reconciling", corev1.ConditionFalse, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Stalled", corev1.ConditionFalse, "")).To(BeTrue())
					// check the rest
					g.Expect(checkCondition(&gitRepo, "GitPolling", corev1.ConditionTrue, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Ready", corev1.ConditionTrue, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Accepted", corev1.ConditionTrue, "")).To(BeTrue())
				}).Should(Succeed())

				// it should log 3 events
				// first one is to log the new commit from the poller
				// second one is to inform that the job was created
				// third one is to inform that the job was deleted because it succeeded
				Eventually(func(g Gomega) {
					events, _ := k8sClientSet.CoreV1().Events(gitRepo.Namespace).List(context.TODO(),
						metav1.ListOptions{
							FieldSelector: "involvedObject.name=success",
							TypeMeta:      metav1.TypeMeta{Kind: "GitRepo"},
						})
					g.Expect(events).ToNot(BeNil())
					g.Expect(len(events.Items)).To(Equal(3))
					g.Expect(events.Items[0].Reason).To(Equal("GotNewCommit"))
					g.Expect(events.Items[0].Message).To(Equal("9ca3a0ad308ed8bffa6602572e2a1343af9c3d2e"))
					g.Expect(events.Items[0].Type).To(Equal("Normal"))
					g.Expect(events.Items[0].Source.Component).To(Equal("gitjob-controller"))
					g.Expect(events.Items[1].Reason).To(Equal("Created"))
					g.Expect(events.Items[1].Message).To(Equal("GitJob was created"))
					g.Expect(events.Items[1].Type).To(Equal("Normal"))
					g.Expect(events.Items[1].Source.Component).To(Equal("gitjob-controller"))
					g.Expect(events.Items[2].Reason).To(Equal("JobDeleted"))
					g.Expect(events.Items[2].Message).To(Equal("job deletion triggered because job succeeded"))
					g.Expect(events.Items[2].Type).To(Equal("Normal"))
					g.Expect(events.Items[2].Source.Component).To(Equal("gitjob-controller"))
				}).Should(Succeed())

				// job should not be present
				Consistently(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
					return errors.IsNotFound(err)
				}, 10*time.Second, 1*time.Second).Should(BeTrue())
			})
		})

		When("a job is in progress", func() {
			BeforeEach(func() {
				gitRepoName = "progress"
			})

			It("sets LastExecutedCommit and JobStatus in GitRepo and InProgress condition", func() {
				// simulate job was successful
				Eventually(func() error {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
					// We could be checking this when the job is still not created
					Expect(client.IgnoreNotFound(err)).ToNot(HaveOccurred())
					job.Status.Succeeded = 1
					job.Status.Conditions = []batchv1.JobCondition{
						{
							Type:   "Ready",
							Status: "False",
						},
					}
					return k8sClient.Status().Update(ctx, &job)
				}).Should(Not(HaveOccurred()))

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(
						ctx,
						types.NamespacedName{Name: gitRepoName, Namespace: gitRepoNamespace},
						&gitRepo,
					)).ToNot(HaveOccurred())
					g.Expect(gitRepo.Status.Commit).To(Equal(commit))
					g.Expect(gitRepo.Status.GitJobStatus).To(Equal("InProgress"))
					// check the conditions related to the job
					// Inprogress.... Stalled=false and Reconcilling=true
					g.Expect(checkCondition(&gitRepo, "Reconciling", corev1.ConditionTrue, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Stalled", corev1.ConditionFalse, "")).To(BeTrue())
					// check the rest
					g.Expect(checkCondition(&gitRepo, "GitPolling", corev1.ConditionTrue, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Ready", corev1.ConditionTrue, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Accepted", corev1.ConditionTrue, "")).To(BeTrue())
				}).Should(Succeed())
			})
		})

		When("a job is terminating", func() {
			BeforeEach(func() {
				gitRepoName = "terminating"
			})

			It("has the expected conditions", func() {
				// simulate job was successful
				Eventually(func() error {
					err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
					// We could be checking this when the job is still not created
					Expect(client.IgnoreNotFound(err)).ToNot(HaveOccurred())
					// Delete the job
					return k8sClient.Delete(ctx, &job)
				}).Should(Not(HaveOccurred()))

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(
						ctx,
						types.NamespacedName{Name: gitRepoName, Namespace: gitRepoNamespace},
						&gitRepo,
					)).ToNot(HaveOccurred())
					g.Expect(gitRepo.Status.Commit).To(Equal(commit))
					g.Expect(gitRepo.Status.GitJobStatus).To(Equal("Terminating"))
					// check the conditions related to the job
					// Terminating.... Stalled=false and Reconcilling=false
					g.Expect(checkCondition(&gitRepo, "Reconciling", corev1.ConditionFalse, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Stalled", corev1.ConditionFalse, "")).To(BeTrue())
					// check the rest
					g.Expect(checkCondition(&gitRepo, "GitPolling", corev1.ConditionTrue, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Ready", corev1.ConditionTrue, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Accepted", corev1.ConditionTrue, "")).To(BeTrue())
				}).Should(Succeed())
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

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(
						ctx,
						types.NamespacedName{Name: gitRepoName, Namespace: gitRepoNamespace},
						&gitRepo,
					)).ToNot(HaveOccurred())
					g.Expect(gitRepo.Status.Commit).To(Equal(commit))
					g.Expect(gitRepo.Status.GitJobStatus).To(Equal("Failed"))
					// check the conditions related to the job
					// Failed.... Stalled=true and Reconcilling=false
					g.Expect(checkCondition(&gitRepo, "Reconciling", corev1.ConditionFalse, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Stalled", corev1.ConditionTrue, "Job Failed. failed: 1/1")).To(BeTrue())

					// check the rest
					g.Expect(checkCondition(&gitRepo, "GitPolling", corev1.ConditionTrue, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Ready", corev1.ConditionTrue, "")).To(BeTrue())
					g.Expect(checkCondition(&gitRepo, "Accepted", corev1.ConditionTrue, "")).To(BeTrue())
				}).Should(Succeed())
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
				jobName := names.SafeConcatName(gitRepoName, names.Hex(repo+commit, 5))
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
					jobName := names.SafeConcatName(gitRepoName, names.Hex(repo+newCommit, 5))
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
				jobName = names.SafeConcatName(gitRepoName, names.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
			}).Should(Not(HaveOccurred()))
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
			// wait until the job has finished
			Eventually(func() bool {
				jobName = names.SafeConcatName(gitRepoName, names.Hex(repo+commit, 5))
				err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
				return errors.IsNotFound(err)
			}).Should(BeTrue())

			// store the generation value to compare against later
			generationValue = gitRepo.Spec.ForceSyncGeneration
			Expect(simulateIncreaseForceSyncGeneration(gitRepo)).ToNot(HaveOccurred())
			// simulate job was successful
			Eventually(func() error {
				jobName = names.SafeConcatName(gitRepoName, names.Hex(repo+commit, 5))
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
			// wait until the job has finished
			Eventually(func() bool {
				jobName = names.SafeConcatName(gitRepoName, names.Hex(repo+commit, 5))
				err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
				return errors.IsNotFound(err)
			}).Should(BeTrue())
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
			// it should log 3 events
			// first one is to log the new commit from the poller
			// second one is to inform that the job was created
			// third one reports on the job being deleted because of ForceUpdateGeneration
			Eventually(func(g Gomega) {
				events, _ := k8sClientSet.CoreV1().Events(gitRepo.Namespace).List(context.TODO(),
					metav1.ListOptions{
						FieldSelector: "involvedObject.name=force-deletion",
						TypeMeta:      metav1.TypeMeta{Kind: "GitRepo"},
					})
				g.Expect(events).ToNot(BeNil())
				g.Expect(len(events.Items)).To(Equal(3))
				g.Expect(events.Items[0].Reason).To(Equal("GotNewCommit"))
				g.Expect(events.Items[0].Message).To(Equal("9ca3a0ad308ed8bffa6602572e2a1343af9c3d2e"))
				g.Expect(events.Items[0].Type).To(Equal("Normal"))
				g.Expect(events.Items[0].Source.Component).To(Equal("gitjob-controller"))
				g.Expect(events.Items[1].Reason).To(Equal("Created"))
				g.Expect(events.Items[1].Message).To(Equal("GitJob was created"))
				g.Expect(events.Items[1].Type).To(Equal("Normal"))
				g.Expect(events.Items[1].Source.Component).To(Equal("gitjob-controller"))
				g.Expect(events.Items[2].Reason).To(Equal("JobDeleted"))
				g.Expect(events.Items[2].Message).To(Equal("job deletion triggered because job succeeded"))
				g.Expect(events.Items[2].Type).To(Equal("Normal"))
				g.Expect(events.Items[2].Source.Component).To(Equal("gitjob-controller"))
			}).Should(Succeed())
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
				jobName = names.SafeConcatName(gitRepoName, names.Hex(repo+commit, 5))
				return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
			}).Should(Not(HaveOccurred()))
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
			// wait until the job has finished
			Eventually(func() bool {
				jobName = names.SafeConcatName(gitRepoName, names.Hex(repo+commit, 5))
				err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: gitRepoNamespace}, &job)
				return errors.IsNotFound(err)
			}).Should(BeTrue())

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
				jobName := names.SafeConcatName(gitRepoName, names.Hex(repo+stableCommit, 5))
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
					saName := names.SafeConcatName("git", gitRepo.Name)
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
					jobName := names.SafeConcatName(gitRepoName, names.Hex(repo+commit, 5))
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
					saName := names.SafeConcatName("git", gitRepo.Name)
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
					jobName := names.SafeConcatName(gitRepoName, names.Hex(repo+commit, 5))
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
				g.Expect(checkCondition(gitrepo, "GitPolling", corev1.ConditionTrue, "")).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Reconciling", corev1.ConditionTrue, "")).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Stalled", corev1.ConditionFalse, "")).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Ready", corev1.ConditionTrue, "")).To(BeTrue())
				g.Expect(checkCondition(gitrepo, "Accepted", corev1.ConditionTrue, "")).To(BeTrue())
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
			By("Receiving a bundle update")
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).ToNot(HaveOccurred())
				bundle.Labels["fleet.cattle.io/repo-name"] = gitrepo.Name
				return k8sClient.Update(ctx, bundle)
			}).ShouldNot(HaveOccurred())
			Expect(bundle.Status.Summary.Ready).ToNot(Equal(1))

			By("Updating the GitRepo status to not ready")
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gitrepo.Name}, gitrepo)
			Expect(err).ToNot(HaveOccurred())
			Expect(gitrepo.Status.Summary.Ready).To(Equal(0))

			// This simulates what the bundle deployment reconciler would do.
			By("Updating the Bundle deployment status to ready")
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

			By("Updating the GitRepo status to ready")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Status.Summary.Ready == 1
			}).Should(BeTrue())
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gitrepo.Name}, gitrepo)
			Expect(err).ToNot(HaveOccurred())
			Expect(gitrepo.Status.Summary.Ready).To(Equal(1))

			By("Deleting a bundle")
			err = k8sClient.Delete(ctx, bundle)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gitrepo.Name}, gitrepo)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(gitrepo.Status.Summary.Ready).To(Equal(0))
				g.Expect(gitrepo.Status.Summary.DesiredReady).To(Equal(0))
				g.Expect(gitrepo.Status.Display.ReadyBundleDeployments).To(Equal("0/0"))
			}).Should(Succeed())
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

func beOwnedBy(expected interface{}) gomegatypes.GomegaMatcher {
	return gcustom.MakeMatcher(func(meta metav1.ObjectMeta) (bool, error) {
		ref, ok := expected.(metav1.OwnerReference)
		if !ok {
			return false, fmt.Errorf("beOwnedBy matcher expects metav1.OwnerReference")
		}

		for _, or := range meta.OwnerReferences {
			if or.Kind == ref.Kind && or.APIVersion == ref.APIVersion && or.Name == ref.Name {
				return true, nil
			}
		}

		return false, nil
	}).WithTemplate(
		"Expected:\n{{.FormattedActual}}\n{{.To}} contain owner reference " +
			"matching Kind, APIVersion and Name of \n{{format .Data 1}}",
	).WithTemplateData(expected)
}
