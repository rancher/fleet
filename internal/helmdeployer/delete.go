package helmdeployer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/kube"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/kv"
	"github.com/rancher/fleet/internal/experimental"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// DeleteRelease deletes the release for the DeployedBundle.
func (h *Helm) DeleteRelease(ctx context.Context, deployment DeployedBundle) error {
	return h.deleteByRelease(ctx, deployment.BundleID, deployment.ReleaseName, deployment.KeepResources)
}

// Delete the release for the given bundleID. The bundleID is the name of the
// bundledeployment.
func (h *Helm) Delete(ctx context.Context, bundleID string) error {
	releaseName := ""
	keepResources := false
	deployments, err := h.ListDeployments(h.NewListAction())
	if err != nil {
		return err
	}
	for _, deployment := range deployments {
		if deployment.BundleID == bundleID {
			releaseName = deployment.ReleaseName
			keepResources = deployment.KeepResources
			break
		}
	}
	if releaseName == "" {
		// Never found anything to delete
		return nil
	}
	return h.deleteByRelease(ctx, bundleID, releaseName, keepResources)
}

func (h *Helm) deleteByRelease(ctx context.Context, bundleID, releaseName string, keepResources bool) error {
	logger := log.FromContext(ctx).WithName("delete-by-release").WithValues("releaseName", releaseName, "keepResources", keepResources)
	releaseNamespace, releaseName := kv.Split(releaseName, "/")
	rels, err := listReleases(h.globalCfg.Releases, func(r *releasev1.Release) bool {
		return r.Namespace == releaseNamespace &&
			r.Name == releaseName &&
			r.Chart.Metadata.Annotations[BundleIDAnnotation] == bundleID &&
			r.Chart.Metadata.Annotations[AgentNamespaceAnnotation] == h.agentNamespace
	})
	if err != nil {
		return err
	}
	if len(rels) == 0 {
		return nil
	}

	var (
		serviceAccountName string
	)
	for _, rel := range rels {
		serviceAccountName = rel.Chart.Metadata.Annotations[ServiceAccountNameAnnotation]
		if serviceAccountName != "" {
			break
		}
	}

	cfg, err := h.getCfg(ctx, releaseNamespace, serviceAccountName)
	if err != nil {
		return err
	}

	if strings.HasPrefix(bundleID, "fleet-agent") {
		// Never uninstall the fleet-agent, just "forget" it
		return deleteHistory(cfg, logger, bundleID)
	}

	if keepResources {
		// don't delete resources, just delete the helm release secrets
		return deleteHistory(cfg, logger, bundleID)
	}

	u := action.NewUninstall(cfg)
	// WaitStrategy must be set in Helm v4 to avoid "unknown wait strategy" error
	// HookOnlyStrategy is the default behavior (equivalent to not waiting)
	u.WaitStrategy = kube.HookOnlyStrategy
	if _, err := u.Run(releaseName); err != nil {
		return fmt.Errorf("failed to delete release %s: %v", releaseName, err)
	}

	return deleteResourcesCopiedFromUpstream(ctx, h.client, bundleID)
}

func (h *Helm) delete(ctx context.Context, bundleID string, options fleet.BundleDeploymentOptions, dryRun bool) error {
	logger := log.FromContext(ctx).WithName("helm-deployer").WithName("delete").WithValues("dryRun", dryRun)
	timeout, _, releaseName := h.getOpts(bundleID, options)

	r, err := getLastRelease(h.globalCfg.Releases, releaseName)
	if err != nil {
		// If the release doesn't exist, there's nothing to delete
		if errors.Is(err, driver.ErrReleaseNotFound) || errors.Is(err, driver.ErrNoDeployedReleases) {
			return nil
		}
		return err
	}

	if r.Chart.Metadata.Annotations[BundleIDAnnotation] != bundleID {
		rels, err := getReleaseHistory(h.globalCfg.Releases, releaseName)
		if err != nil {
			// If we can't get the history, treat it as not found
			if errors.Is(err, driver.ErrReleaseNotFound) || errors.Is(err, driver.ErrNoDeployedReleases) {
				return nil
			}
			return err
		}
		r = nil
		for _, rel := range rels {
			if rel.Chart.Metadata.Annotations[BundleIDAnnotation] == bundleID {
				r = rel
				break
			}
		}
		if r == nil {
			return fmt.Errorf("failed to find helm release to delete for %s", bundleID)
		}
	}

	serviceAccountName := r.Chart.Metadata.Annotations[ServiceAccountNameAnnotation]
	cfg, err := h.getCfg(ctx, r.Namespace, serviceAccountName)
	if err != nil {
		return err
	}

	if strings.HasPrefix(bundleID, "fleet-agent") {
		// Never uninstall the fleet-agent, just "forget" it
		return deleteHistory(cfg, logger, bundleID)
	}

	u := action.NewUninstall(cfg)
	// WaitStrategy must be set in Helm v4 to avoid "unknown wait strategy" error
	// HookOnlyStrategy is the default behavior (equivalent to not waiting)
	u.WaitStrategy = kube.HookOnlyStrategy
	u.DryRun = dryRun
	u.Timeout = timeout

	if !dryRun {
		logger.Info("Helm: Uninstalling")
	}
	_, err = u.Run(releaseName)
	return err
}

func deleteHistory(cfg *action.Configuration, logger logr.Logger, bundleID string) error {
	releases, err := listReleases(cfg.Releases, func(r *releasev1.Release) bool {
		return r.Name == bundleID && r.Chart.Metadata.Annotations[BundleIDAnnotation] == bundleID
	})
	if err != nil {
		return err
	}
	for _, release := range releases {
		logger.Info("Helm: Deleting release", "releaseVersion", release.Version)
		if _, err := cfg.Releases.Delete(release.Name, release.Version); err != nil {
			return err
		}
	}
	return nil
}

// deleteResourcesCopiedFromUpstream deletes resources referenced through a bundle's `DownstreamResources`
// field, and copied from downstream.
func deleteResourcesCopiedFromUpstream(ctx context.Context, c client.Client, bdName string) error {
	if !experimental.CopyResourcesDownstreamEnabled() {
		return nil
	}

	var merr []error

	// No information is available about a deleted bundle deployment beside its name and namespace;
	// in particular, we do not know where its resources copied from the upstream cluster, if any, might live, so we
	// cannot delete them by name and namespace; instead, we need to resort to labels.
	opts := client.MatchingLabels{
		fleet.BundleDeploymentOwnershipLabel: bdName,
	}

	secrets := corev1.SecretList{}

	// XXX: should we log instead of erroring?
	if err := c.List(ctx, &secrets, opts); err != nil {
		merr = append(merr, fmt.Errorf("failed to list copied secrets from upstream to delete from outdated bundle: %w", err))
	}

	for _, s := range secrets.Items {
		if err := c.Delete(ctx, &s); err != nil {
			merr = append(merr, fmt.Errorf("failed to delete outdated secrets copied from downstream: %w", err))
		}
	}

	cms := corev1.ConfigMapList{}

	if err := c.List(ctx, &cms, opts); err != nil {
		return fmt.Errorf("failed to list copied configmaps from upstream to delete from outdated bundle: %w", err)
	}
	for _, cm := range cms.Items {
		if err := c.Delete(ctx, &cm); err != nil {
			merr = append(merr, fmt.Errorf("failed to delete outdated configmaps copied from downstream: %w", err))
		}
	}

	return errutil.NewAggregate(merr)
}
