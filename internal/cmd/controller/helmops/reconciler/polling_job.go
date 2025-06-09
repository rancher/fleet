// Copyright (c) 2021-2025 SUSE LLC

package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/reugn/go-quartz/quartz"
	"golang.org/x/sync/semaphore"

	"github.com/rancher/fleet/internal/bundlereader"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetevent "github.com/rancher/fleet/pkg/event"

	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/kstatus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"
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

	recorder record.EventRecorder
}

func newHelmPollingJob(c client.Client, r record.EventRecorder, namespace string, name string) *helmPollingJob {
	return &helmPollingJob{
		sem:      semaphore.NewWeighted(1),
		client:   c,
		recorder: r,

		namespace: namespace,
		name:      name,
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
	return fmt.Sprintf("helmops-polling-%s-%s", j.namespace, j.name)
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

	fail := func(origErr error, eventReason string) error {
		if eventReason != "" {
			j.recorder.Event(h, fleetevent.Warning, eventReason, origErr.Error())
		}

		return j.updateErrorStatus(ctx, h, origErr)
	}

	auth := bundlereader.Auth{}
	if h.Spec.HelmSecretName != "" {
		req := types.NamespacedName{Namespace: h.Namespace, Name: h.Spec.HelmSecretName}

		var err error
		auth, err = bundlereader.ReadHelmAuthFromSecret(ctx, j.client, req)
		if err != nil {
			return fail(fmt.Errorf("could not read Helm auth from secret: %w", err), "FailedToReadHelmAuth")
		}
	}
	auth.InsecureSkipVerify = h.Spec.InsecureSkipTLSverify

	// XXX: do this only if bundle can be fetched? Save request if not.
	version, err := bundlereader.ChartVersion(*h.Spec.Helm, auth)
	if err != nil {
		return fail(fmt.Errorf("could not get a chart version: %w", err), "FailedToGetNewChartVersion")
	}

	b := &fleet.Bundle{}

	if err := j.client.Get(ctx, nsName, b); err != nil {
		return fail(fmt.Errorf("could not get bundle before patching its version: %w", err), "FailedToGetBundle")
	}

	orig := b.DeepCopy()
	b.Spec.Helm.Version = version

	j.recorder.Event(h, fleetevent.Normal, "GotNewChartVersion", version)

	patch := client.MergeFrom(orig)
	if patchData, err := patch.Data(b); err == nil && string(patchData) == "{}" {
		// skip update if patch is empty
		return nil
	}

	if err := j.client.Patch(ctx, b, patch); err != nil {
		return fail(fmt.Errorf("could not patch bundle to set the resolved version: %w", err), "FailedToPatchBundle")
	}

	merr := []error{}
	nsn := types.NamespacedName{Name: h.Name, Namespace: h.Namespace}

	// XXX: Use defer() to run status update anyway, updating the polling timestamp if the registry could be polled,
	// even if subsequent steps failed?
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.HelmOp{}
		if err := j.client.Get(ctx, nsn, t); err != nil {
			return fmt.Errorf("could not get HelmOp to update its status: %w", err)
		}

		t.Status.LastPollingTime = metav1.Time{Time: time.Now().UTC()}
		condition.Cond(fleet.HelmOpPolledCondition).SetStatusBool(&t.Status, true)

		statusPatch := client.MergeFrom(h)
		if patchData, err := statusPatch.Data(t); err == nil && string(patchData) == "{}" {
			// skip update if patch is empty
			return nil
		}
		return j.client.Status().Patch(ctx, t, statusPatch)
	})
	if err != nil {
		merr = append(merr, err)

		return fail(
			fmt.Errorf(
				"could not update HelmOp status with polling timestamp: %w",
				errutil.NewAggregate(merr),
			),
			"FailedToUpdateHelmOpStatus",
		)
	}

	return nil
}

// updateErrorStatus updates the provided helmOp's status to reflect the provided orgErr.
func (j *helmPollingJob) updateErrorStatus(ctx context.Context, helmOp *fleet.HelmOp, orgErr error) error {
	nsn := types.NamespacedName{Name: helmOp.Name, Namespace: helmOp.Namespace}

	condition.Cond(fleet.HelmOpPolledCondition).SetError(&helmOp.Status, "", orgErr)
	kstatus.SetError(helmOp, orgErr.Error())
	merr := []error{orgErr}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.HelmOp{}
		if err := j.client.Get(ctx, nsn, t); err != nil {
			return fmt.Errorf("could not get HelmOp to update its status: %w", err)
		}
		t.Status = helmOp.Status
		return j.client.Status().Update(ctx, t)
	})
	if err != nil {
		merr = append(merr, err)
	}
	return errutil.NewAggregate(merr)
}
