package cluster

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fleetclient "github.com/rancher/fleet/internal/client"
	"github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/desiredset"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/connection"
	ctrlApply "github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/apply"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/manageagent"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/scheduling"
	fleetns "github.com/rancher/fleet/internal/cmd/controller/namespace"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/wrangler/v3/pkg/randomtoken"
	"github.com/rancher/wrangler/v3/pkg/yaml"
)

// ClusterImportReconciler reconciles clusters for import (manager-initiated deployments)
type ClusterImportReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	SystemNamespace string
}

// +kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fleet.cattle.io,resources=clusterregistrationtokens,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=fleet.cattle.io,resources=bundles,verbs=delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;delete

// Reconcile handles cluster import operations
func (r *ClusterImportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cluster fleet.Cluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if manageagent.SkipCluster(&cluster) {
		return ctrl.Result{}, nil
	}

	// Handle OnChange logic: update client ID for manager-initiated deployments
	if cluster.Spec.KubeConfigSecret != "" && !agentDeployed(&cluster) && cluster.Spec.ClientID == "" {
		logger.Info("Updating ClientID for cluster import", "cluster", req.NamespacedName)
		clientID, err := randomtoken.Generate()
		if err != nil {
			return ctrl.Result{}, err
		}
		cluster.Spec.ClientID = clientID
		if err := r.Update(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Handle import logic (status handler equivalent)
	if cluster.Spec.KubeConfigSecret == "" || agentDeployed(&cluster) || cluster.Spec.ClientID == "" {
		return ctrl.Result{}, nil
	}

	// Import cluster
	if err := r.importCluster(ctx, &cluster); err != nil {
		logger.Error(err, "Failed to import cluster", "cluster", req.NamespacedName)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// importCluster handles the import process for manager-initiated deployments
func (r *ClusterImportReconciler) importCluster(ctx context.Context, cluster *fleet.Cluster) error {
	logger := log.FromContext(ctx)

	if shouldMigrateFromLegacyNamespace(cluster.Status.Agent.Namespace) {
		cluster.Status.CattleNamespaceMigrated = false
	}

	kubeConfigSecretNamespace := getKubeConfigSecretNS(cluster)
	logger.V(1).Info("Getting kubeconfig from secret", "namespace", kubeConfigSecretNamespace, "secret", cluster.Spec.KubeConfigSecret)

	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: kubeConfigSecretNamespace,
		Name:      cluster.Spec.KubeConfigSecret,
	}, &secret); err != nil {
		return err
	}

	logger.V(1).Info("Setting up agent with kubeconfig", "cluster", cluster.Namespace+"/"+cluster.Name)

	cfg := config.Get()
	apiServerURL := string(secret.Data[config.APIServerURLKey])
	apiServerCA := secret.Data[config.APIServerCAKey]

	if apiServerURL == "" {
		if len(cfg.APIServerURL) == 0 {
			logger.Info("Missing apiServerURL, cannot import cluster", "cluster", cluster.Namespace+"/"+cluster.Name)
			cluster.Status.AgentConfigChanged = false
			return r.Status().Update(ctx, cluster)
		}
		logger.V(1).Info("Using apiServerURL from fleet-controller config")
		apiServerURL = cfg.APIServerURL
	}

	if len(apiServerCA) == 0 {
		apiServerCA = cfg.APIServerCA
	}

	restConfig, err := r.restConfigFromKubeConfig(secret.Data[config.KubeConfigSecretValueKey], cfg.AgentTLSMode)
	if err != nil {
		return err
	}
	restConfig.Timeout = durations.RestConfigTimeout

	kc, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	if err := connection.SmokeTestKubeClientConnection(kc); err != nil {
		logger.Error(err, "Smoke test failed", "cluster", cluster.Namespace+"/"+cluster.Name)
		return err
	}

	// Create a scheme with all necessary Kubernetes types for the downstream cluster
	downstreamScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(downstreamScheme))
	utilruntime.Must(fleet.AddToScheme(downstreamScheme))

	// Create a controller-runtime client for the downstream cluster
	downstreamClient, err := client.New(restConfig, client.Options{
		Scheme: downstreamScheme,
	})
	if err != nil {
		return err
	}

	setID := desiredset.GetSetID(config.AgentBootstrapConfigName, "", cluster.Spec.AgentNamespace)
	apply := ctrlApply.NewApply(downstreamClient, setID).WithNoDeleteGVK(fleetns.GVK())

	tokenName := names.SafeConcatName(ImportTokenPrefix + cluster.Name)
	var token fleet.ClusterRegistrationToken
	err = r.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: tokenName}, &token)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Create token
			token = fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: cluster.Namespace,
					Name:      tokenName,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: fleet.SchemeGroupVersion.String(),
							Kind:       "Cluster",
							Name:       cluster.Name,
							UID:        cluster.UID,
						},
					},
				},
				Spec: fleet.ClusterRegistrationTokenSpec{
					TTL: &metav1.Duration{Duration: durations.ClusterImportTokenTTL},
				},
			}
			if err := r.Create(ctx, &token); err != nil && !apierrors.IsAlreadyExists(err) {
				logger.V(1).Info("Failed to create ClusterRegistrationToken, will retry", "token", tokenName)
				return err
			}
		} else {
			return err
		}
	}

	agentNamespace := r.SystemNamespace
	if cluster.Spec.AgentNamespace != "" {
		agentNamespace = cluster.Spec.AgentNamespace
	}

	clusterLabels := yaml.CleanAnnotationsForExport(cluster.Labels)
	agentReplicas := cmd.ParseEnvAgentReplicaCount()

	var (
		objs              []runtime.Object
		priorityClassName string
	)
	if sc := cluster.Spec.AgentSchedulingCustomization; sc != nil {
		if sc.PriorityClass != nil {
			priorityClassName = scheduling.FleetAgentPriorityClassName
			objs = append(objs, scheduling.PriorityClass(sc.PriorityClass))
		}

		if sc.PodDisruptionBudget != nil {
			pdb, err := scheduling.PodDisruptionBudget(agentNamespace, sc.PodDisruptionBudget)
			if err != nil {
				return err
			}
			objs = append(objs, pdb)
		}
	}

	agentObjs, err := agent.AgentWithConfig(
		ctx, agentNamespace, r.SystemNamespace,
		cluster.Spec.AgentNamespace,
		&fleetclient.Getter{Namespace: cluster.Namespace},
		token.Name,
		&agent.Options{
			APIServerCA:  apiServerCA,
			APIServerURL: apiServerURL,
			ConfigOptions: agent.ConfigOptions{
				ClientID:                  cluster.Spec.ClientID,
				Labels:                    clusterLabels,
				AgentTLSMode:              cfg.AgentTLSMode,
				GarbageCollectionInterval: cfg.GarbageCollectionInterval,
			},
			ManifestOptions: agent.ManifestOptions{
				AgentEnvVars:      cluster.Spec.AgentEnvVars,
				AgentTolerations:  cluster.Spec.AgentTolerations,
				PrivateRepoURL:    cluster.Spec.PrivateRepoURL,
				AgentAffinity:     cluster.Spec.AgentAffinity,
				AgentResources:    cluster.Spec.AgentResources,
				HostNetwork:       *cmp.Or(cluster.Spec.HostNetwork, ptr.To(false)),
				AgentReplicas:     agentReplicas,
				PriorityClassName: priorityClassName,
			},
		})
	objs = append(objs, agentObjs...)
	if err != nil {
		return err
	}

	if cluster.Status.Agent.Namespace != agentNamespace || !cluster.Status.AgentNamespaceMigrated {
		// delete old agent bundle
		var bundle fleet.Bundle
		bundleName := names.SafeConcatName(manageagent.AgentBundleName, cluster.Name)
		if err := r.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: bundleName}, &bundle); err == nil {
			if err := r.Delete(ctx, &bundle); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}

		if cluster.Status.Agent.Namespace != "" {
			if err := r.deleteOldAgent(ctx, kc, cluster.Status.Agent.Namespace); err != nil {
				return err
			}
		}
	}

	if err := r.deleteOldAgent(ctx, kc, agentNamespace); err != nil {
		return err
	}

	if err := apply.ApplyObjects(ctx, objs...); err != nil {
		logger.Error(err, "Failed to create agent deployment", "cluster", cluster.Namespace+"/"+cluster.Name)
		return err
	}
	logger.Info("Deployed new agent", "cluster", cluster.Namespace+"/"+cluster.Name)

	if r.SystemNamespace != config.DefaultNamespace {
		_, err := kc.CoreV1().Namespaces().Get(ctx, config.DefaultNamespace, metav1.GetOptions{})
		if err == nil {
			logger.Info("Cleaning up leftover objects in default namespace")
			if err := r.deleteOldAgent(ctx, kc, config.DefaultNamespace); err != nil {
				return err
			}
		} else if !apierrors.IsNotFound(err) {
			return err
		}

		err = kc.CoreV1().Namespaces().Delete(ctx, fleetns.SystemRegistrationNamespace(config.DefaultNamespace), metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	// Update status
	cluster.Status.AgentDeployedGeneration = &cluster.Spec.RedeployAgentGeneration
	cluster.Status.AgentMigrated = true
	cluster.Status.CattleNamespaceMigrated = true
	cluster.Status.Agent = fleet.AgentStatus{
		Namespace: cluster.Spec.AgentNamespace,
	}
	cluster.Status.AgentNamespaceMigrated = true
	cluster.Status.AgentConfigChanged = false
	cluster.Status.APIServerURL = apiServerURL
	caHash, err := hashStatusField(apiServerCA)
	if err != nil {
		return err
	}
	cluster.Status.APIServerCAHash = caHash
	cluster.Status.AgentTLSMode = cfg.AgentTLSMode
	cluster.Status.GarbageCollectionInterval = &cfg.GarbageCollectionInterval

	return r.Status().Update(ctx, cluster)
}

func (r *ClusterImportReconciler) deleteOldAgent(ctx context.Context, kc kubernetes.Interface, namespace string) error {
	err := kc.CoreV1().Secrets(namespace).Delete(ctx, config.AgentConfigName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	err = kc.CoreV1().Secrets(namespace).Delete(ctx, config.AgentBootstrapConfigName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if err := kc.AppsV1().StatefulSets(namespace).Delete(ctx, config.AgentConfigName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := kc.AppsV1().Deployments(namespace).Delete(ctx, config.AgentConfigName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := kc.SchedulingV1().PriorityClasses().Delete(ctx, scheduling.FleetAgentPriorityClassName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := kc.PolicyV1().PodDisruptionBudgets(namespace).Delete(ctx, scheduling.FleetAgentPodDisruptionBudgetName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	logrus.Infof("Deleted old agent in namespace %s", namespace)
	return nil
}

func (r *ClusterImportReconciler) restConfigFromKubeConfig(data []byte, agentTLSMode string) (*rest.Config, error) {
	if agentTLSMode != config.AgentTLSModeStrict && agentTLSMode != config.AgentTLSModeSystemStore {
		return nil, fmt.Errorf(
			"provided config value for agentTLSMode is none of [%q,%q]",
			config.AgentTLSModeStrict,
			config.AgentTLSModeSystemStore,
		)
	}

	clientConfig, err := clientcmd.NewClientConfigFromBytes(data)
	if err != nil {
		return nil, err
	}

	raw, err := clientConfig.RawConfig()
	if err != nil {
		return nil, err
	}

	if agentTLSMode == config.AgentTLSModeSystemStore && raw.Contexts[raw.CurrentContext] != nil {
		cluster := raw.Contexts[raw.CurrentContext].Cluster
		if raw.Clusters[cluster] != nil {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, raw.Clusters[cluster].Server, nil)
			if err == nil {
				if resp, err := http.DefaultClient.Do(req); err == nil {
					resp.Body.Close()
					raw.Clusters[cluster].CertificateAuthorityData = nil
				}
			}
		}
	}

	return clientcmd.NewDefaultClientConfig(raw, &clientcmd.ConfigOverrides{}).ClientConfig()
}

// SetupWithManager sets up the controller with the Manager
func (r *ClusterImportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Watch Clusters
	builder := ctrl.NewControllerManagedBy(mgr).
		Named("cluster-import").
		For(&fleet.Cluster{})

	// Watch Secrets (kubeconfig secrets) and trigger cluster reconciliation
	builder = builder.Watches(
		&corev1.Secret{},
		ctrlhandler.EnqueueRequestsFromMapFunc(r.findClustersForSecret),
	)

	// Register config change callback
	config.OnChange(context.Background(), func(cfg *config.Config) error {
		return r.onConfigChange(context.Background(), cfg)
	})

	return builder.Complete(r)
}

// findClustersForSecret maps Secret changes to Cluster reconcile requests
func (r *ClusterImportReconciler) findClustersForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	// Find clusters that reference this secret
	var clusterList fleet.ClusterList
	if err := r.List(ctx, &clusterList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	secretKey := secret.Namespace + "/" + secret.Name

	for _, cluster := range clusterList.Items {
		if cluster.Spec.KubeConfigSecret == "" {
			continue
		}
		clusterSecretNS := getKubeConfigSecretNS(&cluster)
		clusterSecretKey := clusterSecretNS + "/" + cluster.Spec.KubeConfigSecret

		if clusterSecretKey == secretKey {
			if err := r.checkForConfigChange(ctx, config.Get(), &cluster, secret); err != nil {
				logrus.WithError(err).Errorf("cluster %s/%s: could not check for config changes", cluster.Namespace, cluster.Name)
			}
		}
	}

	return requests
}

func (r *ClusterImportReconciler) onConfigChange(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config is nil: this should never happen")
	}

	var clusterList fleet.ClusterList
	if err := r.List(ctx, &clusterList); err != nil {
		return err
	}

	for _, cluster := range clusterList.Items {
		if cluster.Spec.KubeConfigSecret == "" {
			continue
		}

		var secret corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: getKubeConfigSecretNS(&cluster),
			Name:      cluster.Spec.KubeConfigSecret,
		}, &secret); err != nil {
			return fmt.Errorf("cluster %s/%s: could not check for config changes: %w", cluster.Namespace, cluster.Name, err)
		}

		if err := r.checkForConfigChange(ctx, cfg, &cluster, &secret); err != nil {
			logrus.WithError(err).Warnf("cluster %s/%s: could not check for config changes", cluster.Namespace, cluster.Name)
			continue
		}
	}
	return nil
}

func (r *ClusterImportReconciler) checkForConfigChange(ctx context.Context, cfg *config.Config, cluster *fleet.Cluster, secret *corev1.Secret) error {
	if cluster.Status.AgentConfigChanged {
		return nil
	}

	apiServerConfigChanged, err := r.hasAPIServerConfigChanged(cfg, secret, cluster)
	if err != nil {
		if errors.Is(err, errUnavailableAPIServerURL) {
			logrus.WithError(err).Warnf("cluster %s/%s: could not check for config changes", cluster.Namespace, cluster.Name)
			return nil
		}
		return err
	}

	hasConfigChanged := apiServerConfigChanged ||
		cfg.AgentTLSMode != cluster.Status.AgentTLSMode ||
		hasGarbageCollectionIntervalChanged(cfg, cluster)

	if !hasConfigChanged {
		return nil
	}

	logrus.Infof("API server config changed, trigger cluster import for cluster %s/%s", cluster.Namespace, cluster.Name)
	cluster.Status.AgentConfigChanged = true
	return r.Status().Update(ctx, cluster)
}

func (r *ClusterImportReconciler) hasAPIServerConfigChanged(cfg *config.Config, secret *corev1.Secret, cluster *fleet.Cluster) (bool, error) {
	var secretAPIServerCA, secretAPIServerURL []byte
	if secret != nil {
		secretAPIServerURL = secret.Data[config.APIServerURLKey]
		secretAPIServerCA = secret.Data[config.APIServerCAKey]
	}

	if len(secretAPIServerURL) == 0 && len(cfg.APIServerURL) == 0 {
		return false, errUnavailableAPIServerURL
	}

	hasURLChanged := len(secretAPIServerURL) == 0 && cfg.APIServerURL != cluster.Status.APIServerURL
	caHash, err := hashStatusField(cfg.APIServerCA)
	if err != nil {
		return false, err
	}
	hasCAChanged := len(secretAPIServerCA) == 0 && caHash != cluster.Status.APIServerCAHash

	return hasURLChanged || hasCAChanged, nil
}
