package bootstrap

import (
	"context"
	"maps"
	"os"
	"regexp"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	secretutil "github.com/rancher/fleet/internal/cmd/controller/agentmanagement/secret"
	fleetns "github.com/rancher/fleet/internal/cmd/controller/namespace"
	fleetconfig "github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/apply"
	appscontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/apps/v1"
	corecontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	FleetBootstrap = "fleet-controller-bootstrap"
)

var (
	splitter          = regexp.MustCompile(`\s*,\s*`)
	ErrNoHostInConfig = errors.New("failed to find cluster server parameter")
)

type handler struct {
	apply               apply.Apply
	systemNamespace     string
	serviceAccountCache corecontrollers.ServiceAccountCache
	secretsCache        corecontrollers.SecretCache
	secretsController   corecontrollers.SecretController
	deploymentsCache    appscontrollers.DeploymentCache
	cfg                 clientcmd.ClientConfig
}

func Register(ctx context.Context,
	systemNamespace string,
	apply apply.Apply,
	cfg clientcmd.ClientConfig,
	serviceAccountCache corecontrollers.ServiceAccountCache,
	secretsController corecontrollers.SecretController,
	secretsCache corecontrollers.SecretCache,
	deploymentCache appscontrollers.DeploymentCache,
) {
	h := handler{
		systemNamespace:     systemNamespace,
		serviceAccountCache: serviceAccountCache,
		secretsCache:        secretsCache,
		secretsController:   secretsController,
		deploymentsCache:    deploymentCache,
		apply:               apply.WithSetID("fleet-bootstrap"),
		cfg:                 cfg,
	}
	fleetconfig.OnChange(ctx, h.OnConfig)
}

func (h *handler) OnConfig(config *fleetconfig.Config) error {
	logrus.Debugf("Bootstrap config set, building namespace '%s', secret, local cluster, cluster group, ...", config.Bootstrap.Namespace)

	var objs []runtime.Object
	localClusterLabels := map[string]string{"name": "local"}

	if config.Bootstrap.ClusterLabels != nil {
		maps.Copy(localClusterLabels, config.Bootstrap.ClusterLabels)
	}

	if config.Bootstrap.Namespace == "" || config.Bootstrap.Namespace == "-" {
		return nil
	}

	secret, err := h.buildSecret(config.Bootstrap.Namespace, h.cfg)
	if err != nil {
		return err
	}
	fleetControllerDeployment, err := h.deploymentsCache.Get(h.systemNamespace, fleetconfig.ManagerConfigName)
	if err != nil {
		return err
	}

	objs = append(objs, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: config.Bootstrap.Namespace,
		},
	}, secret, &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local",
			Namespace: config.Bootstrap.Namespace,
			Labels:    localClusterLabels,
		},
		Spec: fleet.ClusterSpec{
			KubeConfigSecret: secret.Name,
			AgentNamespace:   config.Bootstrap.AgentNamespace,
			// copy tolerations from fleet-controller
			AgentTolerations: fleetControllerDeployment.Spec.Template.Spec.Tolerations,
		},
	}, &fleet.ClusterGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: config.Bootstrap.Namespace,
		},
		Spec: fleet.ClusterGroupSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "local",
				},
			},
		},
	})

	// A repo to add at install time that will deploy to the local cluster. This allows
	// one to fully bootstrap fleet, its configuration and all its downstream clusters
	// in one shot.
	if config.Bootstrap.Repo != "" {
		var paths []string
		if len(config.Bootstrap.Paths) > 0 {
			paths = splitter.Split(config.Bootstrap.Paths, -1)
		}
		objs = append(objs, &fleet.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bootstrap",
				Namespace: config.Bootstrap.Namespace,
			},
			Spec: fleet.GitRepoSpec{
				Repo:             config.Bootstrap.Repo,
				Branch:           config.Bootstrap.Branch,
				ClientSecretName: config.Bootstrap.Secret,
				Paths:            paths,
			},
		})
	}

	return h.apply.WithNoDeleteGVK(fleetns.GVK()).ApplyObjects(objs...)
}

func (h *handler) buildSecret(bootstrapNamespace string, cfg clientcmd.ClientConfig) (*corev1.Secret, error) {
	rawConfig, err := cfg.RawConfig()
	if err != nil {
		return nil, err
	}

	host, err := getHost(rawConfig)
	if err != nil {
		return nil, err
	}

	ca, err := getCA(rawConfig)
	if err != nil {
		return nil, err
	}

	token, err := h.getToken()
	if err != nil {
		return nil, err
	}

	value, err := buildKubeConfig(host, ca, token, rawConfig)
	if err != nil {
		return nil, err
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local-cluster",
			Namespace: bootstrapNamespace,
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Data: map[string][]byte{
			fleetconfig.KubeConfigSecretValueKey: value,
			fleetconfig.APIServerURLKey:          []byte(host),
			fleetconfig.APIServerCAKey:           ca,
		},
	}, nil
}

func getHost(rawConfig clientcmdapi.Config) (string, error) {
	icc, err := rest.InClusterConfig()
	if err == nil {
		return icc.Host, nil
	}

	cluster, ok := rawConfig.Clusters[rawConfig.CurrentContext]
	if ok {
		return cluster.Server, nil
	}

	for _, v := range rawConfig.Clusters {
		return v.Server, nil
	}

	return "", ErrNoHostInConfig
}

func getCA(rawConfig clientcmdapi.Config) ([]byte, error) {
	icc, err := rest.InClusterConfig()
	if err == nil {
		return os.ReadFile(icc.CAFile)
	}

	cluster, ok := rawConfig.Clusters[rawConfig.CurrentContext]
	if !ok {
		for _, v := range rawConfig.Clusters {
			cluster = v
			break
		}
	}

	if cluster != nil {
		if len(cluster.CertificateAuthorityData) > 0 {
			return cluster.CertificateAuthorityData, nil
		}
		return os.ReadFile(cluster.CertificateAuthority)
	}

	return nil, nil
}

func (h *handler) getToken() (string, error) {
	sa, err := h.serviceAccountCache.Get(h.systemNamespace, FleetBootstrap)
	if apierrors.IsNotFound(err) {
		icc, err := rest.InClusterConfig()
		if err == nil {
			return icc.BearerToken, nil
		}
		return "", nil
	} else if err != nil {
		return "", err
	}

	// kubernetes 1.24 doesn't populate sa.Secrets
	if len(sa.Secrets) == 0 {
		logrus.Infof("waiting on secret for service account %s/%s", h.systemNamespace, FleetBootstrap)
		secret, err := secretutil.GetServiceAccountTokenSecret(sa, h.secretsController)
		if err != nil {
			return "", err
		}
		return string(secret.Data[corev1.ServiceAccountTokenKey]), nil
	}

	secret, err := h.secretsCache.Get(h.systemNamespace, sa.Secrets[0].Name)
	if err != nil {
		return "", err
	}

	return string(secret.Data[corev1.ServiceAccountTokenKey]), nil
}

func buildKubeConfig(host string, ca []byte, token string, rawConfig clientcmdapi.Config) ([]byte, error) {
	if token == "" {
		return clientcmd.Write(rawConfig)
	}
	return clientcmd.Write(clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"cluster": {
				Server:                   host,
				CertificateAuthorityData: ca,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {
				Token: token,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default": {
				Cluster:  "cluster",
				AuthInfo: "user",
			},
		},
		CurrentContext: "default",
	})

}
