// Package bundledeployment deploys bundles, monitors them and cleans up. (fleetagent)
package bundledeployment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/modules/agent/pkg/deployer"
	"github.com/rancher/fleet/modules/agent/pkg/trigger"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/helmdeployer"

	"github.com/rancher/wrangler/pkg/condition"
	"github.com/rancher/wrangler/pkg/merr"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
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
		case <-time.After(wait.Jitter(durations.GarbageCollect, 1.0)):
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
	if err := h.checkDependency(bd); err != nil {
		return status, err
	}

	release, err := h.deployManager.Deploy(bd)
	if err != nil {
		// When an error from DeployBundle is returned it causes DeployBundle
		// to requeue and keep trying to deploy on a loop. If there is something
		// wrong with the deployed manifests this will be a loop that re-deploying
		// cannot fix. Here we catch those errors and update the status to note
		// the problem while skipping the constant requeuing.
		if do, newStatus := deployErrToStatus(err, status); do {
			// Setting the release to an empty string remove the previous
			// release name. When a deployment fails the release name is not
			// returned. Keeping the old release name can lead to other functions
			// looking up old data in the history and presenting the wrong status.
			// For example, the h.deployManager.Deploy function will find the old
			// release and not return an error. It will set everything as if the
			// current one is running properly.
			newStatus.Release = ""
			newStatus.AppliedDeploymentID = bd.Spec.DeploymentID
			return newStatus, nil
		}
		return status, err
	}
	status.Release = release
	status.AppliedDeploymentID = bd.Spec.DeploymentID

	// Setting the error to nil clears any existing error
	condition.Cond(fleet.BundleDeploymentConditionInstalled).SetError(&status, "", nil)
	return status, nil
}

// deployErrToStatus converts an error into a status update
func deployErrToStatus(err error, status fleet.BundleDeploymentStatus) (bool, fleet.BundleDeploymentStatus) {
	if err == nil {
		return false, status
	}

	msg := err.Error()

	// The following error conditions are turned into a status
	// * when a Helm wait occurs and it times out
	// * manifests fail to pass validation
	// * atomic is set and a rollback occurs
	// Note: these error strings are returned by the Helm SDK and its dependencies
	if strings.Contains(msg, "timed out waiting for the condition") ||
		strings.Contains(msg, "error validating data") ||
		strings.Contains(msg, "failed, and has been rolled back due to atomic being set") {

		status.Ready = false
		status.NonModified = true

		// The ready status is displayed throughout the UI. Setting this as well
		// as installed enables the status to be displayed when looking at the
		// CRD or a UI build on that.
		readyError := fmt.Errorf("not ready: %s", msg)
		condition.Cond(fleet.BundleDeploymentConditionReady).SetError(&status, "", readyError)

		// Deployed and Monitored conditions are automated. They are true if their
		// handlers return no error and false if an error is returned. When an
		// error is returned they are requeued. To capture the state of an error
		// that is not returned a new condition is being captured. Ready is the
		// condition that displays for status in general and it is used for
		// the readiness of resources. Only when we cannot capture the status of
		// resources, like here, can use use it for a message like the above.
		// The Installed condition lets us have a condition to capture the error
		// that can be bubbled up for Bundles and Gitrepos to consume.
		installError := fmt.Errorf("not installed: %s", msg)
		condition.Cond(fleet.BundleDeploymentConditionInstalled).SetError(&status, "", installError)

		return true, status
	}

	// The case that the bundle is already in an error state. A previous
	// condition with the error should already be applied.
	if err == helmdeployer.ErrNoResourceID {
		return true, status
	}

	return false, status
}

func (h *handler) checkDependency(bd *fleet.BundleDeployment) error {
	var depBundleList []string
	bundleNamespace := bd.Labels["fleet.cattle.io/bundle-namespace"]
	for _, depend := range bd.Spec.DependsOn {
		// skip empty BundleRef definitions. Possible if there is a typo in the yaml
		if depend.Name != "" || depend.Selector != nil {
			ls := &metav1.LabelSelector{}
			if depend.Selector != nil {
				ls = depend.Selector
			}

			if depend.Name != "" {
				ls = metav1.AddLabelToSelector(ls, "fleet.cattle.io/bundle-name", depend.Name)
				ls = metav1.AddLabelToSelector(ls, "fleet.cattle.io/bundle-namespace", bundleNamespace)
			}

			selector, err := metav1.LabelSelectorAsSelector(ls)
			if err != nil {
				return err
			}
			bds, err := h.bdController.Cache().List(bd.Namespace, selector)
			if err != nil {
				return err
			}

			if len(bds) == 0 {
				return fmt.Errorf("no bundles matching labels %s in namespace %s", selector.String(), bundleNamespace)
			}

			for _, depBundle := range bds {
				c := condition.Cond("Ready")
				if c.IsTrue(depBundle) {
					continue
				} else {
					depBundleList = append(depBundleList, depBundle.Name)
				}
			}
		}
	}

	if len(depBundleList) != 0 {
		return fmt.Errorf("dependent bundle(s) are not ready: %v", depBundleList)
	}

	return nil
}

func (h *handler) Trigger(key string, bd *fleet.BundleDeployment) (*fleet.BundleDeployment, error) {
	if bd == nil {
		return bd, h.trigger.Clear(key)
	}

	logrus.Debugf("Triggering for bundledeployment '%s'", key)

	resources, err := h.deployManager.Resources(bd)
	if err != nil {
		return bd, err
	}

	if resources != nil {
		logrus.Debugf("Adding OnChange for bundledeployment's '%s' resource list", key)
		return bd, h.trigger.OnChange(key, resources.DefaultNamespace, func() {
			// enqueue bundledeployment if any resource changes
			h.bdController.Enqueue(bd.Namespace, bd.Name)
		}, resources.Objects...)
	}

	return bd, nil
}

func isAgent(bd *fleet.BundleDeployment) bool {
	return strings.HasPrefix(bd.Name, "fleet-agent")
}

func shouldRedeploy(bd *fleet.BundleDeployment) bool {
	if isAgent(bd) {
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

// removePrivateFields removes fields from the status, which won't be marshalled to JSON.
// They would however trigger a status update in apply
func removePrivateFields(s1 *fleet.BundleDeploymentStatus) {
	for id := range s1.NonReadyStatus {
		s1.NonReadyStatus[id].Summary.Relationships = nil
		s1.NonReadyStatus[id].Summary.Attributes = nil
	}
}

func (h *handler) MonitorBundle(bd *fleet.BundleDeployment, status fleet.BundleDeploymentStatus) (fleet.BundleDeploymentStatus, error) {
	if bd.Spec.DeploymentID != status.AppliedDeploymentID {
		return status, nil
	}

	// If the bundle failed to install the status should not be updated. Updating
	// here would remove the condition message that was previously set on it.
	if condition.Cond(fleet.BundleDeploymentConditionInstalled).IsFalse(bd) {
		return status, nil
	}

	deploymentStatus, err := h.deployManager.MonitorBundle(bd)
	if err != nil {

		// Returning an error will cause MonitorBundle to requeue in a loop.
		// When there is no resourceID the error should be on the status. Without
		// the ID we do not have the information to lookup the resources to
		// compute the plan and discover the state of resources.
		if err == helmdeployer.ErrNoResourceID {
			return status, nil
		}

		return status, err
	}

	status.NonReadyStatus = deploymentStatus.NonReadyStatus
	status.ModifiedStatus = deploymentStatus.ModifiedStatus
	status.Ready = deploymentStatus.Ready
	status.NonModified = deploymentStatus.NonModified

	readyError := readyError(status)
	condition.Cond(fleet.BundleDeploymentConditionReady).SetError(&status, "", readyError)
	if len(status.ModifiedStatus) > 0 {
		h.bdController.EnqueueAfter(bd.Namespace, bd.Name, durations.MonitorBundleDelay)
		if shouldRedeploy(bd) {
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

	removePrivateFields(&status)
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
