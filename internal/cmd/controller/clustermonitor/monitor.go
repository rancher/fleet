package clustermonitor

import (
	"context"
	"fmt"
	"math"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/monitor"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// Run monitors Fleet cluster resources' agent last seen dates. If a cluster's agent was last seen longer ago than
// a certain threshold, then Run updates statuses of all bundle deployments targeting that cluster, to reflect the fact
// that the cluster is offline. This prevents those bundle deployments from displaying outdated status information.
//
// A cluster will be considered offline if its Fleet agent has not reported its status for more than:
// - three times the agent check-in interval
// - or any larger configured interval.
// Therefore, this function requires configuration to have been loaded into the config package using `Load` before
// running.
//
// Bundle deployment status updates done here are unlikely to conflict with those done by the bundle deployment
// reconciler, which are either run from an online target cluster (from its Fleet agent) or triggered by other status
// updates such as this one (eg. bundle deployment reconciler living in the Fleet controller).
func Run(ctx context.Context, c client.Client, interval, threshold time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		cfg := config.Get() // This enables config changes to take effect

		thresholdSecs := math.Max(cfg.AgentCheckinInterval.Seconds()*3, threshold.Seconds())

		UpdateOfflineBundleDeployments(ctx, c, time.Second*time.Duration(thresholdSecs))
	}
}

func UpdateOfflineBundleDeployments(ctx context.Context, c client.Client, threshold time.Duration) {
	logger := ctrl.Log.WithName("cluster status monitor")

	clusters := &v1alpha1.ClusterList{}
	if err := c.List(ctx, clusters); err != nil {
		logger.Error(err, "Failed to get list of clusters")
		return
	}

	for _, cluster := range clusters.Items {
		lastSeen := cluster.Status.Agent.LastSeen

		logger.Info("Checking cluster status", "cluster", cluster.Name, "last seen", lastSeen.UTC().String())

		// XXX: do we want to run this more than once per cluster, updating the timestamp each time?
		// Or would it make sense to keep the oldest possible timestamp in place, for users to know since when the
		// cluster is offline?

		// lastSeen being 0 would typically mean that the cluster is not registered yet, in which case bundle
		// deployments should not be deployed there.
		if lastSeen.IsZero() || time.Now().UTC().Sub(lastSeen.UTC()) < threshold {
			continue
		}

		logger.Info("Detected offline cluster", "cluster", cluster.Name)

		// Cluster is offline
		bundleDeployments := &v1alpha1.BundleDeploymentList{}
		if err := c.List(ctx, bundleDeployments, client.InNamespace(cluster.Status.Namespace)); err != nil {
			logger.Error(
				err,
				"Failed to get list of bundle deployments for offline cluster",
				"cluster",
				cluster.Name,
				"namespace",
				cluster.Status.Namespace,
			)
			continue
		}

		for _, bd := range bundleDeployments.Items {
			logger.Info("Updating bundle deployment in offline cluster", "cluster", cluster.Name, "bundledeployment", bd.Name)
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				t := &v1alpha1.BundleDeployment{}
				nsn := types.NamespacedName{Name: bd.Name, Namespace: bd.Namespace}
				if err := c.Get(ctx, nsn, t); err != nil {
					return err
				}
				t.Status = bd.Status
				// Any information about resources living in an offline cluster is likely to be
				// outdated.
				t.Status.ModifiedStatus = nil
				t.Status.NonReadyStatus = nil

				for _, cond := range bd.Status.Conditions {
					switch cond.Type {
					// XXX: which messages do we want to set and where?
					case "Ready":
						// FIXME: avoid relying on agent pkg for this?
						mc := monitor.Cond(v1alpha1.BundleDeploymentConditionReady)
						mc.SetError(&t.Status, "Cluster offline", fmt.Errorf("cluster is offline"))
						// XXX: do we want to set Deployed and Installed conditions as well?
						// XXX: should we set conditions to `Unknown`?
					case "Monitored":
						mc := monitor.Cond(v1alpha1.BundleDeploymentConditionMonitored)
						mc.SetError(&t.Status, "Cluster offline", fmt.Errorf("cluster is offline"))

					}
				}

				return c.Status().Update(ctx, t)
			})
			if err != nil {
				logger.Error(
					err,
					"Failed to update bundle deployment status for offline cluster",
					"bundledeployment",
					bd.Name,
					"cluster",
					cluster.Name,
					"namespace",
					cluster.Status.Namespace,
				)
			}
		}
	}
}
