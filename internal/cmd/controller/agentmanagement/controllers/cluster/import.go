package cluster

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/client"
	"github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/desiredset"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/connection"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/manageagent"
	fleetns "github.com/rancher/fleet/internal/cmd/controller/namespace"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v3/pkg/randomtoken"
	"github.com/rancher/wrangler/v3/pkg/yaml"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
)

var (
	ImportTokenPrefix = "import-token-"
)

type importHandler struct {
	ctx                 context.Context
	systemNamespace     string
	secrets             corecontrollers.SecretCache
	clusters            fleetcontrollers.ClusterController
	tokens              fleetcontrollers.ClusterRegistrationTokenCache
	tokenClient         fleetcontrollers.ClusterRegistrationTokenClient
	bundleClient        fleetcontrollers.BundleClient
	namespaceController corecontrollers.NamespaceController
}

func RegisterImport(
	ctx context.Context,
	systemNamespace string,
	secrets corecontrollers.SecretCache,
	clusters fleetcontrollers.ClusterController,
	tokens fleetcontrollers.ClusterRegistrationTokenController,
	bundles fleetcontrollers.BundleClient,
	namespaceController corecontrollers.NamespaceController,
) {
	h := importHandler{
		ctx:                 ctx,
		systemNamespace:     systemNamespace,
		secrets:             secrets,
		clusters:            clusters,
		tokens:              tokens.Cache(),
		tokenClient:         tokens,
		namespaceController: namespaceController,
		bundleClient:        bundles,
	}

	clusters.OnChange(ctx, "import-cluster", h.OnChange)
	fleetcontrollers.RegisterClusterStatusHandler(ctx, clusters, "Imported", "import-cluster", h.importCluster)
	config.OnChange(ctx, h.onConfig)
}

// onConfig triggers clusters which rely on the fallback config in the
// fleet-controller config map. This is important for changes to apiServerURL
// and apiServerCA, as they are needed e.g. to update the fleet-agent-bootstrap
// secret.
func (i *importHandler) onConfig(cfg *config.Config) error {
	clusters, err := i.clusters.List("", metav1.ListOptions{})
	if err != nil {
		return err
	}

	if len(clusters.Items) == 0 {
		return nil
	}

	if cfg == nil {
		return errors.New("config is nil: this should never happen")
	}

	for _, cluster := range clusters.Items {
		if cluster.Spec.KubeConfigSecret == "" {
			continue
		}

		hasConfigChanged, err := i.hasAPIServerConfigChanged(cfg, cluster)
		if err != nil {
			return fmt.Errorf("cluster %s/%s: could not check for config changes: %v", cluster.Namespace, cluster.Name, err)
		}

		hasConfigChanged = hasConfigChanged || cfg.AgentTLSMode != cluster.Status.AgentTLSMode ||
			hasGarbageCollectionIntervalChanged(cfg, cluster)

		if hasConfigChanged {
			logrus.Infof("API server config changed, trigger cluster import for cluster %s/%s", cluster.Namespace, cluster.Name)
			c := cluster.DeepCopy()
			c.Status.AgentConfigChanged = true
			_, err := i.clusters.UpdateStatus(c)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// hasAPIServerConfigChanged checks for changes in API server URL or CA configuration, comparing the current state of
// the cluster with cfg. However, if the cluster references a secret through its `KubeConfigSecret` field, then API
// server URL and CA are understood to be sourced from there, hence config changes for those fields will be skipped.
// Returns a boolean indicating whether URL or CA config has changed, and any error that may have occurred (such as the
// referenced secret not being found).
func (i *importHandler) hasAPIServerConfigChanged(cfg *config.Config, cluster fleet.Cluster) (bool, error) {
	kubeConfigSecretNamespace := getKubeConfigSecretNS(cluster)

	var secretAPIServerCA, secretAPIServerURL []byte
	var secret *corev1.Secret

	if cluster.Spec.KubeConfigSecret != "" {
		var err error
		secret, err = i.secrets.Get(kubeConfigSecretNamespace, cluster.Spec.KubeConfigSecret)
		if err != nil {
			return false, err
		}

		secretAPIServerURL = secret.Data[config.APIServerURLKey]
		secretAPIServerCA = secret.Data[config.APIServerCAKey]
	}

	// if the API server URL is non-empty in the secret, then it is sourced from there; config changes for that field
	// are irrelevant.
	// The same applies to the CA.
	hasURLChanged := len(secretAPIServerURL) == 0 && cfg.APIServerURL != cluster.Status.APIServerURL
	hasCAChanged := len(secretAPIServerCA) == 0 && hashStatusField(cfg.APIServerCA) != cluster.Status.APIServerCAHash

	return hasURLChanged || hasCAChanged, nil
}

func hashStatusField(field any) string {
	hasher := sha256.New224()
	b, err := json.Marshal(field)
	if err != nil {
		return ""
	}
	hasher.Write(b)
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func agentDeployed(cluster *fleet.Cluster) bool {
	if cluster.Status.AgentConfigChanged {
		return false
	}

	if !cluster.Status.AgentMigrated {
		return false
	}

	if !cluster.Status.CattleNamespaceMigrated {
		return false
	}

	if cluster.Status.AgentDeployedGeneration == nil {
		return false
	}

	if !cluster.Status.AgentNamespaceMigrated {
		return false
	}

	if cluster.Spec.AgentNamespace != "" && cluster.Status.Agent.Namespace != cluster.Spec.AgentNamespace {
		return false
	}

	return *cluster.Status.AgentDeployedGeneration == cluster.Spec.RedeployAgentGeneration
}

// OnChange is triggered when a cluster changes, for manager initiated
// deployments and the local agent. It updates the client ID, only when
// KubeConfigSecret is configured or the agent is not already deployed.
func (i *importHandler) OnChange(key string, cluster *fleet.Cluster) (_ *fleet.Cluster, err error) {
	if cluster == nil {
		return cluster, nil
	}

	if cluster.Spec.KubeConfigSecret == "" || agentDeployed(cluster) {
		return cluster, nil
	}

	// NOTE(mm): why is this not done in importCluster?
	if cluster.Spec.ClientID == "" {
		logrus.Debugf("Cluster import for '%s/%s'. Agent found, updating ClientID", cluster.Namespace, cluster.Name)

		cluster = cluster.DeepCopy()
		cluster.Spec.ClientID, err = randomtoken.Generate()
		if err != nil {
			return nil, err
		}
		return i.clusters.Update(cluster)
	}

	return cluster, nil
}

func (i *importHandler) deleteOldAgentBundle(cluster *fleet.Cluster) error {
	if err := i.bundleClient.Delete(cluster.Namespace, names.SafeConcatName(manageagent.AgentBundleName, cluster.Name), nil); err != nil {
		return err
	}
	i.namespaceController.Enqueue(cluster.Namespace)
	return nil
}

func (i *importHandler) deleteOldAgent(cluster *fleet.Cluster, kc kubernetes.Interface, namespace string) error {
	err := kc.CoreV1().Secrets(namespace).Delete(i.ctx, config.AgentConfigName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	err = kc.CoreV1().Secrets(namespace).Delete(i.ctx, config.AgentBootstrapConfigName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if err := kc.AppsV1().StatefulSets(namespace).Delete(i.ctx, config.AgentConfigName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := kc.AppsV1().Deployments(namespace).Delete(i.ctx, config.AgentConfigName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	logrus.Infof("Deleted old agent for cluster (%s/%s) in namespace %s", cluster.Namespace, cluster.Name, namespace)

	return nil
}

// importCluster is triggered for manager initiated deployments and the local agent, It re-deploys the agent on the downstream cluster.
// Since it re-creates the fleet-agent-bootstrap secret, it will also re-register the agent.
func (i *importHandler) importCluster(cluster *fleet.Cluster, status fleet.ClusterStatus) (_ fleet.ClusterStatus, err error) {
	if shouldMigrateFromLegacyNamespace(cluster.Status.Agent.Namespace) {
		cluster.Status.CattleNamespaceMigrated = false
	}

	if cluster.Spec.KubeConfigSecret == "" ||
		agentDeployed(cluster) ||
		cluster.Spec.ClientID == "" {
		return status, nil
	}

	kubeConfigSecretNamespace := getKubeConfigSecretNS(*cluster)

	logrus.Debugf("Cluster import for '%s/%s'. Getting kubeconfig from secret in namespace %s", cluster.Namespace, cluster.Name, kubeConfigSecretNamespace)

	secret, err := i.secrets.Get(kubeConfigSecretNamespace, cluster.Spec.KubeConfigSecret)
	if err != nil {
		return status, err
	}

	logrus.Debugf("Cluster import for '%s/%s'. Setting up agent with kubeconfig from secret '%s/%s'", cluster.Namespace, cluster.Name, kubeConfigSecretNamespace, cluster.Spec.KubeConfigSecret)
	var (
		cfg          = config.Get()
		apiServerURL = string(secret.Data[config.APIServerURLKey])
		apiServerCA  = secret.Data[config.APIServerCAKey]
	)

	if apiServerURL == "" {
		if len(cfg.APIServerURL) == 0 {
			return status, fmt.Errorf("missing apiServerURL in fleet config for cluster auto registration")
		}
		logrus.Debugf("Cluster import for '%s/%s'. Using apiServerURL from fleet-controller config", cluster.Namespace, cluster.Name)
		apiServerURL = cfg.APIServerURL
	}

	if len(apiServerCA) == 0 {
		apiServerCA = cfg.APIServerCA
	}

	restConfig, err := i.restConfigFromKubeConfig(secret.Data[config.KubeConfigSecretValueKey], cfg.AgentTLSMode)
	if err != nil {
		return status, err
	}
	restConfig.Timeout = durations.RestConfigTimeout

	kc, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return status, err
	}

	if err := connection.SmokeTestKubeClientConnection(kc); err != nil {
		logrus.Errorf("Cluster import for '%s/%s'. Smoke test failed: %v", cluster.Namespace, cluster.Name, err)
		return status, err
	}

	apply, err := apply.NewForConfig(restConfig)
	if err != nil {
		return status, err
	}
	setID := desiredset.GetSetID(config.AgentBootstrapConfigName, "", cluster.Spec.AgentNamespace)
	apply = apply.WithDynamicLookup().WithSetID(setID).WithNoDeleteGVK(fleetns.GVK())

	tokenName := names.SafeConcatName(ImportTokenPrefix + cluster.Name)
	token, err := i.tokens.Get(cluster.Namespace, tokenName)
	if err != nil {
		// ignore error
		_, err = i.tokenClient.Create(&fleet.ClusterRegistrationToken{
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
		})
		logrus.Debugf("Failed to create ClusterRegistrationToken for cluster %s/%s: %v (requeuing)", cluster.Namespace, cluster.Name, err)
		i.clusters.EnqueueAfter(cluster.Namespace, cluster.Name, durations.TokenClusterEnqueueDelay)
		return status, nil
	}

	agentNamespace := i.systemNamespace
	if cluster.Spec.AgentNamespace != "" {
		agentNamespace = cluster.Spec.AgentNamespace
	}

	clusterLabels := yaml.CleanAnnotationsForExport(cluster.Labels)
	agentReplicas := cmd.ParseEnvAgentReplicaCount()

	// Notice we only set the agentScope when it's a non-default agentNamespace. This is for backwards compatibility
	// for when we didn't have agent scope before
	objs, err := agent.AgentWithConfig(
		i.ctx, agentNamespace, i.systemNamespace,
		cluster.Spec.AgentNamespace,
		&client.Getter{Namespace: cluster.Namespace},
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
			// keep in sync with manageagent.go
			ManifestOptions: agent.ManifestOptions{
				AgentEnvVars:     cluster.Spec.AgentEnvVars,
				AgentTolerations: cluster.Spec.AgentTolerations,
				PrivateRepoURL:   cluster.Spec.PrivateRepoURL,
				AgentAffinity:    cluster.Spec.AgentAffinity,
				AgentResources:   cluster.Spec.AgentResources,
				HostNetwork:      *cmp.Or(cluster.Spec.HostNetwork, ptr.To(false)),
				AgentReplicas:    agentReplicas,
			},
		})
	if err != nil {
		return status, err
	}

	if cluster.Status.Agent.Namespace != agentNamespace || !cluster.Status.AgentNamespaceMigrated {
		// delete old agent if moving namespaces for agent
		if err := i.deleteOldAgentBundle(cluster); err != nil {
			return status, err
		}
		if cluster.Status.Agent.Namespace != "" {
			if err := i.deleteOldAgent(cluster, kc, cluster.Status.Agent.Namespace); err != nil {
				return status, err
			}
		}
	}

	if err := i.deleteOldAgent(cluster, kc, agentNamespace); err != nil {
		return status, err
	}

	if err := apply.ApplyObjects(objs...); err != nil {
		logrus.Errorf("Failed cluster import for '%s/%s'. Cannot create agent deployment", cluster.Namespace, cluster.Name)
		return status, err
	}
	logrus.Infof("Cluster import for '%s/%s'. Deployed new agent", cluster.Namespace, cluster.Name)

	if i.systemNamespace != config.DefaultNamespace {
		// Clean up the leftover agent if it exists.
		_, err := kc.CoreV1().Namespaces().Get(i.ctx, config.DefaultNamespace, metav1.GetOptions{})
		if err == nil {
			logrus.Infof("System namespace (%s) does not equal default namespace (%s), checking for leftover objects...", i.systemNamespace, config.DefaultNamespace)
			if err := i.deleteOldAgent(cluster, kc, config.DefaultNamespace); err != nil {
				return status, err
			}
		} else if !apierrors.IsNotFound(err) {
			return status, err
		}

		// Clean up the leftover clusters namespace if it exists.
		// We want to keep the DefaultNamespace alive, but not the clusters namespace.
		err = kc.CoreV1().Namespaces().Delete(i.ctx, fleetns.SystemRegistrationNamespace(config.DefaultNamespace), metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return status, err
		}
	}

	status.AgentDeployedGeneration = &cluster.Spec.RedeployAgentGeneration
	status.AgentMigrated = true
	status.CattleNamespaceMigrated = true
	status.Agent = fleet.AgentStatus{
		Namespace: cluster.Spec.AgentNamespace,
	}
	status.AgentNamespaceMigrated = true
	status.AgentConfigChanged = false
	status.APIServerURL = apiServerURL
	status.APIServerCAHash = hashStatusField(apiServerCA)
	status.AgentTLSMode = cfg.AgentTLSMode
	status.GarbageCollectionInterval = &cfg.GarbageCollectionInterval

	return status, nil
}

func shouldMigrateFromLegacyNamespace(agentStatusNs string) bool {
	return !isLegacyAgentNamespaceSelectedByUser() && agentStatusNs == config.LegacyDefaultNamespace
}

func isLegacyAgentNamespaceSelectedByUser() bool {
	cfg := config.Get()

	return os.Getenv("NAMESPACE") == config.LegacyDefaultNamespace ||
		cfg.Bootstrap.AgentNamespace == config.LegacyDefaultNamespace
}

// restConfigFromKubeConfig checks kubeconfig data and tries to connect to server. If server is behind public CA, remove
// CertificateAuthorityData in kubeconfig file unless strict TLS mode is enabled.
func (i *importHandler) restConfigFromKubeConfig(data []byte, agentTLSMode string) (*rest.Config, error) {
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
			if _, err := http.Get(raw.Clusters[cluster].Server); err == nil {
				raw.Clusters[cluster].CertificateAuthorityData = nil
			}
		}
	}

	return clientcmd.NewDefaultClientConfig(raw, &clientcmd.ConfigOverrides{}).ClientConfig()
}

func getKubeConfigSecretNS(cluster fleet.Cluster) string {
	if cluster.Spec.KubeConfigSecretNamespace == "" {
		return cluster.Namespace
	}

	return cluster.Spec.KubeConfigSecretNamespace
}

func hasGarbageCollectionIntervalChanged(config *config.Config, cluster fleet.Cluster) bool {
	return (config.GarbageCollectionInterval.Duration != 0 && cluster.Status.GarbageCollectionInterval == nil) ||
		(cluster.Status.GarbageCollectionInterval != nil &&
			config.GarbageCollectionInterval.Duration != cluster.Status.GarbageCollectionInterval.Duration)
}
