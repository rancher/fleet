package helmdeployer

import (
	"context"
	"strings"

	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// RemoveExternalChanges does a helm rollback to remove changes made outside of fleet.
// It removes the helm history entry if the rollback fails.
func (h *Helm) RemoveExternalChanges(ctx context.Context, bd *fleet.BundleDeployment) error {
	log.FromContext(ctx).WithName("RemoveExternalChanges").Info("Drift correction: rollback")

	_, defaultNamespace, releaseName := h.getOpts(bd.Name, bd.Spec.Options)
	cfg, err := h.getCfg(ctx, defaultNamespace, bd.Spec.Options.ServiceAccount)
	if err != nil {
		return err
	}
	currentRelease, err := cfg.Releases.Last(releaseName)
	if err != nil {
		return err
	}

	r := action.NewRollback(&cfg)
	r.Version = currentRelease.Version
	r.MaxHistory = bd.Spec.Options.Helm.MaxHistory
	if r.MaxHistory == 0 {
		r.MaxHistory = MaxHelmHistory
	}
	if bd.Spec.CorrectDrift.Force {
		r.Force = true
	}
	err = r.Run(releaseName)
	if err != nil && !bd.Spec.CorrectDrift.KeepFailHistory {
		return removeFailedRollback(cfg, currentRelease, err)
	}

	return err
}

func removeFailedRollback(cfg action.Configuration, currentRelease *release.Release, err error) error {
	failedRelease, errRel := cfg.Releases.Last(currentRelease.Name)
	if errRel != nil {
		return errors.Wrap(err, errRel.Error())
	}
	if failedRelease.Version == currentRelease.Version+1 &&
		failedRelease.Info.Status == release.StatusFailed &&
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
