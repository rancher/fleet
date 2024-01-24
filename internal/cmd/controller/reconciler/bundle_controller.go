// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type BundleQuery interface {
	// BundlesForCluster is used to map from a cluster to bundles
	BundlesForCluster(context.Context, *fleet.Cluster) ([]*fleet.Bundle, []*fleet.Bundle, error)
}

type Store interface {
	Store(context.Context, *manifest.Manifest) (string, error)
}

type TargetBuilder interface {
	Targets(ctx context.Context, bundle *fleet.Bundle, manifestID string) ([]*target.Target, error)
}

// BundleReconciler reconciles a Bundle object
type BundleReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Builder TargetBuilder
	Store   Store
	Query   BundleQuery
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundles/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundles/finalizers,verbs=update

// Reconcile creates bundle deployments for a bundle
func (r *BundleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("bundle")
	ctx = log.IntoContext(ctx, logger)

	bundle := &fleet.Bundle{}
	err := r.Get(ctx, req.NamespacedName, bundle)
	if apierrors.IsNotFound(err) {
		logger.V(1).Info("Bundle not found, purging bundle deployments")
		if err := purgeBundleDeployments(ctx, r.Client, req.NamespacedName); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}
	logger.V(1).Info("Reconciling bundle, checking targets, calculating changes, building objects", "generation", bundle.Generation, "observedGeneration", bundle.Status.ObservedGeneration)

	manifest := manifest.FromBundle(bundle)
	if bundle.Generation != bundle.Status.ObservedGeneration {
		manifest.ResetSHASum()
	}

	manifestDigest, err := manifest.SHASum()
	if err != nil {
		return ctrl.Result{}, err
	}
	bundle.Status.ResourcesSHA256Sum = manifestDigest

	// this does not need to happen after merging the
	// BundleDeploymentOptions, since 'fleet apply' already put the right
	// resources into bundle.Spec.Resources
	manifestID, err := r.Store.Store(ctx, manifest)
	if err != nil {
		return ctrl.Result{}, err
	}

	matchedTargets, err := r.Builder.Targets(ctx, bundle, manifestID)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := resetStatus(&bundle.Status, matchedTargets); err != nil {
		updateDisplay(&bundle.Status)
		return ctrl.Result{}, err
	}

	// this will add the defaults for a new bundledeployment
	if err := target.UpdatePartitions(&bundle.Status, matchedTargets); err != nil {
		updateDisplay(&bundle.Status)
		return ctrl.Result{}, err
	}

	if bundle.Status.ObservedGeneration != bundle.Generation {
		if err := setResourceKey(context.Background(), &bundle.Status, bundle, manifest, r.isNamespaced); err != nil {
			updateDisplay(&bundle.Status)
			return ctrl.Result{}, err
		}
	}

	summary.SetReadyConditions(&bundle.Status, "Cluster", bundle.Status.Summary)
	bundle.Status.ObservedGeneration = bundle.Generation

	// build BundleDeployments out of targets discarding Status, replacing
	// DependsOn with the bundle's DependsOn (pure function) and replacing
	// the labels with the bundle's labels
	for _, target := range matchedTargets {
		if target.Deployment == nil {
			continue
		}
		if target.Deployment.Namespace == "" {
			logger.V(1).Info("Skipping bundledeployment with empty namespace, waiting for agentmanagement to set cluster.status.namespace", "bundledeployment", target.Deployment)
			continue
		}

		// NOTE we don't use the existing BundleDeployment, we discard annotations, status, etc
		// copy labels from Bundle as they might have changed
		bd := &fleet.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      target.Deployment.Name,
				Namespace: target.Deployment.Namespace,
				Labels:    target.BundleDeploymentLabels(target.Cluster.Namespace, target.Cluster.Name),
			},
			Spec: target.Deployment.Spec,
		}
		bd.Spec.Paused = target.IsPaused()
		bd.Spec.DependsOn = bundle.Spec.DependsOn
		bd.Spec.CorrectDrift = target.Options.CorrectDrift

		updated := bd.DeepCopy()
		op, err := controllerutil.CreateOrUpdate(ctx, r.Client, bd, func() error {
			bd.Spec = updated.Spec
			bd.Labels = updated.GetLabels()
			return nil
		})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			logger.Error(err, "Reconcile failed to create or update bundledeployment", "bundledeployment", bd, "operation", op)
			return ctrl.Result{}, err
		}
		logger.V(1).Info(upper(op)+" bundledeployment", "bundledeployment", bd, "operation", op)
	}

	updateDisplay(&bundle.Status)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.Bundle{}
		err := r.Get(ctx, req.NamespacedName, t)
		if err != nil {
			return err
		}
		t.Status = bundle.Status
		return r.Status().Update(ctx, t)
	})
	if err != nil {
		logger.V(1).Error(err, "Reconcile failed final update to bundle status", "status", bundle.Status)
	}

	return ctrl.Result{}, err
}

func (r *BundleReconciler) isNamespaced(gvk schema.GroupVersionKind) bool {
	mapping, err := r.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return true
	}
	return mapping.Scope.Name() == meta.RESTScopeNameNamespace
}

func upper(op controllerutil.OperationResult) string {
	switch op {
	case controllerutil.OperationResultNone:
		return "Unchanged"
	case controllerutil.OperationResultCreated:
		return "Created"
	case controllerutil.OperationResultUpdated:
		return "Updated"
	case controllerutil.OperationResultUpdatedStatus:
		return "Updated"
	case controllerutil.OperationResultUpdatedStatusOnly:
		return "Updated"
	default:
		return "Unknown"
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *BundleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.Bundle{}).
		// Note: Maybe improve with WatchesMetadata, does it have access to labels?
		Watches(
			// Fan out from bundledeployment to bundle
			&fleet.BundleDeployment{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
				bd := a.(*fleet.BundleDeployment)
				labels := bd.GetLabels()
				if labels == nil {
					return nil
				}

				ns, name := target.BundleFromDeployment(labels)
				if ns != "" && name != "" {
					return []ctrl.Request{{
						NamespacedName: types.NamespacedName{
							Namespace: ns,
							Name:      name,
						},
					}}
				}

				return nil
			}),
			builder.WithPredicates(bundleDeploymentStatusChangedPredicate()),
		).
		Watches(
			// Fan out from cluster to bundle
			&fleet.Cluster{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
				cluster := a.(*fleet.Cluster)
				bundlesToRefresh, _, err := r.Query.BundlesForCluster(ctx, cluster)
				if err != nil {
					return nil
				}
				requests := []ctrl.Request{}
				for _, bundle := range bundlesToRefresh {
					requests = append(requests, ctrl.Request{
						NamespacedName: types.NamespacedName{
							Namespace: bundle.Namespace,
							Name:      bundle.Name,
						},
					})
				}

				return requests
			}),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Complete(r)
}
