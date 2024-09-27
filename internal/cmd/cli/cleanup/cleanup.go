package cleanup

import (
	"context"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"github.com/jpillora/backoff"

	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Options struct {
	Min    time.Duration
	Max    time.Duration
	Factor float64
}

func ClusterRegistrations(ctx context.Context, cl client.Client, opts Options) error {
	logger := log.FromContext(ctx)

	// lookup for all existing clusters
	seen := map[types.NamespacedName]struct{}{}
	clusterList := &fleetv1.ClusterList{}
	_ = cl.List(ctx, clusterList)
	for _, c := range clusterList.Items {
		clusterKey := types.NamespacedName{Namespace: c.Namespace, Name: c.Name}
		seen[clusterKey] = struct{}{}
	}

	crList := &fleetv1.ClusterRegistrationList{}
	_ = cl.List(ctx, crList)

	logger.Info("Listing resources", "clusters", len(clusterList.Items), "clusterRegistrations", len(crList.Items))

	// figure out the latest granted registration request per cluster
	latestGranted := map[types.NamespacedName]metav1.Time{}
	for _, cr := range crList.Items {
		if cr.Status.ClusterName == "" {
			continue
		}

		logger.Info("Mapping cluster registration")
		clusterKey := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Status.ClusterName}
		ts := latestGranted[clusterKey]
		if cr.Status.Granted && ts.Before(&cr.CreationTimestamp) {
			latestGranted[clusterKey] = cr.CreationTimestamp
		}
	}

	// sleep to not overload the API server
	b := backoff.Backoff{
		Min:    opts.Min,
		Max:    opts.Max,
		Factor: opts.Factor,
		Jitter: true,
	}

	// it should be safe to delete up to, but not including, the latest
	// granted registration
	// * requests after that might be in flight
	// * requests before that are outdated
	for _, cr := range crList.Items {
		logger := logger.WithValues("namespace", cr.Namespace, "name", cr.Name)
		logger.Info("Inspect cluster registration")
		if cr.Status.ClusterName == "" {
			continue
		}

		clusterKey := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Status.ClusterName}
		latest, found := latestGranted[clusterKey]
		if found && cr.CreationTimestamp.Before(&latest) {
			t := b.Duration()
			logger.Info("Deleting outdated, granted cluster registration, waiting", "duration", t)
			time.Sleep(t)
			if err := cl.Delete(ctx, &cr); err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to delete clusterregistration")
			}
			// also try to delete orphan resources
			_ = cl.Delete(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: cr.Namespace, Name: cr.Name}})
			_ = cl.Delete(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: cr.Namespace, Name: cr.Name}})

			saList := &corev1.ServiceAccountList{}
			_ = cl.List(ctx, saList, client.MatchingLabels{
				"fleet.cattle.io/cluster-registration":           cr.Name,
				"fleet.cattle.io/cluster-registration-namespace": cr.Namespace})
			for _, sa := range saList.Items {
				_ = cl.Delete(ctx, &sa)
			}

			owner := client.MatchingLabels{
				"objectset.rio.cattle.io/owner-name":      cr.Name,
				"objectset.rio.cattle.io/owner-namespace": cr.Namespace,
			}
			rbList := &rbacv1.RoleBindingList{}
			_ = cl.List(ctx, rbList, owner)
			for _, rb := range rbList.Items {
				_ = cl.Delete(ctx, &rb)
			}

			crbList := &rbacv1.ClusterRoleBindingList{}
			_ = cl.List(ctx, crbList, owner)
			for _, crb := range crbList.Items {
				_ = cl.Delete(ctx, &crb)
			}
		}
	}

	// delete cluster registrations that have no cluster
	for _, cr := range crList.Items {
		if cr.Status.ClusterName == "" {
			continue
		}

		clusterKey := types.NamespacedName{Namespace: cr.Namespace, Name: cr.Status.ClusterName}
		if _, found := seen[clusterKey]; !found {
			logger := logger.WithValues("namespace", cr.Namespace, "name", cr.Name)
			logger.Info("Deleting granted cluster registration without cluster")
			if err := cl.Delete(ctx, &cr); err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to delete cluster registration")
			}
			continue
		}
	}

	return nil
}

func GitJobs(ctx context.Context, cl client.Client, bs int) error {
	logger := log.FromContext(ctx).WithName("cleanup-jobs").WithValues("batchSize", bs)

	list := &batchv1.JobList{}
	if err := cl.List(ctx, list, client.Limit(bs)); err != nil {
		return err
	}
	logger.Info("Listing resources", "jobs", len(list.Items))

	jobs := list.Items

	for list.Continue != "" {
		if err := cl.List(ctx, list, client.Limit(bs), client.Continue(list.Continue)); err != nil {
			return err
		}
		logger.Info("Listing more resources", "jobs", len(list.Items))

		jobs = append(jobs, list.Items...)
	}

	if err := cleanupGitJobs(ctx, logger, cl, jobs); err != nil {
		return err
	}

	return nil
}

func cleanupGitJobs(ctx context.Context, logger logr.Logger, cl client.Client, jobs []batchv1.Job) error {
	// jobs by namespace, gitrepo
	gitjobs := map[string]map[string][]batchv1.Job{}
	// gitrepos with running jobs
	running := map[string]map[string]struct{}{}

	for _, job := range jobs {
		if job.OwnerReferences == nil {
			continue
		}
		for _, or := range job.OwnerReferences {
			if or.Kind == "GitRepo" && or.APIVersion == "fleet.cattle.io/v1alpha1" {
				if job.Status.Succeeded != 1 || job.Status.CompletionTime == nil {
					if running[job.Namespace] == nil {
						running[job.Namespace] = map[string]struct{}{}
					}
					running[job.Namespace][or.Name] = struct{}{}
				} else {
					if gitjobs[job.Namespace] == nil {
						gitjobs[job.Namespace] = map[string][]batchv1.Job{}
					}
					gitjobs[job.Namespace][or.Name] = append(gitjobs[job.Namespace][or.Name], job)
				}
				break
			}
		}
	}

	for ns, gitrepos := range gitjobs {
		for gitrepo, jobs := range gitrepos {
			sort.Slice(jobs, func(i, j int) bool {
				return jobs[j].Status.CompletionTime.Before(jobs[i].Status.CompletionTime)
			})

			// if there is a running job delete all the jobs in the
			// list, otherwise all but the newest
			start := 1
			if _, ok := running[ns][gitrepo]; ok {
				start = 0
			}

			logger.V(1).Info("Deleting jobs for gitrepo", "n", len(jobs)-start, "namespace", ns, "gitrepo", gitrepo)

			for i := start; i < len(jobs); i++ {
				job := jobs[i]
				logger.V(1).Info("Deleting job", "namespace", ns, "name", job.Name, "gitrepo", gitrepo)
				err := cl.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground))
				if err != nil && !apierrors.IsNotFound(err) {
					logger.Error(err, "Failed to delete job", "namespace", ns, "name", job.Name)
				}
			}
		}
	}
	return nil
}
