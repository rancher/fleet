package reconciler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

	"github.com/Masterminds/semver/v3"
	"github.com/go-logr/logr"
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/internal/bundlereader"
	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
)

// HelmOpReconciler reconciles a HelmOp resource to create and apply bundles for helm charts
type HelmOpReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Scheduler quartz.Scheduler
	Workers   int
	ShardID   string
	Recorder  record.EventRecorder
}

func (r *HelmOpReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.HelmOp{},
			builder.WithPredicates(
				predicate.Or(
					// Note: These predicates prevent cache
					// syncPeriod from triggering reconcile, since
					// cache sync is an Update event.
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
// the HelmOp object against the actual cluster state, and then
// performs operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *HelmOpReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("HelmOp")
	helmop := &fleet.HelmOp{}

	if err := r.Get(ctx, req.NamespacedName, helmop); err != nil && !k8serrors.IsNotFound(err) {
		return ctrl.Result{}, err
	} else if k8serrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	}

	// Finalizer handling
	purgeBundlesFn := func() error {
		nsName := types.NamespacedName{Name: helmop.Name, Namespace: helmop.Namespace}
		if err := finalize.PurgeBundles(ctx, r.Client, nsName, fleet.HelmOpLabel); err != nil {
			return err
		}
		return nil
	}

	if !helmop.GetDeletionTimestamp().IsZero() {
		metrics.HelmCollector.Delete(helmop.Name, helmop.Namespace)

		if err := purgeBundlesFn(); err != nil {
			return ctrl.Result{}, err
		}
		if controllerutil.ContainsFinalizer(helmop, finalize.HelmOpFinalizer) {
			if err := deleteFinalizer(ctx, r.Client, helmop, finalize.HelmOpFinalizer); err != nil {
				return ctrl.Result{}, err
			}
		}

		err := r.Scheduler.DeleteJob(jobKey(*helmop))
		if errors.Is(err, quartz.ErrJobNotFound) { // ignore error in this case
			err = nil
		}

		return ctrl.Result{}, err
	}

	if !controllerutil.ContainsFinalizer(helmop, finalize.HelmOpFinalizer) {
		if err := addFinalizer(ctx, r.Client, helmop, finalize.HelmOpFinalizer); err != nil {
			return ctrl.Result{}, err
		}

		// nolint: staticcheck // Requeue is deprecated; see fleet#3746.
		return ctrl.Result{Requeue: true}, nil
	}

	if err := validate(ctx, *helmop); err != nil {
		return ctrl.Result{}, updateErrorStatusHelm(ctx, r.Client, req.NamespacedName, helmop, err)
	}

	// Reconciling
	logger = logger.WithValues("generation", helmop.Generation, "chart", helmop.Spec.Helm.Chart)
	ctx = log.IntoContext(ctx, logger)

	logger.V(1).Info("Reconciling HelmOp")

	if _, err := r.createUpdateBundle(ctx, helmop); err != nil {
		return ctrl.Result{}, updateErrorStatusHelm(ctx, r.Client, req.NamespacedName, helmop, err)
	}

	// Running this logic after creating/updating the bundle to avoid scheduling a job if the bundle has not been created.
	if err := r.managePollingJob(logger, *helmop); err != nil {
		return ctrl.Result{}, updateErrorStatusHelm(ctx, r.Client, req.NamespacedName, helmop, err)
	}

	err := updateStatus(ctx, r.Client, req.NamespacedName, helmop, nil)
	if err != nil {
		logger.Error(err, "Reconcile failed final update to HelmOp status", "status", helmop.Status)

		// nolint: staticcheck // Requeue is deprecated; see fleet#3746.
		return ctrl.Result{Requeue: true}, err
	}

	return ctrl.Result{}, err
}

func (r *HelmOpReconciler) createUpdateBundle(ctx context.Context, helmop *fleet.HelmOp) (*fleet.Bundle, error) {
	b := &fleet.Bundle{}
	nsName := types.NamespacedName{
		Name:      helmop.Name,
		Namespace: helmop.Namespace,
	}

	err := r.Get(ctx, nsName, b)
	if err != nil && !k8serrors.IsNotFound(err) {
		return nil, err
	}

	if err == nil && b.Spec.HelmOpOptions == nil {
		// A gitOps bundle with the same name exists; abort.
		return nil, fmt.Errorf("a non-helmops bundle already exists with name %s; aborting", helmop.Name)
	}

	// calculate the new representation of the helmop resource
	bundle := r.calculateBundle(helmop)

	if err := r.handleVersion(ctx, b, bundle, helmop); err != nil {
		return nil, err
	}

	updated := bundle.DeepCopy()
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, bundle, func() error {
		bundle.Spec = updated.Spec
		bundle.Annotations = updated.Annotations
		bundle.Labels = updated.Labels
		return nil
	})

	return bundle, err
}

// Calculates the bundle representation of the given HelmOp resource
func (r *HelmOpReconciler) calculateBundle(helmop *fleet.HelmOp) *fleet.Bundle {
	spec := helmop.Spec.BundleSpec

	// set target names
	for i, target := range spec.Targets {
		if target.Name == "" {
			spec.Targets[i].Name = fmt.Sprintf("target%03d", i)
		}
	}

	propagateHelmOpProperties(&spec)

	bundle := &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: helmop.Namespace,
			Name:      helmop.Name,
		},
		// We ensure the bundle and HelmOp spec are independent. This prevents versions constraints from being overwritten
		// in the HelmOp spec with actual versions resolved from Helm when the bundle version is updated.
		Spec: *spec.DeepCopy(),
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
	for k, v := range helmop.Spec.Labels {
		if bundle.Labels == nil {
			bundle.Labels = make(map[string]string)
		}
		bundle.Labels[k] = v
	}
	bundle.Labels = labels.Merge(bundle.Labels, map[string]string{
		fleet.HelmOpLabel: helmop.Name,
	})

	// Setting the Resources to nil, the agent will download the helm chart
	bundle.Spec.Resources = nil
	// store the helm options (this will also enable the helm chart deployment in the bundle)
	bundle.Spec.HelmOpOptions = &fleet.BundleHelmOptions{
		SecretName:            helmop.Spec.HelmSecretName,
		InsecureSkipTLSverify: helmop.Spec.InsecureSkipTLSverify,
	}

	return bundle
}

// handleVersion validates the version configured on the provided HelmOp.
// In particular:
//   - it returns an error in case that version represents an invalid semver constraint.
//   - it handles empty or * versions, downloading the current version from the registry
//
// This is calculated in the upstream cluster so all downstream bundle deployments have the same
// version. (Potentially we could be gathering the version at the very moment it is being updated, for example)
func (r *HelmOpReconciler) handleVersion(ctx context.Context, oldBundle *fleet.Bundle, bundle *fleet.Bundle, helmop *fleet.HelmOp) error {
	if helmop == nil {
		return fmt.Errorf("the provided HelmOp is nil; this should not happen")
	}

	if _, err := semver.StrictNewVersion(helmop.Spec.Helm.Version); err == nil {
		bundle.Spec.Helm.Version = helmop.Spec.Helm.Version
		return nil
	}

	if !helmChartSpecChanged(oldBundle.Spec.Helm, bundle.Spec.Helm, helmop.Status.Version) {
		bundle.Spec.Helm.Version = helmop.Status.Version

		return nil
	}

	version, err := getChartVersion(ctx, r.Client, *helmop)
	if err != nil {
		return fmt.Errorf("could not get chart version: %w", err)
	}

	if usesPolling(*helmop) {
		return nil // Field updates will be run from the polling job, to prevent race conditions.
	}

	bundle.Spec.Helm.Version = version
	helmop.Status.Version = bundle.Spec.Helm.Version

	return nil
}

// managePollingJob creates, updates or deletes a polling job for the provided HelmOp.
func (r *HelmOpReconciler) managePollingJob(logger logr.Logger, helmop fleet.HelmOp) error {
	if r.Scheduler == nil {
		logger.V(1).Info("Scheduler is not set; this should only happen in tests")
		return nil
	}

	jobKey := jobKey(helmop)
	scheduled, err := r.Scheduler.GetScheduledJob(jobKey)

	if err != nil && !errors.Is(err, quartz.ErrJobNotFound) {
		return fmt.Errorf("an unknown error occurred when looking for a polling job: %w", err)
	}

	if usesPolling(helmop) {
		scheduledJobDescription := ""

		if err == nil {
			if detail := scheduled.JobDetail(); detail != nil {
				scheduledJobDescription = detail.Job().Description()
			}
		}

		newJob := newHelmPollingJob(r.Client, r.Recorder, helmop.Namespace, helmop.Name, *helmop.Spec.Helm)
		currentTrigger := newHelmOpTrigger(helmop.Spec.PollingInterval.Duration)
		// A changing trigger description would indicate the polling interval has changed.
		// On the other hand, if the job description changes, this implies that one of the following fields has
		// been updated:
		// * Helm repo
		// * Helm chart
		// * Helm version constraint
		if errors.Is(err, quartz.ErrJobNotFound) ||
			scheduled.Trigger().Description() != currentTrigger.Description() ||
			scheduledJobDescription != newJob.Description() {
			err = r.Scheduler.ScheduleJob(
				quartz.NewJobDetailWithOptions(
					newJob,
					jobKey,
					&quartz.JobDetailOptions{
						Replace: true,
					},
				),
				currentTrigger,
			)

			if err != nil {
				return fmt.Errorf("failed to schedule polling job: %w", err)
			}

			logger.V(1).Info("Scheduled new polling job")
		}
	} else if err == nil {
		// A job still exists, but is no longer needed; delete it.
		if err = r.Scheduler.DeleteJob(jobKey); err != nil {
			return fmt.Errorf("failed to delete polling job: %w", err)
		}
	}

	return nil
}

// propagateHelmOpProperties propagates root Helm chart properties to the child targets.
// This is necessary, so we can download the correct chart version for each target.
func propagateHelmOpProperties(spec *fleet.BundleSpec) {
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

// usesPolling returns a boolean indicating whether polling makes sense for the provided helmop.
func usesPolling(helmop fleet.HelmOp) bool {
	if helmop.Spec.PollingInterval == nil || helmop.Spec.PollingInterval.Duration == 0 {
		return false
	}

	// Polling does not apply to OCI and tarball charts, where no index.yaml file is available to check for new
	// chart versions.
	if helmop.Spec.Helm.Repo == "" {
		return false
	}

	if strings.HasSuffix(strings.ToLower(helmop.Spec.Helm.Chart), ".tgz") {
		return false
	}

	if strings.HasPrefix(strings.ToLower(helmop.Spec.Helm.Repo), "oci://") {
		return false
	}

	// we only need to poll if the version is set to a constraint on versions, which may resolve to
	// different available versions as the contents of the Helm repository evolves over time.
	_, err := semver.StrictNewVersion(helmop.Spec.Helm.Version)

	return err != nil
}

// updateStatus updates the status for the HelmOp resource. It retries on
// conflict. If the status was updated successfully, it also collects (as in
// updates) metrics for the HelmOp resource.
func updateStatus(ctx context.Context, c client.Client, req types.NamespacedName, orig *fleet.HelmOp, orgErr error) error {
	if orig == nil {
		return fmt.Errorf("the HelmOp provided for a status update is nil; this should not happen")
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.HelmOp{}
		if err := c.Get(ctx, req, t); err != nil {
			return err
		}

		// selectively update the status fields this reconciler is responsible for
		t.Status.Version = orig.Status.Version

		// only keep the Ready condition from live status, it's calculated by the status reconciler
		conds := []genericcondition.GenericCondition{}
		for _, c := range t.Status.Conditions {
			if c.Type == "Ready" {
				conds = append(conds, c)
				break
			}
		}
		for _, c := range orig.Status.Conditions {
			if c.Type == "Ready" {
				continue
			}
			conds = append(conds, c)
		}
		t.Status.Conditions = conds

		setAcceptedConditionHelm(&t.Status, orgErr)

		statusPatch := client.MergeFrom(orig)
		if patchData, err := statusPatch.Data(t); err == nil && string(patchData) == "{}" {
			metrics.HelmCollector.Collect(ctx, t)
			// skip update if patch is empty
			return nil
		}

		if err := c.Status().Patch(ctx, t, statusPatch); err != nil {
			return err
		}

		metrics.HelmCollector.Collect(ctx, t)

		return nil
	})
}

// updateErrorStatusHelm sets the condition in the status and tries to update the resource
func updateErrorStatusHelm(ctx context.Context, c client.Client, req types.NamespacedName, helmOp *fleet.HelmOp, orgErr error) error {
	if statusErr := updateStatus(ctx, c, req, helmOp, orgErr); statusErr != nil {
		merr := []error{orgErr, fmt.Errorf("failed to update the status: %w", statusErr)}
		return errutil.NewAggregate(merr)
	}
	return orgErr
}

// setAcceptedCondition sets the condition and updates the timestamp, if the condition changed
func setAcceptedConditionHelm(status *fleet.HelmOpStatus, err error) {
	cond := condition.Cond(fleet.HelmOpAcceptedCondition)
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
		return true
	}
	if o.Chart != n.Chart {
		return true
	}
	// check also against statusVersion in case that Reconcile is called
	// before the status subresource has been fully updated in the cluster (and the cache)
	if o.Version != n.Version && statusVersion != o.Version {
		return true
	}
	return false
}

// getChartVersion fetches the latest chart version from the Helm registry referenced by helmop, and returns it.
// If this fails, it returns an empty version along with an error.
func getChartVersion(ctx context.Context, c client.Client, helmop fleet.HelmOp) (string, error) {
	auth := bundlereader.Auth{}
	if helmop.Spec.HelmSecretName != "" {
		req := types.NamespacedName{Namespace: helmop.Namespace, Name: helmop.Spec.HelmSecretName}
		var err error
		auth, err = bundlereader.ReadHelmAuthFromSecret(ctx, c, req)
		if err != nil {
			return "", fmt.Errorf("could not read Helm auth from secret: %w", err)
		}
	}
	auth.InsecureSkipVerify = helmop.Spec.InsecureSkipTLSverify

	version, err := bundlereader.ChartVersion(*helmop.Spec.Helm, auth)
	if err != nil {
		return "", fmt.Errorf("could not get a chart version: %w", err)
	}

	return version, nil
}

func jobKey(h fleet.HelmOp) *quartz.JobKey {
	return quartz.NewJobKey(string(h.UID))
}

// validate checks combinations of Chart, Repo and Version fields in h's Helm options.
// It returns an error if those options are nil, or if they don't fall under any of these categories,
// as per https://helm.sh/docs/helm/helm_install/ :
// * tarball URL in Chart, empty Repo, empty Version
// * OCI reference in the Repo field, empty Chart, optional Version
// * non-empty Repo URL, non-empty Chart name, optional Version
func validate(ctx context.Context, h fleet.HelmOp) error {
	if h.Spec.Helm == nil {
		return fmt.Errorf("helm options are empty in the HelmOp's spec")
	}

	fail := func(msg string) error {
		return fmt.Errorf("helm options invalid: %s", msg)
	}

	if strings.HasSuffix(strings.ToLower(h.Spec.Helm.Chart), ".tgz") {
		if len(h.Spec.Helm.Repo) > 0 {
			return fail("tarball chart with a non-empty repo field")
		}

		if len(h.Spec.Helm.Version) > 0 {
			return fail("tarball chart with a non-empty version field")
		}
	} else if strings.HasPrefix(strings.ToLower(h.Spec.Helm.Repo), "oci://") {
		if len(h.Spec.Helm.Chart) > 0 {
			return fail("OCI repository with a non-empty chart field")
		}
	} else { // Expecting full reference: chart + repo + optional version
		if len(h.Spec.Helm.Chart) == 0 {
			return fail("non-OCI repository with an empty chart field")
		}

		if len(h.Spec.Helm.Repo) == 0 {
			return fail("non-tarball chart with an empty repo field")
		}
	}

	return nil
}

// helmOpTrigger is a custom trigger, implementing the quartz.Trigger interface. This trigger is
// used to schedule jobs to be run both:
// * periodically, after the first polling interval, as would happen with Quartz's `simpleTrigger`
// * right away, without waiting for that first polling interval to elapse.
type helmOpTrigger struct {
	isInitRunDone bool
	simpleTrigger *quartz.SimpleTrigger
}

func (t *helmOpTrigger) NextFireTime(prev int64) (int64, error) {
	if !t.isInitRunDone {
		t.isInitRunDone = true

		return prev, nil
	}

	return t.simpleTrigger.NextFireTime(prev)
}

func (t *helmOpTrigger) Description() string {
	return t.simpleTrigger.Description()
}

func newHelmOpTrigger(interval time.Duration) *helmOpTrigger {
	return &helmOpTrigger{simpleTrigger: quartz.NewSimpleTrigger(interval)}
}
