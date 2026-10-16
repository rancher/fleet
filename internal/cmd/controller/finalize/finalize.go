package finalize

import (
	"context"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	HelmOpFinalizer           = "fleet.cattle.io/helmop-finalizer"
	GitRepoFinalizer          = "fleet.cattle.io/gitrepo-finalizer"
	BundleFinalizer           = "fleet.cattle.io/bundle-finalizer"
	BundleDeploymentFinalizer = "fleet.cattle.io/bundle-deployment-finalizer"
	ClusterFinalizer          = "fleet.cattle.io/cluster-finalizer"
	ScheduleFinalizer         = "fleet.cattle.io/schedule-finalizer"
)

// PurgeBundles deletes all bundles related to the given resource namespaced name
// It deletes resources in cascade. Deleting Bundles, its BundleDeployments, and
// the related namespace if Bundle.Spec.DeleteNamespace is set to true.
func PurgeBundles(ctx context.Context, c client.Client, gitrepo types.NamespacedName, resourceLabel string) error {
	bundles := &v1alpha1.BundleList{}
	err := c.List(ctx, bundles, client.MatchingLabels{resourceLabel: gitrepo.Name}, client.InNamespace(gitrepo.Namespace))
	if err != nil {
		return err
	}

	for _, bundle := range bundles.Items {
		// Just delete the bundle and let the Bundle reconciler purge its BundleDeployments
		err := c.Delete(ctx, &bundle)
		if client.IgnoreNotFound(err) != nil {
			return err
		}
	}

	return nil
}

// EnsureFinalizer adds a finalizer to the given object if it doesn't exist.
func EnsureFinalizer(ctx context.Context, c client.Client, obj client.Object, finalizer string) error {
	if controllerutil.ContainsFinalizer(obj, finalizer) {
		return nil
	}

	controllerutil.AddFinalizer(obj, finalizer)
	return c.Update(ctx, obj)
}
