//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/client_mock.go -package=mocks sigs.k8s.io/controller-runtime/pkg/client Client,SubResourceWriter
//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/scheduler_mock.go -package=mocks github.com/reugn/go-quartz/quartz Scheduler,ScheduledJob

package reconciler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"github.com/reugn/go-quartz/quartz"

	batchv1 "k8s.io/api/batch/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	helmRepoIndex = `apiVersion: v1
entries:
  alpine:
    - created: 2016-10-06T16:23:20.499814565-06:00
      description: Deploy a basic Alpine Linux pod
      digest: 99c76e403d752c84ead610644d4b1c2f2b453a74b921f422b9dcb8a7c8b559cd
      home: https://helm.sh/helm
      name: alpine
      sources:
      - https://github.com/helm/helm
      urls:
      - https://technosophos.github.io/tscharts/alpine-0.2.0.tgz
      version: 0.2.0
    - created: 2016-10-06T16:23:20.499543808-06:00
      description: Deploy a basic Alpine Linux pod
      digest: 515c58e5f79d8b2913a10cb400ebb6fa9c77fe813287afbacf1a0b897cd78727
      home: https://helm.sh/helm
      name: alpine
      sources:
      - https://github.com/helm/helm
      urls:
      - https://technosophos.github.io/tscharts/alpine-0.1.0.tgz
      version: 0.1.0
  nginx:
    - created: 2016-10-06T16:23:20.499543808-06:00
      description: Create a basic nginx HTTP server
      digest: aaff4545f79d8b2913a10cb400ebb6fa9c77fe813287afbacf1a0b897cdffffff
      home: https://helm.sh/helm
      name: nginx
      sources:
      - https://github.com/helm/charts
      urls:
      - https://technosophos.github.io/tscharts/nginx-0.1.0.tgz
      version: 0.1.0
generated: 2016-10-06T16:23:20.499029981-06:00`
)

func createHelmServer() *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, helmRepoIndex)
	}))
}

func getCondition(helmop *fleet.HelmOp, condType string) (genericcondition.GenericCondition, bool) {
	for _, cond := range helmop.Status.Conditions {
		if cond.Type == condType {
			return cond, true
		}
	}
	return genericcondition.GenericCondition{}, false
}

func TestReconcile_ReturnsAndRequeuesAfterAddingFinalizer(t *testing.T) {
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

func TestReconcile_Validate(t *testing.T) {
	cases := []struct {
		name   string
		helmop fleet.HelmOp
		err    string
	}{
		{
			name: "error if Helm spec is empty",
			helmop: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{},
			},
			err: "helm options are empty",
		},
		{
			name: "error if tarball chart with non-empty repo",
			helmop: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Chart: "https://foo.bar/baz.tgz",
								Repo:  "non-empty",
							},
						},
					},
				},
			},
			err: "tarball chart with a non-empty repo field",
		},
		{
			name: "error if tarball chart with non-empty nor wildcard version",
			helmop: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Chart:   "https://foo.bar/baz.tgz",
								Version: "1.2.3",
							},
						},
					},
				},
			},
			err: "tarball chart with a non-empty version field",
		},
		{
			name: "error if tarball chart with 'latest' version",
			helmop: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Chart:   "https://foo.bar/baz.tgz",
								Version: "latest",
							},
						},
					},
				},
			},
			err: "tarball chart with a non-empty version field",
		},
		{
			name: "error if tarball chart with wildcard version",
			helmop: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Chart:   "https://foo.bar/baz.tgz",
								Version: "*",
							},
						},
					},
				},
			},
			err: "tarball chart with a non-empty version field",
		},
		{
			name: "error if OCI repo with non-empty chart",
			helmop: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Chart:   "hello",
								Repo:    "oci://foo/bar/baz",
								Version: "*",
							},
						},
					},
				},
			},
			err: "OCI repository with a non-empty chart field",
		},
		{
			name: "error if non-OCI repo with empty chart",
			helmop: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo: "https://foo/bar/baz",
							},
						},
					},
				},
			},
			err: "non-OCI repository with an empty chart field",
		},
		{
			name: "error if non-tarball chart with empty repo",
			helmop: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Chart: "foo",
							},
						},
					},
				},
			},
			err: "non-tarball chart with an empty repo field",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			scheme := runtime.NewScheme()
			utilruntime.Must(batchv1.AddToScheme(scheme))

			namespacedName := types.NamespacedName{Name: c.helmop.Name, Namespace: c.helmop.Namespace}
			client := mocks.NewMockClient(mockCtrl)
			client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
				func(ctx context.Context, req types.NamespacedName, fh *fleet.HelmOp, opts ...interface{}) error {
					fh.Name = c.helmop.Name
					fh.Namespace = c.helmop.Namespace
					fh.Spec = c.helmop.Spec

					controllerutil.AddFinalizer(fh, finalize.HelmOpFinalizer)
					return nil
				},
			)

			client.EXPECT().Get(gomock.Any(), namespacedName, matchesBundle(c.helmop.Name, c.helmop.Namespace), gomock.Any()).
				DoAndReturn(
					func(ctx context.Context, req types.NamespacedName, bundle *fleet.Bundle, opts ...interface{}) error {
						bundle.ObjectMeta = metav1.ObjectMeta{
							Name:      c.helmop.Name,
							Namespace: c.helmop.Namespace,
						}
						bundle.Spec.HelmOpOptions = &fleet.BundleHelmOptions{
							SecretName: "foo", //prevent collision errors; the value does not matter.
						}

						return nil
					},
				).AnyTimes()

			// expected from addFinalizer
			client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
			client.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes().Return(nil)

			statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
			client.EXPECT().Status().Return(statusClient)
			statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any())

			scheduler := mocks.NewMockScheduler(mockCtrl)

			if len(c.err) > 0 {
				job := mocks.NewMockScheduledJob(mockCtrl)
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(job, nil)
				scheduler.EXPECT().DeleteJob(gomock.Any()).Return(nil)
			}

			r := HelmOpReconciler{
				Client:    client,
				Scheme:    scheme,
				Scheduler: scheduler,
			}

			ctx := context.TODO()

			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("unexpected error:\r\n\twanted\r\n\t\t%q in error message,\r\n\tgot \r\n\t\t%v", c.err, err)
			}

		})
	}
}

func TestReconcile_ErrorCreatingBundleIsShownInStatus(t *testing.T) {
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
				Chart: "chart.tgz",
			}
			fh.Status = fleet.HelmOpStatus{}

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
	statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, helmop *fleet.HelmOp, patch crclient.Patch, opts ...interface{}) {
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
				Chart: "chart.tgz",
			}
			fh.Status = fleet.HelmOpStatus{}

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
	statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, helmop *fleet.HelmOp, patch crclient.Patch, opts ...interface{}) {
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
	svr1 := createHelmServer()
	defer svr1.Close()

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
			fh.Spec.InsecureSkipTLSverify = true
			fh.Spec.Helm = &fleet.HelmOptions{
				Repo:    svr1.URL,
				Chart:   "alpine",
				Version: "0.x.x",
			}
			fh.Status = fleet.HelmOpStatus{}

			controllerutil.AddFinalizer(fh, finalize.HelmOpFinalizer)
			return nil
		},
	)

	client.EXPECT().Get(gomock.Any(), namespacedName, gomock.Any(), gomock.Any()).Times(2).
		DoAndReturn(
			func(ctx context.Context, req types.NamespacedName, bundle *fleet.Bundle, opts ...interface{}) error {
				return k8serrors.NewNotFound(schema.GroupResource{}, "Not found")
			},
		)

	client.EXPECT().Create(gomock.Any(), matchesBundle(helmop.Name, helmop.Namespace), gomock.Any()).
		DoAndReturn(
			func(ctx context.Context, bundle *fleet.Bundle, opts ...interface{}) error {
				return nil
			},
		)

	client.EXPECT().Get(gomock.Any(), gomock.Any(), &fleet.HelmOp{}, gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, _ *fleet.HelmOp, opts ...interface{}) error {
			return nil
		},
	)

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	client.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, helmop *fleet.HelmOp, patch crclient.Patch, opts ...interface{}) {
			// version in status should be the one in the spec
			if helmop.Status.Version != "0.2.0" {
				t.Errorf("expecting Status.Version == 0.2.0, got %q", helmop.Status.Version)
			}
		},
	).Times(1)

	// Note: this test case does not try to fetch the actual version from the Helm registry, because the repo field
	// is empty.

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

func TestReconcile_ManagePollingJobs(t *testing.T) {
	svr1 := createHelmServer()
	defer svr1.Close()

	svr2 := createHelmServer()
	defer svr2.Close()

	cases := []struct {
		name                   string
		helmOp                 fleet.HelmOp
		expectedSchedulerCalls func(*gomock.Controller, *mocks.MockScheduler, fleet.HelmOp)
		expectedError          string
	}{
		{
			name: "does not poll if the version is static",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "chart",
								Version: "1.1.2", // static version
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(_ *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(nil, quartz.ErrJobNotFound)

				// No job expected to be created nor deleted
			},
		},
		{
			name: "deletes existing polling job when the version is static",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "chart",
								Version: "1.1.2", // static version
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				job := mocks.NewMockScheduledJob(ctrl)
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(job, nil)
				scheduler.EXPECT().DeleteJob(gomock.Any()).Return(nil)
			},
		},
		{
			name: "does not poll if the polling interval is not set",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					InsecureSkipTLSverify: true,
					// polling interval not set
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(_ *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(nil, quartz.ErrJobNotFound)

				// No job expected to be created nor deleted
			},
		},
		{
			name: "deletes existing polling job when the polling interval is not set",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					InsecureSkipTLSverify: true,
					// polling interval not set
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				job := mocks.NewMockScheduledJob(ctrl)
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(job, nil)
				scheduler.EXPECT().DeleteJob(gomock.Any()).Return(nil)
			},
		},
		{
			name: "does not poll if the polling interval is set to 0",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					InsecureSkipTLSverify: true,
					PollingInterval:       &metav1.Duration{Duration: 0 * time.Minute},
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(_ *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(nil, quartz.ErrJobNotFound)

				// No job expected to be created nor deleted
			},
		},
		{
			name: "deletes existing polling job when the polling interval is set to 0",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					InsecureSkipTLSverify: true,
					PollingInterval:       &metav1.Duration{Duration: 0 * time.Minute},
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				job := mocks.NewMockScheduledJob(ctrl)
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(job, nil)
				scheduler.EXPECT().DeleteJob(gomock.Any()).Return(nil)
			},
		},
		{
			name: "does not poll when referencing an OCI chart",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					InsecureSkipTLSverify: true,
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    "oci://chart",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(_ *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(nil, quartz.ErrJobNotFound)

				// No job expected to be created nor deleted
			},
		},
		{
			name: "deletes existing polling job when referencing an OCI chart",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					InsecureSkipTLSverify: true,
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    "oci://chart",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				job := mocks.NewMockScheduledJob(ctrl)
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(job, nil)
				scheduler.EXPECT().DeleteJob(gomock.Any()).Return(nil)
			},
		},
		{
			name: "does not poll when referencing a tarball chart",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					InsecureSkipTLSverify: true,
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Chart: fmt.Sprintf("%s/alpine/alpine-0.1.0.tgz", svr1.URL),
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(_ *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(nil, quartz.ErrJobNotFound)

				// No job expected to be created nor deleted
			},
		},
		{
			name: "deletes existing polling job when referencing a tarball chart",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					InsecureSkipTLSverify: true,
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Chart: fmt.Sprintf("%s/alpine/alpine-0.1.0.tgz", svr1.URL),
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				job := mocks.NewMockScheduledJob(ctrl)
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(job, nil)
				scheduler.EXPECT().DeleteJob(gomock.Any()).Return(nil)
			},
		},
		{
			name: "returns an error when failing to delete a polling job",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					InsecureSkipTLSverify: true,
					PollingInterval:       &metav1.Duration{Duration: 0 * time.Minute},
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				job := mocks.NewMockScheduledJob(ctrl)
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(job, nil)
				scheduler.EXPECT().DeleteJob(gomock.Any()).Return(errors.New("something happened!"))
			},
			expectedError: "something happened!",
		},
		{
			name: "returns an error when failing to schedule a new job replacing an existing one",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
					UID:       "foo",
				},
				Spec: fleet.HelmOpSpec{
					InsecureSkipTLSverify: true,
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				trigger := quartz.NewSimpleTrigger(2 * helmop.Spec.PollingInterval.Duration)

				job := mocks.NewMockScheduledJob(ctrl)
				job.EXPECT().Trigger().Return(trigger)
				job.EXPECT().JobDetail().Return(nil)

				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(job, nil)
				scheduler.EXPECT().ScheduleJob(matchesJobDetailReplace(true, helmop), gomock.Any()).Return(errors.New("something happened!"))
			},
			expectedError: "something happened!",
		},
		{
			name: "returns an error when failing to schedule a new job with no existing one",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
					UID:       "foo",
				},
				Spec: fleet.HelmOpSpec{
					InsecureSkipTLSverify: true,
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(nil, quartz.ErrJobNotFound)
				scheduler.EXPECT().ScheduleJob(matchesJobDetailReplace(true, helmop), gomock.Any()).Return(errors.New("something happened!"))
			},
			expectedError: "something happened!",
		},
		{
			name: "creates a polling job if all conditions are met",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
					UID:       "foo",
				},
				Spec: fleet.HelmOpSpec{
					InsecureSkipTLSverify: true,
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(_ *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(nil, quartz.ErrJobNotFound)
				scheduler.EXPECT().ScheduleJob(matchesJobDetailReplace(true, helmop), gomock.Any()).Return(nil)
			},
		},
		{
			name: "does not create a polling job if the same one already exists",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
				},
				Spec: fleet.HelmOpSpec{
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					InsecureSkipTLSverify: true,
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.x.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				trigger := newHelmOpTrigger(helmop.Spec.PollingInterval.Duration)
				job := newHelmPollingJob(nil, nil, helmop.Namespace, helmop.Name, *helmop.Spec.Helm)

				detail := quartz.NewJobDetail(job, nil)

				scheduled := mocks.NewMockScheduledJob(ctrl)
				scheduled.EXPECT().Trigger().Return(trigger)
				scheduled.EXPECT().JobDetail().Return(detail)

				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(scheduled, nil)
			},
		},
		{
			name: "creates a polling job if the version constraint has changed",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
					UID:       "foo",
				},
				Spec: fleet.HelmOpSpec{
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					InsecureSkipTLSverify: true,
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.2.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				oldHelmSpec := helmop.Spec.Helm.DeepCopy()
				oldHelmSpec.Version = "0.1.x"

				trigger := newHelmOpTrigger(helmop.Spec.PollingInterval.Duration)
				job := newHelmPollingJob(nil, nil, helmop.Namespace, helmop.Name, *oldHelmSpec)

				detail := quartz.NewJobDetail(job, nil)

				scheduled := mocks.NewMockScheduledJob(ctrl)
				scheduled.EXPECT().Trigger().Return(trigger)
				scheduled.EXPECT().JobDetail().Return(detail)

				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(scheduled, nil)
				scheduler.EXPECT().ScheduleJob(matchesJobDetailReplace(true, helmop), gomock.Any()).Return(nil)
			},
		},
		{
			name: "creates a polling job if the Helm repo has changed",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
					UID:       "foo",
				},
				Spec: fleet.HelmOpSpec{
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					InsecureSkipTLSverify: true,
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.2.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				oldHelmSpec := helmop.Spec.Helm.DeepCopy()
				oldHelmSpec.Repo = svr2.URL

				trigger := newHelmOpTrigger(helmop.Spec.PollingInterval.Duration)
				job := newHelmPollingJob(nil, nil, helmop.Namespace, helmop.Name, *oldHelmSpec)

				detail := quartz.NewJobDetail(job, nil)

				scheduled := mocks.NewMockScheduledJob(ctrl)
				scheduled.EXPECT().Trigger().Return(trigger)
				scheduled.EXPECT().JobDetail().Return(detail)

				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(scheduled, nil)
				scheduler.EXPECT().ScheduleJob(matchesJobDetailReplace(true, helmop), gomock.Any()).Return(nil)
			},
		},
		{
			name: "creates a polling job if the Helm chart has changed",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
					UID:       "foo",
				},
				Spec: fleet.HelmOpSpec{
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					InsecureSkipTLSverify: true,
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "nginx",
								Version: "0.1.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				oldHelmSpec := helmop.Spec.Helm.DeepCopy()
				oldHelmSpec.Chart = "alpine"

				trigger := newHelmOpTrigger(helmop.Spec.PollingInterval.Duration)
				job := newHelmPollingJob(nil, nil, helmop.Namespace, helmop.Name, *oldHelmSpec)

				detail := quartz.NewJobDetail(job, nil)

				scheduled := mocks.NewMockScheduledJob(ctrl)
				scheduled.EXPECT().Trigger().Return(trigger)
				scheduled.EXPECT().JobDetail().Return(detail)

				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(scheduled, nil)
				scheduler.EXPECT().ScheduleJob(matchesJobDetailReplace(true, helmop), gomock.Any()).Return(nil)
			},
		},
		{
			name: "creates a polling job if the polling interval has changed",
			helmOp: fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "helmop",
					Namespace: "default",
					UID:       "foo",
				},
				Spec: fleet.HelmOpSpec{
					PollingInterval:       &metav1.Duration{Duration: 1 * time.Minute},
					InsecureSkipTLSverify: true,
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Repo:    svr1.URL,
								Chart:   "alpine",
								Version: "0.1.x",
							},
						},
					},
				},
			},
			expectedSchedulerCalls: func(ctrl *gomock.Controller, scheduler *mocks.MockScheduler, helmop fleet.HelmOp) {
				trigger := newHelmOpTrigger(2 * helmop.Spec.PollingInterval.Duration)
				job := newHelmPollingJob(nil, nil, helmop.Namespace, helmop.Name, *helmop.Spec.Helm)

				detail := quartz.NewJobDetail(job, nil)

				scheduled := mocks.NewMockScheduledJob(ctrl)
				scheduled.EXPECT().Trigger().Return(trigger)
				scheduled.EXPECT().JobDetail().Return(detail)

				scheduler.EXPECT().GetScheduledJob(gomock.Any()).Return(scheduled, nil)
				scheduler.EXPECT().ScheduleJob(matchesJobDetailReplace(true, helmop), gomock.Any()).Return(nil)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			namespacedName := types.NamespacedName{Name: c.helmOp.Name, Namespace: c.helmOp.Namespace}
			client := mocks.NewMockClient(mockCtrl)
			scheme := runtime.NewScheme()
			scheduler := mocks.NewMockScheduler(mockCtrl)

			r := HelmOpReconciler{
				Client:    client,
				Scheme:    scheme,
				Scheduler: scheduler,
			}

			ctx := context.TODO()

			// Initial reconcile get
			client.EXPECT().Get(gomock.Any(), gomock.Any(), &fleet.HelmOp{}, gomock.Any()).DoAndReturn(
				func(ctx context.Context, req types.NamespacedName, fh *fleet.HelmOp, opts ...interface{}) error {
					fh.Name = c.helmOp.Name
					fh.Namespace = c.helmOp.Namespace
					fh.UID = c.helmOp.UID
					fh.Spec = c.helmOp.Spec
					controllerutil.AddFinalizer(fh, finalize.HelmOpFinalizer)
					return nil
				}).AnyTimes()

			// Check to create or update the bundle
			client.EXPECT().Get(gomock.Any(), namespacedName, matchesBundle(c.helmOp.Name, c.helmOp.Namespace), gomock.Any()).DoAndReturn(
				func(ctx context.Context, req types.NamespacedName, b *fleet.Bundle, opts ...interface{}) error {
					b.Spec.HelmOpOptions = &fleet.BundleHelmOptions{
						SecretName: "foo", //prevent collision errors; the value does not matter.
					}
					return nil
				}).AnyTimes()

			client.EXPECT().Get(gomock.Any(), namespacedName, &fleet.Bundle{}, gomock.Any()).DoAndReturn(
				func(ctx context.Context, req types.NamespacedName, b *fleet.Bundle, opts ...interface{}) error {
					b.Spec.HelmOpOptions = &fleet.BundleHelmOptions{
						SecretName: "foo", //prevent collision errors; the value does not matter.
					}
					return nil
				}).AnyTimes()

			// Only expected in happy cases. If errors happen, only status updates are expected.
			client.EXPECT().Update(gomock.Any(), matchesBundle(c.helmOp.Name, c.helmOp.Namespace), gomock.Any()).Return(nil).AnyTimes()

			statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
			statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

			client.EXPECT().Status().Return(statusClient).Times(1)

			c.expectedSchedulerCalls(mockCtrl, scheduler, c.helmOp)

			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
			if (c.expectedError == "" && err != nil) ||
				(c.expectedError != "" && (err == nil || !strings.Contains(err.Error(), c.expectedError))) {
				t.Errorf("error mismatch: want %v, got %v", c.expectedError, err)
			}

		})
	}
}

func TestHelmOpTrigger(t *testing.T) {
	tr := newHelmOpTrigger(1 * time.Second)

	if tr.isInitRunDone {
		t.Errorf("unexpected %t value for isInitRunDone, expected %t", true, false)
	}

	now := time.Now().UnixNano()
	ft, err := tr.NextFireTime(now)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !tr.isInitRunDone {
		t.Errorf("unexpected %t value for isInitRunDone, expected %t", false, true)
	}

	if ft != now {
		t.Errorf("unexpected fire time value for trigger, expected %d, got %d", now, ft)
	}
}

type bundleMatcher struct {
	name      string
	namespace string
}

func matchesBundle(name, namespace string) gomock.Matcher {
	return &bundleMatcher{name: name, namespace: namespace}
}

func (b *bundleMatcher) Matches(x interface{}) bool {
	ho, ok := x.(*fleet.Bundle)
	if !ok {
		return false
	}

	return ho.Name == b.name && ho.Namespace == b.namespace
}

func (b *bundleMatcher) String() string {
	return fmt.Sprintf("matches namespace %q and name %q", b.namespace, b.name)
}

type scheduledJobMatcher struct {
	replaceExisting bool
	key             *quartz.JobKey
}

func matchesJobDetailReplace(replace bool, helmop fleet.HelmOp) gomock.Matcher {
	return &scheduledJobMatcher{replaceExisting: replace, key: quartz.NewJobKey(string(helmop.UID))}
}

func (s *scheduledJobMatcher) Matches(x interface{}) bool {
	jd, ok := x.(*quartz.JobDetail)
	if !ok {
		return false
	}

	if jd.JobKey() == nil {
		return false
	}

	if jd.Options() == nil {
		return false
	}

	return jd.JobKey().Equals(s.key) && jd.Options().Replace == s.replaceExisting
}

func (s *scheduledJobMatcher) String() string {
	return fmt.Sprintf("matches replace %t and job key %s", s.replaceExisting, s.key)
}
