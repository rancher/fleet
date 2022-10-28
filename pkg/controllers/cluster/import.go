package cluster

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/modules/cli/pkg/client"
	"github.com/rancher/fleet/pkg/agent"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/fleet/pkg/connection"
	"github.com/rancher/fleet/pkg/controllers/manageagent"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/helmdeployer"
	fleetns "github.com/rancher/fleet/pkg/namespace"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/randomtoken"
	"github.com/rancher/wrangler/pkg/yaml"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	ImportTokenPrefix = "import-token-"
	ImportTokenTTL    = durations.ClusterImportTokenTTL
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
}

func agentDeployed(cluster *fleet.Cluster) bool {
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

func (i *importHandler) OnChange(key string, cluster *fleet.Cluster) (_ *fleet.Cluster, err error) {
	if cluster == nil {
		return cluster, nil
	}

	if cluster.Spec.KubeConfigSecret == "" || agentDeployed(cluster) {
		return cluster, nil
	}

	if cluster.Spec.ClientID == "" {
		logrus.Debugf("Cluster '%s' changed, agent deployed, updating ClientID", cluster.Name)

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
	if err := i.bundleClient.Delete(cluster.Namespace, name.SafeConcatName(manageagent.AgentBundleName, cluster.Name), nil); err != nil {
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

	deployment, err := kc.AppsV1().Deployments(namespace).Get(i.ctx, config.AgentConfigName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}

	if err := kc.AppsV1().Deployments(namespace).Delete(i.ctx, config.AgentConfigName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	logrus.Infof("Deleted old agent for cluster (%s/%s) in namespace %s", cluster.Namespace, cluster.Name, namespace)

	pods, err := kc.CoreV1().Pods(namespace).List(i.ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(deployment.Spec.Selector),
	})
	if apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		if err := kc.CoreV1().Pods(namespace).Delete(i.ctx, pod.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func (i *importHandler) importCluster(cluster *fleet.Cluster, status fleet.ClusterStatus) (_ fleet.ClusterStatus, err error) {
	if cluster.Spec.KubeConfigSecret == "" ||
		agentDeployed(cluster) ||
		cluster.Spec.ClientID == "" {
		return status, nil
	}
	secret, err := i.secrets.Get(cluster.Namespace, cluster.Spec.KubeConfigSecret)
	if err != nil {
		return status, err
	}

	logrus.Debugf("ClusterStatusHandler cluster '%s/%s' changed, setting up agent with kubeconfig from %s", cluster.Namespace, cluster.Name, cluster.Spec.KubeConfigSecret)
	var (
		cfg          = config.Get()
		apiServerURL = string(secret.Data["apiServerURL"])
		apiServerCA  = secret.Data["apiServerCA"]
	)

	if apiServerURL == "" {
		if len(cfg.APIServerURL) == 0 {
			return status, fmt.Errorf("missing apiServerURL in fleet config for cluster auto registration")
		}
		apiServerURL = cfg.APIServerURL
	}

	if len(apiServerCA) == 0 {
		apiServerCA = cfg.APIServerCA
	}

	restConfig, err := i.restConfigFromKubeConfig(secret.Data["value"])
	if err != nil {
		return status, err
	}
	restConfig.Timeout = durations.RestConfigTimeout

	kc, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return status, err
	}

	if err := connection.SmokeTestKubeClientConnection(kc); err != nil {
		return status, err
	}

	apply, err := apply.NewForConfig(restConfig)
	if err != nil {
		return status, err
	}
	setID := helmdeployer.GetSetID(config.AgentBootstrapConfigName, "", cluster.Spec.AgentNamespace)
	apply = apply.WithDynamicLookup().WithSetID(setID).WithNoDeleteGVK(fleetns.GVK())

	token, err := i.tokens.Get(cluster.Namespace, ImportTokenPrefix+cluster.Name)
	if err != nil {
		// ignore error
		_, _ = i.tokenClient.Create(&fleet.ClusterRegistrationToken{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cluster.Namespace,
				Name:      ImportTokenPrefix + cluster.Name,
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
				TTL: &metav1.Duration{Duration: ImportTokenTTL},
			},
		})
		i.clusters.EnqueueAfter(cluster.Namespace, cluster.Name, durations.TokenClusterEnqueueDelay)
		return status, nil
	}

	output := &bytes.Buffer{}
	agentNamespace := i.systemNamespace
	if cluster.Spec.AgentNamespace != "" {
		agentNamespace = cluster.Spec.AgentNamespace
	}
	// Notice we only set the agentScope when it's a non-default agentNamespace. This is for backwards compatibility
	// for when we didn't have agent scope before
	err = agent.AgentWithConfig(
		i.ctx, agentNamespace, i.systemNamespace,
		cluster.Spec.AgentNamespace,
		&client.Getter{Namespace: cluster.Namespace},
		output,
		token.Name,
		&agent.Options{
			CA:   apiServerCA,
			Host: apiServerURL,
			ConfigOptions: agent.ConfigOptions{
				ClientID: cluster.Spec.ClientID,
				Labels:   cluster.Labels,
			},
			ManifestOptions: agent.ManifestOptions{
				AgentEnvVars:    cluster.Spec.AgentEnvVars,
				CheckinInterval: cfg.AgentCheckinInternal.Duration.String(),
				Generation:      string(cluster.UID) + "-" + strconv.FormatInt(cluster.Generation, 10),
				PrivateRepoURL:  cluster.Spec.PrivateRepoURL,
			},
		})
	if err != nil {
		return status, err
	}

	obj, err := yaml.ToObjects(output)
	if err != nil {
		return status, err
	}

	if cluster.Spec.AgentNamespace != "" && (cluster.Status.Agent.Namespace != agentNamespace || !cluster.Status.AgentNamespaceMigrated) {
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

	if err := apply.ApplyObjects(obj...); err != nil {
		return status, err
	}
	logrus.Infof("Deployed new agent for cluster %s/%s", cluster.Namespace, cluster.Name)

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
		err = kc.CoreV1().Namespaces().Delete(i.ctx, fleetns.RegistrationNamespace(config.DefaultNamespace), metav1.DeleteOptions{})
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
	return status, nil
}

// restConfigFromKubeConfig checks kubeconfig data and tries to connect to server. If server is behind public CA, remove CertificateAuthorityData in kubeconfig file.
func (i *importHandler) restConfigFromKubeConfig(data []byte) (*rest.Config, error) {
	clientConfig, err := clientcmd.NewClientConfigFromBytes(data)
	if err != nil {
		return nil, err
	}

	raw, err := clientConfig.RawConfig()
	if err != nil {
		return nil, err
	}

	if raw.Contexts[raw.CurrentContext] != nil {
		cluster := raw.Contexts[raw.CurrentContext].Cluster
		if raw.Clusters[cluster] != nil {
			_, err := http.Get(raw.Clusters[cluster].Server)
			if err == nil {
				raw.Clusters[cluster].CertificateAuthorityData = nil
			}
		}
	}

	return clientcmd.NewDefaultClientConfig(raw, &clientcmd.ConfigOverrides{}).ClientConfig()
}
