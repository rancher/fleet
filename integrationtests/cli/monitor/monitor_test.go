package monitor

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	"github.com/rancher/fleet/internal/cmd/cli"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Fleet monitor", func() {
	var (
		gitrepoName string
	)

	act := func(args []string, ns string) (*gbytes.Buffer, *gbytes.Buffer, error) {
		cmd := cli.NewMonitor()
		fmt.Printf("Using kubeconfig: %s\n", kubeconfigPath)
		args = append([]string{"--kubeconfig", kubeconfigPath, "-n", ns}, args...)
		cmd.SetArgs(args)

		buf := gbytes.NewBuffer()
		errBuf := gbytes.NewBuffer()
		cmd.SetOut(buf)
		cmd.SetErr(errBuf)

		err := cmd.Execute()
		return buf, errBuf, err
	}

	BeforeEach(func() {
		// Use default namespace since envtest doesn't support namespace creation/deletion
		namespace = "default"

		gitrepoName = "test-gitrepo"
		gitrepo := &fleet.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      gitrepoName,
				Namespace: namespace,
			},
			Spec: fleet.GitRepoSpec{
				Repo:   "https://github.com/rancher/fleet-examples",
				Branch: "master",
			},
		}
		Expect(k8sClient.Create(ctx, gitrepo)).ToNot(HaveOccurred())

		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, gitrepo)
		})
	})

	When("monitoring resources", func() {
		It("should output the resources as JSON", func() {
			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var resources cli.Resources
			err = json.Unmarshal(buf.Contents(), &resources)
			Expect(err).NotTo(HaveOccurred())

			Expect(resources.GitRepos).To(HaveLen(1))
			Expect(resources.GitRepos[0].Name).To(Equal(gitrepoName))
		})

		It("should identify bundles with generation mismatch", func() {
			bundleName := "test-bundle"
			bundle := &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:       bundleName,
					Namespace:  namespace,
					Generation: 1,
				},
				Spec: fleet.BundleSpec{
					Paused: true,
				},
			}
			Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bundle)
			})

			// Update the bundle status to create a generation mismatch
			bundle.Status.ObservedGeneration = 0
			Expect(k8sClient.Status().Update(ctx, bundle)).ToNot(HaveOccurred())

			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var resources cli.Resources
			err = json.Unmarshal(buf.Contents(), &resources)
			Expect(err).NotTo(HaveOccurred())

			Expect(resources.Diagnostics.BundlesWithGenerationMismatch).To(HaveLen(1))
			Expect(resources.Diagnostics.BundlesWithGenerationMismatch[0].Name).To(Equal(bundleName))
		})

		It("should identify invalid secret owners", func() {
			bundleName := "test-bundle-invalid-secret"
			bundle := &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleName,
					Namespace: namespace,
				},
			}
			Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())

			secretName := "test-secret"
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "fleet.cattle.io/v1alpha1",
							Kind:       "Bundle",
							Name:       bundleName,
							UID:        bundle.UID,
						},
					},
				},
				Type: "fleet.cattle.io/bundle-values/v1alpha1",
			}
			Expect(k8sClient.Create(ctx, secret)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, secret)
				_ = k8sClient.Delete(ctx, bundle)
			})

			// Recreate the bundle to invalidate the secret's owner reference
			Expect(k8sClient.Delete(ctx, bundle)).ToNot(HaveOccurred())
			bundle.ResourceVersion = ""
			bundle.UID = ""
			Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())

			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var resources cli.Resources
			err = json.Unmarshal(buf.Contents(), &resources)
			Expect(err).NotTo(HaveOccurred())

			Expect(resources.Diagnostics.InvalidSecretOwners).To(HaveLen(1))
			Expect(resources.Diagnostics.InvalidSecretOwners[0].Name).To(Equal(secretName))
		})

		It("should identify gitrepo bundle inconsistencies - old forceSyncGeneration", func() {
			gitrepo := &fleet.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gitrepo-forcesync",
					Namespace: namespace,
				},
				Spec: fleet.GitRepoSpec{
					Repo:                "https://github.com/rancher/fleet-examples",
					Branch:              "master",
					ForceSyncGeneration: 5,
				},
			}
			Expect(k8sClient.Create(ctx, gitrepo)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, gitrepo)
			})

			bundleName := "test-bundle-forcesync"
			bundle := &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleName,
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/repo-name": "test-gitrepo-forcesync",
						"fleet.cattle.io/commit":    "abc123",
					},
				},
				Spec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						ForceSyncGeneration: 3, // Older than gitrepo
					},
				},
			}
			Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bundle)
			})

			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var resources cli.Resources
			err = json.Unmarshal(buf.Contents(), &resources)
			Expect(err).NotTo(HaveOccurred())

			Expect(resources.Diagnostics.GitRepoBundleInconsistencies).NotTo(BeEmpty())
			found := false
			for _, b := range resources.Diagnostics.GitRepoBundleInconsistencies {
				if b.Name == bundleName {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should identify gitrepo bundle inconsistencies - stale commit hash", func() {
			gitrepo := &fleet.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gitrepo-commit",
					Namespace: namespace,
				},
				Spec: fleet.GitRepoSpec{
					Repo:   "https://github.com/rancher/fleet-examples",
					Branch: "master",
				},
			}
			Expect(k8sClient.Create(ctx, gitrepo)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, gitrepo)
			})

			// Update gitrepo status with current commit
			gitrepo.Status.Commit = "def456"
			Expect(k8sClient.Status().Update(ctx, gitrepo)).ToNot(HaveOccurred())

			bundleName := "test-bundle-commit"
			bundle := &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleName,
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/repo-name": "test-gitrepo-commit",
						"fleet.cattle.io/commit":    "abc123", // Old commit
					},
				},
			}
			Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bundle)
			})

			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var resources cli.Resources
			err = json.Unmarshal(buf.Contents(), &resources)
			Expect(err).NotTo(HaveOccurred())

			Expect(resources.Diagnostics.GitRepoBundleInconsistencies).NotTo(BeEmpty())
			found := false
			for _, b := range resources.Diagnostics.GitRepoBundleInconsistencies {
				if b.Name == bundleName {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should count bundles with deletion timestamp", func() {
			bundleName := "test-bundle-deleting"
			bundle := &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:       bundleName,
					Namespace:  namespace,
					Finalizers: []string{"test-finalizer"},
				},
			}
			Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				// Remove finalizer to allow deletion
				bundle.Finalizers = nil
				_ = k8sClient.Update(ctx, bundle)
				_ = k8sClient.Delete(ctx, bundle)
			})

			// Delete the bundle - it will be stuck with deletion timestamp due to finalizer
			Expect(k8sClient.Delete(ctx, bundle)).ToNot(HaveOccurred())

			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var resources cli.Resources
			err = json.Unmarshal(buf.Contents(), &resources)
			Expect(err).NotTo(HaveOccurred())

			Expect(resources.Diagnostics.BundlesWithDeletionTimestamp).To(BeNumerically(">=", 1))
		})

		It("should identify stuck bundledeployment with deployment ID mismatch", func() {
			bdName := "test-bd-deployment-id"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bdName,
					Namespace: namespace,
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-newcontent:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd)
			})

			// Update status with different appliedDeploymentID
			bd.Status.AppliedDeploymentID = "s-oldcontent:options"
			Expect(k8sClient.Status().Update(ctx, bd)).ToNot(HaveOccurred())

			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var resources cli.Resources
			err = json.Unmarshal(buf.Contents(), &resources)
			Expect(err).NotTo(HaveOccurred())

			Expect(resources.Diagnostics.StuckBundleDeployments).NotTo(BeEmpty())
			found := false
			for _, b := range resources.Diagnostics.StuckBundleDeployments {
				if b.Name == bdName {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should detect content issues", func() {
			bdName := "test-bd-missing-content"
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bdName,
					Namespace: namespace,
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "s-nonexistent:options",
				},
			}
			Expect(k8sClient.Create(ctx, bd)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, bd)
			})

			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var resources cli.Resources
			err = json.Unmarshal(buf.Contents(), &resources)
			Expect(err).NotTo(HaveOccurred())

			Expect(resources.ContentIssues).NotTo(BeEmpty())
			found := false
			for _, issue := range resources.ContentIssues {
				if issue.Name == bdName {
					found = true
					Expect(issue.Issues).To(ContainElement("content_not_found"))
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should check API consistency", func() {
			buf, _, err := act([]string{}, namespace)
			Expect(err).NotTo(HaveOccurred())

			var resources cli.Resources
			err = json.Unmarshal(buf.Contents(), &resources)
			Expect(err).NotTo(HaveOccurred())

			Expect(resources.APIConsistency).NotTo(BeNil())
			Expect(resources.APIConsistency.Versions).To(HaveLen(3))
			// In normal conditions, API should be consistent
			Expect(resources.APIConsistency.Consistent).To(BeTrue())
		})
	})
})
