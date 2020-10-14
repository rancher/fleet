package cluster

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/rancher/fleet/modules/cli/agentmanifest"
	"github.com/rancher/fleet/modules/cli/pkg/client"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/randomtoken"
	"github.com/rancher/wrangler/pkg/yaml"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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

func (i *importHandler) deleteOldAgent(cluster *fleet.Cluster, kc kubernetes.Interface) error {
	err := kc.CoreV1().Secrets(i.systemNamespace).Delete(i.ctx, "fleet-agent", metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	err = kc.CoreV1().Secrets(i.systemNamespace).Delete(i.ctx, "fleet-agent-bootstrap", metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	deployment, err := kc.AppsV1().Deployments(i.systemNamespace).Get(i.ctx, "fleet-agent", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return nil
	}

	logrus.Infof("Deleted old agent for cluster %s/%s", cluster.Namespace, cluster.Name)

	err = kc.AppsV1().Deployments(i.systemNamespace).Delete(i.ctx, "fleet-agent", metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	pods, err := kc.CoreV1().Pods(i.systemNamespace).List(i.ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(deployment.Spec.Selector),
	})
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		err := kc.CoreV1().Pods(i.systemNamespace).Delete(i.ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
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

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["value"])
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
	apply = apply.WithDynamicLookup().WithSetID("fleet-agent-bootstrap")

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

	if err := i.deleteOldAgent(cluster, kc); err != nil {
		return status, err
	}

	if err := apply.ApplyObjects(obj...); err != nil {
		return status, err
	}

	logrus.Infof("Deployed new agent for cluster %s/%s", cluster.Namespace, cluster.Name)

	status.AgentDeployedGeneration = &cluster.Spec.RedeployAgentGeneration
	status.Agent = fleet.AgentStatus{}
	return status, nil
}
