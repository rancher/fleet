//go:generate mockgen --build_flags=--mod=mod -destination=../../../mocks/reconciler_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/controller/reconciler TargetBuilder,Store
package reconciler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/mocks"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	"go.uber.org/mock/gomock"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

	// Not expecting any status update

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}

	if errors.Is(err, reconcile.TerminalError(nil)) {
		t.Fatalf("expected non-terminal error, got %v", err)
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
	expectGetWithFinalizer(mockClient, bundle)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		Return(errors.New("something went wrong"))

	expectedErrorMsg := "failed to load values secret for bundle:"

	statusClient := mocks.NewMockStatusWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)

	expectStatusPatch(t, statusClient, expectedErrorMsg)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
	}

	ctx := context.TODO()
	rs, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if !errors.Is(err, reconcile.TerminalError(nil)) {
		t.Errorf("expected terminal error, got: %v", err)
	}

	if rs.RequeueAfter != 0 {
		t.Errorf("expected no retries, with zero RequeueAfter in result")
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
	expectGetWithFinalizer(mockClient, bundle)

	expectedErrorMsg := "chart version cannot be deployed; check HelmOp status for more details:"

	statusClient := mocks.NewMockStatusWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)

	expectStatusPatch(t, statusClient, expectedErrorMsg)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
	}

	ctx := context.TODO()
	rs, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if !errors.Is(err, reconcile.TerminalError(nil)) {
		t.Errorf("expected terminal error, got: %v", err)
	}

	if rs.RequeueAfter != 0 {
		t.Errorf("expected no retries, with zero RequeueAfter in result")
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
	expectGetWithFinalizer(mockClient, bundle)

	expectedErrorMsg := "targeting error: something went wrong"

	statusClient := mocks.NewMockStatusWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)

	expectStatusPatch(t, statusClient, expectedErrorMsg)

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
	rs, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if !errors.Is(err, reconcile.TerminalError(nil)) {
		t.Errorf("expected terminal error, got: %v", err)
	}

	if rs.RequeueAfter != 0 {
		t.Errorf("expected no retries, with zero RequeueAfter in result")
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
	expectGetWithFinalizer(mockClient, bundle)

	expectedErrorMsg := "failed to reset bundle status from targets: invalid maxUnavailable"

	statusClient := mocks.NewMockStatusWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)

	expectStatusPatch(t, statusClient, expectedErrorMsg)

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
	rs, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if !errors.Is(err, reconcile.TerminalError(nil)) {
		t.Errorf("expected terminal error, got: %v", err)
	}

	if rs.RequeueAfter != 0 {
		t.Errorf("expected no retries, with zero RequeueAfter in result")
	}
}

func TestReconcile_ManifestStorageError(t *testing.T) {
	cases := []struct {
		name                      string
		storeErr                  error
		expectedStatusPatchErrMsg string
	}{
		{
			name:                      "non-retryable error",
			storeErr:                  errors.New("something went wrong"),
			expectedStatusPatchErrMsg: "could not copy manifest into Content resource: something went wrong",
		},
		{
			name:     "retryable error",
			storeErr: fmt.Errorf("%w: %w", errorutil.ErrRetryable, errors.New("something went wrong")),
			// no expected reconcile error (requeue set instead)
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
			expectGetWithFinalizer(mockClient, bundle)

			if c.expectedStatusPatchErrMsg != "" {
				statusClient := mocks.NewMockStatusWriter(mockCtrl)
				mockClient.EXPECT().Status().Return(statusClient).Times(1)

				expectStatusPatch(t, statusClient, c.expectedStatusPatchErrMsg)
			}

			recorderMock := mocks.NewMockEventRecorder(mockCtrl)

			matchedTargets := []*target.Target{{DeploymentID: "foo"}} // just needs to be non-empty
			targetBuilderMock := mocks.NewMockTargetBuilder(mockCtrl)
			targetBuilderMock.EXPECT().Targets(gomock.Any(), gomock.Any(), gomock.Any()).Return(matchedTargets, nil)

			storeMock := mocks.NewMockStore(mockCtrl)
			storeMock.EXPECT().Store(gomock.Any(), gomock.Any()).Return(c.storeErr)

			r := reconciler.BundleReconciler{
				Client:   mockClient,
				Scheme:   scheme,
				Recorder: recorderMock,
				Builder:  targetBuilderMock,
				Store:    storeMock,
			}

			ctx := context.TODO()
			rs, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})

			if c.expectedStatusPatchErrMsg != "" && !errors.Is(err, reconcile.TerminalError(nil)) {
				t.Errorf("expected terminal error, got: %v", err)
			} else if c.expectedStatusPatchErrMsg == "" && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if c.expectedStatusPatchErrMsg == "" && rs.RequeueAfter == 0 {
				t.Errorf("expected non-zero RequeueAfter in result")
			}
		})
	}
}

func TestReconcile_OptionsSecretCreateUpdateError(t *testing.T) {
	cases := []struct {
		name        string
		secretCalls func(*mocks.MockK8sClient)
	}{
		{
			"create",
			func(mc *mocks.MockK8sClient) {
				// Get + Create (CreateOrUpdate) of new options secret
				mc.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(&k8serrors.StatusError{ErrStatus: metav1.Status{Code: http.StatusNotFound}})
				mc.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(errors.New("something went wrong"))
			},
		},
		{
			"update",
			func(mc *mocks.MockK8sClient) {
				// Get + Update (CreateOrUpdate) of existing options secret
				mc.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(nil)
				mc.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(errors.New("something went wrong"))
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
			expectGetWithFinalizer(mockClient, bundle)

			c.secretCalls(mockClient)

			// No expected status update (retryable error)

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
						Spec: fleetv1.BundleDeploymentSpec{
							DeploymentID: "newdeploymentID", // allows the deployment to be updated from staged.
							Options: fleetv1.BundleDeploymentOptions{
								Helm: &fleetv1.HelmOptions{
									Values: &fleetv1.GenericMap{
										Data: map[string]interface{}{"foo": "bar"}, // non-empty, to generate a non-empty hash and force secret creation/update
									},
								},
							},
						},
					},
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
			rs, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if rs.RequeueAfter == 0 {
				t.Errorf("expected non-zero RequeueAfter in result")
			}
		})
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
	expectGetWithFinalizer(mockClient, bundle)

	mockClient.EXPECT().Delete(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		Return(errors.New("something went wrong"))

	// No expected status update (retryable error)

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
	rs, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if rs.RequeueAfter == 0 {
		t.Errorf("expected non-zero RequeueAfter in result")
	}
}

func TestReconcile_OCIReferenceSecretResolutionError(t *testing.T) {
	cases := []struct {
		name               string
		secretGet          func(ctx context.Context, req types.NamespacedName, s *corev1.Secret, opts ...interface{}) error
		expectStatusUpdate bool
		expectedErrMsg     string
	}{
		{
			name: "non-retryable error",
			secretGet: func(ctx context.Context, req types.NamespacedName, s *corev1.Secret, opts ...interface{}) error {
				// Necessary reference field is missing → non-retryable
				return nil
			},
			expectStatusUpdate: true,
			expectedErrMsg:     "failed to build OCI reference: expected data [reference] not found in secret",
		},
		{
			name: "retryable error",
			secretGet: func(ctx context.Context, req types.NamespacedName, s *corev1.Secret, opts ...interface{}) error {
				return errors.New("something went wrong")
			},
			// no expected reconcile error (requeue set instead)
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
			expectGetWithFinalizer(mockClient, bundle)

			// OCI reference secret
			mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
				DoAndReturn(c.secretGet)

			if c.expectStatusUpdate {
				statusClient := mocks.NewMockStatusWriter(mockCtrl)
				mockClient.EXPECT().Status().Return(statusClient).Times(1)

				expectStatusPatch(t, statusClient, c.expectedErrMsg)
			}

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
			rs, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})

			if c.expectStatusUpdate && !errors.Is(err, reconcile.TerminalError(nil)) {
				t.Errorf("expected terminal error, got: %v", err)
			} else if !c.expectStatusUpdate && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if c.expectedErrMsg == "" && rs.RequeueAfter == 0 {
				t.Errorf("expected non-zero RequeueAfter in result")
			}
		})
	}
}

func TestReconcile_DownstreamObjectsHandlingError(t *testing.T) {
	envVar := "EXPERIMENTAL_COPY_RESOURCES_DOWNSTREAM"
	bkp := os.Getenv(envVar)
	defer func() {
		os.Setenv(envVar, bkp)
	}()

	os.Setenv(envVar, "true")

	cases := []struct {
		name                        string
		downstreamResources         []fleetv1.DownstreamResource
		downstreamResourcesGetCalls func(mc *mocks.MockK8sClient)
		expectedErrorMsg            string
		expectRetries               bool
	}{
		{
			name: "secret not found",
			downstreamResources: []fleetv1.DownstreamResource{
				{
					Kind: "Secret",
					Name: "my-top-secret",
				},
				// will not be processed
				{
					Kind: "ConfigMap",
					Name: "my-configmap",
				},
			},
			downstreamResourcesGetCalls: func(mc *mocks.MockK8sClient) {
				mc.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(errors.New("something went wrong"))

			},
			expectedErrorMsg: `failed to clone config maps and secrets downstream: failed to copy secret`,
			expectRetries:    true,
		},
		{
			name: "config map not found",
			downstreamResources: []fleetv1.DownstreamResource{
				{
					Kind: "Secret",
					Name: "my-top-secret",
				},
				{
					Kind: "ConfigMap",
					Name: "my-configmap",
				},
			},
			downstreamResourcesGetCalls: func(mc *mocks.MockK8sClient) {
				// Getting the source secret
				mc.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(nil)

				// Checking if the destination secret exists
				mc.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(&k8serrors.StatusError{ErrStatus: metav1.Status{Code: http.StatusNotFound}})

				mc.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{})).Return(nil)

				mc.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{}), gomock.Any()).
					Return(errors.New("something went wrong"))

			},
			expectedErrorMsg: `failed to clone config maps and secrets downstream: failed to copy config map`,
			expectRetries:    true,
		},
		{
			name: "unsupported resource",
			downstreamResources: []fleetv1.DownstreamResource{
				{
					Kind: "SomethingElse",
					Name: "what",
				},
			},
			downstreamResourcesGetCalls: func(mc *mocks.MockK8sClient) {},
			expectedErrorMsg:            `failed to clone config maps and secrets downstream: unsupported kind for object to copy to downstream`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			scheme := runtime.NewScheme()
			utilruntime.Must(batchv1.AddToScheme(scheme))
			utilruntime.Must(fleetv1.AddToScheme(scheme))

			bundle := fleetv1.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-bundle",
					Namespace: "default",
				},
				Spec: fleetv1.BundleSpec{
					RolloutStrategy: nil,
					BundleDeploymentOptions: fleetv1.BundleDeploymentOptions{
						DownstreamResources: c.downstreamResources,
					},
				},
			}

			namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

			mockClient := mocks.NewMockK8sClient(mockCtrl)
			expectGetWithFinalizer(mockClient, bundle)

			// Options secret: deletion attempt in case it exists, as the bundle deployment's values hash is empty
			mockClient.EXPECT().Delete(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
				Return(nil)

			mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
				Return(nil)

			c.downstreamResourcesGetCalls(mockClient)

			if !c.expectRetries {
				statusClient := mocks.NewMockStatusWriter(mockCtrl)
				mockClient.EXPECT().Status().Return(statusClient).Times(1)

				expectStatusPatch(t, statusClient, c.expectedErrorMsg)
			}

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
			rs, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			if c.expectRetries {
				if err != nil {
					t.Errorf("expected nil error, got: %v", err)
				}

				if rs.RequeueAfter == 0 {
					t.Errorf("expected non-zero RequeueAfter in result")
				}
			} else {
				if !errors.Is(err, reconcile.TerminalError(nil)) {
					t.Errorf("expected terminal error, got: %v", err)
				}

				if !strings.Contains(err.Error(), c.expectedErrorMsg) {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestReconcile_AccessSecretsHandlingError(t *testing.T) {
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
			ContentsID:      "foo", // non-empty, to force OCI storage secret cloning
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	expectGetWithFinalizer(mockClient, bundle)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
		Return(nil)

	// Options secret: deletion attempt in case it exists, as the bundle deployment's values hash is empty
	mockClient.EXPECT().Delete(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		Return(nil)

	// OCI contents secret
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		DoAndReturn(
			func(ctx context.Context, req types.NamespacedName, s *corev1.Secret, opts ...interface{}) error {
				s.Data = map[string][]byte{
					"reference": []byte("foo"), // key exists
				}

				return nil
			},
		)

	// get OCI storage secret before cloning
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		Return(errors.New("something went wrong"))

	// No status update expected (errors which may happen while cloning secrets are all retryable, except for
	// framework internals)

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
	rs, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if rs.RequeueAfter == 0 {
		t.Errorf("expected non-zero RequeueAfter in result")
	}
}

func expectStatusPatch(t *testing.T, sClient *mocks.MockStatusWriter, errMsg string) {
	t.Helper()
	sClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).Do(
		func(ctx context.Context, b *fleetv1.Bundle, p client.Patch, opts ...interface{}) {
			cond, found := getBundleReadyCondition(b)
			if !found {
				t.Errorf("expecting Condition %s to be found", fleetv1.BundleConditionReady)
			}
			if !strings.Contains(cond.Message, errMsg) {
				t.Errorf("expecting condition message containing [%s], got [%s]", errMsg, cond.Message)
			}
			if cond.Type != fleetv1.BundleConditionReady {
				t.Errorf("expecting condition type [%s], got [%s]", fleetv1.BundleConditionReady, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)
}

func getBundleReadyCondition(b *fleetv1.Bundle) (genericcondition.GenericCondition, bool) {
	for _, cond := range b.Status.Conditions {
		if cond.Type == fleetv1.BundleConditionReady {
			return cond, true
		}
	}
	return genericcondition.GenericCondition{}, false
}

func TestBundleDeploymentMapFunc(t *testing.T) {
	r := &reconciler.BundleReconciler{ShardID: "test-shard"}
	mapFunc := reconciler.BundleDeploymentMapFunc(r)

	testCases := []struct {
		name     string
		obj      client.Object
		expected []reconcile.Request
	}{
		{
			name: "Matching Shard ID",
			obj: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd-1",
					Namespace: "cluster-ns",
					Labels: map[string]string{
						fleetv1.BundleLabel:          "my-bundle",
						fleetv1.BundleNamespaceLabel: "fleet-ns",
						sharding.ShardingRefLabel:    "test-shard",
					},
				},
			},
			expected: []reconcile.Request{
				{NamespacedName: types.NamespacedName{Namespace: "fleet-ns", Name: "my-bundle"}},
			},
		},
		{
			name: "Non-matching Shard ID",
			obj: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd-2",
					Namespace: "cluster-ns",
					Labels: map[string]string{
						fleetv1.BundleLabel:          "my-bundle",
						fleetv1.BundleNamespaceLabel: "fleet-ns",
						sharding.ShardingRefLabel:    "other-shard",
					},
				},
			},
			expected: nil,
		},
		{
			name: "Default Shard, Object has no shard label",
			obj: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd-3",
					Namespace: "cluster-ns",
					Labels: map[string]string{
						fleetv1.BundleLabel:          "my-bundle",
						fleetv1.BundleNamespaceLabel: "fleet-ns",
					},
				},
			},
			expected: nil, // default shard is "", not "test-shard"
		},
		{
			name: "Missing bundle labels",
			obj: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd-4",
					Namespace: "cluster-ns",
					Labels: map[string]string{
						sharding.ShardingRefLabel: "test-shard",
					},
				},
			},
			expected: nil,
		},
		{
			name: "Nil labels",
			obj: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bd-5",
					Namespace: "cluster-ns",
				},
			},
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reqs := mapFunc(context.Background(), tc.obj)
			if diff := cmp.Diff(tc.expected, reqs); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("Default Shard ID", func(t *testing.T) {
		r := &reconciler.BundleReconciler{ShardID: ""}
		mapFunc := reconciler.BundleDeploymentMapFunc(r)

		bd := &fleetv1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bd-default",
				Namespace: "cluster-ns",
				Labels: map[string]string{
					fleetv1.BundleLabel:          "my-bundle",
					fleetv1.BundleNamespaceLabel: "fleet-ns",
				},
			},
		}

		expected := []reconcile.Request{
			{NamespacedName: types.NamespacedName{Namespace: "fleet-ns", Name: "my-bundle"}},
		}

		reqs := mapFunc(context.Background(), bd)
		if diff := cmp.Diff(expected, reqs); diff != "" {
			t.Errorf("mismatch (-want +got):\n%s", diff)
		}
	})
}

func expectGetWithFinalizer(mockCli *mocks.MockK8sClient, bundle fleetv1.Bundle) {
	mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, b *fleetv1.Bundle, opts ...interface{}) error {
			b.Name = bundle.Name
			b.Namespace = bundle.Namespace
			controllerutil.AddFinalizer(b, finalize.BundleFinalizer)

			b.Spec = bundle.Spec

			return nil
		},
	)
}

func TestReconcile_DownstreamResourcesGeneration_Increment(t *testing.T) {
	envVar := "EXPERIMENTAL_COPY_RESOURCES_DOWNSTREAM"
	bkp := os.Getenv(envVar)
	defer func() {
		os.Setenv(envVar, bkp)
	}()

	os.Setenv(envVar, "true")

	testCases := []struct {
		name                     string
		downstreamResources      []fleetv1.DownstreamResource
		initialGeneration        int64
		existingBundleDeployment *fleetv1.BundleDeployment
		setupResourceMocks       func(*gomock.Controller, *mocks.MockK8sClient)
	}{
		{
			name: "Secret created - generation increments from 0 to 1",
			downstreamResources: []fleetv1.DownstreamResource{
				{Kind: "Secret", Name: "my-secret"},
			},
			initialGeneration: 0,
			existingBundleDeployment: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bd",
					Namespace: "cluster-ns",
					UID:       "test-uid",
				},
				Spec: fleetv1.BundleDeploymentSpec{
					DeploymentID:                  "test-deployment",
					DownstreamResourcesGeneration: 0,
				},
			},
			setupResourceMocks: func(ctrl *gomock.Controller, mc *mocks.MockK8sClient) {
				// Source secret get
				mc.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "default", Name: "my-secret"}, gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, key types.NamespacedName, s *corev1.Secret, opts ...interface{}) error {
						s.Name = "my-secret"
						s.Namespace = "default"
						s.Data = map[string][]byte{"key": []byte("value")}
						return nil
					})

				// Target secret get (not found) + create
				mc.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "cluster-ns", Name: "my-secret"}, gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(&k8serrors.StatusError{ErrStatus: metav1.Status{Code: http.StatusNotFound}})
				mc.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(nil)

				// Patch to increment generation
				mc.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, bd *fleetv1.BundleDeployment, patch client.Patch, opts ...interface{}) error {
						if bd.Spec.DownstreamResourcesGeneration != 1 {
							t.Errorf("Expected generation 1, got %d", bd.Spec.DownstreamResourcesGeneration)
						}
						return nil
					})
			},
		},
		{
			name: "ConfigMap updated - generation increments from 5 to 6",
			downstreamResources: []fleetv1.DownstreamResource{
				{Kind: "ConfigMap", Name: "my-config"},
			},
			initialGeneration: 5,
			existingBundleDeployment: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bd",
					Namespace: "cluster-ns",
					UID:       "test-uid",
				},
				Spec: fleetv1.BundleDeploymentSpec{
					DeploymentID:                  "test-deployment",
					DownstreamResourcesGeneration: 5,
				},
			},
			setupResourceMocks: func(ctrl *gomock.Controller, mc *mocks.MockK8sClient) {
				// Source configmap get
				mc.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "default", Name: "my-config"}, gomock.AssignableToTypeOf(&corev1.ConfigMap{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, key types.NamespacedName, cm *corev1.ConfigMap, opts ...interface{}) error {
						cm.Name = "my-config"
						cm.Namespace = "default"
						cm.Data = map[string]string{"key": "new-value"}
						return nil
					})

				// Target configmap get (exists) + update
				mc.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "cluster-ns", Name: "my-config"}, gomock.AssignableToTypeOf(&corev1.ConfigMap{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, key types.NamespacedName, cm *corev1.ConfigMap, opts ...interface{}) error {
						cm.Name = "my-config"
						cm.Namespace = "cluster-ns"
						cm.Data = map[string]string{"key": "old-value"}
						return nil
					})
				mc.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{}), gomock.Any()).
					Return(nil)

				// Patch to increment generation
				mc.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, bd *fleetv1.BundleDeployment, patch client.Patch, opts ...interface{}) error {
						if bd.Spec.DownstreamResourcesGeneration != 6 {
							t.Errorf("Expected generation 6, got %d", bd.Spec.DownstreamResourcesGeneration)
						}
						return nil
					})
			},
		},
		{
			name: "Both Secret and ConfigMap updated - generation increments once",
			downstreamResources: []fleetv1.DownstreamResource{
				{Kind: "Secret", Name: "my-secret"},
				{Kind: "ConfigMap", Name: "my-config"},
			},
			initialGeneration: 10,
			existingBundleDeployment: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bd",
					Namespace: "cluster-ns",
					UID:       "test-uid",
				},
				Spec: fleetv1.BundleDeploymentSpec{
					DeploymentID:                  "test-deployment",
					DownstreamResourcesGeneration: 10,
				},
			},
			setupResourceMocks: func(ctrl *gomock.Controller, mc *mocks.MockK8sClient) {
				// Source secret get
				mc.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "default", Name: "my-secret"}, gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, key types.NamespacedName, s *corev1.Secret, opts ...interface{}) error {
						s.Name = "my-secret"
						s.Namespace = "default"
						s.Data = map[string][]byte{"key": []byte("value")}
						return nil
					})

				// Target secret get (not found) + create
				mc.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "cluster-ns", Name: "my-secret"}, gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(&k8serrors.StatusError{ErrStatus: metav1.Status{Code: http.StatusNotFound}})
				mc.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					Return(nil)

				// Source configmap get
				mc.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "default", Name: "my-config"}, gomock.AssignableToTypeOf(&corev1.ConfigMap{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, key types.NamespacedName, cm *corev1.ConfigMap, opts ...interface{}) error {
						cm.Name = "my-config"
						cm.Namespace = "default"
						cm.Data = map[string]string{"key": "value"}
						return nil
					})

				// Target configmap get (exists) + update
				mc.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "cluster-ns", Name: "my-config"}, gomock.AssignableToTypeOf(&corev1.ConfigMap{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, key types.NamespacedName, cm *corev1.ConfigMap, opts ...interface{}) error {
						cm.Name = "my-config"
						cm.Namespace = "cluster-ns"
						cm.Data = map[string]string{"key": "old-value"}
						return nil
					})
				mc.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{}), gomock.Any()).
					Return(nil)

				// Patch to increment generation (only once for both resources)
				mc.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, bd *fleetv1.BundleDeployment, patch client.Patch, opts ...interface{}) error {
						if bd.Spec.DownstreamResourcesGeneration != 11 {
							t.Errorf("Expected generation 11, got %d", bd.Spec.DownstreamResourcesGeneration)
						}
						return nil
					})
			},
		},
		{
			name: "Resource unchanged - no generation increment",
			downstreamResources: []fleetv1.DownstreamResource{
				{Kind: "Secret", Name: "my-secret"},
			},
			initialGeneration: 3,
			existingBundleDeployment: &fleetv1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bd",
					Namespace: "cluster-ns",
					UID:       "test-uid",
				},
				Spec: fleetv1.BundleDeploymentSpec{
					DeploymentID:                  "test-deployment",
					DownstreamResourcesGeneration: 3,
				},
			},
			setupResourceMocks: func(ctrl *gomock.Controller, mc *mocks.MockK8sClient) {
				// Source secret get
				mc.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "default", Name: "my-secret"}, gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, key types.NamespacedName, s *corev1.Secret, opts ...interface{}) error {
						s.Name = "my-secret"
						s.Namespace = "default"
						s.Data = map[string][]byte{"key": []byte("value")}
						return nil
					})

				// Target secret get (exists with same data) - no update needed
				mc.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "cluster-ns", Name: "my-secret"}, gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
					DoAndReturn(func(ctx context.Context, key types.NamespacedName, s *corev1.Secret, opts ...interface{}) error {
						s.Name = "my-secret"
						s.Namespace = "cluster-ns"
						s.Data = map[string][]byte{"key": []byte("value")}
						return nil
					})
				// No create/update call expected

				// No patch expected since nothing changed
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			scheme := runtime.NewScheme()
			utilruntime.Must(corev1.AddToScheme(scheme))
			utilruntime.Must(fleetv1.AddToScheme(scheme))

			bundle := fleetv1.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bundle",
					Namespace: "default",
				},
				Spec: fleetv1.BundleSpec{
					BundleDeploymentOptions: fleetv1.BundleDeploymentOptions{
						DownstreamResources: tc.downstreamResources,
					},
				},
			}

			namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

			mockClient := mocks.NewMockK8sClient(mockCtrl)
			expectGetWithFinalizer(mockClient, bundle)

			// Options secret deletion (no values to store)
			mockClient.EXPECT().Delete(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
				Return(&k8serrors.StatusError{ErrStatus: metav1.Status{Code: http.StatusNotFound}})

			// Target builder returns our test bundle deployment
			matchedTargets := []*target.Target{
				{
					Bundle: &bundle,
					Cluster: &fleetv1.Cluster{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "fleet-default",
							Name:      "test-cluster",
						},
						Status: fleetv1.ClusterStatus{
							Namespace: "cluster-ns",
						},
					},
					Deployment:   tc.existingBundleDeployment,
					DeploymentID: "test-deployment",
				},
			}

			targetBuilderMock := mocks.NewMockTargetBuilder(mockCtrl)
			targetBuilderMock.EXPECT().Targets(gomock.Any(), gomock.Any(), gomock.Any()).Return(matchedTargets, nil)

			storeMock := mocks.NewMockStore(mockCtrl)
			storeMock.EXPECT().Store(gomock.Any(), gomock.Any()).Return(nil)

			// List BundleDeployments for cleanup
			mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeploymentList{}), gomock.Any()).
				DoAndReturn(func(ctx context.Context, list *fleetv1.BundleDeploymentList, opts ...interface{}) error {
					list.Items = []fleetv1.BundleDeployment{*tc.existingBundleDeployment}
					return nil
				})

			// BundleDeployment get (for CreateOrUpdate)
			mockClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "cluster-ns", Name: "test-bd"}, gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
				DoAndReturn(func(ctx context.Context, key types.NamespacedName, bd *fleetv1.BundleDeployment, opts ...interface{}) error {
					*bd = *tc.existingBundleDeployment
					return nil
				})

			// BundleDeployment update (for CreateOrUpdate)
			mockClient.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
				Return(nil)

			// Setup resource-specific mocks (secrets/configmaps)
			tc.setupResourceMocks(mockCtrl, mockClient)

			// Status update
			statusClient := mocks.NewMockStatusWriter(mockCtrl)
			mockClient.EXPECT().Status().Return(statusClient).Times(1)
			statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).
				Return(nil)

			recorderMock := mocks.NewMockEventRecorder(mockCtrl)

			r := reconciler.BundleReconciler{
				Client:   mockClient,
				Scheme:   scheme,
				Recorder: recorderMock,
				Builder:  targetBuilderMock,
				Store:    storeMock,
			}

			ctx := context.TODO()
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestReconcile_DownstreamResources_FeatureDisabled(t *testing.T) {
	envVar := "EXPERIMENTAL_COPY_RESOURCES_DOWNSTREAM"
	bkp := os.Getenv(envVar)
	defer func() {
		os.Setenv(envVar, bkp)
	}()

	// Explicitly disable the feature
	os.Setenv(envVar, "false")

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(fleetv1.AddToScheme(scheme))

	bundle := fleetv1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-bundle",
			Namespace: "default",
		},
		Spec: fleetv1.BundleSpec{
			BundleDeploymentOptions: fleetv1.BundleDeploymentOptions{
				DownstreamResources: []fleetv1.DownstreamResource{
					{Kind: "Secret", Name: "my-secret"},
					{Kind: "ConfigMap", Name: "my-config"},
				},
			},
		},
	}

	existingBundleDeployment := &fleetv1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-bd",
			Namespace: "cluster-ns",
			UID:       "test-uid",
		},
		Spec: fleetv1.BundleDeploymentSpec{
			DeploymentID:                  "test-deployment",
			DownstreamResourcesGeneration: 5, // Has a non-zero value
		},
	}

	namespacedName := types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	expectGetWithFinalizer(mockClient, bundle)

	// Options secret deletion
	mockClient.EXPECT().Delete(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}), gomock.Any()).
		Return(&k8serrors.StatusError{ErrStatus: metav1.Status{Code: http.StatusNotFound}})

	matchedTargets := []*target.Target{
		{
			Bundle: &bundle,
			Cluster: &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fleet-default",
					Name:      "test-cluster",
				},
				Status: fleetv1.ClusterStatus{
					Namespace: "cluster-ns",
				},
			},
			Deployment:   existingBundleDeployment,
			DeploymentID: "test-deployment",
		},
	}

	targetBuilderMock := mocks.NewMockTargetBuilder(mockCtrl)
	targetBuilderMock.EXPECT().Targets(gomock.Any(), gomock.Any(), gomock.Any()).Return(matchedTargets, nil)

	storeMock := mocks.NewMockStore(mockCtrl)
	storeMock.EXPECT().Store(gomock.Any(), gomock.Any()).Return(nil)

	// List BundleDeployments for cleanup
	mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeploymentList{}), gomock.Any()).
		DoAndReturn(func(ctx context.Context, list *fleetv1.BundleDeploymentList, opts ...interface{}) error {
			list.Items = []fleetv1.BundleDeployment{*existingBundleDeployment}
			return nil
		})

	// BundleDeployment get (for CreateOrUpdate)
	mockClient.EXPECT().Get(gomock.Any(), types.NamespacedName{Namespace: "cluster-ns", Name: "test-bd"}, gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
		DoAndReturn(func(ctx context.Context, key types.NamespacedName, bd *fleetv1.BundleDeployment, opts ...interface{}) error {
			*bd = *existingBundleDeployment
			return nil
		})

	// BundleDeployment update - when feature is disabled, generation stays unchanged
	// because handleDownstreamObjects modifies BD in memory but doesn't persist it
	mockClient.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.BundleDeployment{}), gomock.Any()).
		DoAndReturn(func(ctx context.Context, obj client.Object, opts ...interface{}) error {
			bd := obj.(*fleetv1.BundleDeployment)
			// Feature is disabled, so resources are not cloned and generation stays as-is (5)
			// The handleDownstreamObjects sets it to 0 in memory but doesn't persist that change
			if bd.Spec.DownstreamResourcesGeneration != 5 {
				t.Errorf("Expected generation to remain unchanged at 5 when feature is disabled, got %d", bd.Spec.DownstreamResourcesGeneration)
			}
			return nil
		})

	// No resource cloning calls expected since feature is disabled

	// Status update
	statusClient := mocks.NewMockStatusWriter(mockCtrl)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.Bundle{}), gomock.Any()).
		Return(nil)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	r := reconciler.BundleReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Recorder: recorderMock,
		Builder:  targetBuilderMock,
		Store:    storeMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
