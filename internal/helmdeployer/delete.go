package helmdeployer

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/kv"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

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
	logger := log.FromContext(ctx).WithName("deleteByRelease").WithValues("releaseName", releaseName, "keepResources", keepResources)
	releaseNamespace, releaseName := kv.Split(releaseName, "/")
	rels, err := h.globalCfg.Releases.List(func(r *release.Release) bool {
		return r.Namespace == releaseNamespace &&
			r.Name == releaseName &&
			r.Chart.Metadata.Annotations[BundleIDAnnotation] == bundleID &&
			r.Chart.Metadata.Annotations[AgentNamespaceAnnotation] == h.agentNamespace
	})
	if err != nil {
		return nil
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

	u := action.NewUninstall(&cfg)
	_, err = u.Run(releaseName)
	return err
}

func (h *Helm) delete(ctx context.Context, bundleID string, options fleet.BundleDeploymentOptions, dryRun bool) error {
	logger := log.FromContext(ctx).WithName("HelmDeployer").WithName("delete").WithValues("dryRun", dryRun)
	timeout, _, releaseName := h.getOpts(bundleID, options)

	r, err := h.globalCfg.Releases.Last(releaseName)
	if err != nil {
		return nil
	}

	if r.Chart.Metadata.Annotations[BundleIDAnnotation] != bundleID {
		rels, err := h.globalCfg.Releases.History(releaseName)
		if err != nil {
			return nil
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

	u := action.NewUninstall(&cfg)
	u.DryRun = dryRun
	u.Timeout = timeout

	if !dryRun {
		logger.Info("Helm: Uninstalling")
	}
	_, err = u.Run(releaseName)
	return err
}

func deleteHistory(cfg action.Configuration, logger logr.Logger, bundleID string) error {
	releases, err := cfg.Releases.List(func(r *release.Release) bool {
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
