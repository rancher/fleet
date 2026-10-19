package status

import (
	"context"
	"sort"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NotReadyBundle returns the first bundle in namespace with the given label
// which is not ready, or nil. Its Ready condition needs to be made explicitly
// visible in the status of the GitRepo or HelmOp owning it, since the other
// statuses are calculated from bundle deployments, which do not exist when
// rendering manifests or targeting fail.
func NotReadyBundle(ctx context.Context, c client.Reader, namespace, label, value string) (*fleet.Bundle, error) {
	bList := &fleet.BundleList{}
	err := c.List(ctx, bList, client.MatchingLabels{label: value}, client.InNamespace(namespace))
	if err != nil {
		return nil, err
	}

	// Make sure the bundles are always iterated in the same order
	sort.Slice(bList.Items, func(i, j int) bool {
		return bList.Items[i].UID < bList.Items[j].UID
	})

	for i, bundle := range bList.Items {
		if _, ok := falseCondition(bundle.Status.Conditions, string(fleet.Ready)); ok {
			return &bList.Items[i], nil
		}
	}

	return nil, nil
}

// SetReadyCondition computes the Ready condition of a GitRepo or HelmOp
// status, from, in order of precedence:
//
//   - the Ready condition of notReadyBundle, a bundle which is not ready, if
//     any. This makes rendering issues visible, as no bundle deployments exist
//     in that case;
//   - the bundle deployments summary, when not all of them are ready;
//   - the condition of type acceptedType, when the reconciler of the resource
//     rejected or failed to process it (eg. a restriction violation, or a
//     missing secret). This applies even when all bundle deployments are
//     ready, as those reflect a previous state of the resource. Without this,
//     a resource with no bundles yet would also be reported as ready, as an
//     empty summary is a ready one.
//
// More specific bundle errors take precedence over Accepted, which remains
// visible on its own condition.
//
// Ready is set exactly once, so that its LastUpdateTime only changes when its
// content does, instead of on each reconcile which would flip it in memory.
//
// It expects status.Summary and status.Display to be set from bundle
// deployments already, see SetFields. When Ready reflects an error which no
// bundle deployment does, the Display message and error are set from it too.
// A bundle is also not ready while its deployments are, eg. during a rollout,
// which is not an error. Hence a bundle's Ready condition is only displayed as
// an error when all bundle deployments are ready, or none exist. The bundle's
// own summary cannot tell those apart: it counts targets, not deployments, and
// is left stale when the bundle reconciler fails before computing it.
func SetReadyCondition(status *fleet.StatusBase, notReadyBundle *fleet.Bundle, acceptedType string) {
	deploymentsReady := summary.ReadyMessage(status.Summary, "Bundle") == ""

	if notReadyBundle != nil {
		if ready, ok := falseCondition(notReadyBundle.Status.Conditions, string(fleet.Ready)); ok {
			setCondition(status, ready)
			if deploymentsReady {
				setDisplayError(status, ready.Message)
			}
			return
		}
	}

	if deploymentsReady {
		if accepted, ok := falseCondition(status.Conditions, acceptedType); ok {
			c := condition.Cond(fleet.Ready)
			c.SetStatusBool(status, false)
			c.Reason(status, accepted.Reason)
			c.Message(status, accepted.Message)
			setDisplayError(status, accepted.Message)
			return
		}
	}

	summary.SetReadyConditions(status, "Bundle", status.Summary)
	// SetReadyConditions only clears the reason when ready, which would keep the
	// reason set by the branches above if Ready was computed from them before.
	condition.Cond(fleet.Ready).Reason(status, "")
}

// setDisplayError sets the Display message and error, unless bundle
// deployments already did.
func setDisplayError(status *fleet.StatusBase, msg string) {
	if status.Display.Message != "" || msg == "" {
		return
	}
	status.Display.Message = msg
	status.Display.Error = true
}

// falseCondition returns the condition of the given type if it is False.
func falseCondition(conds []genericcondition.GenericCondition, condType string) (genericcondition.GenericCondition, bool) {
	for _, c := range conds {
		if c.Type == condType && c.Status == corev1.ConditionFalse {
			return c, true
		}
	}
	return genericcondition.GenericCondition{}, false
}

// setCondition replaces the condition of the same type in status, or appends it.
func setCondition(status *fleet.StatusBase, cond genericcondition.GenericCondition) {
	for i, c := range status.Conditions {
		if c.Type == cond.Type {
			status.Conditions[i] = cond
			return
		}
	}
	status.Conditions = append(status.Conditions, cond)
}
