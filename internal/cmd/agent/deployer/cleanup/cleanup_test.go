//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/helm_deployer_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup HelmDeployer

package cleanup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

	mockHelmDeployer := mocks.NewMockHelmDeployer(mockCtrl)
	mockHelmDeployer.EXPECT().NewListAction()
	mockHelmDeployer.EXPECT().ListDeployments(gomock.Any()).Return(deployedBundles, nil)
	mockHelmDeployer.EXPECT().DeleteRelease(gomock.Any(), deployedBundles[1]).Return(nil)
	mockHelmDeployer.EXPECT().DeleteRelease(gomock.Any(), deployedBundles[2]).Return(nil)

	cleanup := New(mockClient, mockClient, nil, nil, mockHelmDeployer, fleetNS, defaultNS, 1*time.Second)

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

	cleanup := New(mockClient, mockClient, nil, nil, mockHelmDeployer, fleetNS, defaultNS, 1*time.Second)

	err := cleanup.cleanup(context.Background(), log.FromContext(context.Background()).WithName("test"))

	if err != nil {
		t.Errorf("cleanup failed: %v", err)
	}
}

// notFound builds the error the cache returns for a BundleDeployment it does
// not hold, which is the same error the API server returns for one that does
// not exist. Telling those two apart is the whole point of the specs below.
func notFound() error {
	return apierrors.NewNotFound(
		schema.GroupResource{Group: fleet.SchemeGroupVersion.Group, Resource: "bundledeployments"},
		"ID1",
	)
}

// TestCleanupKeepsReleaseWhenCacheIsStale covers rancher/fleet#5406: a
// BundleDeployment missing from the agent's cache but still present on the API
// server must not have its release uninstalled.
func TestCleanupKeepsReleaseWhenCacheIsStale(t *testing.T) {
	fleetNS := "foo"
	defaultNS := "bar"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	deployedBundles := []helmdeployer.DeployedBundle{
		{BundleID: "ID1", ReleaseName: defaultNS + "/TestRelease1"},
	}
	nsn := types.NamespacedName{Namespace: fleetNS, Name: "ID1"}

	// The cache has lost the object...
	cache := mocks.NewMockK8sClient(mockCtrl)
	cache.EXPECT().Get(gomock.Any(), nsn, gomock.Any()).Return(notFound())

	// ...but the API server still has it.
	reader := mocks.NewMockK8sClient(mockCtrl)
	reader.EXPECT().Get(gomock.Any(), nsn, gomock.Any()).Return(nil)

	mockHelmDeployer := mocks.NewMockHelmDeployer(mockCtrl)
	mockHelmDeployer.EXPECT().NewListAction()
	mockHelmDeployer.EXPECT().ListDeployments(gomock.Any()).Return(deployedBundles, nil)
	// No DeleteRelease call is expected; gomock fails the test if one happens.

	cleanup := New(cache, reader, nil, nil, mockHelmDeployer, fleetNS, defaultNS, 1*time.Second)

	if err := cleanup.cleanup(context.Background(), log.FromContext(context.Background()).WithName("test")); err != nil {
		t.Errorf("cleanup failed: %v", err)
	}
}

// TestCleanupDeletesReleaseWhenReallyGone makes sure the fix for #5406 did not
// disable the feature: a BundleDeployment the API server confirms is gone
// still gets its release uninstalled.
func TestCleanupDeletesReleaseWhenReallyGone(t *testing.T) {
	fleetNS := "foo"
	defaultNS := "bar"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	deployedBundles := []helmdeployer.DeployedBundle{
		{BundleID: "ID1", ReleaseName: defaultNS + "/TestRelease1"},
	}
	nsn := types.NamespacedName{Namespace: fleetNS, Name: "ID1"}

	cache := mocks.NewMockK8sClient(mockCtrl)
	cache.EXPECT().Get(gomock.Any(), nsn, gomock.Any()).Return(notFound())

	reader := mocks.NewMockK8sClient(mockCtrl)
	reader.EXPECT().Get(gomock.Any(), nsn, gomock.Any()).Return(notFound())

	mockHelmDeployer := mocks.NewMockHelmDeployer(mockCtrl)
	mockHelmDeployer.EXPECT().NewListAction()
	mockHelmDeployer.EXPECT().ListDeployments(gomock.Any()).Return(deployedBundles, nil)
	mockHelmDeployer.EXPECT().DeleteRelease(gomock.Any(), deployedBundles[0]).Return(nil)

	cleanup := New(cache, reader, nil, nil, mockHelmDeployer, fleetNS, defaultNS, 1*time.Second)

	if err := cleanup.cleanup(context.Background(), log.FromContext(context.Background()).WithName("test")); err != nil {
		t.Errorf("cleanup failed: %v", err)
	}
}

// TestCleanupKeepsReleaseWhenConfirmationFails checks that an unconfirmed read
// is never treated as a deletion either.
func TestCleanupKeepsReleaseWhenConfirmationFails(t *testing.T) {
	fleetNS := "foo"
	defaultNS := "bar"

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	deployedBundles := []helmdeployer.DeployedBundle{
		{BundleID: "ID1", ReleaseName: defaultNS + "/TestRelease1"},
	}
	nsn := types.NamespacedName{Namespace: fleetNS, Name: "ID1"}

	cache := mocks.NewMockK8sClient(mockCtrl)
	cache.EXPECT().Get(gomock.Any(), nsn, gomock.Any()).Return(notFound())

	reader := mocks.NewMockK8sClient(mockCtrl)
	reader.EXPECT().Get(gomock.Any(), nsn, gomock.Any()).Return(errors.New("connection refused"))

	mockHelmDeployer := mocks.NewMockHelmDeployer(mockCtrl)
	mockHelmDeployer.EXPECT().NewListAction()
	mockHelmDeployer.EXPECT().ListDeployments(gomock.Any()).Return(deployedBundles, nil)
	// No DeleteRelease call is expected.

	cleanup := New(cache, reader, nil, nil, mockHelmDeployer, fleetNS, defaultNS, 1*time.Second)

	err := cleanup.cleanup(context.Background(), log.FromContext(context.Background()).WithName("test"))
	if err == nil {
		t.Error("expected cleanup to report that it could not confirm the deletion")
	}
}
