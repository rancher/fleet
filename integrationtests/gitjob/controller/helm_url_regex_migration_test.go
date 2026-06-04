package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/cli/migrate"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	migrationSystemNamespace = "default"
	migrationMarkerName      = "fleet-helm-url-regex-migrated"
)

func runHelmURLRegexMigration() error {
	return migrate.GitRepoHelmURLRegex(ctx, k8sClient, migrationSystemNamespace)
}

func deleteHelmURLRegexMarker() {
	marker := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migrationMarkerName,
			Namespace: migrationSystemNamespace,
		},
	}
	err := k8sClient.Delete(ctx, marker)
	if err != nil && !k8serrors.IsNotFound(err) {
		Fail("failed to delete migration marker: " + err.Error())
	}
}

// createBundle creates a Bundle in namespace owned by the named GitRepo,
// with the given Helm Repo and Chart values.
func createMigrationBundle(namespace, gitRepoName, name, helmRepo, helmChart string) {
	bundle := &v1alpha1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				v1alpha1.RepoLabel: gitRepoName,
			},
		},
		Spec: v1alpha1.BundleSpec{
			BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
				Helm: &v1alpha1.HelmOptions{
					Repo:  helmRepo,
					Chart: helmChart,
				},
			},
			Targets: []v1alpha1.BundleTarget{},
		},
	}
	Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())
	DeferCleanup(func() {
		Expect(k8sClient.Delete(ctx, &v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		})).ToNot(HaveOccurred())
	})
}

var _ = Describe("HelmURLRegex migration", func() {
	var namespace string

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		})).ToNot(HaveOccurred())

		deleteHelmURLRegexMarker()

		DeferCleanup(func() {
			deleteHelmURLRegexMarker()
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: namespace},
			})).ToNot(HaveOccurred())
		})
	})

	createGitRepo := func(name string, helmSecretName, helmSecretNameForPaths, helmRepoURLRegex string) {
		gr := &v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: v1alpha1.GitRepoSpec{
				Repo:                   "https://github.com/rancher/fleet-test-data/not-found",
				HelmSecretName:         helmSecretName,
				HelmSecretNameForPaths: helmSecretNameForPaths,
				HelmRepoURLRegex:       helmRepoURLRegex,
			},
		}
		Expect(k8sClient.Create(ctx, gr)).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			})).ToNot(HaveOccurred())
		})
	}

	getGitRepo := func(name string) *v1alpha1.GitRepo {
		gr := &v1alpha1.GitRepo{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, gr)).To(Succeed())
		return gr
	}

	It("derives regex from https helm.repo URL in existing Bundle", func() {
		createGitRepo("migrate-https", "some-secret", "", "")
		createMigrationBundle(namespace, "migrate-https", "bundle-https",
			"https://charts.example.com/stable", "")

		Expect(runHelmURLRegexMigration()).To(Succeed())

		gr := getGitRepo("migrate-https")
		Expect(gr.Spec.HelmRepoURLRegex).To(Equal(`^https://charts\.example\.com/`))
		Expect(gr.Annotations).To(HaveKeyWithValue(migrate.HelmRegexAutoMigratedAnnotation, "true"))
	})

	It("derives regex from oci helm.chart URL in existing Bundle", func() {
		createGitRepo("migrate-oci", "", "some-secret", "")
		createMigrationBundle(namespace, "migrate-oci", "bundle-oci",
			"", "oci://registry.example.com/org/chart")

		Expect(runHelmURLRegexMigration()).To(Succeed())

		gr := getGitRepo("migrate-oci")
		Expect(gr.Spec.HelmRepoURLRegex).To(Equal(`^oci://registry\.example\.com/`))
		Expect(gr.Annotations).To(HaveKeyWithValue(migrate.HelmRegexAutoMigratedAnnotation, "true"))
	})

	It("includes port in derived regex when the URL has a non-standard port", func() {
		createGitRepo("migrate-port", "some-secret", "", "")
		createMigrationBundle(namespace, "migrate-port", "bundle-port",
			"https://charts.example.com:8443/stable", "")

		Expect(runHelmURLRegexMigration()).To(Succeed())

		gr := getGitRepo("migrate-port")
		Expect(gr.Spec.HelmRepoURLRegex).To(Equal(`^https://charts\.example\.com:8443/`))
		Expect(gr.Annotations).To(HaveKeyWithValue(migrate.HelmRegexAutoMigratedAnnotation, "true"))
	})

	It("builds alternation regex for multiple distinct helm origins", func() {
		createGitRepo("migrate-multi", "some-secret", "", "")
		createMigrationBundle(namespace, "migrate-multi", "bundle-a",
			"https://charts.example.com/stable", "")
		createMigrationBundle(namespace, "migrate-multi", "bundle-b",
			"", "oci://registry.example.com/org/chart")

		Expect(runHelmURLRegexMigration()).To(Succeed())

		gr := getGitRepo("migrate-multi")
		Expect(gr.Spec.HelmRepoURLRegex).To(HavePrefix("^("))
		Expect(gr.Spec.HelmRepoURLRegex).To(ContainSubstring(`https://charts\.example\.com/`))
		Expect(gr.Spec.HelmRepoURLRegex).To(ContainSubstring(`oci://registry\.example\.com/`))
		Expect(gr.Annotations).To(HaveKeyWithValue(migrate.HelmRegexAutoMigratedAnnotation, "true"))
	})

	It("deduplicates identical origins across Bundles", func() {
		createGitRepo("migrate-dedup", "some-secret", "", "")
		createMigrationBundle(namespace, "migrate-dedup", "bundle-c",
			"https://charts.example.com/stable", "")
		createMigrationBundle(namespace, "migrate-dedup", "bundle-d",
			"https://charts.example.com/stable", "")

		Expect(runHelmURLRegexMigration()).To(Succeed())

		// Single distinct origin → no alternation group.
		gr := getGitRepo("migrate-dedup")
		Expect(gr.Spec.HelmRepoURLRegex).To(Equal(`^https://charts\.example\.com/`))
		Expect(gr.Annotations).To(HaveKeyWithValue(migrate.HelmRegexAutoMigratedAnnotation, "true"))
	})

	It("leaves helmRepoURLRegex empty and skips annotation when no Bundles exist", func() {
		createGitRepo("migrate-no-bundles", "some-secret", "", "")

		Expect(runHelmURLRegexMigration()).To(Succeed())

		gr := getGitRepo("migrate-no-bundles")
		Expect(gr.Spec.HelmRepoURLRegex).To(BeEmpty())
		Expect(gr.Annotations).NotTo(HaveKey(migrate.HelmRegexAutoMigratedAnnotation))
	})

	It("ignores plain chart names (non-URL) in Bundle helm.chart", func() {
		createGitRepo("migrate-plain-chart", "some-secret", "", "")
		createMigrationBundle(namespace, "migrate-plain-chart", "bundle-plain",
			"", "stable/nginx")

		Expect(runHelmURLRegexMigration()).To(Succeed())

		// Non-URL chart → no regex derivable → left empty.
		Expect(getGitRepo("migrate-plain-chart").Spec.HelmRepoURLRegex).To(BeEmpty())
	})

	It("does not migrate GitRepos without a helm secret", func() {
		createGitRepo("no-secret", "", "", "")

		Expect(runHelmURLRegexMigration()).To(Succeed())

		Expect(getGitRepo("no-secret").Spec.HelmRepoURLRegex).To(BeEmpty())
	})

	It("does not overwrite an existing helmRepoURLRegex", func() {
		createGitRepo("has-regex", "some-secret", "", `https://charts\.example\.com.*`)
		createMigrationBundle(namespace, "has-regex", "bundle-existing",
			"https://charts.example.com/stable", "")

		Expect(runHelmURLRegexMigration()).To(Succeed())

		Expect(getGitRepo("has-regex").Spec.HelmRepoURLRegex).To(Equal(`https://charts\.example\.com.*`))
	})

	It("skips migration when the marker ConfigMap already exists", func() {
		createGitRepo("no-migrate", "some-secret", "", "")
		createMigrationBundle(namespace, "no-migrate", "bundle-skip",
			"https://charts.example.com/stable", "")

		Expect(k8sClient.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      migrationMarkerName,
				Namespace: migrationSystemNamespace,
			},
		})).To(Succeed())

		Expect(runHelmURLRegexMigration()).To(Succeed())

		// Marker was present → migration did not run.
		Expect(getGitRepo("no-migrate").Spec.HelmRepoURLRegex).To(BeEmpty())
	})

	It("creates the marker ConfigMap after migration", func() {
		Expect(runHelmURLRegexMigration()).To(Succeed())

		marker := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Namespace: migrationSystemNamespace,
			Name:      migrationMarkerName,
		}, marker)).To(Succeed())
	})

	It("treats second run as no-op once marker is created", func() {
		createGitRepo("run-twice", "some-secret", "", "")
		createMigrationBundle(namespace, "run-twice", "bundle-twice",
			"https://charts.example.com/stable", "")

		// First run: migrates GitRepo and creates marker.
		Expect(runHelmURLRegexMigration()).To(Succeed())
		Expect(getGitRepo("run-twice").Spec.HelmRepoURLRegex).To(Equal(`^https://charts\.example\.com/`))

		// Manually reset to simulate a hypothetical re-entry attempt.
		gr := getGitRepo("run-twice")
		gr.Spec.HelmRepoURLRegex = ""
		Expect(k8sClient.Update(ctx, gr)).To(Succeed())

		// Second run: marker present, migration skipped.
		Expect(runHelmURLRegexMigration()).To(Succeed())
		Expect(getGitRepo("run-twice").Spec.HelmRepoURLRegex).To(BeEmpty())
	})
})
