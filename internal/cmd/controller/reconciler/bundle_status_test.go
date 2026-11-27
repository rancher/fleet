package reconciler

import (
	"context"
	"testing"

	"github.com/rancher/fleet/internal/cmd/controller/bundleevents"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetsummary "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"

	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// deployedTarget is a target whose bundle deployment applied the current
// deployment, with the given readiness.
func deployedTarget(ready bool, nonReady ...fleet.NonReadyStatus) *target.Target {
	return &target.Target{
		Cluster:      cluster(),
		DeploymentID: "d1",
		Deployment: &fleet.BundleDeployment{
			Spec: fleet.BundleDeploymentSpec{DeploymentID: "d1", StagedDeploymentID: "d1"},
			Status: fleet.BundleDeploymentStatus{
				AppliedDeploymentID: "d1",
				Ready:               ready,
				NonModified:         true,
				NonReadyStatus:      nonReady,
			},
		},
	}
}

func TestFailureProfileCountsEveryTarget(t *testing.T) {
	progressing := fleet.NonReadyStatus{Kind: "Deployment", Name: "web",
		Summary: fleetsummary.Summary{State: "updating", Transitioning: true}}
	failed := fleet.NonReadyStatus{Kind: "Deployment", Name: "web",
		Summary: fleetsummary.Summary{State: "error", Error: true}}

	errApplied := &target.Target{
		Cluster:      cluster(),
		DeploymentID: "d2",
		Deployment: &fleet.BundleDeployment{
			Spec: fleet.BundleDeploymentSpec{DeploymentID: "d2", StagedDeploymentID: "d2"},
			Status: fleet.BundleDeploymentStatus{
				AppliedDeploymentID: "d1",
				Conditions: []genericcondition.GenericCondition{
					{Type: fleet.BundleDeploymentConditionDeployed, Status: "False", Message: "render failed"},
				},
			},
		},
	}

	// More targets than the summary lists, so that the profile is seen to
	// count past them.
	var targets []*target.Target
	for range 12 {
		targets = append(targets, &target.Target{Cluster: cluster()}) // no bundle deployment yet: Pending
	}
	for range 15 {
		targets = append(targets, deployedTarget(false, progressing))
	}
	targets = append(targets,
		deployedTarget(false, failed),
		deployedTarget(true),
		errApplied,
	)

	got := failureProfile(targets)

	if got.ErrApplied != 1 || got.NotReadyWithError != 1 || got.NotReadyProgressing != 15 {
		t.Fatalf("expected 1 errApplied, 1 not ready with an error and 15 progressing, got %+v", *got)
	}
}

func cluster() *fleet.Cluster {
	return &fleet.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "fleet-default", Name: "downstream"}}
}

// observingNotifier counts the bundles and bundle deployments reported to it.
type observingNotifier struct {
	observed            int
	observedDeployments int
}

func (n *observingNotifier) ObserveBundle(context.Context, *fleet.Bundle, *fleet.Bundle, *bundleevents.Profile) {
	n.observed++
}

func (n *observingNotifier) ObserveBundleDeployment(context.Context, *fleet.BundleDeployment, *fleet.BundleDeployment) {
	n.observedDeployments++
}

func (n *observingNotifier) Forget(types.UID) {}

func TestUnchangedStatusIsReportedToo(t *testing.T) {
	// The summary lists only 10 deployments, so a failure can change on
	// the others without changing the status. Nothing is patched, so no
	// client is needed.
	notifier := &observingNotifier{}
	r := &BundleReconciler{Events: notifier}
	b := &fleet.Bundle{Status: fleet.BundleStatus{Summary: fleet.BundleSummary{DesiredReady: 1, ErrApplied: 1}}}

	ctx := bundleevents.WithProfile(context.TODO(), &bundleevents.Profile{})
	if err := r.updateStatus(ctx, b.DeepCopy(), b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if notifier.observed != 1 {
		t.Errorf("expected the bundle to be reported once, got %d", notifier.observed)
	}
}
