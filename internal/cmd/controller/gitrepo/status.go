package gitrepo

import (
	"context"
	"sort"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	gitjob "github.com/rancher/fleet/pkg/apis/gitjob.cattle.io/v1"

	"github.com/rancher/wrangler/v2/pkg/genericcondition"

	"k8s.io/apimachinery/pkg/types"
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

func SetStatusFromGitJob(ctx context.Context, c client.Client, gitrepo *fleet.GitRepo) error {
	gitJob := &gitjob.GitJob{}
	err := c.Get(ctx, types.NamespacedName{Namespace: gitrepo.Namespace, Name: gitrepo.Name}, gitJob)
	if err == nil {
		gitrepo.Status.Commit = gitJob.Status.Commit
		gitrepo.Status.Conditions = mergeConditions(gitrepo.Status.Conditions, gitJob.Status.Conditions)
		gitrepo.Status.GitJobStatus = gitJob.Status.JobStatus
	} else {
		gitrepo.Status.Commit = ""
	}

	if gitrepo.Status.GitJobStatus != "Current" {
		gitrepo.Status.Display.State = "GitUpdating"
	}

	return nil
}

func mergeConditions(existing, next []genericcondition.GenericCondition) []genericcondition.GenericCondition {
	result := make([]genericcondition.GenericCondition, 0, len(existing)+len(next))
	names := map[string]int{}
	for i, existing := range existing {
		result = append(result, existing)
		names[existing.Type] = i
	}
	for _, next := range next {
		if i, ok := names[next.Type]; ok {
			result[i] = next
		} else {
			result = append(result, next)
		}
	}
	return result
}
