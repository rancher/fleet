package manageagent

import (
	"cmp"
	"context"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/scheduling"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/yaml"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ManageAgentReconciler reconciles Cluster resources and manages agent bundles
type ManageAgentReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	SystemNamespace string
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile updates agent bundles for clusters in the namespace
func (r *ManageAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Get the namespace
	var ns corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: req.Name}, &ns); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cfg := config.Get()
	// managed agents are disabled, so we don't need to create the bundle
	if cfg.ManageAgent != nil && !*cfg.ManageAgent {
		return ctrl.Result{}, nil
	}

	// List all clusters in the namespace
	var clusterList fleet.ClusterList
	if err := r.List(ctx, &clusterList, client.InNamespace(req.Name)); err != nil {
		return ctrl.Result{}, err
	}

	if len(clusterList.Items) == 0 {
		return ctrl.Result{}, nil
	}

	// Update agent bundles for all clusters
	for _, cluster := range clusterList.Items {
		if SkipCluster(&cluster) {
			continue
		}

		logrus.Infof("Update agent bundle for cluster %s/%s", cluster.Namespace, cluster.Name)

		bundle, err := r.newAgentBundle(req.Name, &cluster)
		if err != nil {
			logrus.Errorf("Failed to update agent bundle for cluster %s/%s: %v", cluster.Namespace, cluster.Name, err)
			return ctrl.Result{}, err
		}

		// Create or update the bundle
		if err := r.createOrUpdateBundle(ctx, bundle, &ns); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ManageAgentReconciler) createOrUpdateBundle(ctx context.Context, bundle *fleet.Bundle, owner client.Object) error {
	// Set namespace as owner
	if err := ctrl.SetControllerReference(owner, bundle, r.Scheme); err != nil {
		return err
	}

	var existing fleet.Bundle
	err := r.Get(ctx, types.NamespacedName{
		Namespace: bundle.Namespace,
		Name:      bundle.Name,
	}, &existing)

	if err == nil {
		// Update existing bundle
		bundle.ResourceVersion = existing.ResourceVersion
		return r.Update(ctx, bundle)
	}

	if apierrors.IsNotFound(err) {
		// Create new bundle
		return r.Create(ctx, bundle)
	}

	return err
}

func (r *ManageAgentReconciler) newAgentBundle(ns string, cluster *fleet.Cluster) (*fleet.Bundle, error) {
	cfg := config.Get()
	agentNamespace := r.SystemNamespace
	if cluster.Spec.AgentNamespace != "" {
		agentNamespace = cluster.Spec.AgentNamespace
	}

	agentReplicas := cmd.ParseEnvAgentReplicaCount()
	leaderElectionOptions, err := cmd.NewLeaderElectionOptionsWithPrefix("FLEET_AGENT")
	if err != nil {
		return nil, err
	}

	priorityClassName := ""
	if cluster.Spec.AgentSchedulingCustomization != nil && cluster.Spec.AgentSchedulingCustomization.PriorityClass != nil {
		priorityClassName = scheduling.FleetAgentPriorityClassName
	}

	if cluster.Spec.AgentTolerations != nil {
		sortTolerations(cluster.Spec.AgentTolerations)
	}

	objs := agent.Manifest(
		agentNamespace, cluster.Spec.AgentNamespace,
		agent.ManifestOptions{
			AgentEnvVars:     cluster.Spec.AgentEnvVars,
			AgentTolerations: cluster.Spec.AgentTolerations,
			PrivateRepoURL:   cluster.Spec.PrivateRepoURL,
			AgentAffinity:    cluster.Spec.AgentAffinity,
			AgentResources:   cluster.Spec.AgentResources,
			HostNetwork:      *cmp.Or(cluster.Spec.HostNetwork, ptr.To(false)),

			AgentImage:              cfg.AgentImage,
			AgentImagePullPolicy:    cfg.AgentImagePullPolicy,
			CheckinInterval:         cfg.AgentCheckinInterval.Duration.String(),
			SystemDefaultRegistry:   cfg.SystemDefaultRegistry,
			BundleDeploymentWorkers: cfg.AgentWorkers.BundleDeployment,
			DriftWorkers:            cfg.AgentWorkers.Drift,
			AgentReplicas:           agentReplicas,
			LeaderElectionOptions:   leaderElectionOptions,
			PriorityClassName:       priorityClassName,
		},
	)

	agentYAML, err := yaml.Export(objs...)
	if err != nil {
		return nil, err
	}

	return &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SafeConcatName(AgentBundleName, cluster.Name),
			Namespace: ns,
		},
		Spec: fleet.BundleSpec{
			BundleDeploymentOptions: fleet.BundleDeploymentOptions{
				DefaultNamespace: agentNamespace,
				Helm: &fleet.HelmOptions{
					TakeOwnership: true,
				},
			},
			Resources: []fleet.BundleResource{
				{
					Name:    "agent.yaml",
					Content: string(agentYAML),
				},
			},
			Targets: []fleet.BundleTarget{
				{
					ClusterSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "fleet.cattle.io/non-managed-agent",
								Operator: metav1.LabelSelectorOpDoesNotExist,
							},
						},
					},
					ClusterName: cluster.Name,
				},
			},
		},
	}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *ManageAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("manageagent").
		For(&corev1.Namespace{}).
		Watches(
			&fleet.Cluster{},
			ctrlhandler.EnqueueRequestsFromMapFunc(r.findNamespaceForCluster),
		).
		Complete(r)
}

func (r *ManageAgentReconciler) findNamespaceForCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	cluster, ok := obj.(*fleet.Cluster)
	if !ok {
		return nil
	}

	// Check if bundle exists
	var bundle fleet.Bundle
	err := r.Get(ctx, types.NamespacedName{
		Namespace: cluster.Namespace,
		Name:      names.SafeConcatName(AgentBundleName, cluster.Name),
	}, &bundle)

	// If bundle doesn't exist or can't be retrieved, enqueue the namespace
	if err != nil {
		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Name: cluster.Namespace}},
		}
	}

	return nil
}

// ClusterStatusReconciler reconciles Cluster status for agent configuration
type ClusterStatusReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	SystemNamespace string
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters/status,verbs=get;update;patch

// Reconcile handles Cluster status updates for agent configuration
func (r *ClusterStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster fleet.Cluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if SkipCluster(&cluster) {
		return ctrl.Result{}, nil
	}

	logrus.Debugf("Reconciling agent settings for cluster %s/%s", cluster.Namespace, cluster.Name)

	originalStatus := cluster.Status.DeepCopy()

	// Reconcile agent environment variables
	varsChanged, err := r.reconcileAgentEnvVars(&cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Update cluster status fields
	statusChanged, err := r.updateClusterStatus(&cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Update status if changed
	if varsChanged || statusChanged {
		cluster.Status.AgentConfigChanged = true
		if err := r.Status().Update(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}

		// If status changed, we need to update the status
		if cluster.Status.AgentConfigChanged != originalStatus.AgentConfigChanged ||
			cluster.Status.AgentEnvVarsHash != originalStatus.AgentEnvVarsHash ||
			cluster.Status.AgentPrivateRepoURL != originalStatus.AgentPrivateRepoURL {
			logrus.Infof("Agent configuration changed for cluster %s/%s", cluster.Namespace, cluster.Name)
		}
	}

	return ctrl.Result{}, nil
}

func (r *ClusterStatusReconciler) reconcileAgentEnvVars(cluster *fleet.Cluster) (bool, error) {
	if len(cluster.Spec.AgentEnvVars) < 1 {
		if cluster.Status.AgentEnvVarsHash != "" {
			cluster.Status.AgentEnvVarsHash = ""
			return true, nil
		}
		return false, nil
	}

	hash, err := hashStatusField(cluster.Spec.AgentEnvVars)
	if err != nil {
		return false, err
	}

	if cluster.Status.AgentEnvVarsHash != hash {
		cluster.Status.AgentEnvVarsHash = hash
		return true, nil
	}

	return false, nil
}

func (r *ClusterStatusReconciler) updateClusterStatus(cluster *fleet.Cluster) (bool, error) {
	changed := false

	if cluster.Status.AgentPrivateRepoURL != cluster.Spec.PrivateRepoURL {
		cluster.Status.AgentPrivateRepoURL = cluster.Spec.PrivateRepoURL
		changed = true
	}

	if hostNetwork := *cmp.Or(cluster.Spec.HostNetwork, ptr.To(false)); cluster.Status.AgentHostNetwork != hostNetwork {
		cluster.Status.AgentHostNetwork = hostNetwork
		changed = true
	}

	if c, hash, err := hashChanged(cluster.Spec.AgentSchedulingCustomization, cluster.Status.AgentSchedulingCustomizationHash); err != nil {
		return changed, err
	} else if c {
		cluster.Status.AgentSchedulingCustomizationHash = hash
		changed = true
	}

	if c, hash, err := hashChanged(cluster.Spec.AgentAffinity, cluster.Status.AgentAffinityHash); err != nil {
		return changed, err
	} else if c {
		cluster.Status.AgentAffinityHash = hash
		changed = true
	}

	if c, hash, err := hashChanged(cluster.Spec.AgentResources, cluster.Status.AgentResourcesHash); err != nil {
		return changed, err
	} else if c {
		cluster.Status.AgentResourcesHash = hash
		changed = true
	}

	if c, hash, err := hashChanged(cluster.Spec.AgentTolerations, cluster.Status.AgentTolerationsHash); err != nil {
		return changed, err
	} else if c {
		cluster.Status.AgentTolerationsHash = hash
		changed = true
	}

	return changed, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *ClusterStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("manageagent-status").
		For(&fleet.Cluster{}).
		Complete(r)
}
