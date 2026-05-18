package migrate

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestHelmURLToRegexPrefix(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		expected string
	}{
		{
			name:     "https URL with path",
			rawURL:   "https://charts.example.com/stable",
			expected: `https://charts\.example\.com/`,
		},
		{
			name:     "https URL root",
			rawURL:   "https://charts.example.com",
			expected: `https://charts\.example\.com/`,
		},
		{
			name:     "https URL with non-standard port",
			rawURL:   "https://charts.example.com:8443/stable",
			expected: `https://charts\.example\.com:8443/`,
		},
		{
			name:     "oci URL",
			rawURL:   "oci://registry.example.com/org/chart",
			expected: `oci://registry\.example\.com/`,
		},
		{
			name:     "http URL",
			rawURL:   "http://chartmuseum.internal:8080/charts",
			expected: `http://chartmuseum\.internal:8080/`,
		},
		{
			name:     "dots in hostname are escaped",
			rawURL:   "https://a.b.c.example.com/repo",
			expected: `https://a\.b\.c\.example\.com/`,
		},
		{
			name:     "empty string",
			rawURL:   "",
			expected: "",
		},
		{
			name:     "plain chart name — not a URL",
			rawURL:   "stable/nginx",
			expected: "",
		},
		{
			name:     "unparsable URL",
			rawURL:   "://bad",
			expected: "",
		},
		{
			name:     "URL with no host",
			rawURL:   "oci:///no-host",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := helmURLToRegexPrefix(tt.rawURL)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestCollectBundleHelmURLs(t *testing.T) {
	bundle := &v1alpha1.Bundle{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: v1alpha1.BundleSpec{
			BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
				Helm: &v1alpha1.HelmOptions{
					Repo:  "https://charts.example.com/stable",
					Chart: "nginx", // plain chart name — not a URL
				},
			},
			Targets: []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Chart: "oci://registry.example.com/org/chart",
						},
					},
				},
				{
					// nil Helm — should not panic
				},
			},
		},
	}

	urls := collectBundleHelmURLs(bundle)
	assert.Equal(t, []string{
		"https://charts.example.com/stable",
		"oci://registry.example.com/org/chart",
	}, urls)
}

func TestCollectBundleHelmURLsNilHelm(t *testing.T) {
	bundle := &v1alpha1.Bundle{
		ObjectMeta: metav1.ObjectMeta{Name: "no-helm"},
		Spec: v1alpha1.BundleSpec{
			Targets: []v1alpha1.BundleTarget{},
		},
	}
	assert.Empty(t, collectBundleHelmURLs(bundle))
}

func TestNeedsHelmURLRegexMigration(t *testing.T) {
	tests := []struct {
		name                   string
		helmSecretName         string
		helmSecretNameForPaths string
		helmRepoURLRegex       string
		want                   bool
	}{
		{"secret + no regex → needs migration", "s", "", "", true},
		{"secretForPaths + no regex → needs migration", "", "s", "", true},
		{"both secrets + no regex → needs migration", "s", "s", "", true},
		{"secret + regex already set → skip", "s", "", "^https://", false},
		{"no secret → skip", "", "", "", false},
		{"no secret + regex already set → skip", "", "", "^https://", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gr := &v1alpha1.GitRepo{
				Spec: v1alpha1.GitRepoSpec{
					HelmSecretName:         tt.helmSecretName,
					HelmSecretNameForPaths: tt.helmSecretNameForPaths,
					HelmRepoURLRegex:       tt.helmRepoURLRegex,
				},
			}
			assert.Equal(t, tt.want, needsHelmURLRegexMigration(gr))
		})
	}
}

// TestMigrateAllErrors verifies that when multiple GitRepos fail to update,
// all errors are aggregated and returned rather than stopping at the first one.
func TestMigrateAllErrors(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))

	makeGitRepo := func(name string) *v1alpha1.GitRepo {
		return &v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       v1alpha1.GitRepoSpec{HelmSecretName: "s", Repo: "https://github.com/x/y"},
		}
	}
	// One Bundle per GitRepo so deriveHelmRepoURLRegex returns a non-empty
	// regex and migrateOne proceeds to the Update call.
	makeBundle := func(gitRepoName string) *v1alpha1.Bundle {
		return &v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      gitRepoName + "-bundle",
				Namespace: "default",
				Labels:    map[string]string{v1alpha1.RepoLabel: gitRepoName},
			},
			Spec: v1alpha1.BundleSpec{
				BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
					Helm: &v1alpha1.HelmOptions{Repo: "https://charts.example.com/stable"},
				},
			},
		}
	}

	gr1 := makeGitRepo("gr1")
	gr2 := makeGitRepo("gr2")

	// Both Updates fail with distinct errors.
	errGR1 := errors.New("injected failure for gr1")
	errGR2 := errors.New("injected failure for gr2")

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(gr1, gr2, makeBundle("gr1"), makeBundle("gr2")).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				switch obj.GetName() {
				case "gr1":
					return errGR1
				case "gr2":
					return errGR2
				}
				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()

	err := migrateAllGitRepos(context.Background(), cl)
	require.Error(t, err)
	require.ErrorIs(t, err, errGR1, "error for gr1 must be present in the aggregated error")
	require.ErrorIs(t, err, errGR2, "error for gr2 must be present in the aggregated error")
}
