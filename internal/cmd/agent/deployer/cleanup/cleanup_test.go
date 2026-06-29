//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/helm_deployer_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup HelmDeployer

package cleanup

import (
	"context"
	"testing"
	"time"

	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestCleanupReleases(t *testing.T) {
	fleetNS := "foo"   // Used to get bundle deployments by bundle ID
	defaultNS := "bar" // Used to compute the expected release key

	// Long bd.Name that exceeds Helm's 53-char release name limit. The helm
	// install path runs this through names.HelmReleaseName and ends up with
	// a truncated+hashed release name; the GC must compute the same value
	// instead of comparing against the raw bd.Name.(issue #5261)
	longName := "repro-bundle-name-that-exceeds-the-53-char-helm-limit-x"
	longNameID := "ID4"
	longNameRelease := defaultNS + "/" + names.HelmReleaseName(longName)

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	deployedBundles := []helmdeployer.DeployedBundle{
		{
			BundleID:    "ID1",
			ReleaseName: defaultNS + "/TestRelease1",
		},
		{
			BundleID:    "ID2",
			ReleaseName: defaultNS + "/TestRelease2",
		},
		{
			BundleID:    "ID3",
			ReleaseName: defaultNS + "/TestRelease3",
		},
		{
			BundleID:    longNameID,
			ReleaseName: longNameRelease, // must NOT be deleted
		},
	}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	bd := &fleet.BundleDeployment{}
	mockClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: fleetNS, Name: "ID1"}, bd).DoAndReturn(
		func(_ context.Context, _ types.NamespacedName, bd *fleet.BundleDeployment, _ ...any) error {
			bd.Spec.Options.TargetNamespace = defaultNS
			bd.Spec.Options.Helm = &fleet.HelmOptions{
				ReleaseName: "TestRelease1", // will be kept
			}

			return nil
		},
	)

	mockClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: fleetNS, Name: "ID2"}, bd).DoAndReturn(
		func(_ context.Context, _ types.NamespacedName, bd *fleet.BundleDeployment, _ ...any) error {
			bd.Spec.Options.TargetNamespace = defaultNS
			bd.Spec.Options.Helm = &fleet.HelmOptions{
				ReleaseName: "TestRelease2-old", // will be deleted
			}

			return nil
		},
	)

	mockClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: fleetNS, Name: "ID3"}, bd).DoAndReturn(
		func(_ context.Context, _ types.NamespacedName, bd *fleet.BundleDeployment, _ ...any) error {
			bd.Spec.Options.TargetNamespace = defaultNS + "-old" // will be deleted
			bd.Spec.Options.Helm = &fleet.HelmOptions{
				ReleaseName: "TestRelease3",
			}

			return nil
		},
	)

	// Long-name bundle with NO explicit helm.releaseName. The release must be kept.
	mockClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: fleetNS, Name: longNameID}, bd).DoAndReturn(
		func(_ context.Context, _ types.NamespacedName, bd *fleet.BundleDeployment, _ ...any) error {
			bd.Name = longName
			bd.Spec.Options.TargetNamespace = defaultNS
			// no helm.releaseName set on purpose
			return nil
		},
	)

	mockHelmDeployer := mocks.NewMockHelmDeployer(mockCtrl)
	mockHelmDeployer.EXPECT().NewListAction()
	mockHelmDeployer.EXPECT().ListDeployments(gomock.Any()).Return(deployedBundles, nil)
	mockHelmDeployer.EXPECT().DeleteRelease(gomock.Any(), deployedBundles[1]).Return(nil)
	mockHelmDeployer.EXPECT().DeleteRelease(gomock.Any(), deployedBundles[2]).Return(nil)

	cleanup := New(mockClient, nil, nil, mockHelmDeployer, fleetNS, defaultNS, 1*time.Second)

	err := cleanup.cleanup(context.Background(), log.FromContext(context.Background()).WithName("test"))

	if err != nil {
		t.Errorf("cleanup failed: %v", err)
	}
}

func TestReleaseKey(t *testing.T) {
	const defaultNS = "fleet-default"

	// A name longer than MaxHelmReleaseNameLen (53). Used to lock in that
	// releaseKey applies the same truncation+hash as the helm install
	// path (issue #5261).
	longName := "repro-bundle-name-that-exceeds-the-53-char-helm-limit-x"

	tests := []struct {
		name string
		bd   *fleet.BundleDeployment
		want string
	}{
		{
			name: "short name, no helm options, falls back to default namespace",
			bd: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "my-bundle"},
			},
			want: defaultNS + "/my-bundle",
		},
		{
			name: "short name, nil helm options",
			bd: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "my-bundle"},
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{TargetNamespace: "app-ns"},
				},
			},
			want: "app-ns/my-bundle",
		},
		{
			name: "explicit helm.releaseName wins over bd.Name (and is not truncated)",
			bd: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: longName},
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						TargetNamespace: "app-ns",
						Helm:            &fleet.HelmOptions{ReleaseName: "explicit-name"},
					},
				},
			},
			want: "app-ns/explicit-name",
		},
		{
			name: "TargetNamespace overrides default",
			bd: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "my-bundle"},
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						TargetNamespace:  "target",
						DefaultNamespace: "fallback",
					},
				},
			},
			want: "target/my-bundle",
		},
		{
			name: "DefaultNamespace used when TargetNamespace is empty",
			bd: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "my-bundle"},
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{DefaultNamespace: "fallback"},
				},
			},
			want: "fallback/my-bundle",
		},
		{
			// issue #5261: GC must produce the same
			// release name as the helm install path so it does not
			// uninstall a valid release.
			name: "long bd.Name with no helm.releaseName is truncated and hashed like helm install",
			bd: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: longName},
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{TargetNamespace: "app-ns"},
				},
			},
			want: "app-ns/" + names.HelmReleaseName(longName),
		},
		{
			name: "long bd.Name with empty helm.ReleaseName is still truncated",
			bd: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: longName},
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						TargetNamespace: "app-ns",
						Helm:            &fleet.HelmOptions{ReleaseName: ""},
					},
				},
			},
			want: "app-ns/" + names.HelmReleaseName(longName),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := releaseKey(defaultNS, tc.bd)
			if got != tc.want {
				t.Errorf("releaseKey() = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("long name result respects MaxHelmReleaseNameLen", func(t *testing.T) {
		bd := &fleet.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: longName},
		}
		got := releaseKey(defaultNS, bd)
		_, release, ok := splitNSAndName(got)
		if !ok {
			t.Fatalf("releaseKey() = %q, expected ns/name format", got)
		}
		if len(release) > 53 {
			t.Errorf("release name %q is %d chars, exceeds Helm's 53-char limit", release, len(release))
		}
	})
}

// splitNSAndName splits an "ns/name" key. Returns ok=false if no "/" is present.
func splitNSAndName(s string) (ns, name string, ok bool) {
	for i := range len(s) {
		if s[i] == '/' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}
