package finalize

import (
	"context"
	"slices"
	"strings"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/kv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	HelmAppFinalizer          = "fleet.cattle.io/helmapp-finalizer"
	GitRepoFinalizer          = "fleet.cattle.io/gitrepo-finalizer"
	BundleFinalizer           = "fleet.cattle.io/bundle-finalizer"
	BundleDeploymentFinalizer = "fleet.cattle.io/bundle-deployment-finalizer"
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

// PurgeBundleDeployments deletes all BundleDeployments related with the given Bundle namespaced name.
func PurgeBundleDeployments(ctx context.Context, c client.Client, bundle types.NamespacedName) error {
	list := &v1alpha1.BundleDeploymentList{}
	err := c.List(
		ctx,
		list,
		client.MatchingLabels{
			v1alpha1.BundleLabel:          bundle.Name,
			v1alpha1.BundleNamespaceLabel: bundle.Namespace,
		},
	)
	if err != nil {
		return err
	}
	for _, bd := range list.Items {
		if err := PurgeBundleDeployment(ctx, c, bd); err != nil {
			return err
		}
	}

	return nil
}

// PurgeContent tries to delete the content resource related with the given bundle deployment.
func PurgeContent(ctx context.Context, c client.Client, name, deplID string) error {
	contentID, _ := kv.Split(deplID, ":")
	content := &v1alpha1.Content{}
	if err := c.Get(ctx, types.NamespacedName{Name: contentID}, content); err != nil {
		return client.IgnoreNotFound(err)
	}

	logger := log.FromContext(ctx).WithName("purge-content").WithValues("contentID", contentID, "finalizerName", name)

	nn := types.NamespacedName{Name: content.Name}
	if controllerutil.ContainsFinalizer(content, name) {
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := c.Get(ctx, nn, content); err != nil {
				return err
			}

			controllerutil.RemoveFinalizer(content, name)

			return c.Update(ctx, content)
		})
		if err != nil {
			return err
		}

		logger.V(1).Info("Removed finalizer from content resource")
	}

	if len(content.Finalizers) == 0 {
		if err := c.Delete(ctx, content); err != nil {
			return err
		}
		logger.V(1).Info("Deleted content resource")
	}

	return nil
}

// PurgeImageScans deletes all ImageScan resources related with the given GitRepo namespaces name.
func PurgeImageScans(ctx context.Context, c client.Client, gitrepo types.NamespacedName) error {
	images := &v1alpha1.ImageScanList{}
	err := c.List(ctx, images, client.InNamespace(gitrepo.Namespace))
	if err != nil {
		return err
	}

	for _, image := range images.Items {
		if image.Spec.GitRepoName == gitrepo.Name {
			err := c.Delete(ctx, &image)
			if err != nil {
				return err
			}
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

// PurgeBundleDeploymentList deletes the given list of bundledeployments
func PurgeBundleDeploymentList(ctx context.Context, c client.Client, bds []types.NamespacedName) error {
	for _, bdId := range bds {
		bd := &v1alpha1.BundleDeployment{}
		if err := c.Get(ctx, bdId, bd); err != nil {
			return err
		}
		if err := PurgeBundleDeployment(ctx, c, *bd); err != nil {
			return err
		}
	}
	return nil
}

// PurgeBundleDeployment deletes the given bundle deployment and the content related to it
func PurgeBundleDeployment(ctx context.Context, c client.Client, bd v1alpha1.BundleDeployment) error {
	if controllerutil.ContainsFinalizer(&bd, BundleDeploymentFinalizer) { // nolint: gosec // does not store pointer
		nn := types.NamespacedName{Namespace: bd.Namespace, Name: bd.Name}
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			t := &v1alpha1.BundleDeployment{}
			if err := c.Get(ctx, nn, t); err != nil {
				return err
			}

			controllerutil.RemoveFinalizer(t, BundleDeploymentFinalizer)

			return c.Update(ctx, t)
		})
		if err != nil {
			return err
		}
	}

	if err := c.Delete(ctx, &bd); err != nil {
		return err
	}

	if err := PurgeContent(ctx, c, bd.Name, bd.Spec.DeploymentID); err != nil {
		return err
	}

	return nil
}
