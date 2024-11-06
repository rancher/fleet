package reconciler

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/internal/bundlereader"
	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// HelmAppReconciler reconciles a HelmApp resource to create and apply bundles for helm charts
type HelmAppReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Scheduler quartz.Scheduler
	Workers   int
	ShardID   string
	Recorder  record.EventRecorder
}

func (r *HelmAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.HelmApp{},
			builder.WithPredicates(
				predicate.Or(
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
				),
			),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// The Reconcile function compares the state specified by
// the GitRepo object against the actual cluster state, and then
// performs operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *HelmAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !experimentalHelmOpsEnabled() {
		return ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx).WithName("HelmApp")
	helmapp := &fleet.HelmApp{}

	if err := r.Get(ctx, req.NamespacedName, helmapp); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	} else if errors.IsNotFound(err) {
		return ctrl.Result{}, nil
	}

	// Finalizer handling
	purgeBundlesFn := func() error {
		nsName := types.NamespacedName{Name: helmapp.Name, Namespace: helmapp.Namespace}
		if err := finalize.PurgeBundles(ctx, r.Client, nsName, fleet.HelmAppLabel); err != nil {
			return err
		}
		return nil
	}

	if !helmapp.GetDeletionTimestamp().IsZero() {
		err := purgeBundlesFn()
		if err != nil {
			return ctrl.Result{}, err
		}
		if controllerutil.ContainsFinalizer(helmapp, finalize.HelmAppFinalizer) {
			if err := deleteFinalizer(ctx, r.Client, helmapp, finalize.HelmAppFinalizer); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(helmapp, finalize.HelmAppFinalizer) {
		if err := addFinalizer(ctx, r.Client, helmapp, finalize.HelmAppFinalizer); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	}

	// Reconciling
	logger = logger.WithValues("generation", helmapp.Generation, "chart", helmapp.Spec.Helm.Chart)
	ctx = log.IntoContext(ctx, logger)

	logger.V(1).Info("Reconciling HelmApp")

	if helmapp.Spec.Helm.Chart == "" {
		return ctrl.Result{}, nil
	}

	bundle, err := r.createUpdateBundle(ctx, &logger, helmapp)
	if err != nil {
		return ctrl.Result{}, updateErrorStatusHelm(ctx, r.Client, req.NamespacedName, helmapp.Status, err)
	}

	helmapp.Status.Version = bundle.Spec.Helm.Version

	err = updateStatusHelm(ctx, r.Client, req.NamespacedName, helmapp.Status)
	if err != nil {
		logger.Error(err, "Reconcile failed final update to helm app status", "status", helmapp.Status)

		return ctrl.Result{Requeue: true}, err
	}

	return ctrl.Result{}, err
}

// Calculates the bundle representation of the given HelmApp resource
func (r *HelmAppReconciler) calculateBundle(helmapp *fleet.HelmApp) *fleet.Bundle {
	spec := helmapp.Spec.BundleSpecBase
	// update targets with target customizations
	spec.Targets = append(spec.Targets, helmapp.Spec.TargetCustomizations...)

	// set target names
	for i, target := range spec.Targets {
		if target.Name == "" {
			spec.Targets[i].Name = fmt.Sprintf("target%03d", i)
		}
	}

	bundleSpec := fleet.BundleSpec{
		BundleSpecBase: spec,
	}

	propagateHelmAppProperties(&spec)

	bundle := &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: helmapp.Namespace,
			Name:      helmapp.Name,
		},
		Spec: bundleSpec,
	}
	if len(bundle.Spec.Targets) == 0 {
		bundle.Spec.Targets = []fleet.BundleTarget{
			{
				Name:         "default",
				ClusterGroup: "default",
			},
		}
	}

	// apply additional labels from spec
	for k, v := range helmapp.Spec.Labels {
		if bundle.Labels == nil {
			bundle.Labels = make(map[string]string)
		}
		bundle.Labels[k] = v
	}
	bundle.Labels = labels.Merge(bundle.Labels, map[string]string{
		fleet.HelmAppLabel: helmapp.Name,
	})

	// Setting the Resources to nil, the agent will download the helm chart
	bundle.Spec.Resources = nil
	// store the helm options (this will also enable the helm chart deployment in the bundle)
	bundle.Spec.HelmAppOptions = &fleet.BundleHelmOptions{
		SecretName:            helmapp.Spec.HelmSecretName,
		InsecureSkipTLSverify: helmapp.Spec.InsecureSkipTLSverify,
	}

	return bundle
}

func (r *HelmAppReconciler) createUpdateBundle(ctx context.Context, logger *logr.Logger, helmapp *fleet.HelmApp) (*fleet.Bundle, error) {
	b := &fleet.Bundle{}
	nsName := types.NamespacedName{
		Name:      helmapp.Name,
		Namespace: helmapp.Namespace,
	}
	// calculate the new representation of the helmapp resource
	bundle := r.calculateBundle(helmapp)

	err := r.Get(ctx, nsName, b)
	if err != nil && !errors.IsNotFound(err) {
		return nil, err
	}
	if err := r.handleVersion(ctx, b, bundle, helmapp); err != nil {
		return nil, err
	}

	if errors.IsNotFound(err) {
		if err := r.Create(ctx, bundle); err != nil && !errors.IsAlreadyExists(err) {
			return nil, err
		}
		logger.V(1).Info(fmt.Sprintf("Bundle %s/%s created", bundle.Namespace, bundle.Name))
	} else if err != nil {
		return nil, err
	} else {
		b.Spec = bundle.Spec
		b.Annotations = bundle.Annotations
		b.Labels = bundle.Labels

		if err := r.Update(ctx, b); err != nil {
			return nil, err
		}
		logger.V(1).Info(fmt.Sprintf("Bundle %s/%s updated", bundle.Namespace, bundle.Name))
	}

	return bundle, nil
}

// propagateHelmAppProperties propagates root Helm chart properties to the child targets.
// This is necessary, so we can download the correct chart version for each target.
func propagateHelmAppProperties(spec *fleet.BundleSpecBase) {
	// Check if there is anything to propagate
	if spec.Helm == nil {
		return
	}
	for _, target := range spec.Targets {
		if target.Helm == nil {
			// This target has nothing to propagate to
			continue
		}
		if target.Helm.Repo == "" {
			target.Helm.Repo = spec.Helm.Repo
		}
		if target.Helm.Chart == "" {
			target.Helm.Chart = spec.Helm.Chart
		}
		if target.Helm.Version == "" {
			target.Helm.Version = spec.Helm.Version
		}
	}
}

func addFinalizer[T client.Object](ctx context.Context, c client.Client, obj T, finalizer string) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		nsName := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
		if err := c.Get(ctx, nsName, obj); err != nil {
			return err
		}

		controllerutil.AddFinalizer(obj, finalizer)

		return c.Update(ctx, obj)
	})

	if err != nil {
		return client.IgnoreNotFound(err)
	}

	return nil
}

func deleteFinalizer[T client.Object](ctx context.Context, c client.Client, obj T, finalizer string) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		nsName := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
		if err := c.Get(ctx, nsName, obj); err != nil {
			return err
		}

		controllerutil.RemoveFinalizer(obj, finalizer)

		return c.Update(ctx, obj)
	})
	if client.IgnoreNotFound(err) != nil {
		return err
	}
	return nil
}

func (r *HelmAppReconciler) handleVersion(ctx context.Context, oldBundle *fleet.Bundle, bundle *fleet.Bundle, helmapp *fleet.HelmApp) error {
	if helmapp.Spec.Helm.Version == "" || helmapp.Spec.Helm.Version == "*" {
		if helmChartSpecChanged(oldBundle.Spec.Helm, bundle.Spec.Helm, helmapp.Status.Version) {
			auth := bundlereader.Auth{}
			if helmapp.Spec.HelmSecretName != "" {
				req := types.NamespacedName{Namespace: helmapp.Namespace, Name: helmapp.Spec.HelmSecretName}
				var err error
				auth, err = bundlereader.ReadHelmAuthFromSecret(ctx, r.Client, req)
				if err != nil {
					return err
				}
			}
			auth.InsecureSkipVerify = helmapp.Spec.InsecureSkipTLSverify

			_, version, err := bundlereader.ChartURLVersion(*bundle.Spec.Helm, auth)
			if err != nil {
				return err
			}
			bundle.Spec.Helm.Version = version
		} else {
			bundle.Spec.Helm.Version = helmapp.Status.Version
		}
	} else {
		bundle.Spec.Helm.Version = helmapp.Spec.Helm.Version
	}

	return nil
}

// updateStatusHelm updates the status for the HelmApp resource. It retries on
// conflict. If the status was updated successfully, it also collects (as in
// updates) metrics for the resource GitRepo resource.
func updateStatusHelm(ctx context.Context, c client.Client, req types.NamespacedName, status fleet.HelmAppStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.HelmApp{}
		err := c.Get(ctx, req, t)
		if err != nil {
			return err
		}

		// selectively update the status fields this reconciler is responsible for
		t.Status.Version = status.Version

		// only keep the Ready condition from live status, it's calculated by the status reconciler
		conds := []genericcondition.GenericCondition{}
		for _, c := range t.Status.Conditions {
			if c.Type == "Ready" {
				conds = append(conds, c)
				break
			}
		}
		for _, c := range status.Conditions {
			if c.Type == "Ready" {
				continue
			}
			conds = append(conds, c)
		}
		t.Status.Conditions = conds

		err = c.Status().Update(ctx, t)
		if err != nil {
			return err
		}

		return nil
	})
}

// updateErrorStatusHelm sets the condition in the status and tries to update the resource
func updateErrorStatusHelm(ctx context.Context, c client.Client, req types.NamespacedName, status fleet.HelmAppStatus, orgErr error) error {
	setAcceptedConditionHelm(&status, orgErr)
	if statusErr := updateStatusHelm(ctx, c, req, status); statusErr != nil {
		merr := []error{orgErr, fmt.Errorf("failed to update the status: %w", statusErr)}
		return errutil.NewAggregate(merr)
	}
	return orgErr
}

// setAcceptedCondition sets the condition and updates the timestamp, if the condition changed
func setAcceptedConditionHelm(status *fleet.HelmAppStatus, err error) {
	cond := condition.Cond(fleet.HelmAppAcceptedCondition)
	origStatus := status.DeepCopy()
	cond.SetError(status, "", fleetutil.IgnoreConflict(err))
	if !equality.Semantic.DeepEqual(origStatus, status) {
		cond.LastUpdated(status, time.Now().UTC().Format(time.RFC3339))
	}
}

func helmChartSpecChanged(o *fleet.HelmOptions, n *fleet.HelmOptions, statusVersion string) bool {
	if o == nil {
		// still not set
		return true
	}
	if o.Repo != n.Repo {
		// check that the difference is not the / at the end
		if o.Repo != fmt.Sprintf("%s/", n.Repo) {
			return true
		}
	}
	if o.Chart != n.Chart {
		return true
	}
	if o.Version != n.Version && statusVersion != o.Version {
		return true
	}
	return false
}

// experimentalHelmOpsEnabled returns true if the EXPERIMENTAL_HELM_OPS env variable is set to true
// returns false otherwise
func experimentalHelmOpsEnabled() bool {
	value, err := strconv.ParseBool(os.Getenv("EXPERIMENTAL_HELM_OPS"))
	return err == nil && value
}
