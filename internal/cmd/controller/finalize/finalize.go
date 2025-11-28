package finalize

import (
	"context"
	"slices"
	"strings"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	HelmOpFinalizer           = "fleet.cattle.io/helmop-finalizer"
	GitRepoFinalizer          = "fleet.cattle.io/gitrepo-finalizer"
	BundleFinalizer           = "fleet.cattle.io/bundle-finalizer"
	BundleDeploymentFinalizer = "fleet.cattle.io/bundle-deployment-finalizer"
	ClusterFinalizer          = "fleet.cattle.io/cluster-finalizer"
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

	// At this point, access to the GitRepo is unavailable as it has been deleted and cannot be found within the cluster.
	// Nevertheless, `deleteNamespace` can be found within all bundles generated from that GitRepo. Checking any bundle to get this value would be enough.
	namespace := ""
	deleteNamespace := false
	sampleBundle := v1alpha1.Bundle{}
	if len(bundles.Items) > 0 {
		sampleBundle = bundles.Items[0]
		deleteNamespace = sampleBundle.Spec.DeleteNamespace
		namespace = sampleBundle.Spec.TargetNamespace

		if sampleBundle.Spec.KeepResources {
			deleteNamespace = false
		}
	}

	if err = PurgeNamespace(ctx, c, deleteNamespace, namespace); err != nil {
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

// PurgeNamespace deletes the given namespace if deleteNamespace is set to true.
// It ignores the following namespaces, that are considered as default by fleet or kubernetes:
// fleet-local, cattle-fleet-system, fleet-default, cattle-fleet-clusters-system, default
func PurgeNamespace(ctx context.Context, c client.Client, deleteNamespace bool, ns string) error {
	if !deleteNamespace {
		return nil
	}

	if ns == "" {
		return nil
	}

	// Ignore default namespaces
	defaultNamespaces := []string{"fleet-local", "cattle-fleet-system", "fleet-default", "cattle-fleet-clusters-system", "default"}
	if slices.Contains(defaultNamespaces, ns) {
		return nil
	}

	// Ignore system namespaces
	if _, isKubeNamespace := strings.CutPrefix(ns, "kube-"); isKubeNamespace {
		return nil
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}
	if err := c.Delete(ctx, namespace); err != nil {
		return err
	}

	return nil
}

func PurgeTargetNamespaceIfNeeded(ctx context.Context, c client.Client, gitrepo *v1alpha1.GitRepo) error {
	deleteNamespace := gitrepo.Spec.DeleteNamespace
	namespace := gitrepo.Spec.TargetNamespace

	if gitrepo.Spec.KeepResources {
		deleteNamespace = false
	}

	return PurgeNamespace(ctx, c, deleteNamespace, namespace)
}
