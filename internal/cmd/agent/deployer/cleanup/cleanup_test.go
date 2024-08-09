//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/helm_deployer_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup HelmDeployer

package cleanup

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/mocks"
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

	mockClient := mocks.NewMockClient(mockCtrl)
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
