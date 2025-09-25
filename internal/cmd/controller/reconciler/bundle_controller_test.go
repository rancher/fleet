//go:generate mockgen --build_flags=--mod=mod -destination=../../../mocks/target_builder_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/controller/reconciler TargetBuilder,Store
package reconciler_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/mocks"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	"go.uber.org/mock/gomock"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestReconcile_FinalizerUpdateError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))

	bundle := fleetv1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-bundle",
			Namespace: "default",
		},
		Spec: fleetv1.BundleSpec{
			ValuesHash: "foo", // non-empty
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, b *fleetv1.Bundle, opts ...interface{}) error {
			b.Name = bundle.Name
			b.Namespace = bundle.Namespace
			// no finalizer

			b.Spec = bundle.Spec

			return nil
		},
	)

	mockClient.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).
		Return(errors.New("something went wrong"))

	expectedErrorMsg := "failed to add finalizer to bundle: something went wrong"

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).Do(
		func(ctx context.Context, b *fleetv1.Bundle, p client.Patch, opts ...interface{}) {
			cond, found := getBundleReadyCondition(b)
			if !found {
				t.Errorf("expecting Condition %s to be found", fleetv1.BundleConditionReady)
			}
			if !strings.Contains(cond.Message, expectedErrorMsg) {
				t.Errorf("expecting condition message containing [%s], got [%s]", expectedErrorMsg, cond.Message)
			}
			if cond.Type != fleetv1.BundleConditionReady {
				t.Errorf("expecting condition type [%s], got [%s]", fleetv1.BundleConditionReady, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Fatalf("expecting an error, got nil")
	}

	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReconcile_HelmValuesLoadError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))

	bundle := fleetv1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-bundle",
			Namespace: "default",
		},
		Spec: fleetv1.BundleSpec{
			ValuesHash: "foo", // non-empty
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, b *fleetv1.Bundle, opts ...interface{}) error {
			b.Name = bundle.Name
			b.Namespace = bundle.Namespace
			controllerutil.AddFinalizer(b, finalize.BundleFinalizer)

			b.Spec = bundle.Spec

			return nil
		},
	)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		Return(errors.New("something went wrong"))

	expectedErrorMsg := "failed to load values secret for bundle:"

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).Do(
		func(ctx context.Context, b *fleetv1.Bundle, p client.Patch, opts ...interface{}) {
			cond, found := getBundleReadyCondition(b)
			if !found {
				t.Errorf("expecting Condition %s to be found", fleetv1.BundleConditionReady)
			}
			if !strings.Contains(cond.Message, expectedErrorMsg) {
				t.Errorf("expecting condition message containing [%s], got [%s]", expectedErrorMsg, cond.Message)
			}
			if cond.Type != fleetv1.BundleConditionReady {
				t.Errorf("expecting condition type [%s], got [%s]", fleetv1.BundleConditionReady, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Fatalf("expecting an error, got nil")
	}

	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReconcile_HelmVersionResolutionError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))

	bundle := fleetv1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-bundle",
			Namespace: "default",
		},
		Spec: fleetv1.BundleSpec{
			ContentsID: "foo", // non-empty
			BundleDeploymentOptions: fleetv1.BundleDeploymentOptions{
				Helm: &fleetv1.HelmOptions{
					Version: "0.1.x", // non-empty, non-strict version
				},
			},
			HelmOpOptions: &fleetv1.BundleHelmOptions{},
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, b *fleetv1.Bundle, opts ...interface{}) error {
			b.Name = bundle.Name
			b.Namespace = bundle.Namespace
			controllerutil.AddFinalizer(b, finalize.BundleFinalizer)

			b.Spec = bundle.Spec

			return nil
		},
	)

	expectedErrorMsg := "chart version cannot be deployed; check HelmOp status for more details:"

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).Do(
		func(ctx context.Context, b *fleetv1.Bundle, p client.Patch, opts ...interface{}) {
			cond, found := getBundleReadyCondition(b)
			if !found {
				t.Errorf("expecting Condition %s to be found", fleetv1.BundleConditionReady)
			}
			if !strings.Contains(cond.Message, expectedErrorMsg) {
				t.Errorf("expecting condition message containing [%s], got [%s]", expectedErrorMsg, cond.Message)
			}
			if cond.Type != fleetv1.BundleConditionReady {
				t.Errorf("expecting condition type [%s], got [%s]", fleetv1.BundleConditionReady, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Fatalf("expecting an error, got nil")
	}

	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReconcile_TargetsBuildingError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))

	bundle := fleetv1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-bundle",
			Namespace: "default",
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, b *fleetv1.Bundle, opts ...interface{}) error {
			b.Name = bundle.Name
			b.Namespace = bundle.Namespace
			controllerutil.AddFinalizer(b, finalize.BundleFinalizer)

			b.Spec = bundle.Spec

			return nil
		},
	)

	expectedErrorMsg := "targeting error: something went wrong"

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).Do(
		func(ctx context.Context, b *fleetv1.Bundle, p client.Patch, opts ...interface{}) {
			cond, found := getBundleReadyCondition(b)
			if !found {
				t.Errorf("expecting Condition %s to be found", fleetv1.BundleConditionReady)
			}
			if !strings.Contains(cond.Message, expectedErrorMsg) {
				t.Errorf("expecting condition message containing [%s], got [%s]", expectedErrorMsg, cond.Message)
			}
			if cond.Type != fleetv1.BundleConditionReady {
				t.Errorf("expecting condition type [%s], got [%s]", fleetv1.BundleConditionReady, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	targetBuilderMock := mocks.NewMockTargetBuilder(mockCtrl)
	targetBuilderMock.EXPECT().Targets(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("something went wrong"))

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
		Builder:  targetBuilderMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Fatalf("expecting an error, got nil")
	}

	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReconcile_StatusResetFromTargetsError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))

	bundle := fleetv1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-bundle",
			Namespace: "default",
		},
		Spec: fleetv1.BundleSpec{
			RolloutStrategy: &fleetv1.RolloutStrategy{
				MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "foo"}, // will fail to parse as number or percentage
			},
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, b *fleetv1.Bundle, opts ...interface{}) error {
			b.Name = bundle.Name
			b.Namespace = bundle.Namespace
			controllerutil.AddFinalizer(b, finalize.BundleFinalizer)

			b.Spec = bundle.Spec

			return nil
		},
	)

	expectedErrorMsg := "failed to reset bundle status from targets: invalid maxUnavailable"

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).Do(
		func(ctx context.Context, b *fleetv1.Bundle, p client.Patch, opts ...interface{}) {
			cond, found := getBundleReadyCondition(b)
			if !found {
				t.Errorf("expecting Condition %s to be found", fleetv1.BundleConditionReady)
			}
			if !strings.Contains(cond.Message, expectedErrorMsg) {
				t.Errorf("expecting condition message containing [%s], got [%s]", expectedErrorMsg, cond.Message)
			}
			if cond.Type != fleetv1.BundleConditionReady {
				t.Errorf("expecting condition type [%s], got [%s]", fleetv1.BundleConditionReady, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	matchedTargets := []*target.Target{
		{
			Bundle: &bundle,
			Cluster: &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "my-ns",
					Name:      "my-cluster",
				},
			},
			Deployment: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "my-bd", // non-empty
				},
			},
			DeploymentID: "foo",
		},
	}
	targetBuilderMock := mocks.NewMockTargetBuilder(mockCtrl)
	targetBuilderMock.EXPECT().Targets(gomock.Any(), gomock.Any(), gomock.Any()).Return(matchedTargets, nil)

	storeMock := mocks.NewMockStore(mockCtrl)
	storeMock.EXPECT().Store(gomock.Any(), gomock.Any()).Return(nil)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
		Builder:  targetBuilderMock,
		Store:    storeMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Fatalf("expecting an error, got nil")
	}

	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReconcile_ManifestStorageError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))

	bundle := fleetv1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-bundle",
			Namespace: "default",
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, b *fleetv1.Bundle, opts ...interface{}) error {
			b.Name = bundle.Name
			b.Namespace = bundle.Namespace
			controllerutil.AddFinalizer(b, finalize.BundleFinalizer)

			b.Spec = bundle.Spec

			return nil
		},
	)

	expectedErrorMsg := "could not copy manifest into Content resource: something went wrong"

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).Do(
		func(ctx context.Context, b *fleetv1.Bundle, p client.Patch, opts ...interface{}) {
			cond, found := getBundleReadyCondition(b)
			if !found {
				t.Errorf("expecting Condition %s to be found", fleetv1.BundleConditionReady)
			}
			if cond.Message != expectedErrorMsg {
				t.Errorf("expecting condition message containing [%s], got [%s]", expectedErrorMsg, cond.Message)
			}
			if cond.Type != fleetv1.BundleConditionReady {
				t.Errorf("expecting condition type [%s], got [%s]", fleetv1.BundleConditionReady, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	matchedTargets := []*target.Target{{DeploymentID: "foo"}} // just needs to be non-empty
	targetBuilderMock := mocks.NewMockTargetBuilder(mockCtrl)
	targetBuilderMock.EXPECT().Targets(gomock.Any(), gomock.Any(), gomock.Any()).Return(matchedTargets, nil)

	storeMock := mocks.NewMockStore(mockCtrl)
	storeMock.EXPECT().Store(gomock.Any(), gomock.Any()).Return(errors.New("something went wrong"))

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
		Builder:  targetBuilderMock,
		Store:    storeMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Fatalf("expecting an error, got nil")
	}

	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReconcile_OptionsSecretCreationError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))

	bundle := fleetv1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-bundle",
			Namespace: "default",
		},
		Spec: fleetv1.BundleSpec{
			RolloutStrategy: nil,
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, b *fleetv1.Bundle, opts ...interface{}) error {
			b.Name = bundle.Name
			b.Namespace = bundle.Namespace
			controllerutil.AddFinalizer(b, finalize.BundleFinalizer)

			b.Spec = bundle.Spec

			return nil
		},
	)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Content{}), gomock.Any()).
		Return(nil)
	mockClient.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Content{}), gomock.Any()).Return(nil)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
		DoAndReturn(
			func(ctx context.Context, req types.NamespacedName, bd *fleetv1.BundleDeployment, opts ...interface{}) error {
				bd.Spec.Options = fleetv1.BundleDeploymentOptions{
					Helm: &fleetv1.HelmOptions{
						Values: &fleetv1.GenericMap{
							Data: map[string]interface{}{"foo": "bar"}, // non-empty
						},
					},
				}

				return nil
			},
		)

	mockClient.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
		DoAndReturn(
			func(ctx context.Context, bd *fleetv1.BundleDeployment, opts ...interface{}) error {
				bd.Spec.ValuesHash = "foo" // non-empty, to force secret create or update

				return nil
			},
		)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		Return(nil)
	mockClient.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		Return(errors.New("something went wrong"))

	expectedErrorMsg := "failed to create options secret: something went wrong"

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).Do(
		func(ctx context.Context, b *fleetv1.Bundle, p client.Patch, opts ...interface{}) {
			cond, found := getBundleReadyCondition(b)
			if !found {
				t.Errorf("expecting Condition %s to be found", fleetv1.BundleConditionReady)
			}
			if cond.Message != expectedErrorMsg {
				t.Errorf("expecting condition message containing [%s], got [%s]", expectedErrorMsg, cond.Message)
			}
			if cond.Type != fleetv1.BundleConditionReady {
				t.Errorf("expecting condition type [%s], got [%s]", fleetv1.BundleConditionReady, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	matchedTargets := []*target.Target{
		{
			Bundle: &bundle,
			Cluster: &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "my-ns",
					Name:      "my-cluster",
				},
			},
			Deployment: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "my-bd", // non-empty
				},
			},
			DeploymentID: "foo",
		},
	}
	targetBuilderMock := mocks.NewMockTargetBuilder(mockCtrl)
	targetBuilderMock.EXPECT().Targets(gomock.Any(), gomock.Any(), gomock.Any()).Return(matchedTargets, nil)

	storeMock := mocks.NewMockStore(mockCtrl)
	storeMock.EXPECT().Store(gomock.Any(), gomock.Any()).Return(nil)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
		Builder:  targetBuilderMock,
		Store:    storeMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Fatalf("expecting an error, got nil")
	}

	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReconcile_OptionsSecretDeletionError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))

	bundle := fleetv1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-bundle",
			Namespace: "default",
		},
		Spec: fleetv1.BundleSpec{
			RolloutStrategy: nil,
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, b *fleetv1.Bundle, opts ...interface{}) error {
			b.Name = bundle.Name
			b.Namespace = bundle.Namespace
			controllerutil.AddFinalizer(b, finalize.BundleFinalizer)

			b.Spec = bundle.Spec

			return nil
		},
	)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Content{}), gomock.Any()).
		Return(nil)
	mockClient.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Content{}), gomock.Any()).Return(nil)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
		Return(nil)

	mockClient.EXPECT().Delete(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		Return(errors.New("something went wrong"))

	expectedErrorMsg := "failed to delete options secret: something went wrong"

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).Do(
		func(ctx context.Context, b *fleetv1.Bundle, p client.Patch, opts ...interface{}) {
			cond, found := getBundleReadyCondition(b)
			if !found {
				t.Errorf("expecting Condition %s to be found", fleetv1.BundleConditionReady)
			}
			if cond.Message != expectedErrorMsg {
				t.Errorf("expecting condition message containing [%s], got [%s]", expectedErrorMsg, cond.Message)
			}
			if cond.Type != fleetv1.BundleConditionReady {
				t.Errorf("expecting condition type [%s], got [%s]", fleetv1.BundleConditionReady, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	matchedTargets := []*target.Target{
		{
			Bundle: &bundle,
			Cluster: &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "my-ns",
					Name:      "my-cluster",
				},
			},
			Deployment: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "my-bd", // non-empty
				},
			},
			DeploymentID: "foo",
		},
	}
	targetBuilderMock := mocks.NewMockTargetBuilder(mockCtrl)
	targetBuilderMock.EXPECT().Targets(gomock.Any(), gomock.Any(), gomock.Any()).Return(matchedTargets, nil)

	storeMock := mocks.NewMockStore(mockCtrl)
	storeMock.EXPECT().Store(gomock.Any(), gomock.Any()).Return(nil)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
		Builder:  targetBuilderMock,
		Store:    storeMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Fatalf("expecting an error, got nil")
	}

	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReconcile_OCIStorageAccessSecretResolutionError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))

	bundle := fleetv1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-bundle",
			Namespace: "default",
		},
		Spec: fleetv1.BundleSpec{
			RolloutStrategy: nil,
			ContentsID:      "foo", // non-empty, to force OCI storage secret lookup
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, b *fleetv1.Bundle, opts ...interface{}) error {
			b.Name = bundle.Name
			b.Namespace = bundle.Namespace
			controllerutil.AddFinalizer(b, finalize.BundleFinalizer)

			b.Spec = bundle.Spec

			return nil
		},
	)

	// OCI storage access secret
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		Return(errors.New("something went wrong"))

	expectedErrorMsg := "failed to get OCI storage access secret: something went wrong"

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).Do(
		func(ctx context.Context, b *fleetv1.Bundle, p client.Patch, opts ...interface{}) {
			cond, found := getBundleReadyCondition(b)
			if !found {
				t.Errorf("expecting Condition %s to be found", fleetv1.BundleConditionReady)
			}
			if cond.Message != expectedErrorMsg {
				t.Errorf("expecting condition message containing [%s], got [%s]", expectedErrorMsg, cond.Message)
			}
			if cond.Type != fleetv1.BundleConditionReady {
				t.Errorf("expecting condition type [%s], got [%s]", fleetv1.BundleConditionReady, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	matchedTargets := []*target.Target{
		{
			Bundle: &bundle,
			Cluster: &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "my-ns",
					Name:      "my-cluster",
				},
			},
			Deployment: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "my-bd", // non-empty
				},
			},
			DeploymentID: "foo",
		},
	}
	targetBuilderMock := mocks.NewMockTargetBuilder(mockCtrl)
	targetBuilderMock.EXPECT().Targets(gomock.Any(), gomock.Any(), gomock.Any()).Return(matchedTargets, nil)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
		Builder:  targetBuilderMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Fatalf("expecting an error, got nil")
	}

	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("unexpected error: %v", err)
	}
}

func getBundleReadyCondition(b *fleetv1.Bundle) (genericcondition.GenericCondition, bool) {
	for _, cond := range b.Status.Conditions {
		if cond.Type == fleetv1.BundleConditionReady {
			return cond, true
		}
	}
	return genericcondition.GenericCondition{}, false
}
