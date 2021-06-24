package bundledeployment

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/robfig/cron"

	"github.com/rancher/fleet/modules/agent/pkg/deployer"
	"github.com/rancher/fleet/modules/agent/pkg/trigger"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/condition"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

type handler struct {
	cleanupOnce sync.Once

	ctx           context.Context
	trigger       *trigger.Trigger
	deployManager *deployer.Manager
	bdController  fleetcontrollers.BundleDeploymentController
}

func Register(ctx context.Context,
	trigger *trigger.Trigger,
	deployManager *deployer.Manager,
	bdController fleetcontrollers.BundleDeploymentController) {

	h := &handler{
		ctx:           ctx,
		trigger:       trigger,
		deployManager: deployManager,
		bdController:  bdController,
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

	if bd.Spec.Options.Schedule != "" && status.ScheduledAt == "" {
		cronSched, err := cron.ParseStandard(bd.Spec.Options.Schedule)
		if err != nil {
			return status, err
		}
		scheduledRun := cronSched.Next(time.Now())
		after := scheduledRun.Sub(time.Now())
		h.bdController.EnqueueAfter(bd.Namespace, bd.Name, after)
		status.ScheduledAt = scheduledRun.Format(time.RFC3339)
		status.Scheduled = true
		condition.Cond(fleet.BundleScheduledCondition).SetStatusBool(&status, true)
		condition.Cond(fleet.BundleDeploymentConditionDeployed).SetStatusBool(&status, false)
		return status, nil
	}

	if bd.Spec.Options.Schedule != "" && status.ScheduledAt != "" {
		nextRun, err := time.Parse(time.RFC3339, status.ScheduledAt)
		if err != nil {
			return status, err
		}
		window := fleet.DefaultWindow
		if bd.Spec.Options.ScheduleWindow != "" {
			window = bd.Spec.Options.ScheduleWindow
		}

		windowDuration, err := time.ParseDuration(window)
		if err != nil {
			return status, err
		}

		if err != nil {
			return status, err
		}
		if nextRun.After(time.Now()) {
			after := nextRun.Sub(time.Now())
			h.bdController.EnqueueAfter(bd.Namespace, bd.Name, after)
			return status, nil
		}

		// case of disconnected agent during the actual window //
		if nextRun.Add(windowDuration).Before(time.Now()) {
			// clean up scheduled at to allow object to fall through scheduling
			status.ScheduledAt = ""
			status.Scheduled = false
			return status, nil
		}
	}

	release, err := h.deployManager.Deploy(bd)
	if err != nil {
		return status, err
	}
	status.Scheduled = false
	status.Release = release
	status.AppliedDeploymentID = bd.Spec.DeploymentID
	return status, nil
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

func shouldRedeploy(bd *fleet.BundleDeployment) bool {
	if bd.Name == "fleet-agent" {
		return true
	}
	if bd.Spec.Options.ForceSyncGeneration <= 0 {
		return false
	}
	if bd.Status.SyncGeneration == nil {
		return true
	}
	return *bd.Status.SyncGeneration != bd.Spec.Options.ForceSyncGeneration
}

func (h *handler) MonitorBundle(bd *fleet.BundleDeployment, status fleet.BundleDeploymentStatus) (fleet.BundleDeploymentStatus, error) {

	if status.Scheduled {
		return status, nil
	}

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
		h.bdController.EnqueueAfter(bd.Namespace, bd.Name, 5*time.Minute)
		if shouldRedeploy(bd) {
			logrus.Infof("Redeploying %s", bd.Name)
			status.AppliedDeploymentID = ""
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
	} else if status.Scheduled {
		msg = "scheduled"
	}

	return errors.New(msg)
}
