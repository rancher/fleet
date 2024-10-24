package reconciler

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/kstatus"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func setStatus(ctx context.Context, c client.Client, gitrepo *fleet.GitRepo) error {
	err := setStatusFromBundleDeployments(ctx, c, gitrepo)
	if err != nil {
		return err
	}

	err = setStatusFromBundles(ctx, c, gitrepo)
	if err != nil {
		return err
	}

	if gitrepo.Status.GitJobStatus != "Current" {
		gitrepo.Status.Display.State = "GitUpdating"
	}

	setResourceKey(ctx, c, gitrepo)

	gitrepo.Status.Display.ReadyBundleDeployments = fmt.Sprintf("%d/%d",
		gitrepo.Status.Summary.Ready,
		gitrepo.Status.Summary.DesiredReady)

	return nil
}

func setStatusFromBundles(ctx context.Context, c client.Client, gitrepo *fleet.GitRepo) error {
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

func setStatusFromBundleDeployments(ctx context.Context, c client.Client, gitrepo *fleet.GitRepo) error {
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
