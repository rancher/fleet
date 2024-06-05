package gitrepo

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v2/pkg/condition"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	errutil "k8s.io/apimachinery/pkg/util/errors"
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
		t.Status = status

		err = c.Status().Update(ctx, t)
		if err != nil {
			return err
		}

		metrics.GitRepoCollector.Collect(ctx, t)

		return nil
	})
}
