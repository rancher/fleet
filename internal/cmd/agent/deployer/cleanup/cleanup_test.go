//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/helm_deployer_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup HelmDeployer

package cleanup

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestCleanupReleases(t *testing.T) {
	fleetNS := "foo"   // Used to get bundle deployments by bundle ID
	defaultNS := "bar" // Used to compute the expected release key

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	deployedBundles := []helmdeployer.DeployedBundle{
		{
			BundleID:    "ID1",
			ReleaseName: fmt.Sprintf("%s/TestRelease1", defaultNS),
		},
		{
			BundleID:    "ID2",
			ReleaseName: fmt.Sprintf("%s/TestRelease2", defaultNS),
		},
		{
			BundleID:    "ID3",
			ReleaseName: fmt.Sprintf("%s/TestRelease3", defaultNS),
		},
	}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	bd := &fleet.BundleDeployment{}
	mockClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: fleetNS, Name: "ID1"}, bd).DoAndReturn(
		func(_ context.Context, _ types.NamespacedName, bd *fleet.BundleDeployment, _ ...interface{}) error {
			bd.Spec.Options.TargetNamespace = defaultNS
			bd.Spec.Options.Helm = &fleet.HelmOptions{
				ReleaseName: "TestRelease1", // will be kept
			}

			return nil
		},
	)

	mockClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: fleetNS, Name: "ID2"}, bd).DoAndReturn(
		func(_ context.Context, _ types.NamespacedName, bd *fleet.BundleDeployment, _ ...interface{}) error {
			bd.Spec.Options.TargetNamespace = defaultNS
			bd.Spec.Options.Helm = &fleet.HelmOptions{
				ReleaseName: "TestRelease2-old", // will be deleted
			}

			return nil
		},
	)

	mockClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: fleetNS, Name: "ID3"}, bd).DoAndReturn(
		func(_ context.Context, _ types.NamespacedName, bd *fleet.BundleDeployment, _ ...interface{}) error {
			bd.Spec.Options.TargetNamespace = defaultNS + "-old" // will be deleted
			bd.Spec.Options.Helm = &fleet.HelmOptions{
				ReleaseName: "TestRelease3",
			}

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

// TestCleanupReleasesNoExplicitReleaseName verifies that a release whose name
// was truncated by names.HelmReleaseName is not mistakenly deleted when no
// explicit Helm.ReleaseName is configured on the bundle deployment.
func TestCleanupReleasesNoExplicitReleaseName(t *testing.T) {
	fleetNS := "foo"   // Used to get bundle deployments by bundle ID
	defaultNS := "bar" // Used to compute the expected release key

	// Bundle ID longer than 53 chars; the deployer truncates it via HelmReleaseName.
	longBundleID := "gitrepo-abc123-some-app-bundle-with-a-name-that-exceeds-the-limit"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	deployedBundles := []helmdeployer.DeployedBundle{
		{
			BundleID:    longBundleID,
			ReleaseName: defaultNS + "/" + names.HelmReleaseName(longBundleID),
		},
	}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	bd := &fleet.BundleDeployment{}
	mockClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: fleetNS, Name: longBundleID}, bd).DoAndReturn(
		func(_ context.Context, _ types.NamespacedName, bd *fleet.BundleDeployment, _ ...any) error {
			bd.Name = longBundleID
			bd.Spec.Options.DefaultNamespace = defaultNS // no explicit ReleaseName

			return nil
		},
	)

	mockHelmDeployer := mocks.NewMockHelmDeployer(mockCtrl)
	mockHelmDeployer.EXPECT().NewListAction()
	mockHelmDeployer.EXPECT().ListDeployments(gomock.Any()).Return(deployedBundles, nil)
	// DeleteRelease must NOT be called: the release name matches.

	cleanup := New(mockClient, nil, nil, mockHelmDeployer, fleetNS, defaultNS, 1*time.Second)

	err := cleanup.cleanup(context.Background(), log.FromContext(context.Background()).WithName("test"))

	if err != nil {
		t.Errorf("cleanup failed: %v", err)
	}
}
