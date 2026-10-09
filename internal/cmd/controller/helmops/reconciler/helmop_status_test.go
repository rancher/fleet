package reconciler

import (
	"context"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	statusAcceptedErrMsg = "could not get a chart version: chart not found"
	statusOldTimestamp   = "2026-01-01T00:00:00Z"
	statusBDNotReadyMsg  = "NotReady(1) [Bundle bd]"
	statusNoHelmErrMsg   = "helm options are empty in the HelmOp's spec"
)

func TestStatusReconcile_ReadyCondition(t *testing.T) {
	tests := map[string]struct {
		conditions    []genericcondition.GenericCondition
		objs          []crclient.Object
		expectStatus  corev1.ConditionStatus
		expectReason  string
		expectMessage string
		// expectKept is true when Ready is expected to keep its LastUpdateTime.
		expectKept bool
		// expectDisplayMessage is also expected to set Display.Error.
		expectDisplayMessage string
		noHelmOptions        bool
	}{
		"missing Helm options, Accepted=False sets Ready=False": {
			conditions: []genericcondition.GenericCondition{
				statusCondition(fleet.HelmOpAcceptedCondition, corev1.ConditionFalse, "Error", statusNoHelmErrMsg),
			},
			noHelmOptions:        true,
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "Error",
			expectMessage:        statusNoHelmErrMsg,
			expectDisplayMessage: statusNoHelmErrMsg,
		},
		"no bundles, Accepted=False sets Ready=False": {
			conditions: []genericcondition.GenericCondition{
				statusCondition(fleet.HelmOpAcceptedCondition, corev1.ConditionFalse, "Error", statusAcceptedErrMsg),
			},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "Error",
			expectMessage:        statusAcceptedErrMsg,
			expectDisplayMessage: statusAcceptedErrMsg,
		},
		"no bundles, Accepted=True sets Ready=True": {
			conditions: []genericcondition.GenericCondition{
				statusCondition(fleet.HelmOpAcceptedCondition, corev1.ConditionTrue, "", ""),
			},
			expectStatus: corev1.ConditionTrue,
		},
		"ready bundle deployments, Accepted=False sets Ready=False": {
			conditions: []genericcondition.GenericCondition{
				statusCondition(fleet.HelmOpAcceptedCondition, corev1.ConditionFalse, "Error", statusAcceptedErrMsg),
				statusCondition(string(fleet.Ready), corev1.ConditionTrue, "", ""),
			},
			objs:                 []crclient.Object{statusBundleDeployment(true)},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "Error",
			expectMessage:        statusAcceptedErrMsg,
			expectDisplayMessage: statusAcceptedErrMsg,
		},
		"Accepted=False does not override a bundle deployment error": {
			conditions: []genericcondition.GenericCondition{
				statusCondition(fleet.HelmOpAcceptedCondition, corev1.ConditionFalse, "Error", statusAcceptedErrMsg),
				statusCondition(string(fleet.Ready), corev1.ConditionFalse, "Error", statusAcceptedErrMsg),
			},
			objs:          []crclient.Object{statusBundleDeployment(false)},
			expectStatus:  corev1.ConditionFalse,
			expectMessage: statusBDNotReadyMsg,
		},
		"Ready=False from Accepted is cleared once accepted": {
			conditions: []genericcondition.GenericCondition{
				statusCondition(fleet.HelmOpAcceptedCondition, corev1.ConditionTrue, "", ""),
				statusCondition(string(fleet.Ready), corev1.ConditionFalse, "Error", statusAcceptedErrMsg),
			},
			expectStatus: corev1.ConditionTrue,
		},
		"steady Ready=False from Accepted": {
			conditions: []genericcondition.GenericCondition{
				statusCondition(fleet.HelmOpAcceptedCondition, corev1.ConditionFalse, "Error", statusAcceptedErrMsg),
				statusCondition(string(fleet.Ready), corev1.ConditionFalse, "Error", statusAcceptedErrMsg),
			},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "Error",
			expectMessage:        statusAcceptedErrMsg,
			expectKept:           true,
			expectDisplayMessage: statusAcceptedErrMsg,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			utilruntime.Must(fleet.AddToScheme(scheme))

			helmop := &fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{Name: "helmop", Namespace: "fleet-local"},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{Chart: "chart"},
						},
					},
				},
				Status: fleet.HelmOpStatus{
					StatusBase: fleet.StatusBase{Conditions: test.conditions},
				},
			}
			if test.noHelmOptions {
				helmop.Spec.Helm = nil
			}
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(append(test.objs, helmop)...).
				WithStatusSubresource(&fleet.HelmOp{}).
				Build()
			r := &HelmOpStatusReconciler{Client: c, Scheme: scheme}
			key := crclient.ObjectKeyFromObject(helmop)

			ctx := context.Background()
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := &fleet.HelmOp{}
			if err := c.Get(ctx, key, got); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			cond, ok := getCondition(got, string(fleet.Ready))
			if !ok {
				t.Fatalf("Ready condition not found")
			}
			if cond.Status != test.expectStatus {
				t.Errorf("expected Ready=%s, got %s (message %q)", test.expectStatus, cond.Status, cond.Message)
			}
			if cond.Reason != test.expectReason {
				t.Errorf("expected Ready reason %q, got %q", test.expectReason, cond.Reason)
			}
			if cond.Message != test.expectMessage {
				t.Errorf("expected Ready message %q, got %q", test.expectMessage, cond.Message)
			}
			if kept := cond.LastUpdateTime == statusOldTimestamp; kept != test.expectKept {
				t.Errorf("expected Ready LastUpdateTime kept to be %t, got %q", test.expectKept, cond.LastUpdateTime)
			}

			display := got.Status.Display
			if display.Message != test.expectDisplayMessage || display.Error != (test.expectDisplayMessage != "") {
				t.Errorf("expected Display message %q and error %t, got %q and %t",
					test.expectDisplayMessage, test.expectDisplayMessage != "", display.Message, display.Error)
			}

			if accepted, ok := getCondition(got, fleet.HelmOpAcceptedCondition); !ok || accepted != test.conditions[0] {
				t.Errorf("expected Accepted to be preserved as %+v, got %+v", test.conditions[0], accepted)
			}
		})
	}
}

func statusCondition(condType string, status corev1.ConditionStatus, reason, msg string) genericcondition.GenericCondition {
	return genericcondition.GenericCondition{
		Type:           condType,
		Status:         status,
		Reason:         reason,
		Message:        msg,
		LastUpdateTime: statusOldTimestamp,
	}
}

func statusBundleDeployment(isReady bool) *fleet.BundleDeployment {
	return &fleet.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bd",
			Namespace: "cluster-ns",
			Labels: map[string]string{
				fleet.HelmOpLabel:          "helmop",
				fleet.BundleNamespaceLabel: "fleet-local",
			},
		},
		Spec: fleet.BundleDeploymentSpec{DeploymentID: "id", StagedDeploymentID: "id"},
		Status: fleet.BundleDeploymentStatus{
			AppliedDeploymentID: "id",
			Ready:               isReady,
			NonModified:         true,
		},
	}
}
