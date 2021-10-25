package bundledeployment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rancher/fleet/modules/agent/pkg/deployer"
	"github.com/rancher/fleet/modules/agent/pkg/trigger"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/condition"
	"github.com/rancher/wrangler/pkg/merr"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
)

const (
	DefaultReapplyInterval = 5 * time.Minute
	DefaultMaxRetries      = -1
	DefaultBackoffInterval = 5 * time.Minute
)

type handler struct {
	cleanupOnce sync.Once

	ctx           context.Context
	trigger       *trigger.Trigger
	deployManager *deployer.Manager
	bdController  fleetcontrollers.BundleDeploymentController
	restMapper    meta.RESTMapper
	dynamic       dynamic.Interface
}

func Register(ctx context.Context,
	trigger *trigger.Trigger,
	restMapper meta.RESTMapper,
	dynamic dynamic.Interface,
	deployManager *deployer.Manager,
	bdController fleetcontrollers.BundleDeploymentController) {

	h := &handler{
		ctx:           ctx,
		trigger:       trigger,
		deployManager: deployManager,
		bdController:  bdController,
		restMapper:    restMapper,
		dynamic:       dynamic,
	}

	fleetcontrollers.RegisterBundleDeploymentStatusHandler(ctx,
		bdController,
		"Deployed",
		"bundle-deploy",
		h.DeployBundle)

	fleetcontrollers.RegisterBundleDeploymentStatusHandler(ctx,
		bdController,
		"Monitored",
		"bundle-monitor",
		h.MonitorBundle)

	bdController.OnChange(ctx, "bundle-trigger", h.Trigger)
	bdController.OnChange(ctx, "bundle-cleanup", h.Cleanup)
}

func (h *handler) garbageCollect() {
	for {
		if err := h.deployManager.Cleanup(); err != nil {
			logrus.Errorf("failed to cleanup orphaned releases: %v", err)
		}
		select {
		case <-h.ctx.Done():
			return
		case <-time.After(wait.Jitter(15*time.Minute, 1.0)):
		}
	}
}

func (h *handler) Cleanup(key string, bd *fleet.BundleDeployment) (*fleet.BundleDeployment, error) {
	h.cleanupOnce.Do(func() {
		go h.garbageCollect()
	})

	if bd != nil {
		return bd, nil
	}
	return nil, h.deployManager.Delete(key)
}

func (h *handler) DeployBundle(bd *fleet.BundleDeployment, status fleet.BundleDeploymentStatus) (fleet.BundleDeploymentStatus, error) {
	dependOn, ok, err := h.checkDependency(bd)
	if err != nil {
		return status, err
	}

	if !ok {
		return status, fmt.Errorf("bundle %s has dependent bundle %s that is not ready", bd.Name, dependOn)
	}

	release, err := h.deployManager.Deploy(bd)
	if err != nil {
		return status, err
	}

	if status.LastApply == nil {
		applyTime := metav1.Now()
		status.LastApply = &applyTime
	}
	status.Release = release
	status.AppliedDeploymentID = bd.Spec.DeploymentID
	return status, nil
}

func (h *handler) checkDependency(bd *fleet.BundleDeployment) (string, bool, error) {
	bundleNamespace := bd.Labels["fleet.cattle.io/bundle-namespace"]
	for _, depend := range bd.Spec.DependsOn {
		ls := &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"fleet.cattle.io/bundle-name":      depend.Name,
				"fleet.cattle.io/bundle-namespace": bundleNamespace,
			},
		}
		selector, err := metav1.LabelSelectorAsSelector(ls)
		if err != nil {
			return "", false, err
		}
		bds, err := h.bdController.Cache().List(bd.Namespace, selector)
		if err != nil {
			return "", false, err
		}
		for _, bd := range bds {
			c := condition.Cond("Ready")
			if c.IsTrue(bd) {
				continue
			} else {
				return fmt.Sprintf("%s/%s", bundleNamespace, depend.Name), false, nil
			}
		}
	}
	return "", true, nil
}

func (h *handler) Trigger(key string, bd *fleet.BundleDeployment) (*fleet.BundleDeployment, error) {
	if bd == nil {
		return bd, h.trigger.Clear(key)
	}

	resources, err := h.deployManager.Resources(bd)
	if err != nil {
		return bd, err
	}

	if resources != nil {
		return bd, h.trigger.OnChange(key, resources.DefaultNamespace, func() {
			h.bdController.Enqueue(bd.Namespace, bd.Name)
		}, resources.Objects...)
	}

	return bd, nil
}

func isAgent(bd *fleet.BundleDeployment) bool {
	return strings.HasPrefix(bd.Name, "fleet-agent")
}

func shouldRedeploy(bd *fleet.BundleDeployment, status *fleet.BundleDeploymentStatus) (ok bool, duration time.Duration, err error) {

	if bd.Spec.Options.Resync || bd.Spec.StagedOptions.Resync {
		ok, duration, err = hasAutoReapply(bd, status)
		return ok, duration, err
	}

	if isAgent(bd) {
		return true, DefaultReapplyInterval, nil
	}

	if bd.Spec.Options.ForceSyncGeneration <= 0 {
		return false, DefaultReapplyInterval, nil
	}
	if bd.Status.SyncGeneration == nil {
		return true, DefaultReapplyInterval, nil
	}

	if *bd.Status.SyncGeneration != bd.Spec.Options.ForceSyncGeneration {
		ok = true
	}

	return ok, DefaultReapplyInterval, nil
}

func (h *handler) cleanupOldAgent(modifiedStatuses []fleet.ModifiedStatus) error {
	var errs []error
	for _, modified := range modifiedStatuses {
		if modified.Delete {
			gvk := schema.FromAPIVersionAndKind(modified.APIVersion, modified.Kind)
			mapping, err := h.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			if err != nil {
				errs = append(errs, fmt.Errorf("mapping resource for %s for agent cleanup: %w", gvk, err))
				continue
			}

			logrus.Infof("Removing old agent resource %s/%s, %s", modified.Namespace, modified.Name, gvk)
			err = h.dynamic.Resource(mapping.Resource).Namespace(modified.Namespace).Delete(h.ctx, modified.Name, metav1.DeleteOptions{})
			if err != nil {
				errs = append(errs, fmt.Errorf("deleting %s/%s for %s for agent cleanup: %w", modified.Namespace, modified.Name, gvk, err))
				continue
			}
		}
	}
	return merr.NewErrors(errs...)
}

func (h *handler) MonitorBundle(bd *fleet.BundleDeployment, status fleet.BundleDeploymentStatus) (fleet.BundleDeploymentStatus, error) {
	if bd.Spec.DeploymentID != status.AppliedDeploymentID {
		return status, nil
	}

	deploymentStatus, err := h.deployManager.MonitorBundle(bd)
	if err != nil {
		return status, err
	}

	status.NonReadyStatus = deploymentStatus.NonReadyStatus
	status.ModifiedStatus = deploymentStatus.ModifiedStatus
	status.Ready = deploymentStatus.Ready
	status.NonModified = deploymentStatus.NonModified

	readyError := readyError(status)
	condition.Cond(fleet.BundleDeploymentConditionReady).SetError(&status, "", readyError)
	if len(status.ModifiedStatus) > 0 {
		ok, duration, err := shouldRedeploy(bd, &status)
		if err != nil {
			return status, err
		}
		defer h.bdController.EnqueueAfter(bd.Namespace, bd.Name, duration)
		if ok {
			logrus.Infof("Redeploying %s", bd.Name)
			status.AppliedDeploymentID = ""
			if isAgent(bd) {
				if err := h.cleanupOldAgent(status.ModifiedStatus); err != nil {
					return status, fmt.Errorf("failed to clean up agent: %w", err)
				}
			}
		}
	}

	status.SyncGeneration = &bd.Spec.Options.ForceSyncGeneration
	if readyError != nil {
		logrus.Errorf("bundle %s: %v", bd.Name, readyError)
	}
	return status, nil
}

func readyError(status fleet.BundleDeploymentStatus) error {
	if status.Ready && status.NonModified {
		return nil
	}

	var msg string
	if !status.Ready {
		msg = "not ready"
		if len(status.NonReadyStatus) > 0 {
			msg = status.NonReadyStatus[0].String()
		}
	} else if !status.NonModified {
		msg = "out of sync"
		if len(status.ModifiedStatus) > 0 {
			msg = status.ModifiedStatus[0].String()
		}
	}

	return errors.New(msg)
}

func hasAutoReapply(bd *fleet.BundleDeployment, status *fleet.BundleDeploymentStatus) (ok bool, requeueAfter time.Duration, err error) {
	requeueAfter = DefaultReapplyInterval
	if bd.Spec.Options.Resync || bd.Spec.StagedOptions.Resync {
		if bd.Spec.Options.ResyncPolicy == nil && bd.Spec.StagedOptions.ResyncPolicy == nil {
			if status.LastApply == nil {
				return false, requeueAfter, nil
			}
			if status.LastApply.Add(DefaultReapplyInterval).Before(time.Now()) {
				status.LastApply = nil
				return true, requeueAfter, nil
			}
		} else {
			// use custom resync policy from the template //
			policy := fleet.ResyncPolicy{}

			if bd.Spec.Options.ResyncPolicy != nil {
				policy = *bd.Spec.Options.ResyncPolicy
			}

			if bd.Spec.StagedOptions.ResyncPolicy != nil {
				policy = *bd.Spec.StagedOptions.ResyncPolicy
			}

			if policy.ResyncDelay != "" {
				requeueAfter, err = time.ParseDuration(policy.ResyncDelay)
				if err != nil {
					logrus.Errorf("inside reapply delay calculation %v", err)
					return false, requeueAfter, err
				}
			}

			if status.ResyncCounter == policy.MaxRetries {
				requeueAfter, err = time.ParseDuration(policy.BackoffDelay)
				if err != nil {
					return false, requeueAfter, err
				}
			}

			if policy.MaxRetries == DefaultMaxRetries && status.LastApply.Add(requeueAfter).Before(time.Now()) {
				// keep checking and reapplying at reapplyDelay. Backoff not needed
				status.LastApply = nil
				return true, requeueAfter, nil
			}

			if policy.MaxRetries == 0 {
				// we never reapply, same as not having the resync flag
				return false, requeueAfter, nil
			}

			if status.ResyncCounter <= policy.MaxRetries && status.LastApply.Add(requeueAfter).Before(time.Now()) {
				status.LastApply = nil
				status.ResyncCounter++
				return true, requeueAfter, nil
			}

			if status.ResyncCounter > policy.MaxRetries && status.LastApply.Add(requeueAfter).Before(time.Now()) {
				status.LastApply = nil
				status.ResyncCounter = 0
				return true, requeueAfter, nil
			}
		}

	}

	// default behaviour when no resync is provided
	// modified bundle will be reapplied as before
	return false, requeueAfter, nil
}
