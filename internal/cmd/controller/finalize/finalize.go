package finalize

import (
	"context"
	"slices"
	"strings"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	GitRepoFinalizer          = "fleet.cattle.io/gitrepo-finalizer"
	BundleFinalizer           = "fleet.cattle.io/bundle-finalizer"
	BundleDeploymentFinalizer = "fleet.cattle.io/bundle-deployment-finalizer"
)

// PurgeBundles deletes all bundles related to the given GitRepo namespaced name
// It deletes resources in cascade. Deleting Bundles, its BundleDeployments, and
// the related namespace if Bundle.Spec.DeleteNamespace is set to true.
func PurgeBundles(ctx context.Context, c client.Client, gitrepo types.NamespacedName) error {
	bundles := &v1alpha1.BundleList{}
	err := c.List(ctx, bundles, client.MatchingLabels{v1alpha1.RepoLabel: gitrepo.Name}, client.InNamespace(gitrepo.Namespace))
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
		err := c.Delete(ctx, &bundle)
		if client.IgnoreNotFound(err) != nil {
			return err
		}

		nn := types.NamespacedName{Namespace: bundle.Namespace, Name: bundle.Name}
		if err = PurgeBundleDeployments(ctx, c, nn); err != nil {
			return client.IgnoreNotFound(err)
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
		if controllerutil.ContainsFinalizer(&bd, BundleDeploymentFinalizer) { // nolint: gosec // does not store pointer
			nn := types.NamespacedName{Namespace: bd.Namespace, Name: bd.Name}
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
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

		err := c.Delete(ctx, &bd)
		if err != nil {
			return err
		}
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
