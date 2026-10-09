package reconciler

import (
	"context"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	acceptedErrMsg    = "error creating git job: secrets \"gitrepo-rancher-cabundle\" not found"
	bundleErrMsg      = "error while rendering the manifests"
	bdNotReadyMsg     = "NotReady(1) [Bundle bd]"
	rollingOutMsg     = "WaitApplied(1) [Cluster fleet-local/local]"
	restrictionErrMsg = "empty targetNamespace denied, because allowedTargetNamespaces restriction is present"

	oldTimestamp    = "2026-01-01T00:00:00Z"
	bundleTimestamp = "2026-02-02T00:00:00Z"
	// updated is a sentinel for a LastUpdateTime set by the reconcile, as opposed to kept.
	updated = "<updated>"
)

func TestStatusReconcile_ReadyCondition(t *testing.T) {
	tests := map[string]struct {
		conditions      []genericcondition.GenericCondition
		objs            []client.Object
		expectStatus    corev1.ConditionStatus
		expectReason    string
		expectMessage   string
		expectTimestamp string
		// expectDisplayMessage is also expected to set Display.Error.
		expectDisplayMessage string
	}{
		"no bundles, Accepted=False sets Ready=False": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionFalse, acceptedErrMsg),
			},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "Error",
			expectMessage:        acceptedErrMsg,
			expectDisplayMessage: acceptedErrMsg,
			expectTimestamp:      updated,
		},
		"no bundles, Accepted=True sets Ready=True": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionTrue, ""),
			},
			expectStatus:    corev1.ConditionTrue,
			expectTimestamp: updated,
		},
		"no bundles, no Accepted condition yet sets Ready=True": {
			expectStatus:    corev1.ConditionTrue,
			expectTimestamp: updated,
		},
		"ready bundle deployments, Accepted=False sets Ready=False": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionFalse, acceptedErrMsg),
				ready(corev1.ConditionTrue, "", ""),
			},
			objs:                 []client.Object{bundleDeployment(true)},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "Error",
			expectMessage:        acceptedErrMsg,
			expectDisplayMessage: acceptedErrMsg,
			expectTimestamp:      updated,
		},
		"Accepted=False does not override a bundle deployment error": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionFalse, acceptedErrMsg),
			},
			objs:            []client.Object{bundleDeployment(false)},
			expectStatus:    corev1.ConditionFalse,
			expectMessage:   bdNotReadyMsg,
			expectTimestamp: updated,
		},
		"Accepted=False does not override a bundle error": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionFalse, acceptedErrMsg),
			},
			objs:                 []client.Object{renderErrorBundle()},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "RenderError",
			expectMessage:        bundleErrMsg,
			expectDisplayMessage: bundleErrMsg,
			expectTimestamp:      bundleTimestamp,
		},
		"restriction violation sets Ready=False": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionFalse, restrictionErrMsg),
				ready(corev1.ConditionTrue, "", ""),
			},
			objs: []client.Object{
				bundleDeployment(true),
				&fleet.GitRepoRestriction{
					ObjectMeta:              metav1.ObjectMeta{Name: "restriction", Namespace: "fleet-local"},
					AllowedTargetNamespaces: []string{"allowed"},
				},
			},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "Error",
			expectMessage:        restrictionErrMsg,
			expectDisplayMessage: restrictionErrMsg,
			expectTimestamp:      updated,
		},
		"other conditions are preserved": {
			conditions: []genericcondition.GenericCondition{
				newCondition("Reconciling", corev1.ConditionFalse, "", ""),
				accepted(corev1.ConditionFalse, acceptedErrMsg),
				newCondition("Stalled", corev1.ConditionTrue, "Error", "job failed"),
			},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "Error",
			expectMessage:        acceptedErrMsg,
			expectDisplayMessage: acceptedErrMsg,
			expectTimestamp:      updated,
		},

		// Transitions between the sources of the Ready condition.
		"Ready=False from Accepted is cleared once accepted": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionTrue, ""),
				ready(corev1.ConditionFalse, "Error", acceptedErrMsg),
			},
			expectStatus:    corev1.ConditionTrue,
			expectTimestamp: updated,
		},
		"Ready=False from Accepted does not keep its reason for a bundle deployment error": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionTrue, ""),
				ready(corev1.ConditionFalse, "Error", acceptedErrMsg),
			},
			objs:            []client.Object{bundleDeployment(false)},
			expectStatus:    corev1.ConditionFalse,
			expectMessage:   bdNotReadyMsg,
			expectTimestamp: updated,
		},
		"Ready=False from Accepted is replaced by a bundle error": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionFalse, acceptedErrMsg),
				ready(corev1.ConditionFalse, "Error", acceptedErrMsg),
			},
			objs:                 []client.Object{renderErrorBundle()},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "RenderError",
			expectMessage:        bundleErrMsg,
			expectDisplayMessage: bundleErrMsg,
			expectTimestamp:      bundleTimestamp,
		},
		"Ready=False from a fixed bundle error falls back to Accepted=False": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionFalse, acceptedErrMsg),
				renderErrorBundle().Status.Conditions[0],
			},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "Error",
			expectMessage:        acceptedErrMsg,
			expectDisplayMessage: acceptedErrMsg,
			expectTimestamp:      updated,
		},

		// Steady states: an unchanged Ready condition keeps its LastUpdateTime, so
		// that status patches do not churn and retrigger reconciles.
		"steady Ready=False from Accepted": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionFalse, acceptedErrMsg),
				ready(corev1.ConditionFalse, "Error", acceptedErrMsg),
			},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "Error",
			expectMessage:        acceptedErrMsg,
			expectDisplayMessage: acceptedErrMsg,
			expectTimestamp:      oldTimestamp,
		},
		"steady Ready=False from a bundle deployment error": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionFalse, acceptedErrMsg),
				ready(corev1.ConditionFalse, "", bdNotReadyMsg),
			},
			objs:            []client.Object{bundleDeployment(false)},
			expectStatus:    corev1.ConditionFalse,
			expectMessage:   bdNotReadyMsg,
			expectTimestamp: oldTimestamp,
		},
		"steady Ready=False from a bundle error": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionFalse, acceptedErrMsg),
				renderErrorBundle().Status.Conditions[0],
			},
			objs:                 []client.Object{renderErrorBundle()},
			expectStatus:         corev1.ConditionFalse,
			expectReason:         "RenderError",
			expectMessage:        bundleErrMsg,
			expectDisplayMessage: bundleErrMsg,
			expectTimestamp:      bundleTimestamp,
		},
		// A bundle is also not ready while its deployments roll out, which is not
		// an error to display.
		"rolling out bundle sets Ready=False without a Display error": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionTrue, ""),
			},
			objs:            []client.Object{waitAppliedBundleDeployment(), rollingOutBundle()},
			expectStatus:    corev1.ConditionFalse,
			expectMessage:   rollingOutMsg,
			expectTimestamp: oldTimestamp,
		},
		"steady Ready=True": {
			conditions: []genericcondition.GenericCondition{
				accepted(corev1.ConditionTrue, ""),
				ready(corev1.ConditionTrue, "", ""),
			},
			objs:            []client.Object{bundleDeployment(true)},
			expectStatus:    corev1.ConditionTrue,
			expectTimestamp: oldTimestamp,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			r, key := newStatusReconciler(t, test.conditions, test.objs...)

			conds := reconcileStatus(t, r, key)
			cond, ok := findCondition(conds, string(fleet.Ready))
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
			if test.expectTimestamp == updated {
				if cond.LastUpdateTime == "" || cond.LastUpdateTime == oldTimestamp {
					t.Errorf("expected Ready LastUpdateTime to be updated, got %q", cond.LastUpdateTime)
				}
			} else if cond.LastUpdateTime != test.expectTimestamp {
				t.Errorf("expected Ready LastUpdateTime %q, got %q", test.expectTimestamp, cond.LastUpdateTime)
			}

			gitrepo := &fleet.GitRepo{}
			if err := r.Get(context.Background(), key, gitrepo); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			display := gitrepo.Status.Display
			if display.Message != test.expectDisplayMessage || display.Error != (test.expectDisplayMessage != "") {
				t.Errorf("expected Display message %q and error %t, got %q and %t",
					test.expectDisplayMessage, test.expectDisplayMessage != "", display.Message, display.Error)
			}

			// Conditions owned by other reconcilers are left untouched.
			for _, want := range test.conditions {
				if want.Type == string(fleet.Ready) {
					continue
				}
				if got, ok := findCondition(conds, want.Type); !ok || got != want {
					t.Errorf("expected condition %s to be preserved as %+v, got %+v", want.Type, want, got)
				}
			}

			// A second reconcile must not change Ready: it would otherwise flip
			// on every reconcile.
			again, _ := findCondition(reconcileStatus(t, r, key), string(fleet.Ready))
			if again != cond {
				t.Errorf("expected Ready to be unchanged by a second reconcile, got %+v, then %+v", cond, again)
			}
		})
	}
}

func newStatusReconciler(
	t *testing.T,
	conditions []genericcondition.GenericCondition,
	objs ...client.Object,
) (*StatusReconciler, types.NamespacedName) {
	t.Helper()

	scheme := runtime.NewScheme()
	utilruntime.Must(fleet.AddToScheme(scheme))

	gitrepo := &fleet.GitRepo{
		ObjectMeta: metav1.ObjectMeta{Name: "gitrepo", Namespace: "fleet-local"},
		Spec:       fleet.GitRepoSpec{Repo: "https://example.com/repo"},
		Status: fleet.GitRepoStatus{
			GitJobStatus: "Current",
			StatusBase:   fleet.StatusBase{Conditions: conditions},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(append(objs, gitrepo)...).
		WithStatusSubresource(&fleet.GitRepo{}).
		Build()

	return &StatusReconciler{Client: c, Scheme: scheme}, client.ObjectKeyFromObject(gitrepo)
}

func reconcileStatus(t *testing.T, r *StatusReconciler, key types.NamespacedName) []genericcondition.GenericCondition {
	t.Helper()

	ctx := context.Background()
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gitrepo := &fleet.GitRepo{}
	if err := r.Get(ctx, key, gitrepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return gitrepo.Status.Conditions
}

func findCondition(conds []genericcondition.GenericCondition, condType string) (genericcondition.GenericCondition, bool) {
	for _, c := range conds {
		if c.Type == condType {
			return c, true
		}
	}
	return genericcondition.GenericCondition{}, false
}

func newCondition(condType string, status corev1.ConditionStatus, reason, msg string) genericcondition.GenericCondition {
	return genericcondition.GenericCondition{
		Type:           condType,
		Status:         status,
		Reason:         reason,
		Message:        msg,
		LastUpdateTime: oldTimestamp,
	}
}

// accepted returns an Accepted condition as the gitjob reconciler sets it.
func accepted(status corev1.ConditionStatus, msg string) genericcondition.GenericCondition {
	reason := ""
	if status == corev1.ConditionFalse {
		reason = "Error"
	}
	return newCondition(fleet.GitRepoAcceptedCondition, status, reason, msg)
}

func ready(status corev1.ConditionStatus, reason, msg string) genericcondition.GenericCondition {
	return newCondition(string(fleet.Ready), status, reason, msg)
}

func bundleDeployment(isReady bool) *fleet.BundleDeployment {
	return &fleet.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bd",
			Namespace: "cluster-ns",
			Labels: map[string]string{
				fleet.RepoLabel:            "gitrepo",
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

func renderErrorBundle() *fleet.Bundle {
	return &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bundle",
			Namespace: "fleet-local",
			Labels:    map[string]string{fleet.RepoLabel: "gitrepo"},
		},
		Status: fleet.BundleStatus{
			Conditions: []genericcondition.GenericCondition{{
				Type:           string(fleet.Ready),
				Status:         corev1.ConditionFalse,
				Reason:         "RenderError",
				Message:        bundleErrMsg,
				LastUpdateTime: bundleTimestamp,
			}},
		},
	}
}

func waitAppliedBundleDeployment() *fleet.BundleDeployment {
	bd := bundleDeployment(false)
	bd.Spec.DeploymentID = "new-id"
	bd.Spec.StagedDeploymentID = "new-id"
	return bd
}

func rollingOutBundle() *fleet.Bundle {
	return &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bundle",
			Namespace: "fleet-local",
			Labels:    map[string]string{fleet.RepoLabel: "gitrepo"},
		},
		Status: fleet.BundleStatus{
			Summary: fleet.BundleSummary{WaitApplied: 1, DesiredReady: 1},
			Conditions: []genericcondition.GenericCondition{
				newCondition(string(fleet.Ready), corev1.ConditionFalse, "", rollingOutMsg),
			},
		},
	}
}
