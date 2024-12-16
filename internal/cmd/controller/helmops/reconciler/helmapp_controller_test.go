//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/poller_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler GitPoller
//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/client_mock.go -package=mocks sigs.k8s.io/controller-runtime/pkg/client Client,SubResourceWriter

package reconciler

import (
	"context"
	"fmt"
	"os"
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

func getCondition(helmapp *fleet.HelmApp, condType string) (genericcondition.GenericCondition, bool) {
	for _, cond := range helmapp.Status.Conditions {
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
	helmapp := fleet.HelmApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "helmapp",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
	client := mocks.NewMockClient(mockCtrl)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, fh *fleet.HelmApp, opts ...interface{}) error {
			fh.Name = helmapp.Name
			fh.Namespace = helmapp.Namespace
			fh.Spec.Helm = &fleet.HelmOptions{
				Chart: "chart",
			}
			return nil
		},
	)
	// expected from addFinalizer
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes().Return(nil)

	r := HelmAppReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.TODO()

	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
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
	helmapp := fleet.HelmApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "helmapp",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
	client := mocks.NewMockClient(mockCtrl)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, fh *fleet.HelmApp, opts ...interface{}) error {
			fh.Name = helmapp.Name
			fh.Namespace = helmapp.Namespace
			fh.Spec.Helm = &fleet.HelmOptions{
				Chart: "chart",
			}
			controllerutil.AddFinalizer(fh, finalize.HelmAppFinalizer)
			return nil
		},
	)

	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, bundle *fleet.Bundle, opts ...interface{}) error {
			return fmt.Errorf("this is a test error")
		},
	)

	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, bundle *fleet.HelmApp, opts ...interface{}) error {
			return nil
		},
	)

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	client.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, helmapp *fleet.HelmApp, opts ...interface{}) {
			c, found := getCondition(helmapp, fleet.HelmAppAcceptedCondition)
			if !found {
				t.Errorf("expecting to find the %s condition and could not find it.", fleet.HelmAppAcceptedCondition)
			}
			if c.Message != "this is a test error" {
				t.Errorf("expecting message [this is a test error] in condition, got [%s]", c.Message)
			}
		},
	).Times(1)

	r := HelmAppReconciler{
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
	helmapp := fleet.HelmApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "helmapp",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
	client := mocks.NewMockClient(mockCtrl)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, fh *fleet.HelmApp, opts ...interface{}) error {
			fh.Name = helmapp.Name
			fh.Namespace = helmapp.Namespace
			fh.Spec.Helm = &fleet.HelmOptions{
				Chart:   "chart",
				Version: "1.1.2",
			}
			controllerutil.AddFinalizer(fh, finalize.HelmAppFinalizer)
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
		func(ctx context.Context, req types.NamespacedName, bundle *fleet.HelmApp, opts ...interface{}) error {
			return nil
		},
	)

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	client.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, helmapp *fleet.HelmApp, opts ...interface{}) {
			// version in status should be the one in the spec
			if helmapp.Status.Version != "1.1.2" {
				t.Errorf("expecting Status.Version == 1.1.2, got %s", helmapp.Status.Version)
			}
		},
	).Times(1)

	r := HelmAppReconciler{
		Client: client,
		Scheme: scheme,
	}

	ctx := context.TODO()

	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("found unexpected error %v", err)
	}
	if res.Requeue {
		t.Errorf("expecting Requeue set to false, it was true")
	}
}
