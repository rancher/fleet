package cluster

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rancher/fleet/modules/cli/agentmanifest"
	"github.com/rancher/fleet/modules/cli/pkg/client"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	fleetns "github.com/rancher/fleet/pkg/namespace"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/randomtoken"
	"github.com/rancher/wrangler/pkg/yaml"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	ImportTokenPrefix = "import-token-"
	ImportTokenTTL    = 12 * time.Hour
	t                 = true
)

type importHandler struct {
	ctx             context.Context
	systemNamespace string
	secrets         corecontrollers.SecretCache
	clusters        fleetcontrollers.ClusterController
	tokens          fleetcontrollers.ClusterRegistrationTokenCache
	tokenClient     fleetcontrollers.ClusterRegistrationTokenClient
}

func RegisterImport(
	ctx context.Context,
	systemNamespace string,
	secrets corecontrollers.SecretCache,
	clusters fleetcontrollers.ClusterController,
	tokens fleetcontrollers.ClusterRegistrationTokenController,
) {
	h := importHandler{
		ctx:             ctx,
		systemNamespace: systemNamespace,
		secrets:         secrets,
		clusters:        clusters,
		tokens:          tokens.Cache(),
		tokenClient:     tokens,
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
		cluster = cluster.DeepCopy()
		cluster.Spec.ClientID, err = randomtoken.Generate()
		if err != nil {
			return nil, err
		}
		return i.clusters.Update(cluster)
	}

	return cluster, nil
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
	restConfig.Timeout = 15 * time.Second

	kc, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return status, err
	}

	if _, err = kc.Discovery().ServerVersion(); err != nil {
		return status, err
	}

	apply, err := apply.NewForConfig(restConfig)
	if err != nil {
		return status, err
	}
	apply = apply.WithDynamicLookup().WithSetID(config.AgentBootstrapConfigName).WithNoDeleteGVK(fleetns.GVK())

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
		i.clusters.EnqueueAfter(cluster.Namespace, cluster.Name, 2*time.Second)
		return status, nil
	}

	output := &bytes.Buffer{}
	err = agentmanifest.AgentManifest(i.ctx, i.systemNamespace, i.systemNamespace, &client.Getter{Namespace: cluster.Namespace}, output, token.Name, &agentmanifest.Options{
		CA:              apiServerCA,
		Host:            apiServerURL,
		ClientID:        cluster.Spec.ClientID,
		AgentEnvVars:    cluster.Spec.AgentEnvVars,
		CheckinInterval: cfg.AgentCheckinInternal.Duration.String(),
		Generation:      string(cluster.UID) + "-" + strconv.FormatInt(cluster.Generation, 10),
	})
	if err != nil {
		return status, err
	}

	obj, err := yaml.ToObjects(output)
	if err != nil {
		return status, err
	}

	if err := i.deleteOldAgent(cluster, kc, i.systemNamespace); err != nil {
		return status, err
	}

	if err := apply.ApplyObjects(obj...); err != nil {
		return status, err
	}
	logrus.Infof("Deployed new agent for cluster %s/%s", cluster.Namespace, cluster.Name)

	if i.systemNamespace != config.DefaultNamespace {
		logrus.Infof("System namespace (%s) does not equal default namespace (%s), checking for leftover objects...", i.systemNamespace, config.DefaultNamespace)
		_, err := kc.CoreV1().Namespaces().Get(i.ctx, config.DefaultNamespace, metav1.GetOptions{})
		if err == nil {
			if err := i.deleteOldAgent(cluster, kc, config.DefaultNamespace); err != nil {
				return status, err
			}
		} else if !apierrors.IsNotFound(err) {
			return status, err
		}
	}

	status.AgentDeployedGeneration = &cluster.Spec.RedeployAgentGeneration
	status.AgentMigrated = true
	status.CattleNamespaceMigrated = true
	status.Agent = fleet.AgentStatus{}
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
