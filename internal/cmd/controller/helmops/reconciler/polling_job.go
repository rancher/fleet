// Copyright (c) 2021-2025 SUSE LLC

package reconciler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/reugn/go-quartz/quartz"
	"golang.org/x/sync/semaphore"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/kstatus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ quartz.Job = &helmPollingJob{}

type helmPollingJob struct {
	sem    *semaphore.Weighted
	client client.Client

	namespace string
	name      string

	repo    string
	chart   string
	version string

	recorder events.EventRecorder
}

func newHelmPollingJob(
	c client.Client,
	r events.EventRecorder,
	namespace,
	name string,
	helmRef fleet.HelmOptions,
) *helmPollingJob {
	return &helmPollingJob{
		sem:      semaphore.NewWeighted(1),
		client:   c,
		recorder: r,

		namespace: namespace,
		name:      name,

		repo:    helmRef.Repo,
		chart:   helmRef.Chart,
		version: helmRef.Version,
	}
}

func (j *helmPollingJob) Execute(ctx context.Context) error {
	logger := log.FromContext(ctx)

	if !j.sem.TryAcquire(1) {
		// already running
		logger.V(1).Info("skipping polling job execution: already running")

		return nil
	}
	defer j.sem.Release(1)

	return j.pollHelm(ctx)
}

// Description returns a description for the job.
// This is needed to implement the Quartz Job interface.
func (j *helmPollingJob) Description() string {
	hasher := sha256.New()
	hasher.Write([]byte(j.repo))
	hasher.Write([]byte(j.chart))
	hasher.Write([]byte(j.version))

	chartRefHash := fmt.Sprintf("%x", hasher.Sum(nil))

	return fmt.Sprintf("helmops-polling-%s-%s-%s", j.namespace, j.name, chartRefHash)
}

func (j *helmPollingJob) pollHelm(ctx context.Context) error {
	h := &fleet.HelmOp{}
	nsName := types.NamespacedName{
		Name:      j.name,
		Namespace: j.namespace,
	}
	if err := j.client.Get(ctx, nsName, h); err != nil {
		return fmt.Errorf("could not get HelmOp resource from polling job: %w", err)
	}

	if h.Spec.Helm == nil {
		// This should not happen unless something has gone wrong in the reconciler's job management logic.
		return fmt.Errorf("helm options are unset")
	}

	// In case the version constraint has changed before the job was updated or deleted, this prevents an unwanted
	// update caused by a race between the scheduler and the reconciler.
	if _, err := semver.StrictNewVersion(h.Spec.Helm.Version); err == nil {
		return nil
	}

	// From here on, polling is considered to have been triggered.
	// Even if it fails, this timestamp will be updated in the HelmOp status.
	pollingTimestamp := time.Now().UTC()

	fail := func(origErr error, eventReason, eventAction string) error {
		if eventReason != "" {
			j.recorder.Eventf(
				h,
				nil,
				corev1.EventTypeWarning,
				eventReason,
				eventAction,
				origErr.Error(),
			)
		}

		return j.updateErrorStatus(ctx, h, pollingTimestamp, origErr)
	}

	version, err := getChartVersion(ctx, j.client, *h)
	if err != nil {
		return fail(err, "FailedToGetNewChartVersion", "GetNewChartVersion")
	}

	b := &fleet.Bundle{}

	if err := j.client.Get(ctx, nsName, b); err != nil {
		return fail(
			fmt.Errorf("could not get bundle before patching its version: %w", err),
			"FailedToGetBundle",
			"GetBundle",
		)
	}

	orig := b.DeepCopy()
	b.Spec.Helm.Version = version

	if version != h.Status.Version {
		j.recorder.Eventf(
			h,
			nil,
			corev1.EventTypeNormal,
			"GotNewChartVersion",
			"GetNewChartVersion",
			version,
		)
	}

	patch := client.MergeFrom(orig)
	if patchData, err := patch.Data(b); err == nil && string(patchData) == "{}" && !isInErrorState(h.Status) {
		// skip update if patch is empty
		return nil
	}

	if err := j.client.Patch(ctx, b, patch); err != nil {
		return fail(
			fmt.Errorf("could not patch bundle to set the resolved version: %w", err),
			"FailedToPatchBundle",
			"PatchBundle",
		)
	}

	nsn := types.NamespacedName{Name: h.Name, Namespace: h.Namespace}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.HelmOp{}
		if err := j.client.Get(ctx, nsn, t); err != nil {
			return fmt.Errorf("could not get HelmOp to update its status: %w", err)
		}

		t.Status.LastPollingTime = metav1.Time{Time: pollingTimestamp}
		t.Status.Version = version

		condition.Cond(fleet.HelmOpAcceptedCondition).SetStatusBool(&t.Status, true)
		condition.Cond(fleet.HelmOpPolledCondition).SetStatusBool(&t.Status, true)
		condition.Cond(fleet.HelmOpPolledCondition).Message(&t.Status, "")
		condition.Cond(fleet.HelmOpPolledCondition).Reason(&t.Status, "")
		kstatus.SetActive(&t.Status)

		statusPatch := client.MergeFrom(h)
		if patchData, err := statusPatch.Data(t); err == nil && string(patchData) == "{}" {
			// skip update if patch is empty
			return nil
		}
		return j.client.Status().Patch(ctx, t, statusPatch)
	})
	if err != nil {
		return fail(
			fmt.Errorf("could not update HelmOp status with polling timestamp: %w", err),
			"FailedToUpdateHelmOpStatus",
			"UpdateHelmOpStatus",
		)
	}

	return nil
}

// updateErrorStatus updates the provided helmOp's status to reflect the provided orgErr.
// This includes updating the helmOp's polling timestamp, if provided.
func (j *helmPollingJob) updateErrorStatus(
	ctx context.Context,
	helmOp *fleet.HelmOp,
	pollingTimestamp time.Time,
	orgErr error,
) error {
	nsn := types.NamespacedName{Name: helmOp.Name, Namespace: helmOp.Namespace}

	merr := []error{orgErr}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.HelmOp{}
		if err := j.client.Get(ctx, nsn, t); err != nil {
			return fmt.Errorf("could not get HelmOp to update its status: %w", err)
		}

		condition.Cond(fleet.HelmOpPolledCondition).SetError(&t.Status, "", orgErr)
		kstatus.SetError(t, orgErr.Error())

		if !pollingTimestamp.IsZero() {
			t.Status.LastPollingTime = metav1.Time{Time: pollingTimestamp}
		}

		statusPatch := client.MergeFrom(helmOp)
		if patchData, err := statusPatch.Data(t); err == nil && string(patchData) == "{}" {
			// skip update if patch is empty
			return nil
		}
		return j.client.Status().Patch(ctx, t, statusPatch)
	})
	if err != nil {
		merr = append(merr, err)
	}
	return errutil.NewAggregate(merr)
}

func isInErrorState(status fleet.HelmOpStatus) bool {
	for _, cond := range status.Conditions {
		// When an error is found we set the Reason to either Error or Stalled
		// and the Message field has the error message.
		if cond.Reason != "" && cond.Message != "" {
			return true
		}
	}

	return false
}
