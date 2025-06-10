//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/poller_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler GitPoller
//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/client_mock.go -package=mocks sigs.k8s.io/controller-runtime/pkg/client Client,SubResourceWriter

package reconciler

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func getCondition(helmop *fleet.HelmOp, condType string) (genericcondition.GenericCondition, bool) {
	for _, cond := range helmop.Status.Conditions {
		if cond.Type == condType {
			return cond, true
		}
	}
	return genericcondition.GenericCondition{}, false
}

func TestReconcile_ReturnsAndRequeuesAfterAddingFinalizer(t *testing.T) {
	os.Setenv("EXPERIMENTAL_HELM_OPS", "true")
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))
	helmop := fleet.HelmOp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "helmop",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: helmop.Name, Namespace: helmop.Namespace}
	client := mocks.NewMockClient(mockCtrl)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, fh *fleet.HelmOp, opts ...interface{}) error {
			fh.Name = helmop.Name
			fh.Namespace = helmop.Namespace
			fh.Spec.Helm = &fleet.HelmOptions{
				Chart: "chart",
			}
			return nil
		},
	)
	// expected from addFinalizer
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes().Return(nil)

	r := HelmOpReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.TODO()

	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	// nolint: staticcheck // Requeue is deprecated; see fleet#3746.
	if !res.Requeue {
		t.Errorf("expecting Requeue set to true, it was false")
	}
}

func TestReconcile_ErrorCreatingBundleIsShownInStatus(t *testing.T) {
	os.Setenv("EXPERIMENTAL_HELM_OPS", "true")
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))
	helmop := fleet.HelmOp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "helmop",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: helmop.Name, Namespace: helmop.Namespace}
	client := mocks.NewMockClient(mockCtrl)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, fh *fleet.HelmOp, opts ...interface{}) error {
			fh.Name = helmop.Name
			fh.Namespace = helmop.Namespace
			fh.Spec.Helm = &fleet.HelmOptions{
				Chart: "chart",
			}
			controllerutil.AddFinalizer(fh, finalize.HelmOpFinalizer)
			return nil
		},
	)

	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, bundle *fleet.Bundle, opts ...interface{}) error {
			return fmt.Errorf("this is a test error")
		},
	)

	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, bundle *fleet.HelmOp, opts ...interface{}) error {
			return nil
		},
	)

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	client.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, helmop *fleet.HelmOp, opts ...interface{}) {
			c, found := getCondition(helmop, fleet.HelmOpAcceptedCondition)
			if !found {
				t.Errorf("expecting to find the %s condition and could not find it.", fleet.HelmOpAcceptedCondition)
			}
			if c.Message != "this is a test error" {
				t.Errorf("expecting message [this is a test error] in condition, got [%s]", c.Message)
			}
		},
	).Times(1)

	r := HelmOpReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.TODO()

	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Errorf("expecting error, got nil")
	}
	if err.Error() != "this is a test error" {
		t.Errorf("expecting error: [this is a test error], got %v", err.Error())
	}
	// nolint: staticcheck // Requeue is deprecated; see fleet#3746.
	if res.Requeue {
		t.Errorf("expecting Requeue set to false, it was true")
	}
}

// Validates that the HelmOps reconciler will not create a bundle if another bundle exists with the same name, for
// instance a gitOps bundle.
func TestReconcile_ErrorCreatingBundleIfBundleWithSameNameExists(t *testing.T) {
	os.Setenv("EXPERIMENTAL_HELM_OPS", "true")
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))
	helmop := fleet.HelmOp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-workload",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: helmop.Name, Namespace: helmop.Namespace}
	client := mocks.NewMockClient(mockCtrl)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, fh *fleet.HelmOp, opts ...interface{}) error {
			fh.Name = helmop.Name
			fh.Namespace = helmop.Namespace
			fh.Spec.Helm = &fleet.HelmOptions{
				Chart: "chart",
			}
			controllerutil.AddFinalizer(fh, finalize.HelmOpFinalizer)
			return nil
		},
	)

	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), fleet.Bundle{}).AnyTimes().DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, bundle *fleet.Bundle, opts ...interface{}) error {
			bundle.ObjectMeta = metav1.ObjectMeta{
				Name:      "my-workload",
				Namespace: "default",
			}

			return nil
		},
	)

	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)

	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), fleet.HelmOp{}).AnyTimes().DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, bundle *fleet.HelmOp, opts ...interface{}) error {
			return nil
		},
	)

	expectedErrorMsg := "non-helmops bundle already exists"
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	client.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, helmop *fleet.HelmOp, opts ...interface{}) {
			c, found := getCondition(helmop, fleet.HelmOpAcceptedCondition)
			if !found {
				t.Errorf("expecting to find the %s condition and could not find it.", fleet.HelmOpAcceptedCondition)
			}
			if !strings.Contains(c.Message, expectedErrorMsg) {
				t.Errorf("expecting message [%s] in condition, got [%s]", expectedErrorMsg, c.Message)
			}
		},
	).Times(1)

	r := HelmOpReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.TODO()

	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Errorf("expecting error, got nil")
	}

	if err != nil && !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("expecting error: [%s], got %v", expectedErrorMsg, err.Error())
	}

	// nolint: staticcheck // Requeue is deprecated; see fleet#3746.
	if res.Requeue {
		t.Errorf("expecting Requeue set to false, it was true")
	}
}

func TestReconcile_CreatesBundleAndUpdatesStatus(t *testing.T) {
	os.Setenv("EXPERIMENTAL_HELM_OPS", "true")
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))
	helmop := fleet.HelmOp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "helmop",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: helmop.Name, Namespace: helmop.Namespace}
	client := mocks.NewMockClient(mockCtrl)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, fh *fleet.HelmOp, opts ...interface{}) error {
			fh.Name = helmop.Name
			fh.Namespace = helmop.Namespace
			fh.Spec.Helm = &fleet.HelmOptions{
				Chart:   "chart",
				Version: "1.1.2",
			}
			controllerutil.AddFinalizer(fh, finalize.HelmOpFinalizer)
			return nil
		},
	)

	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(2).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, bundle *fleet.Bundle, opts ...interface{}) error {
			return errors.NewNotFound(schema.GroupResource{}, "Not found")
		},
	)

	client.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, bundle *fleet.Bundle, opts ...interface{}) error {
			return nil
		},
	)

	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, bundle *fleet.HelmOp, opts ...interface{}) error {
			return nil
		},
	)

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	client.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, helmop *fleet.HelmOp, opts ...interface{}) {
			// version in status should be the one in the spec
			if helmop.Status.Version != "1.1.2" {
				t.Errorf("expecting Status.Version == 1.1.2, got %s", helmop.Status.Version)
			}
		},
	).Times(1)

	r := HelmOpReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.TODO()

	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("found unexpected error %v", err)
	}
	// nolint: staticcheck // Requeue is deprecated; see fleet#3746.
	if res.Requeue {
		t.Errorf("expecting Requeue set to false, it was true")
	}
}
