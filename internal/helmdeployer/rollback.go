package helmdeployer

import (
	"context"
	"strings"

	"github.com/pkg/errors"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/kube"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// RemoveExternalChanges removes changes made outside of fleet using Helm rollback.
func (h *Helm) RemoveExternalChanges(ctx context.Context, bd *fleet.BundleDeployment) (string, error) {
	log.FromContext(ctx).WithName("remove-external-changes").Info("Drift correction: rollback")

	_, defaultNamespace, releaseName := h.getOpts(bd.Name, bd.Spec.Options)
	cfg, err := h.getCfg(ctx, defaultNamespace, bd.Spec.Options.ServiceAccount)
	if err != nil {
		return "", err
	}
	currentRelease, err := getLastRelease(cfg.Releases, releaseName)
	if err != nil {
		return "", err
	}

	r := action.NewRollback(cfg)
	// When using ForceReplace, must disable ServerSideApply
	// ForceReplace and ServerSideApply cannot be used together in Helm v4
	// Set to "false" (not "auto" or empty string) to explicitly disable server-side apply
	if bd.Spec.CorrectDrift.Force {
		r.ServerSideApply = "false"
		r.ForceReplace = true
	} else {
		// For non-force mode, use "auto" to preserve the apply method from the original release
		r.ServerSideApply = "auto"
	}
	// WaitStrategy must be set in Helm v4 to avoid "unknown wait strategy" error
	// HookOnlyStrategy is the default behavior (equivalent to not waiting)
	r.WaitStrategy = kube.HookOnlyStrategy
	r.Version = currentRelease.Version
	r.MaxHistory = bd.Spec.Options.Helm.MaxHistory
	if r.MaxHistory == 0 {
		r.MaxHistory = MaxHelmHistory
	}
	if err := r.Run(releaseName); err != nil {
		if bd.Spec.CorrectDrift.KeepFailHistory {
			return "", err
		}
		return "", removeFailedRollback(cfg, currentRelease, err)
	}
	release, err := getLastRelease(cfg.Releases, releaseName)
	if err != nil {
		return "", err
	}
	return ReleaseToResourceID(release), nil
}

func removeFailedRollback(cfg *action.Configuration, currentRelease *releasev1.Release, err error) error {
	failedRelease, errRel := getLastRelease(cfg.Releases, currentRelease.Name)
	if errRel != nil {
		return errors.Wrap(err, errRel.Error())
	}
	if failedRelease.Version == currentRelease.Version+1 &&
		failedRelease.Info.Status == releasecommon.StatusFailed &&
		strings.HasPrefix(failedRelease.Info.Description, "Rollback") {
		_, errDel := cfg.Releases.Delete(failedRelease.Name, failedRelease.Version)
		if errDel != nil {
			return errors.Wrap(err, errDel.Error())
		}
		errUpdate := cfg.Releases.Update(currentRelease)
		if errUpdate != nil {
			return errors.Wrap(err, errUpdate.Error())
		}
	}

	return err
}
