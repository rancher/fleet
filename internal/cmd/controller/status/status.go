package status

import (
	"reflect"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// BundleStatusChangedPredicate returns true if the bundle
// status has changed, or the bundle was created
func BundleStatusChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			n, isBundle := e.ObjectNew.(*v1alpha1.Bundle)
			if !isBundle {
				return false
			}
			o := e.ObjectOld.(*v1alpha1.Bundle)
			if n == nil || o == nil {
				return false
			}
			return !reflect.DeepEqual(n.Status, o.Status)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}
}

// setFields sets bundledeployment related status fields:
// Summary, ReadyClusters, DesiredReadyClusters, Display.State, Display.Message, Display.Error
func SetFields(list *fleet.BundleDeploymentList, status *fleet.StatusBase) error {
	var (
		maxState   fleet.BundleState
		message    string
		count      = map[client.ObjectKey]int{}
		readyCount = map[client.ObjectKey]int{}
	)

	status.Summary = fleet.BundleSummary{}

	for _, bd := range list.Items {
		state := summary.GetDeploymentState(&bd)
		summary.IncrementState(&status.Summary, bd.Name, state, summary.MessageFromDeployment(&bd), bd.Status.ModifiedStatus, bd.Status.NonReadyStatus)
		status.Summary.DesiredReady++
		if fleet.StateRank[state] > fleet.StateRank[maxState] {
			maxState = state
			message = summary.MessageFromDeployment(&bd)
		}

		// gather status per cluster
		// try to avoid old bundle deployments, which might be missing the labels
		if bd.Labels == nil {
			// this should not happen
			continue
		}

		name := bd.Labels[fleet.ClusterLabel]
		namespace := bd.Labels[fleet.ClusterNamespaceLabel]
		if name == "" || namespace == "" {
			// this should not happen
			continue
		}

		key := client.ObjectKey{Name: name, Namespace: namespace}
		count[key]++
		if state == fleet.Ready {
			readyCount[key]++
		}
	}

	// unique number of clusters from bundledeployments
	status.DesiredReadyClusters = len(count)

	// number of clusters where all deployments are ready
	readyClusters := 0
	for key, n := range readyCount {
		if count[key] == n {
			readyClusters++
		}
	}
	status.ReadyClusters = readyClusters

	if maxState == fleet.Ready {
		maxState = ""
		message = ""
	}

	status.Display.State = string(maxState)
	status.Display.Message = message
	status.Display.Error = len(message) > 0

	return nil
}
