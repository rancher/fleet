// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/pkg/sharding"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// GitRepoMonitorReconciler monitors GitRepo reconciliations
type GitRepoMonitorReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	ShardID string
	Workers int

	// Cache to store previous state
	cache *ObjectCache

	// Per-controller logging mode
	DetailedLogs   bool
	EventFilters   EventTypeFilters
	ResourceFilter *ResourceFilter
}

// SetupWithManager sets up the controller - mirrors GitJobReconciler.SetupWithManager
func (r *GitRepoMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.cache = NewObjectCache()

	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.GitRepo{},
			builder.WithPredicates(
				// do not trigger for GitRepo status changes (except for commit changes and cache sync)
				predicate.Or(
					TypedResourceVersionUnchangedPredicate[client.Object]{},
					predicate.GenerationChangedPredicate{},
					// Use nonSecretAnnotationChangedPredicate instead of predicate.AnnotationChangedPredicate
					// to avoid redundant reconciles when the controller updates secret data hash
					// tracking annotations (e.g., fleet.cattle.io/client-secret-hash).
					nonSecretAnnotationChangedPredicate(),
					predicate.LabelChangedPredicate{},
					commitChangedPredicate(),
				),
			),
		).
		Owns(&batchv1.Job{}, builder.WithPredicates(jobUpdatedPredicate())).
		Watches(
			// Fan out from secret to gitrepo, reconcile gitrepos when a secret
			// referenced in ClientSecretName, HelmSecretName, or HelmSecretNameForPaths changes.
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.secretMapFunc()),
			builder.WithPredicates(secretDataChangedPredicate()),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// secretMapFunc returns a function that maps a Secret to GitRepos that reference it.
// Mirrors production gitjob_controller.go secretMapFunc.
func (r *GitRepoMonitorReconciler) secretMapFunc() func(ctx context.Context, obj client.Object) []ctrl.Request {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		logger := log.FromContext(ctx).WithName("secret-watch")
		secretName := obj.GetName()
		namespace := obj.GetNamespace()

		// Use a map to deduplicate requests (same GitRepo might reference secret in multiple fields)
		seen := make(map[types.NamespacedName]struct{})
		requests := make([]ctrl.Request, 0)

		addRequest := func(gitRepo *fleet.GitRepo) {
			if !sharding.ShouldProcess(gitRepo, r.ShardID) {
				return
			}
			if !r.ResourceFilter.Matches(gitRepo.Namespace, gitRepo.Name) {
				return
			}
			key := types.NamespacedName{
				Namespace: gitRepo.Namespace,
				Name:      gitRepo.Name,
			}
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				requests = append(requests, ctrl.Request{NamespacedName: key})
			}
		}

		// Find GitRepos using this secret as ClientSecretName
		gitRepoList := &fleet.GitRepoList{}
		if err := r.List(ctx, gitRepoList,
			client.InNamespace(namespace),
			client.MatchingFields{config.GitRepoClientSecretNameIndex: secretName},
		); err != nil {
			logger.V(1).Error(err, "Failed to list GitRepos by ClientSecretName", "secret", secretName)
		} else {
			for i := range gitRepoList.Items {
				addRequest(&gitRepoList.Items[i])
			}
		}

		// Find GitRepos using this secret as HelmSecretName
		gitRepoList = &fleet.GitRepoList{}
		if err := r.List(ctx, gitRepoList,
			client.InNamespace(namespace),
			client.MatchingFields{config.GitRepoHelmSecretNameIndex: secretName},
		); err != nil {
			logger.V(1).Error(err, "Failed to list GitRepos by HelmSecretName", "secret", secretName)
		} else {
			for i := range gitRepoList.Items {
				addRequest(&gitRepoList.Items[i])
			}
		}

		// Find GitRepos using this secret as HelmSecretNameForPaths
		gitRepoList = &fleet.GitRepoList{}
		if err := r.List(ctx, gitRepoList,
			client.InNamespace(namespace),
			client.MatchingFields{config.GitRepoHelmSecretNameForPathsIndex: secretName},
		); err != nil {
			logger.V(1).Error(err, "Failed to list GitRepos by HelmSecretNameForPaths", "secret", secretName)
		} else {
			for i := range gitRepoList.Items {
				addRequest(&gitRepoList.Items[i])
			}
		}

		return requests
	}
}

// Reconcile monitors GitRepo reconciliation events (READ-ONLY)
func (r *GitRepoMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Check resource filter - skip if resource doesn't match
	if !r.ResourceFilter.Matches(req.Namespace, req.Name) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx).WithName("gitrepo-monitor")
	logger = logger.WithValues(
		"gitrepo", req.NamespacedName.String(),
		"mode", LogMode(r.DetailedLogs))
	ctx = log.IntoContext(ctx, logger)

	gitrepo := &fleet.GitRepo{}
	if err := r.Get(ctx, req.NamespacedName, gitrepo); err != nil {
		if apierrors.IsNotFound(err) {
			logNotFound(logger, r.DetailedLogs, r.EventFilters, "GitRepo", req.Namespace, req.Name)
			r.cache.Delete(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Add more context to logger
	logger = logger.WithValues("generation", gitrepo.Generation, "commit", gitrepo.Status.Commit)
	if gitrepo.Labels[fleet.RepoLabel] != "" {
		logger = logger.WithValues("repo", gitrepo.Labels[fleet.RepoLabel])
	}
	ctx = log.IntoContext(ctx, logger)

	// Check for deletion
	if !gitrepo.DeletionTimestamp.IsZero() {
		logDeletion(logger, r.DetailedLogs, r.EventFilters, "GitRepo", gitrepo.Namespace, gitrepo.Name, gitrepo.DeletionTimestamp.String())
		r.cache.Delete(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Retrieve old object from cache
	oldGitRepo, exists := r.cache.Get(req.NamespacedName)
	if !exists {
		logCreate(logger, r.DetailedLogs, r.EventFilters, "GitRepo", gitrepo.Namespace, gitrepo.Name, gitrepo.Generation, gitrepo.ResourceVersion)
		r.cache.Set(req.NamespacedName, gitrepo.DeepCopy())
		return ctrl.Result{}, nil
	}

	oldGitRepoTyped := oldGitRepo.(*fleet.GitRepo)

	// Detect what changed
	logSpecChange(logger, r.DetailedLogs, r.EventFilters, "GitRepo", gitrepo.Namespace, gitrepo.Name, oldGitRepoTyped.Spec, gitrepo.Spec, oldGitRepoTyped.Generation, gitrepo.Generation)
	logStatusChange(logger, r.DetailedLogs, r.EventFilters, "GitRepo", gitrepo.Namespace, gitrepo.Name, oldGitRepoTyped.Status, gitrepo.Status)
	logResourceVersionChangeWithMetadata(logger, r.DetailedLogs, r.EventFilters, "GitRepo", gitrepo.Namespace, gitrepo.Name, oldGitRepoTyped, gitrepo)
	logAnnotationChange(logger, r.DetailedLogs, r.EventFilters, "GitRepo", gitrepo.Namespace, gitrepo.Name, oldGitRepoTyped.Annotations, gitrepo.Annotations)
	logLabelChange(logger, r.DetailedLogs, r.EventFilters, "GitRepo", gitrepo.Namespace, gitrepo.Name, oldGitRepoTyped.Labels, gitrepo.Labels)

	// Log specific GitRepo changes (only in detailed mode)
	if r.DetailedLogs {
		if oldGitRepoTyped.Spec.Repo != gitrepo.Spec.Repo {
			logger.Info("Repository URL changed",
				"event", "repo-change",
				"oldRepo", oldGitRepoTyped.Spec.Repo,
				"newRepo", gitrepo.Spec.Repo,
			)
		}
		if oldGitRepoTyped.Spec.Branch != gitrepo.Spec.Branch {
			logger.Info("Branch changed",
				"event", "branch-change",
				"oldBranch", oldGitRepoTyped.Spec.Branch,
				"newBranch", gitrepo.Spec.Branch,
			)
		}
		if oldGitRepoTyped.Spec.Revision != gitrepo.Spec.Revision {
			logger.Info("Revision changed",
				"event", "revision-change",
				"oldRevision", oldGitRepoTyped.Spec.Revision,
				"newRevision", gitrepo.Spec.Revision,
			)
		}
		if oldGitRepoTyped.Status.Commit != gitrepo.Status.Commit {
			logger.Info("Commit changed",
				"event", "commit-change",
				"oldCommit", oldGitRepoTyped.Status.Commit,
				"newCommit", gitrepo.Status.Commit,
			)
		}
		if oldGitRepoTyped.Status.WebhookCommit != gitrepo.Status.WebhookCommit {
			logger.Info("Webhook commit changed",
				"event", "webhook-commit-change",
				"oldWebhookCommit", oldGitRepoTyped.Status.WebhookCommit,
				"newWebhookCommit", gitrepo.Status.WebhookCommit,
			)
		}
		if oldGitRepoTyped.Spec.ForceSyncGeneration != gitrepo.Spec.ForceSyncGeneration {
			logger.Info("ForceSyncGeneration changed",
				"event", "force-sync-change",
				"oldForceSyncGeneration", oldGitRepoTyped.Spec.ForceSyncGeneration,
				"newForceSyncGeneration", gitrepo.Spec.ForceSyncGeneration,
			)
		}
	}

	// Update cache with new state
	r.cache.Set(req.NamespacedName, gitrepo.DeepCopy())

	return ctrl.Result{}, nil
}
