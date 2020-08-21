package cluster

import (
	"bytes"
	"context"
	"fmt"
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
	ImportTokenPrefix   = "import-token-"
	ImportTokenTTL      = int((12 * time.Hour) / time.Second)
	ControllerNamespace = "fleet-system"
	t                   = true
)

type importHandler struct {
	ctx             context.Context
	systemNamespace string
	secrets         corecontrollers.SecretCache
	clusters        fleetcontrollers.ClusterClient
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
}

func (i *importHandler) OnChange(key string, cluster *fleet.Cluster) (_ *fleet.Cluster, err error) {
	if cluster == nil {
		return cluster, nil
	}

	if cluster.Spec.KubeConfigSecret == "" ||
		(cluster.Status.AgentDeployed != nil && *cluster.Status.AgentDeployed) {
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

	secret, err := i.secrets.Get(cluster.Namespace, cluster.Spec.KubeConfigSecret)
	if err != nil {
		return nil, err
	}

	var (
		cfg          = config.Get()
		noCheck      = false
		apiServerURL = string(secret.Data["apiServerURL"])
		apiServerCA  = secret.Data["apiServerCA"]
	)

	if apiServerURL == "" {
		if len(cfg.APIServerURL) == 0 {
			return nil, fmt.Errorf("missing apiServerURL in fleet config for cluster auto registration")
		}
		apiServerURL = cfg.APIServerURL
	}

	if len(apiServerCA) == 0 {
		apiServerCA = cfg.APIServerCA
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["value"])
	if err != nil {
		return nil, err
	}

	kc, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	if _, err = kc.Discovery().ServerVersion(); err != nil {
		return nil, err
	}

	apply, err := apply.NewForConfig(restConfig)
	if err != nil {
		return nil, err
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
				TTLSeconds: ImportTokenTTL,
			},
		})
		return nil, err
	}

	output := &bytes.Buffer{}
	err = agentmanifest.AgentManifest(i.ctx, i.systemNamespace, ControllerNamespace, &client.Getter{Namespace: cluster.Namespace}, output, token.Name, &agentmanifest.Options{
		CA:       apiServerCA,
		Host:     apiServerURL,
		ClientID: cluster.Spec.ClientID,
		NoCheck:  noCheck,
	})
	if err != nil {
		return nil, err
	}

	obj, err := yaml.ToObjects(output)
	if err != nil {
		return nil, err
	}

	if err := apply.ApplyObjects(obj...); err != nil {
		return nil, err
	}

	cluster = cluster.DeepCopy()
	cluster.Status.AgentDeployed = &t
	return i.clusters.UpdateStatus(cluster)
}
