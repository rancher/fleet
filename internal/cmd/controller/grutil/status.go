package grutil

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v2/pkg/condition"
	"github.com/rancher/wrangler/v2/pkg/kstatus"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func SetStatusFromBundles(ctx context.Context, c client.Client, gitrepo *fleet.GitRepo) error {
	bundles := &fleet.BundleList{}
	err := c.List(ctx, bundles, client.InNamespace(gitrepo.Namespace), client.MatchingLabels{
		fleet.RepoLabel: gitrepo.Name,
	})
	if err != nil {
		return err
	}

	sort.Slice(bundles.Items, func(i, j int) bool {
		return bundles.Items[i].Name < bundles.Items[j].Name
	})

	var (
		clustersDesiredReady int
		clustersReady        = -1
	)

	for _, bundle := range bundles.Items {
		if bundle.Status.Summary.DesiredReady > 0 {
			clustersDesiredReady = bundle.Status.Summary.DesiredReady
			if clustersReady < 0 || bundle.Status.Summary.Ready < clustersReady {
				clustersReady = bundle.Status.Summary.Ready
			}
		}
	}

	if clustersReady < 0 {
		clustersReady = 0
	}
	gitrepo.Status.DesiredReadyClusters = clustersDesiredReady
	gitrepo.Status.ReadyClusters = clustersReady
	summary.SetReadyConditions(&gitrepo.Status, "Bundle", gitrepo.Status.Summary)
	return nil
}

func SetStatusFromBundleDeployments(ctx context.Context, c client.Client, gitrepo *fleet.GitRepo) error {
	list := &fleet.BundleDeploymentList{}
	err := c.List(ctx, list, client.MatchingLabels{
		fleet.RepoLabel:            gitrepo.Name,
		fleet.BundleNamespaceLabel: gitrepo.Namespace,
	})
	if err != nil {
		return err
	}

	gitrepo.Status.Summary = fleet.BundleSummary{}

	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].UID < list.Items[j].UID
	})

	var (
		maxState fleet.BundleState
		message  string
	)

	for _, bd := range list.Items {
		bd := bd // fix gosec warning regarding "Implicit memory aliasing in for loop"
		state := summary.GetDeploymentState(&bd)
		summary.IncrementState(&gitrepo.Status.Summary, bd.Name, state, summary.MessageFromDeployment(&bd), bd.Status.ModifiedStatus, bd.Status.NonReadyStatus)
		gitrepo.Status.Summary.DesiredReady++
		if fleet.StateRank[state] > fleet.StateRank[maxState] {
			maxState = state
			message = summary.MessageFromDeployment(&bd)
		}
	}

	if maxState == fleet.Ready {
		maxState = ""
		message = ""
	}

	gitrepo.Status.Display.State = string(maxState)
	gitrepo.Status.Display.Message = message
	gitrepo.Status.Display.Error = len(message) > 0

	return nil
}

// SetStatusFromGitjob sets the status fields relative to the given job in the gitRepo
func SetStatusFromGitjob(ctx context.Context, c client.Client, gitRepo *fleet.GitRepo, job *batchv1.Job) error {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(job)
	if err != nil {
		return err
	}
	uJob := &unstructured.Unstructured{Object: obj}

	result, err := status.Compute(uJob)
	if err != nil {
		return err
	}

	terminationMessage := ""
	if result.Status == status.FailedStatus {
		selector := labels.SelectorFromSet(labels.Set{"job-name": job.Name})
		podList := &corev1.PodList{}
		err := c.List(ctx, podList, &client.ListOptions{LabelSelector: selector})
		if err != nil {
			return err
		}

		sort.Slice(podList.Items, func(i, j int) bool {
			return podList.Items[i].CreationTimestamp.Before(&podList.Items[j].CreationTimestamp)
		})

		terminationMessage = result.Message
		if len(podList.Items) > 0 {
			for _, podStatus := range podList.Items[len(podList.Items)-1].Status.ContainerStatuses {
				if podStatus.Name != "step-git-source" && podStatus.State.Terminated != nil {
					terminationMessage += podStatus.State.Terminated.Message
				}
			}
		}
	}

	gitRepo.Status.GitJobStatus = result.Status.String()

	for _, con := range result.Conditions {
		condition.Cond(con.Type.String()).SetStatus(gitRepo, string(con.Status))
		condition.Cond(con.Type.String()).SetMessageIfBlank(gitRepo, con.Message)
		condition.Cond(con.Type.String()).Reason(gitRepo, con.Reason)
	}

	switch result.Status {
	case status.FailedStatus:
		kstatus.SetError(gitRepo, terminationMessage)
	case status.CurrentStatus:
		if strings.Contains(result.Message, "Job Completed") {
			gitRepo.Status.Commit = job.Annotations["commit"]
		}
		kstatus.SetActive(gitRepo)
	}

	return nil
}

func UpdateDisplayState(gitrepo *fleet.GitRepo) error {
	if gitrepo.Status.GitJobStatus != "Current" {
		gitrepo.Status.Display.State = "GitUpdating"
	}

	return nil
}

// SetCondition sets the condition and updates the timestamp, if the condition changed
func SetCondition(status *fleet.GitRepoStatus, err error) {
	cond := condition.Cond(fleet.GitRepoAcceptedCondition)
	origStatus := status.DeepCopy()
	cond.SetError(status, "", fleetutil.IgnoreConflict(err))
	if !equality.Semantic.DeepEqual(origStatus, status) {
		cond.LastUpdated(status, time.Now().UTC().Format(time.RFC3339))
	}
}

// UpdateErrorStatus sets the condition in the status and tries to update the resource
func UpdateErrorStatus(ctx context.Context, c client.Client, req types.NamespacedName, status fleet.GitRepoStatus, orgErr error) error {
	SetCondition(&status, orgErr)
	if statusErr := UpdateStatus(ctx, c, req, status); statusErr != nil {
		merr := []error{orgErr, fmt.Errorf("failed to update the status: %w", statusErr)}
		return errutil.NewAggregate(merr)
	}
	return orgErr
}

// UpdateStatus updates the status for the GitRepo resource. It retries on
// conflict. If the status was updated successfully, it also collects (as in
// updates) metrics for the resource GitRepo resource.
func UpdateStatus(ctx context.Context, c client.Client, req types.NamespacedName, status fleet.GitRepoStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.GitRepo{}
		err := c.Get(ctx, req, t)
		if err != nil {
			return err
		}
		commit := t.Status.Commit
		t.Status = status
		if commit != "" && status.Commit == "" {
			// we could incur in a race condition between the poller job
			// setting the Commit and the first time the reconciler runs.
			// The poller could be faster than the reconciler setting the
			// Commit and we could reset back to "" in here
			t.Status.Commit = commit
		}

		err = c.Status().Update(ctx, t)
		if err != nil {
			return err
		}

		metrics.GitRepoCollector.Collect(ctx, t)

		return nil
	})
}
