package helmdeployer

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/rancher/fleet/internal/helmdeployer/helmcache"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"

	"github.com/rancher/fleet/internal/names"
	"github.com/rancher/fleet/internal/namespaces"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	BundleIDAnnotation           = "fleet.cattle.io/bundle-id"
	CommitAnnotation             = "fleet.cattle.io/commit"
	AgentNamespaceAnnotation     = "fleet.cattle.io/agent-namespace"
	ServiceAccountNameAnnotation = "fleet.cattle.io/service-account"
	DefaultServiceAccount        = "fleet-default"
	KeepResourcesAnnotation      = "fleet.cattle.io/keep-resources"
	HelmUpgradeInterruptedError  = "another operation (install/upgrade/rollback) is in progress"
	MaxHelmHistory               = 2
)

var (
	ErrNoRelease    = errors.New("failed to find release")
	ErrNoResourceID = errors.New("no resource ID available")
	DefaultKey      = "values.yaml"
)

type Helm struct {
	client         client.Client
	agentNamespace string
	getter         genericclioptions.RESTClientGetter
	globalCfg      action.Configuration
	// useGlobalCfg is only used by Template
	useGlobalCfg     bool
	template         bool
	defaultNamespace string
	labelPrefix      string
	labelSuffix      string
}

// Resources contains information from a helm release
type Resources struct {
	// DefaultNamespace is the namespace of the helm release
	DefaultNamespace string           `json:"defaultNamespace,omitempty"`
	Objects          []runtime.Object `json:"objects,omitempty"`
}

// DeployedBundle is the link between a bundledeployment and a helm release
type DeployedBundle struct {
	// BundleID is the bundledeployment.Name
	BundleID string
	// ReleaseName is actually in the form "namespace/release name"
	ReleaseName string
	// KeepResources indicate if resources should be kept when deleting a GitRepo or Bundle
	KeepResources bool
}

// New returns a new helm deployer
// * agentNamespace is the system namespace, which is the namespace the agent is running in, e.g. cattle-fleet-system
func New(agentNamespace, defaultNamespace, labelPrefix, labelSuffix string) *Helm {
	return &Helm{
		agentNamespace:   agentNamespace,
		defaultNamespace: defaultNamespace,
		labelPrefix:      labelPrefix,
		labelSuffix:      labelSuffix,
	}
}

func (h *Helm) Setup(ctx context.Context, client client.Client, getter genericclioptions.RESTClientGetter) error {
	h.client = client
	h.getter = getter

	cfg, err := h.createCfg(ctx, "")
	if err != nil {
		return err
	}
	h.globalCfg = cfg

	return nil
}

func (h *Helm) getOpts(bundleID string, options fleet.BundleDeploymentOptions) (time.Duration, string, string) {
	if options.Helm == nil {
		options.Helm = &fleet.HelmOptions{}
	}

	var timeout time.Duration
	if options.Helm.TimeoutSeconds > 0 {
		timeout = time.Second * time.Duration(options.Helm.TimeoutSeconds)
	}

	ns := namespaces.GetDeploymentNS(h.defaultNamespace, options)

	if options.Helm != nil && options.Helm.ReleaseName != "" {
		// JSON schema validation makes sure that the option is valid
		return timeout, ns, options.Helm.ReleaseName
	}

	// releaseName has a limit of 53 in helm https://github.com/helm/helm/blob/main/pkg/action/install.go#L58
	// fleet apply already produces valid names, but we need to make sure
	// that bundles from other sources are valid
	return timeout, ns, names.HelmReleaseName(bundleID)
}

func (h *Helm) getCfg(ctx context.Context, namespace, serviceAccountName string) (action.Configuration, error) {
	var (
		cfg    action.Configuration
		getter = h.getter
	)

	if h.useGlobalCfg {
		return h.globalCfg, nil
	}

	serviceAccountNamespace, serviceAccountName, err := h.getServiceAccount(ctx, serviceAccountName)
	if err != nil {
		return cfg, err
	}

	if serviceAccountName != "" {
		getter, err = newImpersonatingGetter(serviceAccountNamespace, serviceAccountName, h.getter)
		if err != nil {
			return cfg, err
		}
	}

	kClient := kube.New(getter)
	kClient.Namespace = namespace

	cfg, err = h.createCfg(ctx, namespace)
	cfg.Releases.MaxHistory = MaxHelmHistory
	cfg.KubeClient = kClient

	cfg.Capabilities, _ = getCapabilities(cfg)

	return cfg, err
}

func (h *Helm) createCfg(ctx context.Context, namespace string) (action.Configuration, error) {
	logger := log.FromContext(ctx).WithName("helmSDK")
	info := func(format string, v ...interface{}) {
		logger.V(1).Info(fmt.Sprintf(format, v...))
	}
	kc := kube.New(h.getter)
	kc.Log = info
	clientSet, err := kc.Factory.KubernetesClientSet()
	if err != nil {
		return action.Configuration{}, err
	}
	driver := driver.NewSecrets(helmcache.NewSecretClient(h.client, clientSet, namespace))
	driver.Log = info
	store := storage.Init(driver)
	store.MaxHistory = MaxHelmHistory

	return action.Configuration{
		RESTClientGetter: h.getter,
		Releases:         store,
		KubeClient:       kc,
		Log:              info,
	}, nil
}
