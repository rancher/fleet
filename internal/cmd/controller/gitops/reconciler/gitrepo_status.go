package reconciler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/kstatus"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func setStatus(list *fleet.BundleDeploymentList, gitrepo *fleet.GitRepo) error {
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].UID < list.Items[j].UID
	})

	err := setStatusFromBundleDeployments(list, gitrepo)
	if err != nil {
		return err
	}

	if gitrepo.Status.GitJobStatus != "Current" {
		gitrepo.Status.Display.State = "GitUpdating"
	}

	setResourceKey(list, gitrepo)

	summary.SetReadyConditions(&gitrepo.Status, "Bundle", gitrepo.Status.Summary)

	gitrepo.Status.Display.ReadyBundleDeployments = fmt.Sprintf("%d/%d",
		gitrepo.Status.Summary.Ready,
		gitrepo.Status.Summary.DesiredReady)

	setCondition(&gitrepo.Status, nil)

	return nil
}

func setStatusFromBundleDeployments(list *fleet.BundleDeploymentList, gitrepo *fleet.GitRepo) error {
	var (
		maxState fleet.BundleState
		message  string
	)
	clusters := map[client.ObjectKey]bool{}
	readyClusters := map[client.ObjectKey]int{}
	gitrepo.Status.Summary = fleet.BundleSummary{}

	for _, bd := range list.Items {
		state := summary.GetDeploymentState(&bd)
		summary.IncrementState(&gitrepo.Status.Summary, bd.Name, state, summary.MessageFromDeployment(&bd), bd.Status.ModifiedStatus, bd.Status.NonReadyStatus)
		gitrepo.Status.Summary.DesiredReady++
		if fleet.StateRank[state] > fleet.StateRank[maxState] {
			maxState = state
			message = summary.MessageFromDeployment(&bd)
		}

		// try to avoid old bundle deployments, which might be missing the labels
		if bd.Labels == nil {
			// this should not happen
			continue
		}

		name := bd.Labels[fleet.ClusterLabel]
		namespace := bd.Labels[fleet.ClusterNamespaceLabel]
		if name == "" || namespace == "" {
			// this should not happen
			continue
		}

		key := client.ObjectKey{Name: name, Namespace: namespace}
		clusters[key] = true
		if state == fleet.Ready {
			readyClusters[key]++
		}
	}

	// unique number of clusters from bundledeployments
	gitrepo.Status.DesiredReadyClusters = len(clusters)

	// lowest amount of ready bundledeployments found in clusters
	min := -1
	for _, ready := range readyClusters {
		if min < 0 || ready < min {
			min = ready
		}
	}
	if min < 0 {
		min = 0
	}
	gitrepo.Status.ReadyClusters = min

	if maxState == fleet.Ready {
		maxState = ""
		message = ""
	}

	gitrepo.Status.Display.State = string(maxState)
	gitrepo.Status.Display.Message = message
	gitrepo.Status.Display.Error = len(message) > 0

	return nil
}

// setStatusFromGitjob sets the status fields relative to the given job in the gitRepo
func setStatusFromGitjob(ctx context.Context, c client.Client, gitRepo *fleet.GitRepo, job *batchv1.Job) error {
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

	// status.Compute possible results are
	//   - InProgress
	//   - Current
	//   - Failed
	//   - Terminating
	switch result.Status {
	case status.FailedStatus:
		kstatus.SetError(gitRepo, terminationMessage)
	case status.CurrentStatus:
		if strings.Contains(result.Message, "Job Completed") {
			gitRepo.Status.Commit = job.Annotations["commit"]
		}
		kstatus.SetActive(gitRepo)
	case status.InProgressStatus:
		kstatus.SetTransitioning(gitRepo, "")
	case status.TerminatingStatus:
		// set active set both conditions to False
		// the job is terminating so avoid reporting errors in
		// that case
		kstatus.SetActive(gitRepo)
	}

	return nil
}

// setCondition sets the condition and updates the timestamp, if the condition changed
func setCondition(status *fleet.GitRepoStatus, err error) {
	cond := condition.Cond(fleet.GitRepoAcceptedCondition)
	origStatus := status.DeepCopy()
	cond.SetError(status, "", fleetutil.IgnoreConflict(err))
	if !equality.Semantic.DeepEqual(origStatus, status) {
		cond.LastUpdated(status, time.Now().UTC().Format(time.RFC3339))
	}
}
