// Package bundledeployment deploys bundles, monitors them and cleans up.
package bundledeployment

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/internal/cmd/agent/trigger"
	"github.com/rancher/fleet/internal/helmdeployer"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/pkg/condition"
	"github.com/rancher/wrangler/pkg/merr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
)

var nsResource = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}

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
	if bd.Spec.Paused {
		// nothing to do
		return status, nil
	}

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

	if err := h.setNamespaceLabelsAndAnnotations(bd, release); err != nil {
		return fleet.BundleDeploymentStatus{}, err
	}

	// Setting the error to nil clears any existing error
	condition.Cond(fleet.BundleDeploymentConditionInstalled).SetError(&status, "", nil)
	return status, nil
}

// setNamespaceLabelsAndAnnotations updates the namespace for the release, applying all labels and annotations to that namespace as configured in the bundle spec.
func (h *handler) setNamespaceLabelsAndAnnotations(bd *fleet.BundleDeployment, releaseID string) error {
	if bd.Spec.Options.NamespaceLabels == nil && bd.Spec.Options.NamespaceAnnotations == nil {
		return nil
	}

	ns, err := h.fetchNamespace(releaseID)
	if err != nil {
		return err
	}

	if reflect.DeepEqual(bd.Spec.Options.NamespaceLabels, ns.Labels) && reflect.DeepEqual(bd.Spec.Options.NamespaceAnnotations, ns.Annotations) {
		return nil
	}

	if bd.Spec.Options.NamespaceLabels != nil {
		addLabelsFromOptions(ns.Labels, *bd.Spec.Options.NamespaceLabels)
	}
	if bd.Spec.Options.NamespaceAnnotations != nil {
		if ns.Annotations == nil {
			ns.Annotations = map[string]string{}
		}
		addAnnotationsFromOptions(ns.Annotations, *bd.Spec.Options.NamespaceAnnotations)
	}
	err = h.updateNamespace(ns)
	if err != nil {
		return err
	}

	return nil
}

// updateNamespace updates a namespace resource in the cluster.
func (h *handler) updateNamespace(ns *corev1.Namespace) error {
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ns)
	if err != nil {
		return err
	}
	_, err = h.dynamic.Resource(nsResource).Update(h.ctx, &unstructured.Unstructured{Object: u}, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}

// fetchNamespace gets the namespace matching the release ID. Returns an error if none is found.
func (h *handler) fetchNamespace(releaseID string) (*corev1.Namespace, error) {
	// releaseID is composed of release.Namespace/release.Name/release.Version
	namespace := strings.Split(releaseID, "/")[0]
	list, err := h.dynamic.Resource(nsResource).List(h.ctx, metav1.ListOptions{
		LabelSelector: "name=" + namespace,
	})
	if err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, fmt.Errorf("namespace %s not found", namespace)
	}
	var ns corev1.Namespace
	err = runtime.DefaultUnstructuredConverter.
		FromUnstructured(list.Items[0].Object, &ns)
	if err != nil {
		return nil, err
	}

	return &ns, nil
}

// addLabelsFromOptions updates nsLabels so that it only contains all labels specified in optLabels, plus the `name` labels added by Helm when creating the namespace.
func addLabelsFromOptions(nsLabels map[string]string, optLabels map[string]string) {
	for k, v := range optLabels {
		nsLabels[k] = v
	}

	// Delete labels not defined in the options.
	// Keep the name label as it is added by helm when creating the namespace.
	for k := range nsLabels {
		if _, ok := optLabels[k]; k != "name" && !ok {
			delete(nsLabels, k)
		}
	}
}

// addAnnotationsFromOptions updates nsAnnotations so that it only contains all annotations specified in optAnnotations.
func addAnnotationsFromOptions(nsAnnotations map[string]string, optAnnotations map[string]string) {
	for k, v := range optAnnotations {
		nsAnnotations[k] = v
	}

	// Delete Annotations not defined in the options.
	for k := range nsAnnotations {
		if _, ok := optAnnotations[k]; !ok {
			delete(nsAnnotations, k)
		}
	}
}

// deployErrToStatus converts an error into a status update
func deployErrToStatus(err error, status fleet.BundleDeploymentStatus) (bool, fleet.BundleDeploymentStatus) {
	if err == nil {
		return false, status
	}

	msg := err.Error()

	// The following error conditions are turned into a status
	// Note: these error strings are returned by the Helm SDK and its dependencies
	re := regexp.MustCompile(
		"(timed out waiting for the condition)|" + // a Helm wait occurs and it times out
			"(error validating data)|" + // manifests fail to pass validation
			"(chart requires kubeVersion)|" + // kubeVersion mismatch
			"(annotation validation error)|" + // annotations fail to pass validation
			"(failed, and has been rolled back due to atomic being set)|" + // atomic is set and a rollback occurs
			"(YAML parse error)|" + // YAML is broken in source files
			"(Forbidden: updates to [0-9A-Za-z]+ spec for fields other than [0-9A-Za-z ']+ are forbidden)|" + // trying to update fields that cannot be updated
			"(Forbidden: spec is immutable after creation)|" + // trying to modify immutable spec
			"(chart requires kubeVersion: [0-9A-Za-z\\.\\-<>=]+ which is incompatible with Kubernetes)", // trying to deploy to incompatible Kubernetes
	)
	if re.MatchString(msg) {
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
	bundleNamespace := bd.Labels[fleet.BundleNamespaceLabel]
	for _, depend := range bd.Spec.DependsOn {
		// skip empty BundleRef definitions. Possible if there is a typo in the yaml
		if depend.Name != "" || depend.Selector != nil {
			ls := &metav1.LabelSelector{}
			if depend.Selector != nil {
				ls = depend.Selector
			}

			// depend.Name is just a shortcut for matchLabels: {bundle-name: name}
			if depend.Name != "" {
				ls = metav1.AddLabelToSelector(ls, fleet.BundleLabel, depend.Name)
				ls = metav1.AddLabelToSelector(ls, fleet.BundleNamespaceLabel, bundleNamespace)
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
				return fmt.Errorf("list bundledeployments: no bundles matching labels %s in namespace %s", selector.String(), bundleNamespace)
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
	if bd == nil || bd.Spec.Paused {
		return bd, h.trigger.Clear(key)
	}

	logrus.Debugf("Triggering for bundledeployment '%s'", key)

	resources, err := h.deployManager.AllResources(bd)
	if err != nil {
		return bd, err
	}

	if resources == nil {
		return bd, nil
	}

	logrus.Debugf("Adding OnChange for bundledeployment's '%s' resource list", key)
	return bd, h.trigger.OnChange(key, resources.DefaultNamespace, func() {
		// enqueue bundledeployment if any resource changes
		h.bdController.EnqueueAfter(bd.Namespace, bd.Name, 0)
	}, resources.Objects...)
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

	// Same considerations in case the bundle is paused
	if bd.Spec.Paused {
		return status, nil
	}

	err := h.deployManager.UpdateBundleDeploymentStatus(h.restMapper, bd)
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
	status = bd.Status

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
