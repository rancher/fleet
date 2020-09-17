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
	if cluster.Status.AgentLastDeployed == nil {
		return false
	}
	return cluster.Spec.ForceUpdateAgent == nil ||
		cluster.Spec.ForceUpdateAgent.Time.After(time.Now().Add(-15*time.Minute)) ||
		cluster.Spec.ForceUpdateAgent.Before(cluster.Status.AgentLastDeployed)
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
		noCheck      = false
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
		CA:         apiServerCA,
		Host:       apiServerURL,
		ClientID:   cluster.Spec.ClientID,
		NoCheck:    noCheck,
		Generation: strconv.FormatInt(cluster.Generation, 10),
	})
	if err != nil {
		return status, err
	}

	obj, err := yaml.ToObjects(output)
	if err != nil {
		return status, err
	}

	if err := apply.ApplyObjects(obj...); err != nil {
		return status, err
	}

	now := metav1.Now()
	status.AgentLastDeployed = &now
	return status, nil
}
